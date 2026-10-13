// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package graph

import (
	"fmt"
	"strings"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/win32"
	"github.com/Till0196/coax/internal/coaxd/win32/directshow"

	"github.com/Till0196/coax/internal/coaxd/bda/filters"
)

// attachTransportInformation hangs a demultiplexer and a transport information
// filter off the tee, the shape BDA expects. Whatever cannot be built is left
// out rather than failing the graph.
func attachTransportInformation(graph *directshow.IGraphBuilder, tee *directshow.IBaseFilter) error {
	demux, err := createBaseFilter(clsidMPEG2Demux)
	if err != nil {
		return fmt.Errorf("create MPEG-2 demultiplexer: %w", err)
	}
	defer demux.Release()
	if err := graph.AddFilter(demux, "MPEG-2 Demultiplexer"); err != nil {
		return fmt.Errorf("add MPEG-2 demultiplexer: %w", err)
	}
	if err := connectFilters(graph, tee, demux); err != nil {
		_ = graph.RemoveFilter(demux)
		return fmt.Errorf("connect the demultiplexer: %w", err)
	}

	information, err := createBaseFilter(clsidMPEG2TransportInformationFilter)
	if err != nil {
		return fmt.Errorf("create MPEG-2 transport information filter: %w", err)
	}
	defer information.Release()
	if err := graph.AddFilter(information, "MPEG2 Transport Information Filter"); err != nil {
		return fmt.Errorf("add transport information filter: %w", err)
	}
	if err := connectFilters(graph, demux, information); err != nil {
		_ = graph.RemoveFilter(information)
		return fmt.Errorf("connect the transport information filter: %w", err)
	}
	return nil
}

// connectCapture puts a receiver component between the tuner and the rest of the
// graph, and reports which one it used. Which one matters: a multi-demodulator
// card publishes one component per demodulator, and a graph that takes the wrong
// one builds, runs, and carries nothing.
func connectCapture(graph *directshow.IGraphBuilder, tuner *directshow.IBaseFilter, writer *directshow.IBaseFilter, captures []filters.Moniker) (string, error) {
	if len(captures) == 0 {
		return "", fmt.Errorf("no BDA receiver component was found")
	}
	var failures []string
	for _, candidate := range captures {
		capture, err := bindBaseFilter(candidate.Moniker)
		if err != nil {
			failures = append(failures, candidate.Name+": bind failed")
			continue
		}
		if err = graph.AddFilter(capture, candidate.Name); err == nil {
			if err = connectFilters(graph, tuner, capture); err == nil {
				err = connectFilters(graph, capture, writer)
			}
		}
		if err == nil {
			capture.Release()
			return candidate.Name, nil
		}
		_ = graph.RemoveFilter(capture)
		capture.Release()
		failures = append(failures, candidate.Name+": "+err.Error())
	}
	return "", fmt.Errorf("no receiver component connected (%s)", strings.Join(failures, "; "))
}

func connectFilters(graph *directshow.IGraphBuilder, source *directshow.IBaseFilter, destination *directshow.IBaseFilter) error {
	outputs, err := filterPins(source, directshow.PINDIR_OUTPUT)
	if err != nil {
		return err
	}
	defer releasePins(outputs)
	inputs, err := filterPins(destination, directshow.PINDIR_INPUT)
	if err != nil {
		return err
	}
	defer releasePins(inputs)
	if len(outputs) == 0 || len(inputs) == 0 {
		return fmt.Errorf("source has %d output pins and destination has %d input pins", len(outputs), len(inputs))
	}
	var lastErr error
	for _, output := range outputs {
		for _, input := range inputs {
			if err := graph.Connect(output, input); err == nil {
				return nil
			} else {
				lastErr = err
			}
		}
	}
	return lastErr
}

func filterPins(filter *directshow.IBaseFilter, direction directshow.PIN_DIRECTION) ([]*directshow.IPin, error) {
	var enumerator *directshow.IEnumPins
	if err := filter.EnumPins(&enumerator); err != nil {
		return nil, err
	}
	defer enumerator.Release()
	var result []*directshow.IPin
	for {
		pins := make([]*directshow.IPin, 1)
		var fetched uint32
		_, err := enumerator.Next(pins, &fetched)
		if err != nil {
			releasePins(result)
			return nil, err
		}
		if fetched == 0 || pins[0] == nil {
			break
		}
		var pinDirection directshow.PIN_DIRECTION
		if err := pins[0].QueryDirection(&pinDirection); err != nil {
			pins[0].Release()
			releasePins(result)
			return nil, err
		}
		if pinDirection == direction {
			result = append(result, pins[0])
		} else {
			pins[0].Release()
		}
	}
	return result, nil
}

func releasePins(pins []*directshow.IPin) {
	for _, pin := range pins {
		pin.Release()
	}
}

// clearGraphClock tells the graph to run without a reference clock.
func clearGraphClock(graph *directshow.IGraphBuilder) error {
	var unknown *win32.IUnknown
	if err := graph.QueryInterface(&directshow.IID_IMediaFilter, &unknown); err != nil {
		return err
	}
	filter := (*directshow.IMediaFilter)(unsafe.Pointer(unknown))
	defer filter.Release()
	return filter.SetSyncSource(nil)
}
