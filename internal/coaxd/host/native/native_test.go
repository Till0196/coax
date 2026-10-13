// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package native

import (
	"context"
	"errors"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/receiver"
)

// fakeDevice stands in for a frontend or a filter graph.
type fakeDevice struct {
	status     receiver.Status
	supports   bool
	supportErr error
	closed     bool
	stream     *fakeStream
}

func (d *fakeDevice) Backend() string { return "fake" }
func (d *fakeDevice) Name() string    { return "fake frontend" }
func (d *fakeDevice) Supports(string) (bool, error) {
	return d.supports, d.supportErr
}
func (d *fakeDevice) Tune(map[string]string) error     { return nil }
func (d *fakeDevice) Status() (receiver.Status, error) { return d.status, nil }
func (d *fakeDevice) OpenStream() (receiver.Stream, error) {
	d.stream = &fakeStream{}
	return d.stream, nil
}
func (d *fakeDevice) Close() error { d.closed = true; return nil }

type fakeStream struct{ closed bool }

func (s *fakeStream) Read(context.Context, []byte) (int, error) {
	return 0, receiver.ErrWouldBlock
}
func (s *fakeStream) Close() error { s.closed = true; return nil }

func newTestBoard(fake *fakeDevice) *board {
	return &board{
		name:   "dvb:adapter0.frontend0",
		tuner:  device.TunerInfo{Index: 0, Name: "fake frontend"},
		device: &tuner{receiver: fake},
	}
}

// TestOneFrontendIsOneTuner: a native device is a board of one tuner, and asking
// for a second must fail rather than hand back the same hardware twice.
func TestOneFrontendIsOneTuner(t *testing.T) {
	fake := &fakeDevice{}
	one := newTestBoard(fake)

	if got := len(one.Tuners()); got != 1 {
		t.Fatalf("the board carries %d tuners, want 1", got)
	}
	if _, err := one.OpenTuner(0); err != nil {
		t.Fatalf("open the tuner: %v", err)
	}
	if _, err := one.OpenTuner(1); err == nil {
		t.Error("handed out a second tuner on a board that has one")
	}

	if err := one.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !fake.closed {
		t.Error("closing the board left the device open")
	}
}

// TestClosingTheTunerDoesNotCloseTheDevice: the pool closes finished tuners and
// expects the board to stay usable, and here the two are one piece of hardware,
// so only the board may close it.
func TestClosingTheTunerDoesNotCloseTheDevice(t *testing.T) {
	fake := &fakeDevice{}
	one := newTestBoard(fake)
	held, err := one.OpenTuner(0)
	if err != nil {
		t.Fatalf("open the tuner: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("close the tuner: %v", err)
	}
	if fake.closed {
		t.Error("closing one tuner closed the whole device")
	}
}

// TestQualityIsCarriedThrough: BDA drivers report a quality percentage and no
// carrier-to-noise ratio, so dropping it would leave Windows listings saying
// nothing about the signal.
func TestQualityIsCarriedThrough(t *testing.T) {
	fake := &fakeDevice{status: receiver.Status{
		Locked:   true,
		Names:    []string{"LOCK"},
		Strength: receiver.Measured(-71.5),
		Quality:  receiver.Measured(80),
	}}
	held, err := newTestBoard(fake).OpenTuner(0)
	if err != nil {
		t.Fatalf("open the tuner: %v", err)
	}
	status, err := held.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Locked || len(status.Names) != 1 {
		t.Errorf("lock state was lost: %+v", status)
	}
	if !status.Quality.Known || status.Quality.Value != 80 {
		t.Errorf("quality came through as %+v", status.Quality)
	}
	if !status.Strength.Known || status.Strength.Value != -71.5 {
		t.Errorf("strength came through as %+v", status.Strength)
	}
	// A CNR must not be invented from a quality percentage: different scales.
	if status.CNR.Known {
		t.Errorf("a carrier-to-noise ratio appeared from nowhere: %+v", status.CNR)
	}
}

// TestWouldBlockIsSpokenInOneVocabulary: a reader above this package should not
// have to know two sentinels for "nothing yet".
func TestWouldBlockIsSpokenInOneVocabulary(t *testing.T) {
	fake := &fakeDevice{}
	held, err := newTestBoard(fake).OpenTuner(0)
	if err != nil {
		t.Fatalf("open the tuner: %v", err)
	}
	stream, err := held.OpenStream()
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	if _, err := stream.Read(context.Background(), make([]byte, 188)); !errors.Is(err, device.ErrWouldBlock) {
		t.Errorf("a read with nothing to give said %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("close the stream: %v", err)
	}
	if !fake.stream.closed {
		t.Error("closing the stream left the underlying one open")
	}
}

// TestATunerThatCannotSayIsNotOffered: Supports answers with an error when the
// name is not one this program knows, and that is a no, not a maybe.
func TestATunerThatCannotSayIsNotOffered(t *testing.T) {
	held, err := newTestBoard(&fakeDevice{supports: true, supportErr: errors.New("no such delivery system")}).OpenTuner(0)
	if err != nil {
		t.Fatalf("open the tuner: %v", err)
	}
	if held.Supports("NONSENSE") {
		t.Error("a tuner that could not answer was taken as agreeing")
	}
}
