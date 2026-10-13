// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package boardkit

import (
	"context"
	"errors"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// fakeTuner records what the kit asked of it.
type fakeTuner struct {
	tuned    int
	starts   int
	stops    int
	released int
	startErr error
}

func (tuner *fakeTuner) Tune(map[string]string) error { tuner.tuned++; return nil }

func (tuner *fakeTuner) Status() (device.Status, error) { return device.Status{Locked: true}, nil }

func (tuner *fakeTuner) StartStream() error {
	if tuner.startErr != nil {
		return tuner.startErr
	}
	tuner.starts++
	return nil
}

func (tuner *fakeTuner) StopStream() error { tuner.stops++; return nil }

func (tuner *fakeTuner) Read(context.Context, []byte) (int, error) { return 0, device.ErrWouldBlock }

func (tuner *fakeTuner) Release() error { tuner.released++; return nil }

// newTestBoard returns a two-tuner board over the given hardware.
func newTestBoard(t *testing.T, tuners []*fakeTuner, released *int) *Board {
	t.Helper()
	board, err := New(Config{
		Model:    "Test",
		Location: "1:2",
		Tuners: []device.TunerInfo{
			{Index: 0, Name: "A", Deliveries: []string{"ISDB-T"}},
			{Index: 1, Name: "B", Deliveries: []string{"ISDB-S"}},
		},
		Release: func() error { *released++; return nil },
		Open:    func(index int) (Tuner, error) { return tuners[index], nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return board
}

func TestNewRejectsAmbiguousOwnership(t *testing.T) {
	base := Config{
		Tuners: []device.TunerInfo{{Index: 0}},
		Open:   func(int) (Tuner, error) { return &fakeTuner{}, nil },
	}
	if _, err := New(base); err == nil {
		t.Fatal("expected an error when neither Owner nor Release is set")
	}
	both := base
	both.Owner = device.NewOwner(func() error { return nil })
	both.Release = func() error { return nil }
	if _, err := New(both); err == nil {
		t.Fatal("expected an error when both Owner and Release are set")
	}
}

func TestNameJoinsModelAndLocation(t *testing.T) {
	board := newTestBoard(t, []*fakeTuner{{}, {}}, new(int))
	if got := board.Name(); got != "Test 1:2" {
		t.Fatalf("Name() = %q, want %q", got, "Test 1:2")
	}
}

func TestOpenTunerIsExclusiveUntilClosed(t *testing.T) {
	board := newTestBoard(t, []*fakeTuner{{}, {}}, new(int))
	opened, err := board.OpenTuner(0)
	if err != nil {
		t.Fatalf("OpenTuner(0): %v", err)
	}
	if _, err := board.OpenTuner(0); !errors.Is(err, device.ErrTunerBusy) {
		t.Fatalf("second OpenTuner(0) = %v, want ErrTunerBusy", err)
	}
	// The board's other tuner is unaffected.
	other, err := board.OpenTuner(1)
	if err != nil {
		t.Fatalf("OpenTuner(1): %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := board.OpenTuner(0); err != nil {
		t.Fatalf("OpenTuner(0) after close: %v", err)
	}
	_ = other.Close()
}

func TestOpenTunerRejectsOutOfRange(t *testing.T) {
	board := newTestBoard(t, []*fakeTuner{{}, {}}, new(int))
	if _, err := board.OpenTuner(2); err == nil {
		t.Fatal("expected an error for a tuner index past the end")
	}
	if _, err := board.OpenTuner(-1); err == nil {
		t.Fatal("expected an error for a negative tuner index")
	}
}

func TestSupportsFallsBackToTheTunerDeliveries(t *testing.T) {
	board := newTestBoard(t, []*fakeTuner{{}, {}}, new(int))
	terrestrial, err := board.OpenTuner(0)
	if err != nil {
		t.Fatalf("OpenTuner(0): %v", err)
	}
	defer terrestrial.Close()
	if !terrestrial.Supports("ISDB-T") {
		t.Error("tuner 0 should support ISDB-T")
	}
	if terrestrial.Supports("ISDB-S") {
		t.Error("tuner 0 should not support ISDB-S")
	}
}

func TestTuneStopsARunningStreamFirst(t *testing.T) {
	hardware := &fakeTuner{}
	board := newTestBoard(t, []*fakeTuner{hardware, {}}, new(int))
	opened, err := board.OpenTuner(0)
	if err != nil {
		t.Fatalf("OpenTuner: %v", err)
	}
	defer opened.Close()
	if _, err := opened.OpenStream(); err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := opened.Tune(nil); err != nil {
		t.Fatalf("Tune: %v", err)
	}
	if hardware.stops != 1 {
		t.Fatalf("StopStream called %d times, want 1", hardware.stops)
	}
	if hardware.tuned != 1 {
		t.Fatalf("Tune called %d times, want 1", hardware.tuned)
	}
}

func TestStreamCloseLeavesTheTunerReusable(t *testing.T) {
	hardware := &fakeTuner{}
	board := newTestBoard(t, []*fakeTuner{hardware, {}}, new(int))
	opened, err := board.OpenTuner(0)
	if err != nil {
		t.Fatalf("OpenTuner: %v", err)
	}
	defer opened.Close()
	stream, err := opened.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close: %v", err)
	}
	// A second close is harmless and must not stop the stream twice.
	if err := stream.Close(); err != nil {
		t.Fatalf("second stream.Close: %v", err)
	}
	if hardware.stops != 1 {
		t.Fatalf("StopStream called %d times, want 1", hardware.stops)
	}
	if _, err := opened.OpenStream(); err != nil {
		t.Fatalf("OpenStream after Close: %v", err)
	}
	if hardware.starts != 2 {
		t.Fatalf("StartStream called %d times, want 2", hardware.starts)
	}
}

func TestOpenStreamRefusesASecondStream(t *testing.T) {
	board := newTestBoard(t, []*fakeTuner{{}, {}}, new(int))
	opened, err := board.OpenTuner(0)
	if err != nil {
		t.Fatalf("OpenTuner: %v", err)
	}
	defer opened.Close()
	if _, err := opened.OpenStream(); err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if _, err := opened.OpenStream(); err == nil {
		t.Fatal("expected an error opening a second stream on one tuner")
	}
}

func TestFailedStartStreamLeavesTheTunerUsable(t *testing.T) {
	hardware := &fakeTuner{startErr: errors.New("no endpoint")}
	board := newTestBoard(t, []*fakeTuner{hardware, {}}, new(int))
	opened, err := board.OpenTuner(0)
	if err != nil {
		t.Fatalf("OpenTuner: %v", err)
	}
	defer opened.Close()
	if _, err := opened.OpenStream(); err == nil {
		t.Fatal("expected OpenStream to report the hardware failure")
	}
	hardware.startErr = nil
	if _, err := opened.OpenStream(); err != nil {
		t.Fatalf("OpenStream after a failure: %v", err)
	}
}

func TestCloseStopsTheStreamReleasesAndIsIdempotent(t *testing.T) {
	hardware := &fakeTuner{}
	released := 0
	board := newTestBoard(t, []*fakeTuner{hardware, {}}, &released)
	opened, err := board.OpenTuner(0)
	if err != nil {
		t.Fatalf("OpenTuner: %v", err)
	}
	if _, err := opened.OpenStream(); err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if hardware.stops != 1 {
		t.Fatalf("StopStream called %d times, want 1", hardware.stops)
	}
	if hardware.released != 1 {
		t.Fatalf("Release called %d times, want 1", hardware.released)
	}
	// The board handle still holds its own reference.
	if released != 0 {
		t.Fatalf("the USB device was released while the board was still open")
	}
	if err := board.Close(); err != nil {
		t.Fatalf("board.Close: %v", err)
	}
	if released != 1 {
		t.Fatalf("the USB device was released %d times, want 1", released)
	}
}

func TestTheUSBDeviceOutlivesTheBoardHandle(t *testing.T) {
	released := 0
	board := newTestBoard(t, []*fakeTuner{{}, {}}, &released)
	opened, err := board.OpenTuner(0)
	if err != nil {
		t.Fatalf("OpenTuner: %v", err)
	}
	if err := board.Close(); err != nil {
		t.Fatalf("board.Close: %v", err)
	}
	if released != 0 {
		t.Fatal("closing the board released the USB device out from under a tuner")
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("opened.Close: %v", err)
	}
	if released != 1 {
		t.Fatalf("the USB device was released %d times, want 1", released)
	}
}

func TestAClosedTunerRefusesWork(t *testing.T) {
	board := newTestBoard(t, []*fakeTuner{{}, {}}, new(int))
	opened, err := board.OpenTuner(0)
	if err != nil {
		t.Fatalf("OpenTuner: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := opened.Tune(nil); err == nil {
		t.Error("Tune on a closed tuner should fail")
	}
	if _, err := opened.Status(); err == nil {
		t.Error("Status on a closed tuner should fail")
	}
	if _, err := opened.OpenStream(); err == nil {
		t.Error("OpenStream on a closed tuner should fail")
	}
}

func TestReadHonoursACancelledContext(t *testing.T) {
	board := newTestBoard(t, []*fakeTuner{{}, {}}, new(int))
	opened, err := board.OpenTuner(0)
	if err != nil {
		t.Fatalf("OpenTuner: %v", err)
	}
	defer opened.Close()
	stream, err := opened.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := stream.Read(ctx, make([]byte, 8)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read = %v, want context.Canceled", err)
	}
}

func TestOpenFailureFreesTheTunerAgain(t *testing.T) {
	released := 0
	failure := errors.New("bring-up failed")
	board, err := New(Config{
		Model:   "Test",
		Tuners:  []device.TunerInfo{{Index: 0, Deliveries: []string{"ISDB-T"}}},
		Release: func() error { released++; return nil },
		Open:    func(int) (Tuner, error) { return nil, failure },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := board.OpenTuner(0); !errors.Is(err, failure) {
		t.Fatalf("OpenTuner = %v, want the hardware failure", err)
	}
	// The failed claim must not leave the tuner marked busy, nor leak the
	// reference it took.
	if _, err := board.OpenTuner(0); !errors.Is(err, failure) {
		t.Fatalf("second OpenTuner = %v, want the hardware failure again", err)
	}
	if err := board.Close(); err != nil {
		t.Fatalf("board.Close: %v", err)
	}
	if released != 1 {
		t.Fatalf("the USB device was released %d times, want 1", released)
	}
}

// countingTuner is a tuner that keeps a drop count, the way a board sharing one
// endpoint between its tuners does.
type countingTuner struct {
	fakeTuner
	dropped uint64
}

func (tuner *countingTuner) Dropped() uint64 { return tuner.dropped }

// TestStreamReportsDropsWhenTheBoardCounts checks that a drop count reaches a
// caller through the Stream, since that is the only handle a consumer holds
// while it is reading.
func TestStreamReportsDropsWhenTheBoardCounts(t *testing.T) {
	inner := &countingTuner{dropped: 42}
	board, err := New(Config{
		Model:    "counting",
		Location: "001:002",
		Tuners:   []device.TunerInfo{{Index: 0, Name: "one", Deliveries: []string{"ISDB-T"}}},
		Release:  func() error { return nil },
		Open:     func(int) (Tuner, error) { return inner, nil },
	})
	if err != nil {
		t.Fatalf("build the board: %v", err)
	}
	opened, err := board.OpenTuner(0)
	if err != nil {
		t.Fatalf("open the tuner: %v", err)
	}
	stream, err := opened.OpenStream()
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	counter, ok := stream.(device.StreamStats)
	if !ok {
		t.Fatal("the stream does not report drops")
	}
	if got := counter.Dropped(); got != 42 {
		t.Errorf("reported %d drops, want 42", got)
	}
}

// TestStreamReportsNoDropsWhenTheBoardCannotCount pins the other half: a board
// with an endpoint per tuner drops nothing.
func TestStreamReportsNoDropsWhenTheBoardCannotCount(t *testing.T) {
	board, err := New(Config{
		Model:    "plain",
		Location: "001:003",
		Tuners:   []device.TunerInfo{{Index: 0, Name: "one", Deliveries: []string{"ISDB-T"}}},
		Release:  func() error { return nil },
		Open:     func(int) (Tuner, error) { return &fakeTuner{}, nil },
	})
	if err != nil {
		t.Fatalf("build the board: %v", err)
	}
	opened, _ := board.OpenTuner(0)
	stream, err := opened.OpenStream()
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	counter, ok := stream.(device.StreamStats)
	if !ok {
		t.Fatal("the stream does not report drops")
	}
	if got := counter.Dropped(); got != 0 {
		t.Errorf("reported %d drops on a board that cannot count them", got)
	}
}

// amplifiedTuner is a fake board tuner with a gain ladder, which is what stops
// the kit refusing the property.
type amplifiedTuner struct {
	fakeTuner
	ladder tuning.GainLadder
	step   int
}

func (tuner *amplifiedTuner) GainLadder(string) tuning.GainLadder { return tuner.ladder }

func (tuner *amplifiedTuner) SetGain(step int) error {
	tuner.step = step
	return nil
}

// TestTheAmplifierSettingIsRefusedByABoardWithoutOne pins the answer given to a
// setting the hardware cannot act on: an error rather than silence.
func TestTheAmplifierSettingIsRefusedByABoardWithoutOne(t *testing.T) {
	hardware := &fakeTuner{}
	board := newTestBoard(t, []*fakeTuner{hardware, {}}, new(int))
	opened, err := board.OpenTuner(0)
	if err != nil {
		t.Fatalf("OpenTuner: %v", err)
	}
	defer opened.Close()
	if err := opened.Tune(map[string]string{"LNA": "off"}); err == nil {
		t.Fatal("a board with no amplifier accepted LNA=off")
	}
	if hardware.tuned != 0 {
		t.Error("the tune went ahead anyway")
	}
	// A channel that says nothing about it still tunes.
	if err := opened.Tune(map[string]string{"FREQUENCY": "473143000"}); err != nil {
		t.Fatalf("Tune: %v", err)
	}
}

func TestABoardWithAnAmplifierIsHandedTheSetting(t *testing.T) {
	for _, one := range []struct {
		name    string
		ladder  tuning.GainLadder
		written string
		refused bool
		step    int
	}{
		{"a switch takes the ends", tuning.NewGainSwitch(), "min", false, 0},
		{"and the other end", tuning.NewGainSwitch(), "max", false, 1},
		{"but not a level it cannot express", tuning.NewGainSwitch(), "+3dB", true, 0},
		{"a ladder matches the nearest step", tuning.NewGainLadder(-9.9, 0, 7.1, 19.7), "+7dB", false, 2},
		{"and rounds a level it does not have", tuning.NewGainLadder(-9.9, 0, 7.1, 19.7), "+100dB", false, 3},
		{"nothing to set is refused", tuning.GainLadder{}, "min", true, 0},
	} {
		t.Run(one.name, func(t *testing.T) {
			hardware := &amplifiedTuner{ladder: one.ladder, step: -1}
			board, err := New(Config{
				Model:   "Test",
				Tuners:  []device.TunerInfo{{Index: 0, Name: "A", Deliveries: []string{"ISDB-T"}}},
				Release: func() error { return nil },
				Open:    func(int) (Tuner, error) { return hardware, nil },
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			opened, err := board.OpenTuner(0)
			if err != nil {
				t.Fatalf("OpenTuner: %v", err)
			}
			defer opened.Close()
			err = opened.Tune(map[string]string{"GAIN": one.written})
			if one.refused {
				if err == nil {
					t.Fatalf("GAIN=%s was accepted", one.written)
				}
				if hardware.step != -1 {
					t.Errorf("the amplifier was moved to step %d anyway", hardware.step)
				}
				return
			}
			if err != nil {
				t.Fatalf("GAIN=%s: %v", one.written, err)
			}
			if hardware.step != one.step {
				t.Errorf("GAIN=%s chose step %d, want %d", one.written, hardware.step, one.step)
			}
		})
	}
}

// TestTheChosenStepIsReported is the other half of matching a level to the
// nearest step: the caller has to find out what it actually got.
func TestTheChosenStepIsReported(t *testing.T) {
	hardware := &amplifiedTuner{ladder: tuning.NewGainLadder(-9.9, 0, 7.1, 19.7)}
	board, err := New(Config{
		Model:   "Test",
		Tuners:  []device.TunerInfo{{Index: 0, Name: "A", Deliveries: []string{"ISDB-T"}}},
		Release: func() error { return nil },
		Open:    func(int) (Tuner, error) { return hardware, nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	opened, err := board.OpenTuner(0)
	if err != nil {
		t.Fatalf("OpenTuner: %v", err)
	}
	defer opened.Close()
	reporter, ok := opened.(interface{ Settings() []device.Setting })
	if !ok {
		t.Fatal("a claimed tuner cannot be asked what it is set to")
	}
	if settings := reporter.Settings(); len(settings) != 0 {
		t.Errorf("a tuner that has been asked for nothing reports %v", settings)
	}
	if err := opened.Tune(map[string]string{"GAIN": "+7dB"}); err != nil {
		t.Fatalf("Tune: %v", err)
	}
	settings := reporter.Settings()
	if len(settings) != 1 || settings[0].Name != "GAIN" || settings[0].Value != "+7.1dB" {
		t.Errorf("the tuner reports %v, want GAIN=+7.1dB", settings)
	}
}
