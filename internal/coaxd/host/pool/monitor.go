// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

import (
	"context"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/mpegts"
	"github.com/Till0196/coax/internal/coaxd/tlv"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// monitored is a tuner's stream with an analyser watching what goes through it.
// Every stream the daemon hands out is wrapped in one: a demodulator reports its
// lock and a driver what it threw away, but only the bytes say whether a tuner
// is producing a multiplex or noise, and only their counters say whether the
// recording has holes. The cost is one pass, against a copy already happening.
type monitored struct {
	device.Stream

	// mutex guards the analyser, which is written by whoever is reading the
	// stream and read by a listing that is not.
	mutex    sync.Mutex
	analyser analyser
}

// analyser is what the two multiplex formats have in common: bytes go in, and it
// says whether they line up and what was in them.
type analyser interface {
	Feed(data []byte)
	Synced() bool
	counts() device.StreamCounts
}

// watch puts an analyser on a stream. The format can only be guessed from the
// channel: the Linux DVB API has no delivery system number for ISDB-S3, so an ISDB-S3
// channel is written as ISDBS with a TLV stream identifier and may produce
// either a transport stream or MMT over TLV. Backends that do spell ISDBS3 give
// a hint. Both are tried and the stream decides; see detecting.
func watch(stream device.Stream, deliverySystem string) *monitored {
	return &monitored{
		Stream:   stream,
		analyser: newDetecting(tuning.StreamFormat(deliverySystem)),
	}
}

// detecting reads a stream as both formats until one lines up, then keeps that
// one. Trying both costs one extra pass over the front of the stream and ends as
// soon as either finds a boundary -- within four packets, under a kilobyte of a
// transport stream. Guessing wrong would report a working TLV broadcast as noise
// and refuse to hand it over.
type detecting struct {
	// first is the format the channel suggested, which is tried first each
	// round. It is a preference and not a decision.
	first  string
	ts     *tsAnalyser
	tlv    *tlvAnalyser
	chosen analyser
}

func newDetecting(hint string) *detecting {
	return &detecting{
		first: hint,
		ts:    &tsAnalyser{Analyser: mpegts.New()},
		tlv:   &tlvAnalyser{Analyser: tlv.New()},
	}
}

func (d *detecting) Feed(data []byte) {
	if d.chosen != nil {
		d.chosen.Feed(data)
		return
	}
	d.ts.Feed(data)
	d.tlv.Feed(data)
	// Both have seen every byte, so whichever is kept counts the whole stream.
	order := []analyser{d.ts, d.tlv}
	if d.first == tuning.FormatTLV {
		order = []analyser{d.tlv, d.ts}
	}
	for _, candidate := range order {
		if candidate.Synced() {
			d.chosen = candidate
			d.ts, d.tlv = nil, nil
			return
		}
	}
}

func (d *detecting) Synced() bool { return d.chosen != nil && d.chosen.Synced() }

func (d *detecting) counts() device.StreamCounts {
	if d.chosen != nil {
		return d.chosen.counts()
	}
	// Nothing has lined up, so no format or packet count would mean anything. The
	// byte count is the same either way and separates a tuner producing nothing
	// from one producing something unrecognisable.
	return device.StreamCounts{Bytes: d.ts.counts().Bytes}
}

func (m *monitored) Read(ctx context.Context, buffer []byte) (int, error) {
	read, err := m.Stream.Read(ctx, buffer)
	if read > 0 {
		m.mutex.Lock()
		m.analyser.Feed(buffer[:read])
		m.mutex.Unlock()
	}
	return read, err
}

func (m *monitored) Synced() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.analyser.Synced()
}

func (m *monitored) StreamCounts() device.StreamCounts {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.analyser.counts()
}

// Dropped forwards the underlying stream's count; without it the wrapper would
// hide the capability from the type assertion that looks for it.
func (m *monitored) Dropped() uint64 {
	counter, ok := m.Stream.(device.StreamStats)
	if !ok {
		return 0
	}
	return counter.Dropped()
}

// counts reports whether the stream underneath keeps a dropped-packet count: a
// board that cannot say is not a board that dropped nothing.
func (m *monitored) counts() bool {
	_, ok := m.Stream.(device.StreamStats)
	return ok
}

// tsAnalyser and tlvAnalyser render each format's counts in shared terms. Fields
// a format has no answer for stay at zero, which is what Format is there for.
type tsAnalyser struct{ *mpegts.Analyser }

func (a *tsAnalyser) counts() device.StreamCounts {
	stats := a.Stats()
	return device.StreamCounts{
		Format:          tuning.FormatTS,
		Bytes:           stats.Bytes,
		Packets:         stats.Packets,
		Skipped:         stats.Skipped,
		SyncLosses:      stats.SyncLosses,
		Discontinuities: stats.Discontinuities,
		Nulls:           stats.Nulls,
		Errors:          stats.Errors,
		Scrambled:       stats.Scrambled,
	}
}

type tlvAnalyser struct{ *tlv.Analyser }

func (a *tlvAnalyser) counts() device.StreamCounts {
	stats := a.Stats()
	return device.StreamCounts{
		Format:          tuning.FormatTLV,
		Bytes:           stats.Bytes,
		Packets:         stats.Packets,
		Skipped:         stats.Skipped,
		SyncLosses:      stats.SyncLosses,
		Discontinuities: stats.Discontinuities,
		Nulls:           stats.Nulls,
		Control:         stats.Control,
		IPv4:            stats.IPv4,
		IPv6:            stats.IPv6,
		CompressedIP:    stats.CompressedIP,
		Undecodable:     stats.Undecodable,
		Forgotten:       stats.Forgotten,
	}
}
