// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/hostapi"
	"github.com/Till0196/coax/internal/coaxd/receiver"
)

// readSize is one read's size, chosen to keep up with a whole multiplex rather
// than sized to a packet.
const readSize = 256 * 1024

// handleStream tunes and sends the transport stream until the client stops reading. The
// connection is the lease, so hanging up is the only way a recording ends; there is no
// request to stop.
//
//	@Summary		Tune, and hand over the transport stream
//	@Description	The endpoint everything else exists for. Takes the same parameters as /tune and then streams what the tuner receives, chunked, until the connection closes or a bound is reached.
//	@Description
//	@Description	**The connection is the lease.** The tuner is held for as long as the connection is open and given back when it closes, however it closes -- so a client that vanishes does not strand hardware.
//	@Description
//	@Description	Unless `sync=off`, the first part of the stream is checked for transport or TLV framing before anything is sent, and a tuner locked to noise is refused rather than recorded in silence.
//	@Tags			receiving
//	@Produce		video/mp2t
//	@Param			tuner			query		string	false	"which tuner, as a listing names it; any that will serve when omitted"
//	@Param			system			query		string	false	"DELIVERY_SYSTEM"
//	@Param			freq			query		string	false	"FREQUENCY, in Hz, or kHz for satellite"
//	@Param			stream_id		query		string	false	"STREAM_ID"
//	@Param			bandwidth_hz	query		string	false	"BANDWIDTH_HZ"
//	@Param			symbol_rate		query		string	false	"SYMBOL_RATE"
//	@Param			modulation		query		string	false	"MODULATION"
//	@Param			polarization	query		string	false	"POLARIZATION"
//	@Param			rolloff			query		string	false	"ROLLOFF"
//	@Param			lna				query		string	false	"LNA"
//	@Param			gain			query		string	false	"front-end gain"
//	@Param			lnb_power		query		string	false	"the supply on a satellite lead: off, low or high"
//	@Param			lnbf			query		string	false	"the low-noise block on the dish"
//	@Param			lock_timeout	query		string	false	"how long to wait for a lock, as a duration"
//	@Param			settle			query		string	false	"how long to wait after the supply comes up, as a duration"
//	@Param			begin			query		string	false	"when the stream starts: lock, or now"	Enums(lock, now)
//	@Param			bytes			query		int		false	"stop after this many bytes"
//	@Param			for				query		string	false	"stop after this long, as a duration"
//	@Param			sync			query		string	false	"check the framing before sending anything"	Enums(on, off)
//	@Param			X-Coax-Priority	header		int		false	"how much this work matters; a tuner is taken from work that asked at a lower number, never from work that asked at the same. 0 when not given"
//	@Success		200				{string}	binary							"the transport stream"
//	@Failure		400				{object}	hostapi.Failure	"a parameter could not be understood"
//	@Failure		404				{object}	hostapi.Failure	"no tuner of that name is attached"
//	@Failure		409				{object}	hostapi.Failure	"every tuner that would serve is in use"
//	@Failure		502				{object}	hostapi.Failure	"the tuner locked to something that is not a transport stream"
//	@Router			/stream [get]
func (s *Server) handleStream(writer http.ResponseWriter, request *http.Request) {
	parsed, err := hostapi.ParseRequest(request.URL.Query(), hostapi.Request{
		LockTimeout: s.options.LockTimeout,
		WaitForLock: true,
		RequireSync: true,
	})
	if err != nil {
		fail(writer, http.StatusBadRequest, hostapi.CodeInvalidParameter, err.Error())
		return
	}
	if parsed.Priority, err = priority(request); err != nil {
		fail(writer, http.StatusBadRequest, hostapi.CodeInvalidParameter, err.Error())
		return
	}
	// Settling is for a caller that wants a measurement; a stream wants to start.
	parsed.Settle = 0

	started := time.Now()
	lease, err := s.acquire(request, parsed)
	if err != nil {
		status, problem := problemFor(err)
		failWith(writer, status, problem, nil)
		return
	}

	result, problem, status := s.tune(request.Context(), lease, parsed, started)
	if problem != nil {
		failWith(writer, status, *problem, result)
		return
	}
	if parsed.WaitForLock && !result.Locked {
		// Nothing came back on this frequency. Tell the player rather than hand
		// it a connection that stays silent for ever; the measurements say
		// whether there was any signal at all.
		failWith(writer, http.StatusBadGateway, hostapi.Problem{
			Code:    hostapi.CodeNoLock,
			Message: "the tuner did not lock to this channel",
		}, result)
		return
	}

	stream, err := lease.Stream()
	if err != nil {
		failWith(writer, http.StatusBadGateway, hostapi.Problem{
			Code:    hostapi.CodeTuneFailed,
			Message: err.Error(),
		}, result)
		return
	}
	defer stream.Close()

	// Nothing has been written yet: a response that has begun cannot be turned
	// into an error, so this is the last moment the check can run.
	prefix, problem := probeSync(request.Context(), stream, parsed)
	if problem != nil {
		failWith(writer, http.StatusBadGateway, *problem, result)
		return
	}

	writer.Header().Set("Content-Type", "video/mp2t")
	// The chosen tuner, so a caller that did not name one can ask after it while
	// the stream runs.
	writer.Header().Set("X-Coax-Tuner", result.Tuner)
	writer.Header().Set("X-Coax-Backend", result.Backend)
	// The rank this stream is held at, which is what says whether it can be
	// taken away and by what. A client that sent no priority is told the zero
	// it was given rather than left to assume it.
	writer.Header().Set(hostapi.PriorityHeader, strconv.Itoa(parsed.Priority))
	if len(result.Ignored) != 0 {
		writer.Header().Set("X-Coax-Ignored", joinComma(result.Ignored))
	}
	writer.WriteHeader(http.StatusOK)

	s.copyStream(writer, request, lease.Done(), stream, prefix, bounds{count: parsed.Bytes, window: parsed.For})
}

// syncProbeBytes and syncProbeWindow bound the look at the front of a stream.
// Neither bounds a working stream, which leaves as soon as it lines up on four
// packets. They cover two failures: a lead carrying noise produces bytes at the
// tuner's full rate, so the byte count stops it, while a tuner producing
// nothing never reaches the count and the window stops it. Half a megabyte is
// generous on purpose: the first read after a tune can carry the tail of
// whatever was in the ring buffer.
const (
	syncProbeBytes  = 512 * 1024
	syncProbeWindow = 3 * time.Second
)

// probeSync reads the front of a stream and reports whether it is framed,
// handing back what it read so no byte is lost.
//
// A tuner whose lead nothing is feeding still locks -- a demodulator locks on
// the state of its own loops -- and then produces noise indefinitely. No
// hardware measurement reveals it: the lock is real, the ratio is a number, the
// packet count climbs. Only the bytes say so. Both framings are recognised.
func probeSync(ctx context.Context, stream device.Stream, parsed hostapi.Request) ([]byte, *hostapi.Problem) {
	if !parsed.RequireSync {
		return nil, nil
	}
	analysis, ok := stream.(device.StreamAnalysis)
	if !ok {
		return nil, nil
	}

	prefix := make([]byte, 0, readSize)
	buffer := make([]byte, readSize)
	deadline := time.Now().Add(syncProbeWindow)
	for len(prefix) < syncProbeBytes && time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return nil, nil
		}
		read, err := stream.Read(ctx, buffer)
		if read > 0 {
			prefix = append(prefix, buffer[:read]...)
			if analysis.Synced() {
				return prefix, nil
			}
			continue
		}
		if err != nil && !errors.Is(err, receiver.ErrWouldBlock) && !errors.Is(err, device.ErrWouldBlock) {
			// The stream ended before saying anything; that is the backend's to
			// report, so let the bytes through and let the copy find the same
			// end.
			return prefix, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil
		case <-time.After(idlePoll):
		}
	}
	if analysis.Synced() {
		return prefix, nil
	}
	counts := analysis.StreamCounts()
	if counts.Bytes == 0 {
		// Nothing arrived at all -- a different fault from producing the wrong
		// thing, and calling zero bytes "not a transport stream" would be
		// inventing a diagnosis.
		return prefix, nil
	}
	return nil, &hostapi.Problem{
		Code: hostapi.CodeNoSync,
		Message: fmt.Sprintf(
			"the tuner locked but is not producing a multiplex this can recognise: %d bytes with neither a "+
				"transport packet nor a TLV packet boundary in them. A satellite lead that nothing is feeding "+
				"looks exactly like this; if this installation has no power inserter of its own, add "+
				"lnb_power=high. Pass sync=off to take the bytes anyway",
			counts.Bytes),
	}
}

// bounds is how much of a stream was asked for; a zero bound is no bound. The
// clock starts at the first byte sent, not at the request: "half a second of it"
// means half a second of stream, not half a second a slow lock spent elsewhere.
type bounds struct {
	count  int
	window time.Duration
}

// reached reports whether a stream that has sent this much since first is done.
func (b bounds) reached(sent int, first time.Time) bool {
	if b.count != 0 && sent >= b.count {
		return true
	}
	if b.window != 0 && !first.IsZero() && time.Since(first) >= b.window {
		return true
	}
	return false
}

// room is how much more may be sent, or all of it when nothing was asked for.
func (b bounds) room(sent int) int {
	if b.count == 0 {
		return -1
	}
	return b.count - sent
}

// copyStream carries the stream to the client until one end stops or the bound
// is reached.
//
// The reader is never made to wait on the client: a board with several tuners
// feeds them all through one endpoint, so a reader that stops to write costs
// its neighbours their packets. The backend drops and counts what this one
// cannot keep up with.
//
// Returning at a bound ends the response properly, so the client reads to the
// end, keeps its connection, and keeps the tuner for the next channel.
//
// Nor is the tuner left to a client that stops reading. Each write has
// streamWriteTimeout to go through, and a lease taken away cuts short the
// write it is stuck in, so whoever took the tuner is not kept waiting on a
// client that is not there.
func (s *Server) copyStream(writer http.ResponseWriter, request *http.Request, gone <-chan struct{}, stream device.Stream, prefix []byte, limit bounds) {
	flusher, _ := writer.(http.Flusher)
	control := http.NewResponseController(writer)
	stop := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		select {
		case <-gone:
			_ = control.SetWriteDeadline(time.Now())
		case <-stop:
		}
	}()
	defer func() {
		close(stop)
		<-watched
		// The connection outlives this response and carries the next request.
		_ = control.SetWriteDeadline(time.Time{})
	}()
	buffer := make([]byte, readSize)
	pending := make([]byte, 0, s.options.WriteThreshold+readSize+len(prefix))
	// What the sync check read is the start of the stream, and goes through the
	// same bound: a caller that asked for fewer bytes than the check needed gets
	// exactly what it asked for.
	pending = append(pending, prefix...)
	if room := limit.room(0); room >= 0 && len(pending) > room {
		pending = pending[:room]
	}
	lastWrite := time.Now()
	ctx := request.Context()
	sent := 0
	var first time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-gone:
			// The board was unplugged. Closing the response is the only way to
			// say so inside a stream of transport packets.
			return
		default:
		}

		read, err := stream.Read(ctx, buffer)
		if read > 0 {
			if room := limit.room(sent + len(pending)); room >= 0 && read > room {
				read = room
			}
			pending = append(pending, buffer[:read]...)
		}
		if err != nil && !errors.Is(err, receiver.ErrWouldBlock) && !errors.Is(err, device.ErrWouldBlock) {
			return
		}

		full := len(pending) >= s.options.WriteThreshold
		overdue := len(pending) > 0 && time.Since(lastWrite) >= s.options.WriteInterval
		// The tail of a bounded stream goes out as it is, rather than waiting for
		// a threshold only more stream could reach.
		last := len(pending) > 0 && limit.reached(sent+len(pending), first)
		if full || overdue || last {
			_ = control.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
			if _, err := writer.Write(pending); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			if first.IsZero() {
				first = time.Now()
			}
			sent += len(pending)
			pending = pending[:0]
			lastWrite = time.Now()
		}
		if limit.reached(sent, first) {
			return
		}

		if read > 0 {
			// More may be waiting; sleeping now would cap the rate at one buffer
			// per interval, which a multiplex outruns.
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-gone:
			return
		case <-time.After(idlePoll):
		}
	}
}

// streamWriteTimeout is how long one write of the stream may wait on the
// client. A client still reading never comes near it; one that has stopped
// would otherwise hold the tuner for as long as its connection stays open.
const streamWriteTimeout = 30 * time.Second

// idlePoll is the wait before asking again when nothing arrived. Backends
// already wait inside a read, so this only softens one that returns at once.
const idlePoll = 10 * time.Millisecond

func joinComma(values []string) string {
	joined := ""
	for index, value := range values {
		if index != 0 {
			joined += ","
		}
		joined += value
	}
	return joined
}
