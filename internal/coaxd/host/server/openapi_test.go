// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/host/hostapi"
)

// The published description is generated, so it cannot claim something the
// program does not do -- but it can fall behind: an endpoint added without the
// generator being run leaves a quietly incomplete document.
//
// These checks stand in for running the generator during the tests, which would
// need the network and a tool this program does not otherwise depend on.

// document is as much of OpenAPI as these checks read.
type document struct {
	OpenAPI string `json:"openapi"`
	Info    struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"info"`
	Paths      map[string]map[string]operation `json:"paths"`
	Components struct {
		Schemas map[string]schema `json:"schemas"`
	} `json:"components"`
}

type operation struct {
	Summary     string      `json:"summary"`
	Description string      `json:"description"`
	Tags        []string    `json:"tags"`
	Parameters  []parameter `json:"parameters"`
	Responses   map[string]struct {
		Description string `json:"description"`
	} `json:"responses"`
}

type parameter struct {
	Name string `json:"name"`
	In   string `json:"in"`
}

type schema struct {
	Properties map[string]json.RawMessage `json:"properties"`
}

func read(t *testing.T) document {
	t.Helper()
	var parsed document
	if err := json.Unmarshal(hostapi.Specification(), &parsed); err != nil {
		t.Fatalf("the published description is not JSON: %v", err)
	}
	return parsed
}

// It has to be a document at all, of the version this program claims to
// publish.
func TestTheDocumentIsOpenAPI31(t *testing.T) {
	parsed := read(t)
	if parsed.OpenAPI != "3.1.0" {
		t.Errorf("the document says it is %q, want OpenAPI 3.1.0", parsed.OpenAPI)
	}
	if parsed.Info.Title == "" || parsed.Info.Version == "" {
		t.Errorf("the document names itself %q version %q", parsed.Info.Title, parsed.Info.Version)
	}
}

// The description's own version is three numbers, since that is what says how
// much a change costs a client: a moved last number is an addition an old client
// can ignore, a moved first number a shape it can no longer read. A bare "1"
// cannot say the difference, which is the whole reason a client looks.
func TestTheDescriptionsVersionSaysWhatMoved(t *testing.T) {
	parsed := read(t)
	parts := strings.Split(parsed.Info.Version, ".")
	if len(parts) != 3 {
		t.Fatalf("the description calls itself version %q, want three numbers", parsed.Info.Version)
	}
	for _, part := range parts {
		if _, err := strconv.Atoi(part); err != nil {
			t.Errorf("the description calls itself version %q, which is not three numbers", parsed.Info.Version)
			break
		}
	}
}

// Every endpoint this server has is described, and nothing else. This is the
// check that fails when the generator has not been run: Routes is what the
// router is built from, so an endpoint there and not in the document is one a
// client cannot find.
func TestEveryRouteIsDescribed(t *testing.T) {
	parsed := read(t)

	described := map[string]bool{}
	for path, methods := range parsed.Paths {
		for method := range methods {
			described[strings.ToUpper(method)+" "+path] = true
		}
	}
	for _, route := range Routes {
		if !described[route.Pattern] {
			t.Errorf("%s is served and not described; run go generate ./internal/coaxd/host/server/", route.Pattern)
		}
		delete(described, route.Pattern)
	}
	for pattern := range described {
		t.Errorf("%s is described and not served", pattern)
	}
}

// Every endpoint says what it is for and what it answers with: a description
// that lists a path and nothing else is worse than none, since it looks like
// documentation.
func TestEveryEndpointSaysWhatItDoes(t *testing.T) {
	parsed := read(t)
	for path, methods := range parsed.Paths {
		for method, one := range methods {
			where := strings.ToUpper(method) + " " + path
			if one.Summary == "" {
				t.Errorf("%s has no summary", where)
			}
			if len(one.Tags) == 0 {
				t.Errorf("%s is in no group", where)
			}
			if _, ok := one.Responses["200"]; !ok {
				t.Errorf("%s does not say what it answers with", where)
			}
		}
	}
}

// The two endpoints that take a channel describe every parameter the daemon
// accepts and none that it does not. A missing parameter is one a client will
// not know to send; a documented one the daemon does not accept is worse, since
// the daemon refuses an unknown parameter outright rather than ignoring it, so
// a client following the document would have every request rejected.
func TestTheTuningParametersAreTheOnesTheDaemonTakes(t *testing.T) {
	parsed := read(t)

	for _, path := range []string{"/tune", "/stream"} {
		one, ok := parsed.Paths[path]["get"]
		if !ok {
			t.Fatalf("%s is not described", path)
		}
		for _, given := range one.Parameters {
			if given.In != "query" {
				continue
			}
			if !hostapi.Accepts(given.Name) {
				t.Errorf("%s describes the parameter %q, which the daemon refuses", path, given.Name)
			}
		}
	}
}

// Every field of every shape that goes over the wire is in the document. The
// shapes are the contract, and a field added without the generator being run is
// one a client cannot see. Checked against the shapes themselves rather than a
// list written here, so adding a field is enough to make this fail.
func TestEveryFieldOfEveryShapeIsDescribed(t *testing.T) {
	parsed := read(t)
	for name, fields := range hostapi.Shapes() {
		described, ok := parsed.Components.Schemas["hostapi."+name]
		if !ok {
			t.Errorf("the shape %s is not described; run go generate ./internal/coaxd/host/server/", name)
			continue
		}
		for _, field := range fields {
			if _, ok := described.Properties[field]; !ok {
				t.Errorf("%s.%s is not described; run go generate ./internal/coaxd/host/server/", name, field)
			}
		}
	}
}
