// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package config reads the channel files a scan is given, and the names channels
// go by: a dvbv5 channel file is sections of KEY = VALUE, which Parse reads,
// beside the spellings one delivery system answers to, the four leads a channel
// can arrive on, and the two ways a Japanese satellite channel is written.
package config

import (
	"bufio"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
)

type Entry struct {
	Name       string            `json:"channel"`
	Properties map[string]string `json:"properties"`
	Source     string            `json:"source"`
	Line       int               `json:"-"`
	Route      string            `json:"route"`
}

// Parse reads a channel file, tagging every section with the lead the scan was
// told it is of. An empty route is left empty rather than guessed at: nothing
// here can tell a dish from a cable passing a transponder through, so Mirakurun
// falls back to the lead its channel type implies. See ValidRoute.
func Parse(path string, route string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	route = strings.ToUpper(strings.TrimSpace(route))
	if route != "" && !ValidRoute(route) {
		return nil, fmt.Errorf("invalid route %q; it is one of TER, SAT, CATV or HIKARI", route)
	}
	return ParseReader(f, path, route)
}

func ParseReader(r io.Reader, source string, route string) ([]Entry, error) {
	scanner := bufio.NewScanner(r)
	// DVB channel files are normally small, but allow long values and comments.
	scanner.Buffer(make([]byte, 4096), 1024*1024)

	var entries []Entry
	var current *Entry
	seen := make(map[string]int)
	lineNumber := 0

	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			if name == "" {
				return nil, fmt.Errorf("%s:%d: empty section name", source, lineNumber)
			}
			if firstLine, ok := seen[name]; ok {
				return nil, fmt.Errorf("%s:%d: duplicate section %q (first declared at line %d)", source, lineNumber, name, firstLine)
			}
			seen[name] = lineNumber
			entries = append(entries, Entry{
				Name:       name,
				Properties: make(map[string]string),
				Source:     source,
				Line:       lineNumber,
				Route:      route,
			})
			current = &entries[len(entries)-1]
			continue
		}

		if current == nil {
			return nil, fmt.Errorf("%s:%d: property outside a section", source, lineNumber)
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected KEY = VALUE", source, lineNumber)
		}
		key = strings.ToUpper(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			return nil, fmt.Errorf("%s:%d: empty property name or value", source, lineNumber)
		}
		if _, exists := current.Properties[key]; exists {
			return nil, fmt.Errorf("%s:%d: duplicate property %q in section %q", source, lineNumber, key, current.Name)
		}
		current.Properties[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s: no channel sections", source)
	}

	for _, entry := range entries {
		for _, required := range []string{"DELIVERY_SYSTEM", "FREQUENCY"} {
			if entry.Properties[required] == "" {
				return nil, fmt.Errorf("%s:%d: section %q has no %s", source, entry.Line, entry.Name, required)
			}
		}
	}
	return entries, nil
}

// ValidRoute reports whether a name is one of the four leads a channel can
// arrive on.
//
// A route is which lead the aerial socket is fed from, and it is not derivable
// from the delivery system: the same cable carries transmodulated channels as
// J.83 and passes some satellite transponders through untouched, and those
// pass-throughs are ISDB-S on the wire and indistinguishable from a dish.
// Reading the system as the lead told Mirakurun that a cable tuner was a
// satellite tuner, and a tuner picked for a transponder the cable does not
// carry fails the recording with nothing in the channel to say why.
//
// So the lead is declared rather than worked out: one scan holds one tuner for
// its whole run, a tuner has one socket, and a socket has one lead.
func ValidRoute(route string) bool {
	switch route {
	case "TER", "SAT", "CATV", "HIKARI":
		return true
	default:
		return false
	}
}

// SortedPropertyKeys is a section's properties in one settled order.
func SortedPropertyKeys(properties map[string]string) []string {
	return slices.Sorted(maps.Keys(properties))
}
