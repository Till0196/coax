// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package graph

import (
	"fmt"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/win32/directshow"

	"github.com/Till0196/coax/internal/coaxd/bda/comutil"
)

// describeGraph lists the filters in a graph and how many of their pins are
// connected, for an error message about a graph that would not run.
func describeGraph(graph *directshow.IGraphBuilder) string {
	var enumerator *directshow.IEnumFilters
	if err := graph.EnumFilters(&enumerator); err != nil {
		return "the graph could not be enumerated"
	}
	defer enumerator.Release()

	var described []string
	for {
		filters := make([]*directshow.IBaseFilter, 1)
		var fetched uint32
		_, err := enumerator.Next(filters, &fetched)
		if err != nil || fetched == 0 || filters[0] == nil {
			break
		}
		filter := filters[0]
		name := "?"
		var info directshow.FILTER_INFO
		if err := filter.QueryFilterInfo(&info); err == nil {
			name = comutil.UTF16BufferString(info.AchName[:])
			if info.PGraph != nil {
				info.PGraph.Release()
			}
		}
		described = append(described, fmt.Sprintf("%s(in %d/%d, out %d/%d)",
			name,
			connectedPins(filter, directshow.PINDIR_INPUT), pinCount(filter, directshow.PINDIR_INPUT),
			connectedPins(filter, directshow.PINDIR_OUTPUT), pinCount(filter, directshow.PINDIR_OUTPUT)))
		filter.Release()
	}
	return strings.Join(described, " ")
}

func pinCount(filter *directshow.IBaseFilter, direction directshow.PIN_DIRECTION) int {
	pins, err := filterPins(filter, direction)
	if err != nil {
		return -1
	}
	defer releasePins(pins)
	return len(pins)
}

func connectedPins(filter *directshow.IBaseFilter, direction directshow.PIN_DIRECTION) int {
	pins, err := filterPins(filter, direction)
	if err != nil {
		return -1
	}
	defer releasePins(pins)
	connected := 0
	for _, pin := range pins {
		var other *directshow.IPin
		if err := pin.ConnectedTo(&other); err == nil && other != nil {
			connected++
			other.Release()
		}
	}
	return connected
}
