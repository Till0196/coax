// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/hostapi"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
	"github.com/Till0196/coax/internal/coaxd/receiver"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// handleTuners lists what is attached. Nothing is opened unless the request asks for
// it: describing a board fully means powering it up and loading firmware into several
// models, which a listing is not a request to do.
//
//	@Summary		What is attached
//	@Description	Every board and the tuners on it.
//	@Description
//	@Description	Nothing is opened unless asked for. Opening a board powers it and on some models loads firmware, which is not a reasonable thing to do to answer a question about what is plugged in -- so the configuration is answered from what each backend knows about the model, and marked `probed: false`. With `open=1` each board is really opened and the answer is marked `probed: true`.
//	@Tags			tuners
//	@Produce		json
//	@Param			open	query		bool	false	"open each board to confirm what is on it"
//	@Success		200		{array}		hostapi.Board
//	@Failure		500		{object}	hostapi.Failure
//	@Router			/tuners [get]
func (s *Server) handleTuners(writer http.ResponseWriter, request *http.Request) {
	boards, err := s.pool.Boards()
	if err != nil {
		status, problem := problemFor(err)
		failWith(writer, status, problem, nil)
		return
	}
	var failures map[string]string
	if request.URL.Query().Get("open") == "1" {
		// Boards are re-read afterwards because opening them is what fills in what
		// they carry.
		failures = s.probe(request.Context(), boards)
		if reread, err := s.pool.Boards(); err == nil {
			boards = reread
		}
	}
	listing := make([]hostapi.Board, 0, len(boards))
	for _, board := range boards {
		described := s.describeBoard(board)
		described.ProbeError = failures[board.Name]
		listing = append(listing, described)
	}
	writeJSON(writer, http.StatusOK, listing)
}

// probe opens each board so that its tuners can be described from the hardware
// rather than from its identifiers, and reports which would not open: missing
// firmware, a missing permission, hardware someone else is holding.
//
// A board whose driver allows one open and no more is left alone: opening it
// takes it away from everything else on the machine, and asking what is
// attached should never be why a recorder could not start.
func (s *Server) probe(ctx context.Context, boards []pool.BoardInfo) map[string]string {
	failures := make(map[string]string)
	for _, board := range boards {
		if board.Exclusive {
			continue
		}
		stop, err := s.pool.WatchContext(ctx, board.Name)
		if err != nil {
			s.options.Report("probe " + board.Name + ": " + err.Error())
			failures[board.Name] = err.Error()
			continue
		}
		stop()
	}
	return failures
}

// describeBoard renders one board for a listing.
func (s *Server) describeBoard(board pool.BoardInfo) hostapi.Board {
	described := hostapi.Board{
		Name:        board.Name,
		Naming:      board.Kind.String(),
		Stable:      board.Stable,
		Backend:     board.Backend,
		Description: board.Description,
		Location:    board.Location,
		Serial:      board.Serial,
		Port:        board.Port,
		Deliveries:  board.Deliveries,
		Open:        board.Open,
		Probed:      board.Probed,
		Tuners:      make([]hostapi.Tuner, 0, len(board.Tuners)),
	}
	for _, tuner := range board.Tuners {
		one := hostapi.Tuner{
			ID:         tuner.ID,
			Index:      tuner.Index,
			Name:       tuner.Name,
			Deliveries: tuner.Deliveries,
			Mode:       tuner.Mode,
			Settings:   settings(tuner.Settings),
			Controls:   controls(tuner.Controls),
			Leased:     tuner.Leased,
			Priority:   heldAt(tuner),
		}
		// A tuner in use is asked what it is doing: the board is already open, and what
		// was recorded when it was opened predates any tune. One nobody holds is left
		// alone, since opening a board powers hardware up.
		if tuner.Leased {
			s.measureInto(&one, tuner.ID)
		}
		described.Tuners = append(described.Tuners, one)
	}
	return described
}

// handleTuner reports one tuner, including what it can measure right now. It works
// while the tuner is streaming, which is the point: a scan holding a channel open
// asks for the settled carrier-to-noise figure. Reading a status does not take the
// tuner.
//
//	@Summary		One tuner, and what it is doing
//	@Description	Lock, carrier-to-noise, input level, quality and dropped packets, plus the statistics of the multiplex leaving it when it is streaming.
//	@Description
//	@Description	It may be asked while the tuner is streaming; that is what it is for. A tuner that is not streaming has no `stream` key at all rather than one full of zeroes, because "saw no packets" and "saw no broken packets" are opposite answers.
//	@Tags			tuners
//	@Produce		json
//	@Param			id	path		string	true	"the tuner, as a listing names it"
//	@Success		200	{object}	hostapi.Tuner
//	@Failure		404	{object}	hostapi.Failure	"no tuner of that name is attached"
//	@Router			/tuners/{id} [get]
func (s *Server) handleTuner(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	boards, err := s.pool.Boards()
	if err != nil {
		status, problem := problemFor(err)
		failWith(writer, status, problem, nil)
		return
	}
	for _, board := range boards {
		for _, tuner := range board.Tuners {
			if tuner.ID != id {
				continue
			}
			described := hostapi.Tuner{
				ID:         tuner.ID,
				Index:      tuner.Index,
				Name:       tuner.Name,
				Deliveries: tuner.Deliveries,
				Mode:       tuner.Mode,
				Settings:   settings(tuner.Settings),
				Controls:   controls(tuner.Controls),
				Leased:     tuner.Leased,
				Priority:   heldAt(tuner),
			}
			s.measureInto(&described, id)
			writeJSON(writer, http.StatusOK, described)
			return
		}
	}
	fail(writer, http.StatusNotFound, hostapi.CodeUnknownTuner, "no tuner is called "+id)
}

// measureInto fills in what the hardware can say about a tuner already in use.
// A tuner nobody has tuned has nothing to report, and opening its board to find
// that out would power up hardware to learn nothing.
func (s *Server) measureInto(described *hostapi.Tuner, id string) {
	reading, ok := s.pool.Measure(id)
	if !ok {
		return
	}
	locked := reading.Status.Locked
	described.Locked = &locked
	described.Status = reading.Status.Names
	described.CNR = measurement(reading.Status.CNR)
	described.Strength = measurement(reading.Status.Strength)
	described.Quality = measurement(reading.Status.Quality)
	if reading.Dropped != nil {
		described.Dropped = reading.Dropped
	}
	described.Stream = streamCounts(reading.Stream)
	// What the hardware says now replaces what it said when it was opened, which was
	// before anything had been tuned.
	if rendered := settings(reading.Settings); rendered != nil {
		described.Settings = rendered
	}
	described.Tuned = tuned(reading.Tuned)
}

// controls renders what a channel may set on a tuner, nothing where there is
// nothing to set.
func controls(known device.Controls) *hostapi.Controls {
	if known.LNB == nil && known.Gain == nil {
		return nil
	}
	rendered := &hostapi.Controls{}
	if lnb := known.LNB; lnb != nil {
		levels := []string{"low", "high"}
		if lnb.Off {
			levels = append([]string{"off"}, levels...)
		}
		rendered.LNB = &hostapi.LNBControl{Levels: levels, Low: lnb.Low, High: lnb.High, Shared: lnb.Shared}
	}
	if gain := known.Gain; gain != nil {
		kind := "steps"
		if gain.Switch() {
			kind = "switch"
		}
		rendered.Gain = &hostapi.GainControl{
			Kind:       kind,
			Decibels:   append([]float64(nil), gain.Decibels...),
			Deliveries: append([]string(nil), gain.Deliveries...),
		}
	}
	return rendered
}

// heldAt is the priority the work on a tuner asked at, and nothing for one nobody
// holds. Zero is a real priority, so a free tuner cannot be reported as zero.
func heldAt(tuner pool.TunerInfo) *int {
	if !tuner.Leased {
		return nil
	}
	priority := tuner.Priority
	return &priority
}

// streamCounts renders what the analyser saw, for a tuner that is streaming. A
// tuner that is not reports nothing rather than zeroes: zero packets seen and
// zero packets wrong are opposite readings.
func streamCounts(counts *device.StreamCounts) *hostapi.StreamCounts {
	if counts == nil {
		return nil
	}
	return &hostapi.StreamCounts{
		Format:          counts.Format,
		Bytes:           counts.Bytes,
		Packets:         counts.Packets,
		Skipped:         counts.Skipped,
		SyncLosses:      counts.SyncLosses,
		Errors:          counts.Errors,
		Discontinuities: counts.Discontinuities,
		Scrambled:       counts.Scrambled,
		Nulls:           counts.Nulls,
		Control:         counts.Control,
		IPv4:            counts.IPv4,
		IPv6:            counts.IPv6,
		CompressedIP:    counts.CompressedIP,
		Undecodable:     counts.Undecodable,
		Forgotten:       counts.Forgotten,
	}
}

// settings renders what a tuner said about how it is tuned.
func settings(values []device.Setting) []hostapi.Setting {
	if len(values) == 0 {
		return nil
	}
	rendered := make([]hostapi.Setting, 0, len(values))
	for _, value := range values {
		rendered = append(rendered, hostapi.Setting{Name: value.Name, Value: value.Value})
	}
	return rendered
}

// tuned renders the channel a tuner is set to, in a settled order so that two
// readings of the same channel read the same. Nothing for a tuner that has not
// tuned: a lease is taken before the first tune.
func tuned(properties map[string]string) []hostapi.Setting {
	if len(properties) == 0 {
		return nil
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	rendered := make([]hostapi.Setting, 0, len(names))
	for _, name := range names {
		rendered = append(rendered, hostapi.Setting{Name: name, Value: properties[name]})
	}
	return rendered
}

// measurement renders a reading that a backend may not have been able to take.
// Zero is a real reading, so what is absent has to be absent rather than zero.
func measurement(reading device.Measurement) *float64 {
	if !reading.Known {
		return nil
	}
	value := reading.Value
	return &value
}

// handleTune sets a tuner to a channel and reports what came back.
//
// Not locking is an answer, not a failure: a scan expects most frequencies to be
// empty and wants their signal strength too, an empty frequency and a strong signal
// that will not decode being different problems.
//
// The tuner stays held by the connection afterwards, because a scan works down a
// list on one connection.
//
//	@Summary		Tune, and say what happened
//	@Description	Takes a tuner, tunes it, waits for a lock and answers what it found. **It does not return the transport stream**; it is what a scan uses to ask whether a channel is there.
//	@Description
//	@Description	Case decides what a parameter is. A name in capitals is a channel-file property, passed through exactly as written, so a section of a channel file can be handed over as it stands. A name in lower case is a shorthand or one of the controls, and an unknown one in lower case is refused rather than ignored.
//	@Tags			receiving
//	@Produce		json
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
//	@Param			gain			query		string	false	"front-end gain: auto, min, max, or a level with its unit such as +13dB"
//	@Param			lnb_power		query		string	false	"the supply on a satellite lead: off, low or high. Nothing is fed when it is not given"
//	@Param			lnbf			query		string	false	"the low-noise block on the dish, when freq is a downlink frequency"
//	@Param			lock_timeout	query		string	false	"how long to wait for a lock, as a duration"
//	@Param			settle			query		string	false	"how long to wait after the supply comes up, as a duration"
//	@Param			X-Coax-Priority	header		int		false	"how much this work matters; a tuner is taken from work that asked at a lower number, never from work that asked at the same. 0 when not given"
//	@Success		200				{object}	hostapi.TuneResult
//	@Failure		400				{object}	hostapi.Failure	"a parameter could not be understood"
//	@Failure		404				{object}	hostapi.Failure	"no tuner of that name is attached"
//	@Failure		409				{object}	hostapi.Failure	"every tuner that would serve is in use"
//	@Router			/tune [get]
func (s *Server) handleTune(writer http.ResponseWriter, request *http.Request) {
	parsed, err := hostapi.ParseRequest(request.URL.Query(), hostapi.Request{
		LockTimeout: s.options.LockTimeout,
		Settle:      s.options.Settle,
	})
	if err != nil {
		fail(writer, http.StatusBadRequest, hostapi.CodeInvalidParameter, err.Error())
		return
	}
	if parsed.Priority, err = priority(request); err != nil {
		fail(writer, http.StatusBadRequest, hostapi.CodeInvalidParameter, err.Error())
		return
	}

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
	writeJSON(writer, http.StatusOK, result)
}

// acquire finds the tuner a request should use, reusing one the connection
// already holds when it will serve.
func (s *Server) acquire(request *http.Request, parsed hostapi.Request) (*pool.Lease, error) {
	return s.pool.LeaseContext(request.Context(), pool.Want{
		TunerID:        parsed.Tuner,
		DeliverySystem: parsed.Properties["DELIVERY_SYSTEM"],
		Owner:          owner(request),
		Priority:       parsed.Priority,
	})
}

// relativeStreamer is a tuner that names a satellite stream by its place on the
// transponder rather than by its identifier. Nearly no backend is one -- only the
// Earthsoft character devices -- so it is asked for rather than required.
type relativeStreamer interface {
	RelativeStreams() int
}

func relativeStreams(device any) int {
	if selector, ok := device.(relativeStreamer); ok {
		return selector.RelativeStreams()
	}
	return 0
}

// tune sets the channel and waits for the demodulator to make up its mind.
func (s *Server) tune(ctx context.Context, lease *pool.Lease, parsed hostapi.Request, started time.Time) (*hostapi.TuneResult, *hostapi.Problem, int) {
	result := &hostapi.TuneResult{
		Tuner:           lease.ID(),
		Backend:         lease.Backend(),
		RelativeStreams: relativeStreams(lease.Device()),
		Ignored:         parsed.Ignored,
	}

	if s.alreadyTuned(lease, parsed) {
		// The tuner is on this channel and locked to it. A scan measures a channel and
		// then opens its stream, which names the same channel again; tuning a second
		// time would cost the acquisition twice.
		status, _ := lease.Device().Status()
		result.Locked = true
		result.Status = status.Names
		result.CNR = measurement(status.CNR)
		result.Strength = measurement(status.Strength)
		result.Quality = measurement(status.Quality)
		result.ElapsedMS = time.Since(started).Milliseconds()
		return result, nil, http.StatusOK
	}

	if err := lease.Device().Tune(parsed.Properties); err != nil {
		lease.Forget()
		result.ElapsedMS = time.Since(started).Milliseconds()
		// Neither a frequency the tuner cannot reach nor an LNB supply it cannot
		// provide is a fault in the tuner or in the request: the two simply do not go
		// together. Both are reported that way, so a caller tries the next tuner.
		if errors.Is(err, tuning.ErrNoLNBSwitch) {
			return result, &hostapi.Problem{
				Code:    hostapi.CodeUnsupported,
				Reason:  hostapi.ReasonLNBSupply,
				Message: err.Error(),
			}, http.StatusConflict
		}
		if errors.Is(err, tuning.ErrNoGain) {
			return result, &hostapi.Problem{
				Code:    hostapi.CodeUnsupported,
				Reason:  hostapi.ReasonGain,
				Message: err.Error(),
			}, http.StatusConflict
		}
		if errors.Is(err, receiver.ErrOutOfRange) {
			return result, &hostapi.Problem{
				Code:    hostapi.CodeUnsupported,
				Reason:  hostapi.ReasonOutOfRange,
				Message: err.Error(),
			}, http.StatusConflict
		}
		if pool.IsGone(err) {
			return result, &hostapi.Problem{Code: hostapi.CodeDeviceGone, Message: err.Error()}, http.StatusBadGateway
		}
		return result, &hostapi.Problem{Code: hostapi.CodeTuneFailed, Message: err.Error()}, http.StatusBadGateway
	}

	status, locked := s.waitForLock(ctx, lease, parsed.LockTimeout)
	if locked && parsed.Settle > 0 {
		// A demodulator reports its lock before it has a carrier-to-noise figure to go
		// with it, so holding the channel a moment and reading again is what makes the
		// number appear at all.
		if settled, ok := s.settle(ctx, lease, parsed.Settle); ok {
			status = settled
		}
	}

	result.Locked = locked
	result.Status = status.Names
	result.CNR = measurement(status.CNR)
	result.Strength = measurement(status.Strength)
	result.Quality = measurement(status.Quality)
	result.ElapsedMS = time.Since(started).Milliseconds()
	// The channel is remembered even when no lock has been seen yet: a caller that
	// does its own waiting asks for the same channel again when it opens the
	// stream, and tuning a second time there costs the acquisition twice.
	// alreadyTuned still reads the lock before it skips a tune.
	lease.Remember(parsed.Properties)
	return result, nil, http.StatusOK
}

// alreadyTuned reports whether the request asks for the channel the tuner is
// already locked to. Both halves matter: a tuner that has lost its lock must be
// tuned again, and this is the only way a caller can ask for that.
func (s *Server) alreadyTuned(lease *pool.Lease, parsed hostapi.Request) bool {
	if !lease.TunedTo(parsed.Properties) {
		return false
	}
	status, err := lease.Device().Status()
	return err == nil && status.Locked
}

// waitForLock polls until the demodulator locks or the caller's patience runs
// out, and reports the last thing it saw either way.
func (s *Server) waitForLock(ctx context.Context, lease *pool.Lease, timeout time.Duration) (device.Status, bool) {
	deadline := time.Now().Add(timeout)
	var last device.Status
	for {
		status, err := lease.Device().Status()
		if err == nil {
			last = status
			if status.Locked {
				return status, true
			}
		}
		if time.Now().After(deadline) {
			return last, false
		}
		select {
		case <-ctx.Done():
			return last, false
		case <-lease.Done():
			return last, false
		case <-time.After(lockPollInterval):
		}
	}
}

// settle holds the channel and reads the measurements once more.
func (s *Server) settle(ctx context.Context, lease *pool.Lease, wait time.Duration) (device.Status, bool) {
	select {
	case <-ctx.Done():
		return device.Status{}, false
	case <-lease.Done():
		return device.Status{}, false
	case <-time.After(wait):
	}
	status, err := lease.Device().Status()
	if err != nil {
		return device.Status{}, false
	}
	return status, true
}

// lockPollInterval is how often the demodulator is asked whether it has locked:
// often enough that a quick lock is not held up, seldom enough not to compete
// with the stream for the bridge.
const lockPollInterval = 100 * time.Millisecond
