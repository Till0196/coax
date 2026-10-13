// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package hostapi

import (
	_ "embed"
	"reflect"
	"strings"
)

// The description of this interface, as OpenAPI 3.1. Generated from the shapes in
// hostapi.go and the rules in query.go, so it cannot claim something the program does not
// do -- only fall behind, which openapi_test.go catches. Regenerate with:
//
//	go generate ./internal/coaxd/host/server/
//
// Only the document is compiled in, as bytes, so a daemon can hand out its own description.
// The file is called swagger.json because that is what the generator names everything it
// writes; the contents are OpenAPI 3.1.
//
//go:embed swagger.json
var specification []byte

// Specification is the OpenAPI description of this interface.
func Specification() []byte { return specification }

// Accepts reports whether a query parameter is one a tuning request may carry.
// It asks the same tables ParseRequest reads, so it cannot drift: an unknown
// parameter is refused outright, so a description naming one parameter too many
// would have every request from a client following it rejected. A channel-file
// property is accepted by its shape.
func Accepts(name string) bool {
	return controls[name] || shorthands[name] != "" || isProperty(name)
}

// Shapes is every shape that goes over the wire and the name each field carries
// there, read off the types themselves so adding a field needs nothing else
// touched. The check that the published description still matches reads it.
func Shapes() map[string][]string {
	shapes := map[string]any{
		"Version":             Version{},
		"Board":               Board{},
		"Tuner":               Tuner{},
		"Setting":             Setting{},
		"StreamCounts":        StreamCounts{},
		"TuneResult":          TuneResult{},
		"Card":                Card{},
		"CardRequest":         CardRequest{},
		"CardSession":         CardSession{},
		"Exchange":            Exchange{},
		"CardConnection":      CardConnection{},
		"CardTransmitRequest": CardTransmitRequest{},
		"CardTransmitResult":  CardTransmitResult{},
		"CardRelease":         CardRelease{},
		"CardState":           CardState{},
		"Failure":             Failure{},
		"Problem":             Problem{},
	}
	named := make(map[string][]string, len(shapes))
	for name, shape := range shapes {
		named[name] = fieldsOf(reflect.TypeOf(shape))
	}
	return named
}

func fieldsOf(shape reflect.Type) []string {
	names := make([]string, 0, shape.NumField())
	for index := range shape.NumField() {
		field := shape.Field(index)
		if !field.IsExported() {
			continue
		}
		tag, ok := field.Tag.Lookup("json")
		if !ok {
			names = append(names, field.Name)
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		names = append(names, name)
	}
	return names
}
