// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package graph

import (
	"fmt"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/win32"
	"github.com/Till0196/coax/internal/coaxd/win32/directshow"
	"github.com/Till0196/coax/internal/coaxd/win32/directshow/tv"
)

// One CoCreateInstance or QueryInterface plus its cast, named once each rather
// than written out at every call site.

func queryMediaEvent(graph *directshow.IGraphBuilder) (*directshow.IMediaEvent, error) {
	var unknown *win32.IUnknown
	if err := graph.QueryInterface(&directshow.IID_IMediaEvent, &unknown); err != nil {
		return nil, err
	}
	return (*directshow.IMediaEvent)(unsafe.Pointer(unknown)), nil
}

// hresultText names the failures worth naming. VFW_E_SAMPLE_TIME_NOT_SET is what
// a sink insisting on timed samples answers to a transport stream, which never
// carries one.
func hresultText(value uint32) string {
	switch value {
	case 0x80040249:
		return "VFW_E_SAMPLE_TIME_NOT_SET (the sink wanted timed samples)"
	case 0x8004020F:
		return "VFW_E_NO_ALLOCATOR"
	case 0x80040208:
		return "VFW_E_NOT_COMMITTED"
	case 0x80040211:
		return "VFW_E_CANNOT_CONNECT"
	}
	return fmt.Sprintf("HRESULT 0x%08X", value)
}

// findNode walks the tuner's BDA topology for a node offering an interface, or
// nil. The node cannot be reached directly: the filter publishes node types,
// each node type publishes the interfaces it supports, and only then can a
// control node be created for the one wanted.
//
// This asks the driver rather than the hardware, which matters on cards whose
// command interface is shared with the driver: reaching one of those while
// streaming stops the stream, while a node takes the driver's lock instead.
func findNode(filter *directshow.IBaseFilter, wanted win32.GUID) *win32.IUnknown {
	var unknown *win32.IUnknown
	if err := filter.QueryInterface(&directshow.IID_IBDA_Topology, &unknown); err != nil {
		return nil
	}
	topology := (*directshow.IBDA_Topology)(unsafe.Pointer(unknown))
	defer topology.Release()

	nodeTypes := make([]uint32, 32)
	count := uint32(len(nodeTypes))
	if err := topology.GetNodeTypes(&count, nodeTypes); err != nil {
		return nil
	}
	if count > uint32(len(nodeTypes)) {
		count = uint32(len(nodeTypes))
	}
	for _, nodeType := range nodeTypes[:count] {
		interfaces := make([]win32.GUID, 32)
		interfaceCount := uint32(len(interfaces))
		if err := topology.GetNodeInterfaces(nodeType, &interfaceCount, interfaces); err != nil {
			continue
		}
		if interfaceCount > uint32(len(interfaces)) {
			interfaceCount = uint32(len(interfaces))
		}
		for _, id := range interfaces[:interfaceCount] {
			if id != wanted {
				continue
			}
			var node *win32.IUnknown
			if err := topology.GetControlNode(0, 1, nodeType, &node); err != nil {
				continue
			}
			var found *win32.IUnknown
			err := node.QueryInterface(&wanted, &found)
			node.Release()
			if err != nil {
				continue
			}
			return found
		}
	}
	return nil
}

func findSignalStatistics(filter *directshow.IBaseFilter) *directshow.IBDA_SignalStatistics {
	found := findNode(filter, directshow.IID_IBDA_SignalStatistics)
	if found == nil {
		return nil
	}
	return (*directshow.IBDA_SignalStatistics)(unsafe.Pointer(found))
}

// findDiseqCommand is the node that puts a DiSEqC message on the lead. Not every
// driver publishes it -- a tuner with no switch, or one that switches itself
// from the tuning space's LNB information, has no reason to -- so its absence is
// a fault only when a channel asked for a position.
func findDiseqCommand(filter *directshow.IBaseFilter) *directshow.IBDA_DiseqCommand {
	found := findNode(filter, directshow.IID_IBDA_DiseqCommand)
	if found == nil {
		return nil
	}
	return (*directshow.IBDA_DiseqCommand)(unsafe.Pointer(found))
}

func findDemodulator(filter *directshow.IBaseFilter) *directshow.IBDA_DigitalDemodulator {
	found := findNode(filter, directshow.IID_IBDA_DigitalDemodulator)
	if found == nil {
		return nil
	}
	return (*directshow.IBDA_DigitalDemodulator)(unsafe.Pointer(found))
}

func createGraph() (*directshow.IGraphBuilder, error) {
	var unknown *win32.IUnknown
	if err := win32.CoCreateInstance(&clsidFilterGraph, nil, win32.CLSCTX_INPROC_SERVER,
		&directshow.IID_IGraphBuilder, &unknown); err != nil {
		return nil, err
	}
	return (*directshow.IGraphBuilder)(unsafe.Pointer(unknown)), nil
}

func createBaseFilter(clsid win32.GUID) (*directshow.IBaseFilter, error) {
	var unknown *win32.IUnknown
	if err := win32.CoCreateInstance(&clsid, nil, win32.CLSCTX_INPROC_SERVER,
		&directshow.IID_IBaseFilter, &unknown); err != nil {
		return nil, err
	}
	return (*directshow.IBaseFilter)(unsafe.Pointer(unknown)), nil
}

func bindBaseFilter(moniker *win32.IMoniker) (*directshow.IBaseFilter, error) {
	var unknown *win32.IUnknown
	if err := moniker.BindToObject(nil, nil, &directshow.IID_IBaseFilter, &unknown); err != nil {
		return nil, err
	}
	return (*directshow.IBaseFilter)(unsafe.Pointer(unknown)), nil
}

func queryTuner(filter *directshow.IBaseFilter) (*tv.ITuner, error) {
	var unknown *win32.IUnknown
	if err := filter.QueryInterface(&tv.IID_ITuner, &unknown); err != nil {
		return nil, err
	}
	return (*tv.ITuner)(unsafe.Pointer(unknown)), nil
}

func queryMediaControl(graph *directshow.IGraphBuilder) (*directshow.IMediaControl, error) {
	var unknown *win32.IUnknown
	if err := graph.QueryInterface(&directshow.IID_IMediaControl, &unknown); err != nil {
		return nil, err
	}
	return (*directshow.IMediaControl)(unsafe.Pointer(unknown)), nil
}
