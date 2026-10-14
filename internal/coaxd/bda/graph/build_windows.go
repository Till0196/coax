// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package graph

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Till0196/coax/pkg/win32"

	"github.com/Till0196/coax/internal/coaxd/bda/cards"
	"github.com/Till0196/coax/internal/coaxd/bda/filters"
	"github.com/Till0196/coax/internal/coaxd/bda/sink"
	"github.com/Till0196/coax/internal/coaxd/bda/tuning"
)

// networkProvider is one candidate for the filter that drives the tuner.
type networkProvider struct {
	name  string
	clsid win32.GUID
}

// networkProviders lists the providers to try, in order: the Microsoft Network
// Provider (serves every network type, and what Windows has wanted since 7),
// then the per-network provider, which some drivers only connect to. Trying is
// the only way to tell -- an unsuitable provider answers VFW_E_CANNOT_CONNECT at
// connect time, not before.
func networkProviders(delivery tuning.Delivery) []networkProvider {
	return []networkProvider{
		{name: "Microsoft Network Provider", clsid: clsidMSNetworkProvider},
		{name: "per-network provider", clsid: networkTypeFor(delivery)},
	}
}

// Options is everything a graph is built from.
type Options struct {
	// Tuner is the tuner filter to build on.
	Tuner filters.Moniker
	// Captures are the receiver components a card publishes, one of which may
	// have to sit between the tuner and the rest of the graph.
	Captures []filters.Moniker
	// Profile is the channel the graph is built against.
	Profile tuning.TuneProfile
	// Limit bounds what the graph may write before anything reads it. Zero is
	// unbounded. See Spool.
	Limit int64
	// Card is the card the tuner is on, held while the graph runs so a mode
	// switch elsewhere cannot restart it mid-stream. See the cards package.
	Card string
}

// Build builds the filter graph, trying each network provider in turn. On
// success the card is held; Session.Close lets go of it again.
func Build(options Options) (*Session, error) {
	var failures []string
	for _, provider := range networkProviders(options.Profile.Delivery) {
		session, err := buildWith(provider, options)
		if err == nil {
			session.card = options.Card
			cards.Hold(options.Card)
			return session, nil
		}
		failures = append(failures, provider.name+": "+err.Error())
	}
	if held := strings.Join(failures, "; "); strings.Contains(held, resourceInUse) {
		return nil, ErrTunerHeld
	} else {
		return nil, fmt.Errorf("no network provider would drive this tuner (%s)", held)
	}
}

// resourceInUse is what Windows reports when the tuner filter belongs to
// somebody else: ERROR_BUSY, as an HRESULT, in the text a provider failed with.
const resourceInUse = "0x800700AA"

// ErrTunerHeld reports a tuner whose filter is already claimed, usually by
// another program. On a Digital Devices card a run killed outright while a graph
// was running also leaves the filter acquired from a system thread nothing in
// user space can reach; restarting the device does not release it (Windows
// answers that a reboot is needed), so the tuner is out of reach until the
// machine restarts. See docs/boards/dd.md.
var ErrTunerHeld = errors.New("this tuner is already in use; if nothing else has it, a run that was killed left it held and the machine needs restarting")

func buildWith(candidate networkProvider, options Options) (*Session, error) {
	tunerMoniker, captureMonikers := options.Tuner, options.Captures
	profile := options.Profile
	graph, err := createGraph()
	if err != nil {
		return nil, fmt.Errorf("create DirectShow graph: %w", err)
	}
	success := false
	defer func() {
		if !success {
			graph.Release()
		}
	}()

	provider, err := createBaseFilter(candidate.clsid)
	if err != nil {
		return nil, fmt.Errorf("create BDA network provider: %w", err)
	}
	defer provider.Release()
	if err := graph.AddFilter(provider, "BDA Network Provider"); err != nil {
		return nil, fmt.Errorf("add BDA network provider: %w", err)
	}

	tunerControl, err := queryTuner(provider)
	if err != nil {
		return nil, fmt.Errorf("query BDA tuner control: %w", err)
	}
	defer tunerControl.Release()
	// The provider is primed with a tuning space and a locator-less tune request
	// before anything is connected: it validates a tune request against the
	// tuning space it already has, so the real one cold answers E_INVALIDARG.
	// The real channel goes on once the graph runs (below).
	space, err := primeTuningSpace(tunerControl, profile, networkTypeFor(profile.Delivery))
	if err != nil {
		return nil, fmt.Errorf("prime BDA tuning space: %w", err)
	}
	defer space.Release()

	tunerFilter, err := bindBaseFilter(tunerMoniker.Moniker)
	if err != nil {
		return nil, fmt.Errorf("bind BDA tuner %q: %w", tunerMoniker.Name, err)
	}
	// The tuner filter outlives this function on success: its input pin is the
	// only route to the card's vendor properties.
	defer func() {
		if !success {
			tunerFilter.Release()
		}
	}()
	// Many drivers expose no statistics node, so a failure here means "no
	// measurements" rather than a failure to tune.
	statistics := findSignalStatistics(tunerFilter)
	defer func() {
		if !success && statistics != nil {
			statistics.Release()
		}
	}()
	if err := graph.AddFilter(tunerFilter, tunerMoniker.Name); err != nil {
		return nil, fmt.Errorf("add BDA tuner %q: %w", tunerMoniker.Name, err)
	}
	if err := connectFilters(graph, provider, tunerFilter); err != nil {
		return nil, fmt.Errorf("connect network provider to tuner: %w", err)
	}

	// Our own sink filter, not the stock Microsoft file writer: that one cannot
	// run on a clockless graph, rejecting the first untimed sample with
	// VFW_E_SAMPLE_TIME_NOT_SET while IMediaControl goes on answering "running".
	spool, err := NewSpool(options.Limit)
	if err != nil {
		return nil, fmt.Errorf("create BDA capture file: %w", err)
	}
	stream := sink.New(spool)
	defer func() {
		if !success {
			stream.Detach()
			stream.Release()
			_ = spool.Remove()
		}
	}()
	writer := stream.Filter()
	if err := graph.AddFilter(writer, "Transport Stream Writer"); err != nil {
		return nil, fmt.Errorf("add transport stream sink: %w", err)
	}

	// One branch is written out, the other carries the tables the transport
	// information filter reads.
	tee, err := createBaseFilter(clsidInfiniteTee)
	if err != nil {
		return nil, fmt.Errorf("create DirectShow tee: %w", err)
	}
	defer tee.Release()
	if err := graph.AddFilter(tee, "Transport Stream Tee"); err != nil {
		return nil, fmt.Errorf("add DirectShow tee: %w", err)
	}

	capture := ""
	if directErr := connectFilters(graph, tunerFilter, tee); directErr != nil {
		chosen, captureErr := connectCapture(graph, tunerFilter, tee, captureMonikers)
		if captureErr != nil {
			return nil, fmt.Errorf("connect tuner output: direct=%v; receiver component=%w", directErr, captureErr)
		}
		capture = chosen
	}
	if err := connectFilters(graph, tee, writer); err != nil {
		return nil, fmt.Errorf("connect the capture file: %w", err)
	}

	// A graph with no transport information filter can build and then refuse to
	// run: BDA expects the network provider to have one to talk to while tuning.
	// Best-effort, but the error is kept for the run failure's message.
	informationErr := attachTransportInformation(graph, tee)

	// A capture graph runs without a clock: left alone the graph manager asks the
	// filters for one at start, and no BDA filter offers one, so that search ends
	// in E_NOINTERFACE and the graph refuses to run.
	if err := clearGraphClock(graph); err != nil {
		return nil, fmt.Errorf("run the graph without a clock: %w", err)
	}

	control, err := queryMediaControl(graph)
	if err != nil {
		return nil, fmt.Errorf("query DirectShow media control: %w", err)
	}
	if err := control.Run(); err != nil {
		control.Release()
		// The graph's shape is not visible from outside the process, so record it.
		shape := describeGraph(graph)
		if informationErr != nil {
			return nil, fmt.Errorf("run BDA graph: %w [%s] (no transport information filter: %v)", err, shape, informationErr)
		}
		return nil, fmt.Errorf("run BDA graph: %w [%s]", err, shape)
	}

	// The real channel goes on now, with the graph already running: a tune request
	// applied before Run is what E_INVALIDARG and VFW_E_CANNOT_CONNECT reported.
	// DiSEqC goes first because until the switch is told which port the lead still
	// carries the last channel's, and also after Run because a control node is
	// unreachable before then.
	if profile.DiseqcWanted {
		if err := sendDiseqc(tunerFilter, profile); err != nil {
			control.Stop()
			control.Release()
			return nil, fmt.Errorf("DiSEqC: %w", err)
		}
	}
	if err := applyTuneProfile(tunerControl, space, profile); err != nil {
		control.Stop()
		control.Release()
		return nil, fmt.Errorf("tune: %w", err)
	}

	// A graph can abort after Run returns, so the queue is read here and again on
	// every status check.
	events, err := queryMediaEvent(graph)
	if err != nil {
		control.Stop()
		control.Release()
		return nil, fmt.Errorf("query DirectShow media event: %w", err)
	}

	success = true
	return &Session{
		graph:      graph,
		control:    control,
		events:     events,
		statistics: statistics,
		tuner:      tunerFilter,
		writer:     stream,
		spool:      spool,
		capture:    capture,
	}, nil
}
