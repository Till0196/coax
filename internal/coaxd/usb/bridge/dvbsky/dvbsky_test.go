// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package dvbsky

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

type bulkCall struct {
	endpoint byte
	data     []byte
}

type fakeBulk struct {
	calls     []bulkCall
	responses [][]byte
}

// opened is a bridge on a device with nothing left at its response endpoint,
// which is what New drains for. The calls the drain made are dropped so that
// each test sees only its own.
func opened(fake *fakeBulk) *Bridge {
	bridge := New(fake)
	fake.calls = nil
	return bridge
}

func (fake *fakeBulk) Bulk(endpoint byte, buffer []byte, _ uint32) (int, error) {
	fake.calls = append(fake.calls, bulkCall{endpoint: endpoint, data: append([]byte(nil), buffer...)})
	if endpoint&0x80 == 0 {
		return len(buffer), nil
	}
	if len(fake.responses) == 0 {
		return 0, fmt.Errorf("unexpected read")
	}
	response := fake.responses[0]
	fake.responses = fake.responses[1:]
	return copy(buffer, response), nil
}

func TestI2CWrite(t *testing.T) {
	fake := &fakeBulk{}
	bridge := opened(fake)
	fake.responses = [][]byte{{0}}
	if err := bridge.Write(0x64, []byte{0x02}); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x08, 0x64, 1, 0x02}
	if len(fake.calls) != 2 || fake.calls[0].endpoint != 0x01 || !bytes.Equal(fake.calls[0].data, want) || fake.calls[1].endpoint != 0x81 {
		t.Fatalf("calls = %#v", fake.calls)
	}
}

func TestI2CReadStripsBridgeStatus(t *testing.T) {
	fake := &fakeBulk{}
	bridge := opened(fake)
	fake.responses = [][]byte{{0, 0x80, 0x41}}
	got, err := bridge.Read(0x64, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{0x80, 0x41}) {
		t.Fatalf("Read = %x", got)
	}
	want := []byte{0x09, 0, 2, 0x64}
	if !bytes.Equal(fake.calls[0].data, want) {
		t.Fatalf("command = %x; want %x", fake.calls[0].data, want)
	}
}

// TestNewDrainsALeftoverAnswer is the case that cost a session on macOS: a
// program that stopped between a command and its answer leaves the answer at
// the endpoint, and without draining it the next exchange reads it as its own.
func TestNewDrainsALeftoverAnswer(t *testing.T) {
	// Two answers nobody collected, then nothing.
	fake := &fakeBulk{responses: [][]byte{{0x08, 0x00}, {0x01}}}
	bridge := opened(fake)
	if len(fake.responses) != 0 {
		t.Fatalf("%d stale answers were left behind", len(fake.responses))
	}

	// The next exchange must see its own answer, not one of those.
	fake.responses = [][]byte{{0, 0x80, 0x41}}
	got, err := bridge.Read(0x64, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{0x80, 0x41}) {
		t.Fatalf("Read = %x; want 8041, so the answers are still one behind", got)
	}
}

// TestStreamCommandsWaitForNoAnswer pins that the stream controls do not read a
// response. The bridge answers most commands but not these two, and a read after
// one waits out the whole command timeout before failing -- at the last step of
// tuning, so the demodulator locks and the stream never starts.
func TestStreamCommandsWaitForNoAnswer(t *testing.T) {
	for _, want := range []struct {
		name string
		run  func(*Bridge) error
	}{
		{"start the stream", func(bridge *Bridge) error { return bridge.Stream(true) }},
		{"stop the stream", func(bridge *Bridge) error { return bridge.Stream(false) }},
	} {
		// The fake answers nothing, so any read of the answer endpoint fails
		// -- which is what the device does after a stream command.
		fake := &fakeBulk{}
		bridge := opened(fake)
		if err := want.run(bridge); err != nil {
			t.Errorf("%s: %v", want.name, err)
		}
		for _, call := range fake.calls {
			if call.endpoint == 0x81 {
				t.Errorf("%s read the answer endpoint, which answers nothing", want.name)
			}
		}
	}
}

// TestResetDrivesTheT230Pins pins the reset writes. The sequence is the one these
// boards come up with, not the Linux driver's: Linux drives 0x04 and 0xc0 on five
// of the six supported products, and neither pin belongs to a T230 reset.
func TestResetDrivesTheT230Pins(t *testing.T) {
	fake := &fakeBulk{}
	bridge := opened(fake)
	// One acknowledgement per GPIO write. They are set after opening, because
	// New drains the endpoint first and would eat the first of them.
	fake.responses = [][]byte{{0}, {0}, {0}, {0}}
	if err := bridge.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	want := [][]byte{
		{0x0e, 0x87, 0},
		{0x0e, 0x86, 1},
		{0x0e, 0x80, 0},
		{0x0e, 0x80, 1},
	}
	var written [][]byte
	for _, call := range fake.calls {
		if call.endpoint&0x80 == 0 {
			written = append(written, call.data)
		}
	}
	if len(written) != len(want) {
		t.Fatalf("Reset wrote %d commands, want %d: %v", len(written), len(want), written)
	}
	for index, command := range want {
		if !bytes.Equal(written[index], command) {
			t.Errorf("command %d is %v, want %v", index, written[index], command)
		}
	}
	// The pins the Linux driver uses must not appear. A sequence that drove
	// both would look like it worked on a bench and would be neither.
	for _, call := range written {
		if call[1] == 0x04 || call[1] == 0xc0 || call[1] == 0x83 {
			t.Errorf("reset touched %#02x, which belongs to the Linux sequence", call[1])
		}
	}
}

// TestResetWaitsOnTheDemodulatorResetPin: the two 50 ms waits are on the pin
// holding the demodulator in reset, which is what the part needs to come up. The
// first two writes have no wait between them.
func TestResetWaitsOnTheDemodulatorResetPin(t *testing.T) {
	if len(resetSequence) != 4 {
		t.Fatalf("the sequence is %d steps, want 4", len(resetSequence))
	}
	for index, step := range resetSequence {
		switch index {
		case 0, 1:
			if step.delay != 0 {
				t.Errorf("step %d waits %v, want no wait", index, step.delay)
			}
		default:
			if step.delay != 50*time.Millisecond {
				t.Errorf("step %d waits %v, want 50ms", index, step.delay)
			}
		}
	}
}
