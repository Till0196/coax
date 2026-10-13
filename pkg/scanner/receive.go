// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/tuner"
)

// Waiting for a lock and taking a sample off the tuner, which is what the scan
// and the discovery it starts with both do to every channel they try.

func waitForLock(ctx context.Context, device tuner.Device, timeout time.Duration, interval time.Duration, update func(time.Duration, tuner.Status)) (tuner.Status, bool, error) {
	started := time.Now()
	deadline := time.Now().Add(timeout)
	var lastStatus tuner.Status
	for {
		status, err := device.Status()
		if err != nil {
			return lastStatus, false, err
		}
		lastStatus = status
		if update != nil {
			update(time.Since(started), status)
		}
		if status.Locked {
			return status, true, nil
		}
		if time.Now().After(deadline) {
			return lastStatus, false, nil
		}
		select {
		case <-ctx.Done():
			return lastStatus, false, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func receiveSample(ctx context.Context, options Options, update func(time.Duration, int)) ([]byte, time.Duration, error) {
	return receiveSampleFor(ctx, options, settledFor(options), update)
}

// settledFor is what ends a channel's capture early, which is nothing at all
// where the caller named a time to spend. The bound still ends the capture,
// and so does the byte limit; what is given up is leaving before either.
func settledFor(options Options) func([]byte) bool {
	if options.HoldAnalysisTime {
		return func([]byte) bool { return false }
	}
	return analysisSettled
}

// receiveSampleFor is the same, for a caller after something narrower than a
// whole reading of the channel -- the satellite bootstrap wants one table and
// stops as soon as it can tell whether this transponder has it. See
// tlvProbeSettled.
func receiveSampleFor(ctx context.Context, options Options, settled func([]byte) bool, update func(time.Duration, int)) ([]byte, time.Duration, error) {
	// The device is told the bounds up front so the stream ends where the
	// sample ends and can be read to its end, which is what keeps the tuner
	// between one channel and the next.
	stream, err := options.Device.OpenStream(tuner.Bounds{
		Bytes: options.MaximumBytes,
		For:   options.AnalysisTime,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("open receiver stream: %w", err)
	}
	defer stream.Close()
	return receiveSampleFrom(ctx, options, settled, stream, update)
}

// receiveSampleFrom collects from an already opened stream, so the pacing of
// the loop can be tested without a device. The duration returned is counted
// from the first byte rather than from the open: what comes before it is the
// device settling.
func receiveSampleFrom(ctx context.Context, options Options, settled func([]byte) bool, stream tuner.Stream, update func(time.Duration, int)) ([]byte, time.Duration, error) {
	started := time.Now()
	var firstByte, lastByte time.Time
	dataDeadline := time.Now().Add(options.DataTimeout)
	var analysisDeadline time.Time
	// checked throttles the settled test, which would otherwise run on every
	// pass of a loop that turns over many times a second.
	var checked time.Time
	sample := make([]byte, 0, min(options.MaximumBytes, max(options.MinimumBytes, 64*1024)))
	// One read has to keep up with the stream: a cable or terrestrial multiplex
	// runs at several megabytes a second, and the tables that identify the
	// services repeat only every second or two, so a loop that falls behind
	// reliably loses them.
	buffer := make([]byte, 256*1024)
	for {
		read, readErr := stream.Read(ctx, buffer)
		if read > 0 {
			if analysisDeadline.IsZero() {
				firstByte = time.Now()
				analysisDeadline = firstByte.Add(options.AnalysisTime)
			}
			lastByte = time.Now()
			remaining := options.MaximumBytes - len(sample)
			if read > remaining {
				read = remaining
			}
			sample = append(sample, buffer[:read]...)
			if update != nil {
				update(time.Since(started), len(sample))
			}
		}
		if errors.Is(readErr, io.EOF) {
			// The bound was reached or the board went away; either way what is
			// here is the sample.
			break
		}
		if readErr != nil && !errors.Is(readErr, tuner.ErrWouldBlock) {
			return sample, lastByte.Sub(firstByte), fmt.Errorf("read receiver stream: %w", readErr)
		}
		now := time.Now()
		enough := len(sample) >= options.MaximumBytes ||
			(!analysisDeadline.IsZero() && !now.Before(analysisDeadline))
		if !enough && len(sample) >= options.MinimumBytes && now.Sub(checked) >= settledCheckInterval {
			// The analysis time is a bound, not a duration to be served out.
			checked = now
			enough = settled(sample)
		}
		if enough && len(sample) >= options.MinimumBytes {
			// Let the response end rather than hang up on it: hanging up on a
			// stream is what costs the tuner.
			drain(ctx, stream, buffer, options.PollInterval)
			break
		}
		if len(sample) < options.MinimumBytes && now.After(dataDeadline) {
			return sample, lastByte.Sub(firstByte), fmt.Errorf("received %d bytes; need at least %d", len(sample), options.MinimumBytes)
		}
		// The poll interval paces an idle device; waiting after a read that
		// returned data throttles the capture to the buffer size divided by
		// the interval, which is far below the rate of a real multiplex.
		if read > 0 {
			if err := ctx.Err(); err != nil {
				return sample, lastByte.Sub(firstByte), err
			}
			continue
		}
		// Said again on an idle pass, so that a channel that locked and sends
		// nothing counts the seconds up rather than sitting at whatever the
		// last read left on the line.
		if update != nil {
			update(time.Since(started), len(sample))
		}
		select {
		case <-ctx.Done():
			return sample, lastByte.Sub(firstByte), ctx.Err()
		case <-time.After(options.PollInterval):
		}
	}

	if len(sample) < options.MinimumBytes {
		return sample, lastByte.Sub(firstByte), fmt.Errorf("received %d bytes; need at least %d", len(sample), options.MinimumBytes)
	}
	return sample, lastByte.Sub(firstByte), nil
}

// settledCheckInterval is how often the sample is read to see whether it is
// already complete. The reading is a full parse, so it is not free; a second
// costs little against a capture measured in seconds.
const settledCheckInterval = time.Second

// analysisSettled reports whether a sample already holds everything the report
// will be made of: the sample is read exactly as it will be read at the end,
// and the answer is whether every stream in it has named its services and its
// network.
//
// The network table is what this is for. Everything else arrives in the first
// two or three seconds; a network table comes round every ten. A stream that
// never says one is never settled, so the capture runs to the bound.
func analysisSettled(sample []byte) bool {
	analysis := isdb.Analyze(sample, isdb.DetectFormat(sample))
	if analysis == nil {
		return false
	}
	if analysis.TSMF != nil {
		return multiplexSettled(analysis)
	}
	return streamSettled(analysis.Transport, analysis.TLV)
}

// networksSettled is the bootstrap's own question, which is not the scan's.
//
// A scan reads a channel to find out what is on it, and one section of the
// network table is enough. The bootstrap reads a transponder to find out where
// the rest of the network is, and a table split into numbered sections arrives
// one at a time: stopping at the first listed three transponders of a satellite
// carrying twelve.
func networksSettled(sample []byte) bool {
	analysis := isdb.Analyze(sample, isdb.DetectFormat(sample))
	if analysis == nil || analysis.Transport == nil {
		return false
	}
	return analysis.Transport.NetworksComplete && len(analysis.Transport.Networks) > 0
}

// multiplexSettled is the same question asked of a multiplexing frame, whose
// slots are separate streams and whose header says whether this carrier holds
// the whole of them.
func multiplexSettled(analysis *isdb.Analysis) bool {
	multiplex := analysis.TSMF
	if len(multiplex.RelativeStreams) == 0 {
		return false
	}
	if multiplex.NumberOfCarriers > 1 {
		// One of several carriers of one multiplex: this carrier holds a fraction of
		// every stream on it, so none can ever be complete from here and the header
		// is all there is to wait for. Measured, such a carrier showed its network
		// table once in 80s.
		return true
	}
	for _, slot := range multiplex.RelativeStreams {
		// A slot the header named a format for is one the report will carry;
		// one it did not is nothing this can wait for.
		if slot.Format != "" && !streamSettled(slot.Transport, slot.TLV) {
			return false
		}
	}
	return true
}

// tlvProbeSettled ends the bootstrap's look at a transponder as soon as the
// answer is known either way. The bootstrap is after a TLV network table, so a
// carrier that turns out to be transport packets has already answered --
// measured, a pass over a satellite with no TLV spent about five seconds on each
// transponder that locked, nearly all of it after the answer was in.
//
// Only a positive reading of transport packets ends it: "unknown" is a sample
// that has not started cleanly yet, a reason to keep reading.
func tlvProbeSettled(sample []byte) bool {
	if isdb.DetectFormat(sample) == "ts" {
		return true
	}
	return analysisSettled(sample)
}

// streamSettled reports whether one stream has said what it is and what is on
// it, whichever multiplexing carried it.
func streamSettled(transport *isdb.TransportInfo, tlv *isdb.TLVInfo) bool {
	switch {
	case transport != nil:
		return len(transport.Services) > 0 && len(transport.Networks) > 0
	case tlv != nil:
		return len(tlv.Services) > 0 && len(tlv.Networks) > 0
	}
	return false
}

// throughput is how fast the bytes actually arrived, which is not the bitrate
// beside it: a multiplex's bitrate comes from the sender's clock, while a slow
// reader, a slipped lock, or a dropped buffer all show up here. It is the only
// figure for a stream carrying no readable clock, and a large gap between the
// two is worth seeing.
func throughput(bytes int, took time.Duration) *float64 {
	if bytes <= 0 || took < minimumThroughputSpan {
		return nil
	}
	value := math.Round(float64(bytes)*8/took.Seconds()/1e4) / 100
	return &value
}

// minimumThroughputSpan is the shortest capture worth dividing by. A capture
// that arrived in one read has no duration to speak of, only the moment it
// was noticed.
const minimumThroughputSpan = 100 * time.Millisecond

// drainReads and drainTimeout bound the wait for a finished stream to say so.
// A bounded stream is already at its bound, so its end is a read or two away;
// an unbounded one never ends and can only be closed.
const (
	drainReads   = 8
	drainTimeout = 200 * time.Millisecond
)

// drain reads what is left of a stream that has just been asked for its last
// bytes, so that the response finishes and the connection it came on stays
// good. What arrives is thrown away: the sample already has what it needs.
func drain(ctx context.Context, stream tuner.Stream, buffer []byte, poll time.Duration) {
	deadline := time.Now().Add(drainTimeout)
	for attempt := 0; attempt < drainReads && time.Now().Before(deadline); attempt++ {
		read, err := stream.Read(ctx, buffer)
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil && !errors.Is(err, tuner.ErrWouldBlock) {
			return
		}
		if read == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(poll):
			}
		}
	}
}
