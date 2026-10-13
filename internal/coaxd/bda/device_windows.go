// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package bda

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/Till0196/coax/internal/coaxd/win32"
	"github.com/Till0196/coax/internal/coaxd/win32/directshow"

	"github.com/Till0196/coax/internal/coaxd/bda/cards"
	"github.com/Till0196/coax/internal/coaxd/bda/filters"
	"github.com/Till0196/coax/internal/coaxd/bda/graph"
	"github.com/Till0196/coax/internal/coaxd/bda/mode"
	"time"
)

// One BDA tuner, opened and in use. Finding one and the monikers it is named by
// are in the filters package; the graph it runs on is in the graph package.

const lockDataBytes = 256 * 1024

type Device struct {
	name     string
	tuner    filters.Moniker
	captures []filters.Moniker
	session  *graph.Session
	// card names the physical card this tuner is on, so a mode switch can see
	// what else on it is streaming.
	card string
	// demod is how this card family is told which standard to be.
	demod       mode.Switcher
	mode        DemodMode
	initialized bool
	// streaming is whether a stream taken from this device is still open, i.e.
	// whether the vendor's driver is busy carrying one.
	streaming bool
	// streamingTuner is the tuner filter of the graph the open stream is
	// reading, held only for as long as that stream is open.
	streamingTuner *directshow.IBaseFilter
	// live is the stream the graph is writing for at the moment. A stream that is
	// closed after the next one has already opened must not put the graph back to
	// idle: the reader would then sit on a file nothing writes to.
	live *Stream
}

// Measurement is a signal reading the driver may not offer. Zero is a real
// reading, so whether one was taken is carried separately.
type Measurement struct {
	Value float64
	Known bool
}

type Status struct {
	Locked bool
	Names  []string
	// Strength is the input level in dBm, as the driver reports it.
	Strength Measurement
	// Quality is the driver's own 0..100 figure. BDA exposes no C/N ratio and
	// this is not one, so it is passed through as a percentage rather than
	// dressed up as decibels.
	Quality Measurement
}

type Stream struct {
	// file is the one of the spool's files being read, and path its name.
	file  *os.File
	path  string
	spool *graph.Spool
	// device is the tuner this came from, so closing the stream can tell it the
	// graph is no longer carrying anything -- nothing else signals when a backend
	// may touch the hardware again.
	device *Device
	// drained is when the graph's event queue was last emptied. A queue nobody
	// empties eventually stops the graph, and a stream is the one stretch where
	// nothing else asks the device anything.
	drained time.Time
	// session is the running graph feeding the file, owned by the stream: the
	// stream is read while it is still arriving, so the graph must outlive it.
	session *graph.Session
}

func Open(selector string) (*Device, error) {
	runtime.LockOSThread()
	if _, err := win32.CoInitializeEx(uint32(win32.COINIT_MULTITHREADED)); err != nil {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("initialize COM: %w", err)
	}
	device := &Device{initialized: true}
	tuners, err := filters.Enumerate(filters.CategoryNetworkTuner)
	if err != nil {
		device.Close()
		return nil, fmt.Errorf("enumerate BDA tuners: %w", err)
	}
	selected, err := filters.Select(tuners, selector)
	if err != nil {
		filters.ReleaseAll(tuners)
		device.Close()
		return nil, err
	}
	for index := range tuners {
		if tuners[index].Moniker != selected.Moniker {
			tuners[index].Moniker.Release()
		}
	}
	device.tuner = selected
	device.name = selected.Name
	device.card = cards.KeyOf(selected.Display)
	device.demod = mode.For(selected)
	device.captures, err = filters.Enumerate(filters.CategoryReceiverComponent)
	if err != nil {
		device.Close()
		return nil, fmt.Errorf("enumerate BDA receiver components: %w", err)
	}
	return device, nil
}

func (device *Device) Name() string { return device.name }

func (device *Device) Tune(properties map[string]string) error {
	profile, err := BuildTuneProfile(properties)
	if err != nil {
		return err
	}
	device.closeSession()
	// A card with one card-wide demodulator mode must be told which standard to
	// be before the graph is built, because the driver advertises one network
	// type and the graph is built against it. A no-op on other cards.
	settled, err := device.demod.BeforeGraph(profile)
	if err != nil {
		return err
	}
	device.mode = settled
	return device.startGraph(profile)
}

// StartTransport brings the capture graph up and leaves the card's mode alone,
// for a caller driving the demodulator itself: the graph is wanted only for the
// card's DMA and the channel it is built against is a formality.
func (device *Device) StartTransport(profile TuneProfile) error {
	device.closeSession()
	return device.startGraph(profile)
}

func (device *Device) startGraph(profile TuneProfile) error {
	// Nothing reads what the graph writes until a stream is opened, and that
	// starts a spool of its own.
	session, err := graph.Build(graph.Options{
		Tuner:    device.tuner,
		Captures: device.captures,
		Profile:  profile,
		Limit:    idleCaptureBytes,
		Card:     device.card,
	})
	if err != nil {
		return err
	}
	device.session = session
	return nil
}

// Mode reports the standard this tuner is set to, reading it through the running
// graph when there is one: a second graph cannot be built on a held tuner.
func (device *Device) Mode() (DemodMode, error) {
	return device.demod.Current(device.session)
}

// ModeName says what a mode value means on this card. The numbers are not a
// shared vocabulary -- the byte meaning DVB-T on one card means ISDB-T on
// another -- so a value is rendered by the card it came from.
func (device *Device) ModeName(mode DemodMode) string {
	return device.demod.NameFor(mode)
}

func (device *Device) Status() (Status, error) {
	if device.session == nil {
		return Status{}, fmt.Errorf("BDA device is not tuned")
	}
	// A BDA graph reports failure only through its event queue and goes on
	// answering State_Running, so without this a stopped graph and a tuner with
	// no signal look identical.
	if err := device.session.DrainEvents(); err != nil {
		return Status{}, err
	}
	if err := device.session.WriteError(); err != nil {
		return Status{}, fmt.Errorf("write the captured stream: %w", err)
	}
	written, warmedUp := device.session.Progress()
	status := Status{
		Locked: written >= lockDataBytes && warmedUp,
		Names:  []string{"graph_running", fmt.Sprintf("data=%d", written)},
	}
	device.readSignalStatistics(&status)
	return status, nil
}

// readSignalStatistics fills in whatever the tuner's statistics node reports.
// All of it is optional: many drivers publish the node then answer with an error
// or a fixed number, so a failure only costs the reading.
func (device *Device) readSignalStatistics(status *Status) {
	statistics := device.session.SignalStatistics()
	if statistics == nil {
		return
	}
	var strength int32
	if err := statistics.Get_SignalStrength(&strength); err == nil {
		status.Strength = Measurement{Value: float64(strength), Known: true}
	}
	var quality int32
	if err := statistics.Get_SignalQuality(&quality); err == nil && quality >= 0 && quality <= 100 {
		status.Quality = Measurement{Value: float64(quality), Known: true}
	}
	// The node's lock flag beats inferring one from bytes arriving.
	var locked byte
	if err := statistics.Get_SignalLocked(&locked); err == nil && locked != 0 {
		status.Locked = true
		status.Names = append(status.Names, "LOCK")
	}
}

func (device *Device) OpenStream() (*Stream, error) {
	if device.session == nil {
		return nil, fmt.Errorf("BDA device is not tuned")
	}
	session := device.session
	// A file of its own for this stream, taking nothing from what came before it.
	// The graph has been running since the tune, and the vendor driver tunes its
	// own way as it brings the graph up -- so what it has written so far is the
	// card acquiring the channel, or whatever carrier the vendor aimed at first.
	// Handing that back as this channel's is what a scan reads as one channel's
	// stream reported under another's name.
	if err := session.Recapture(0); err != nil {
		return nil, err
	}
	spool := session.Spool()
	path := spool.First()
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	// The device keeps the session. A graph is what costs a tune here -- the
	// vendor driver tunes its own way as it builds one -- so it is kept standing
	// for the next channel. The stream still names the session it is reading,
	// because a caller that retunes takes the graph down and the stream must then
	// close the one it had.
	device.streaming = true
	// The tuner filter the driver's measurements are asked through, held for as
	// long as the stream is open.
	device.streamingTuner = session.Tuner()
	if device.streamingTuner != nil {
		device.streamingTuner.AddRef()
	}
	stream := &Stream{
		file:    file,
		path:    path,
		spool:   spool,
		session: session,
		device:  device,
		drained: time.Now(),
	}
	device.live = stream
	return stream, nil
}

// RemoveLeftovers deletes the capture files a run that was killed left behind.
func RemoveLeftovers() { graph.RemoveLeftovers() }

// idleCaptureBytes is how much a graph with nothing reading it may write. It
// only has to be enough for the backends that tell a lock from bytes arriving.
const idleCaptureBytes = 32 << 20

// Written is how much the running graph has carried, or zero when there is no
// graph. A backend reusing a graph asks this to tell a card still carrying a
// stream from one that stopped when a channel did not lock.
func (device *Device) Written() int64 {
	if device.session == nil {
		return 0
	}
	return device.session.Written()
}

func (device *Device) closeSession() {
	if device.session == nil {
		return
	}
	device.session.Close()
	device.session = nil
}

// Streaming reports whether a stream taken from this device is still open.
func (device *Device) Streaming() bool { return device.streaming }

// TransportLive reports whether the graph this device's pins came from is still
// standing, which is not the same question as whether this device holds the
// session: OpenStream hands the session to the stream, and the graph then lives
// as long as that stream.
//
// It is what a caller holding a pin of that graph has to ask before using it. A
// released pin is not an invalid handle that answers with an error; it is freed
// memory.
func (device *Device) TransportLive() bool { return device.session != nil || device.streaming }

func (device *Device) releaseStreamingTuner() {
	if device.streamingTuner != nil {
		device.streamingTuner.Release()
		device.streamingTuner = nil
	}
}

func (device *Device) Close() error {
	device.streaming = false
	device.releaseStreamingTuner()
	device.closeSession()
	if device.tuner.Moniker != nil {
		device.tuner.Moniker.Release()
		device.tuner.Moniker = nil
	}
	filters.ReleaseAll(device.captures)
	device.captures = nil
	if device.initialized {
		win32.CoUninitialize()
		device.initialized = false
		runtime.UnlockOSThread()
	}
	return nil
}

// drainEvery is how often a stream empties the graph's event queue. Often
// enough that the queue cannot fill between two reads of a carrier, seldom
// enough to be nothing beside the reading itself.
const drainEvery = 500 * time.Millisecond

func (stream *Stream) Read(ctx context.Context, buffer []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// The graph posts events whether or not anyone is listening, and a queue left
	// to fill stops it delivering -- which looks from here like a carrier that
	// went quiet, since IMediaControl goes on answering State_Running. Emptying it
	// is also the only way a graph that gave up says so.
	if stream.session != nil && time.Since(stream.drained) >= drainEvery {
		stream.drained = time.Now()
		if err := stream.session.DrainEvents(); err != nil {
			return 0, err
		}
	}
	for {
		read, err := stream.file.Read(buffer)
		if !errors.Is(err, io.EOF) {
			return read, err
		}
		if read > 0 {
			return read, nil
		}
		// The end of a file is the end of the stream so far, unless the spool
		// has gone on to another: then this one is finished, and read to its
		// end it has given everything it holds.
		next, more := stream.spool.Next(stream.path)
		if !more {
			return 0, ErrWouldBlock
		}
		if read, err = stream.file.Read(buffer); read > 0 || !errors.Is(err, io.EOF) {
			return read, err
		}
		opened, err := os.Open(next)
		if err != nil {
			return 0, err
		}
		_ = stream.file.Close()
		_ = stream.spool.Consumed(stream.path)
		stream.file, stream.path = opened, next
	}
}

func (stream *Stream) Close() error {
	streamed := stream.device
	// Whether this is still the stream the graph is writing for. A caller that let
	// go of one carrier and asked for the next is two streams for a moment, and
	// the older one's teardown must leave the graph pointed where the newer one is
	// reading.
	current := streamed != nil && streamed.live == stream
	if current {
		streamed.live = nil
		streamed.streaming = false
		streamed.releaseStreamingTuner()
	}
	stream.device = nil
	err := stream.file.Close()
	var removeErr error
	switch {
	case current && streamed.session == stream.session:
		// The graph is still the device's, so it stays up for the next
		// channel; only what it was writing to is dropped, and it is dropped
		// by handing the graph a new file rather than by closing this one.
		if removeErr = streamed.session.Recapture(idleCaptureBytes); removeErr != nil {
			// A graph that cannot be pointed at a new file would go on filling
			// the one just closed, so it is taken down and the next tune
			// builds another.
			streamed.closeSession()
		}
	case current && stream.session != nil:
		stream.session.Close()
		removeErr = stream.spool.Remove()
	case !current:
		// The graph has moved on to a newer stream and is writing that one's
		// spool, so nothing here touches the graph: only the files this stream
		// was reading go.
		removeErr = stream.spool.Remove()
	}
	stream.session = nil
	if err != nil {
		return err
	}
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return nil
}

var ErrWouldBlock = errors.New("BDA stream would block")
