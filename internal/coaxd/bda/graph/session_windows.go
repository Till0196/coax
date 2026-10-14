// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package graph builds the DirectShow filter graph a BDA tuner streams through
// and holds it while something is reading from it.
//
// A graph is the exclusive thing about a BDA tuner: it opens the hardware, and
// a second graph cannot be built on a tuner the first one holds. What follows
// from that -- reading vendor properties through the running graph, holding the
// card against a mode switch elsewhere -- is here.
package graph

import (
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/pkg/win32/directshow"

	"github.com/Till0196/coax/internal/coaxd/bda/cards"
	"github.com/Till0196/coax/internal/coaxd/bda/sink"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
)

// Session is one running graph.
type Session struct {
	graph   *directshow.IGraphBuilder
	control *directshow.IMediaControl
	// events is the graph's event queue. A BDA graph that fails while running
	// keeps answering "running" and reports the failure only here.
	events *directshow.IMediaEvent
	// tuner is the tuner filter, kept because its input pin carries the card's
	// vendor properties: the demodulator mode and the raw I2C bus.
	tuner    *directshow.IBaseFilter
	pin      *directshow.IPin
	property *vendors.PinProperties
	// writer is the filter the transport stream is written through, and spool
	// is what it writes to.
	writer *sink.Sink
	spool  *Spool
	// eventsMutex keeps the event queue from being released under a drain. A
	// stream drains it from its reader's goroutine, and the graph may be torn
	// down from the COM thread at the same moment.
	eventsMutex sync.Mutex
	// card is the card whose demodulator mode this session depends on, held while
	// the graph runs so a mode switch elsewhere cannot pull the standard away.
	card     string
	released bool
	// statistics is the tuner's signal statistics node, or nil when the driver
	// exposes none. Held for the session's life because it can only be found
	// through the filter.
	statistics *directshow.IBDA_SignalStatistics
	dataSince  time.Time
	// capture is the receiver component the stream is carried through, or empty
	// if the tuner filter feeds the graph directly. A multi-demodulator card has
	// one component each, and taking the wrong one runs and carries nothing.
	capture string
}

// Spool is what the captured stream is being written to.
func (session *Session) Spool() *Spool { return session.spool }

// Tuner is the tuner filter this graph was built on, through which a vendor's
// own properties are asked.
func (session *Session) Tuner() *directshow.IBaseFilter { return session.tuner }

// Demodulator takes the tuner's demodulator node, or nil where the driver
// offers none. The caller releases it.
//
// Unlike the statistics node beside it, this one is made on demand and released
// straight after: a BDA control node is a live handle on part of the graph, and
// holding it for a session's life stopped the stream on a TBS 6205se after
// about 770KB.
func (session *Session) Demodulator() *directshow.IBDA_DigitalDemodulator {
	if session.tuner == nil {
		return nil
	}
	return findDemodulator(session.tuner)
}

// SignalStatistics is the tuner's statistics node, or nil where the driver
// publishes none.
func (session *Session) SignalStatistics() *directshow.IBDA_SignalStatistics {
	return session.statistics
}

// Capture names the receiver component the stream is carried through, or "" if
// the tuner filter feeds the graph directly.
func (session *Session) Capture() string { return session.capture }

// WriteError reports a failure to write the captured stream out.
func (session *Session) WriteError() error { return session.writer.WriteError() }

// Progress reports how much has reached the sink, and whether data has been
// arriving long enough to mean anything. The first byte's arrival is stamped
// here, so asking twice does not restart the clock.
func (session *Session) Progress() (written int64, warmedUp bool) {
	written = session.writer.BytesWritten()
	if written > 0 && session.dataSince.IsZero() {
		session.dataSince = time.Now()
	}
	warmedUp = !session.dataSince.IsZero() && time.Since(session.dataSince) >= time.Second
	return written, warmedUp
}

// Written is how much the sink has taken since the graph came up, across every
// file it has written to. It is what says whether the card is still carrying a
// stream, which a graph's own state does not.
func (session *Session) Written() int64 { return session.writer.BytesWritten() }

// Recapture points the graph at a new spool and drops the old one, while it
// goes on running. It is what lets one graph serve a run of channels.
//
// limit bounds what the new spool may take, for the stretches when nothing is
// reading it -- a graph is kept up between channels, and a card carrying 39
// Mbps into files nobody reads fills a disk. Zero is unbounded. What the sink
// counts is unaffected either way.
func (session *Session) Recapture(limit int64) error {
	spool, err := NewSpool(limit)
	if err != nil {
		return err
	}
	session.writer.Attach(spool)
	old := session.spool
	session.spool = spool
	if old != nil {
		_ = old.Remove()
	}
	return nil
}

// The graph events that mean it has given up. DirectShow reports them here and
// nowhere else: IMediaControl keeps answering State_Running long after streaming
// has stopped.
const (
	ecComplete         = 0x01
	ecUserAbort        = 0x02
	ecErrorAbort       = 0x03
	ecStreamErrorStop  = 0x04
	ecStreamErrorStill = 0x05
	ecErrorStillPlay   = 0x06
)

// DrainEvents empties the graph's event queue and returns the first failure, or
// nil. Every event is taken off whether or not it is reported: an unemptied
// queue eventually stops the graph.
func (session *Session) DrainEvents() error {
	session.eventsMutex.Lock()
	defer session.eventsMutex.Unlock()
	if session.events == nil {
		return nil
	}
	var failure error
	for {
		var code int32
		var first, second uintptr
		if err := session.events.GetEvent(&code, &first, &second, 0); err != nil {
			return failure
		}
		switch code {
		case ecErrorAbort, ecStreamErrorStop, ecStreamErrorStill, ecErrorStillPlay:
			if failure == nil {
				failure = fmt.Errorf("the graph stopped streaming: event 0x%02X, %s", code, hresultText(uint32(first)))
			}
		case ecUserAbort:
			if failure == nil {
				failure = fmt.Errorf("the graph was aborted")
			}
		}
	}
}

// Close tears the graph down and lets go of the card.
func (session *Session) Close() {
	if !session.released {
		cards.Release(session.card)
		session.released = true
	}
	if session.statistics != nil {
		session.statistics.Release()
		session.statistics = nil
	}
	if session.control != nil {
		_ = session.control.Stop()
		session.control.Release()
		session.control = nil
	}
	// The sink stops writing before the file closes, so a sample in flight during
	// teardown cannot reach a closed file.
	if session.writer != nil {
		session.writer.Detach()
	}
	session.eventsMutex.Lock()
	if session.events != nil {
		session.events.Release()
		session.events = nil
	}
	session.eventsMutex.Unlock()
	if session.property != nil {
		session.property.Release()
		session.property = nil
	}
	if session.pin != nil {
		session.pin.Release()
		session.pin = nil
	}
	if session.tuner != nil {
		session.tuner.Release()
		session.tuner = nil
	}
	if session.graph != nil {
		session.graph.Release()
		session.graph = nil
	}
	if session.writer != nil {
		session.writer.Release()
		session.writer = nil
	}
	if session.spool != nil {
		_ = session.spool.Remove()
	}
}

// VendorProperty is the vendor property set on the tuner's input pin, reached
// through this session's running graph -- the only way while the tuner is in
// use, since a KS pin has no handle until connected. The pin is found once and
// kept for the session.
func (session *Session) VendorProperty() (*vendors.PinProperties, error) {
	if session.property != nil {
		return session.property, nil
	}
	if session.tuner == nil {
		return nil, fmt.Errorf("the graph has no tuner filter")
	}
	pin := findPin(session.tuner, directshow.PINDIR_INPUT)
	if pin == nil {
		return nil, fmt.Errorf("the BDA tuner has no input pin")
	}
	property, err := vendors.Pin(pin)
	if err != nil {
		pin.Release()
		return nil, err
	}
	session.pin = pin
	session.property = property
	return session.property, nil
}
