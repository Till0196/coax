// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mb86h47

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/Till0196/coax/pkg/fr30/sim"
)

// i2cDevice is a device on the I2C bus: 16-bit registers, numbered with two
// bytes or (short) one.
type i2cDevice struct {
	short bool
	regs  map[uint16]uint16
	// computed answers a read of a register its own way, when it says so.
	computed func(register uint16) (uint16, bool)
}

// value is what a read of a register answers.
func (d *i2cDevice) value(register uint16) uint16 {
	if d.computed != nil {
		if v, ok := d.computed(register); ok {
			return v
		}
	}
	return d.regs[register]
}

// i2cBus models the I2C master core 1 drives (0xFD8FE0), with devices at
// 8-bit write addresses. Every step it takes is logged.
type i2cBus struct {
	devices map[byte]*i2cDevice
	log     []string

	data       uint32 // the data register
	busy       bool   // between a start and a stop
	nack       bool   // the last byte sent was not acknowledged
	reading    bool   // the last address had the read bit
	dev        *i2cDevice
	sent       []byte // the bytes after the address, of a write
	register   uint16 // where a read goes on from
	readHigh   bool   // the next byte read is a register's high byte
	stuckBusy  bool   // the bus stays busy (scenario)
	busErrorAt int    // a bus error on this byte (0: none)
	bytes      int
}

func newI2CBus() *i2cBus {
	return &i2cBus{devices: map[byte]*i2cDevice{
		0x10: {regs: map[uint16]uint16{}},
		0x12: {short: true, regs: map[uint16]uint16{}},
	}}
}

// commit ends a write: its register number, then values from it on.
func (m *i2cBus) commit() {
	if m.dev == nil || len(m.sent) == 0 {
		return
	}
	n := 2
	if m.dev.short {
		n = 1
	}
	if len(m.sent) < n {
		return
	}
	reg := uint16(m.sent[0])
	if n == 2 {
		reg = uint16(m.sent[0])<<8 | uint16(m.sent[1])
	}
	m.register, m.readHigh = reg, true
	for i := n; i+1 < len(m.sent); i += 2 {
		m.dev.regs[reg] = uint16(m.sent[i])<<8 | uint16(m.sent[i+1])
		reg++
	}
	m.sent = nil
}

func (m *i2cBus) write(address uint32, value uint32) {
	switch address {
	case i2cData:
		m.data = value & 0xff
		m.log = append(m.log, fmt.Sprintf("data %02x", m.data))
	case i2cControl:
		m.log = append(m.log, fmt.Sprintf("control %02x", value))
		m.bytes++
		switch {
		case value == 0x40: // stop
			m.commit()
			m.busy, m.dev, m.reading = false, nil, false
		case value == 0x70 || value == 0x50 && (!m.busy || !m.reading) || value == 0x58 && !m.reading:
			// A byte out: an address after a (repeated) start, or data.
			start := value == 0x70 || !m.busy
			m.busy = true
			if start {
				m.commit()
				d, ok := m.devices[byte(m.data)&0xfe]
				m.dev, m.nack, m.reading = d, !ok, m.data&1 != 0
				if !ok {
					m.dev = nil
				}
				return
			}
			m.sent = append(m.sent, byte(m.data))
			m.nack = m.dev == nil
		default: // a byte in (0x58 acknowledged, 0x50 the last)
			if m.dev == nil {
				m.data = 0xff
				return
			}
			v := m.dev.value(m.register)
			if m.readHigh {
				m.data = uint32(v >> 8)
			} else {
				m.data = uint32(v & 0xff)
				m.register++
			}
			m.readHigh = !m.readHigh
		}
	default:
		m.log = append(m.log, fmt.Sprintf("write %x %x", address, value))
	}
}

func (m *i2cBus) read(address uint32) uint32 {
	switch address {
	case i2cControl:
		v := uint32(1) // done
		if m.busErrorAt != 0 && m.bytes == m.busErrorAt {
			v |= 0x80
		}
		return v
	case i2cStatus:
		var v uint32
		if m.busy || m.stuckBusy {
			v |= 0x80
		}
		if m.nack {
			v |= 0x10
		}
		return v
	case i2cData:
		return m.data
	}
	return 0
}

// core1Run is core 1's program on a simulated core with the bus, and what
// came out on endpoint 0x83.
type core1Run struct {
	cpu      *sim.CPU
	bus      *i2cBus
	answer   []uint32 // words put in endpoint 0x83's FIFO; 0xFFFFFFFF where it was sent
	ringNext uint32
	seq      uint32
	slot     uint32
}

func newCore1Run(t *testing.T, program []uint32) *core1Run {
	r := &core1Run{cpu: sim.New(), bus: newI2CBus()}
	c := r.cpu
	for i, w := range program {
		c.Write(core1Origin+uint32(i)*4, 4, w)
	}
	c.AddRegion(sim.Region{Start: i2cBase, End: i2cBase + 0x20,
		Read:  func(a uint32, size int) uint32 { return r.bus.read(a) },
		Write: func(a uint32, size int, v uint32) { r.bus.write(a, v) }})
	c.AddRegion(sim.Region{Start: 0xfdbd00, End: 0xfdbe00,
		Read: func(a uint32, size int) uint32 { return 0 },
		Write: func(a uint32, size int, v uint32) {
			switch a {
			case answerFIFO:
				r.answer = append(r.answer, v)
			case answerSend:
				r.answer = append(r.answer, 0xffffffff)
			}
		}})
	// As core1StartLocked leaves things.
	r.seq = 0x1234
	c.Write(commandVars+commandSlot, 4, commandBase)
	c.Write(commandVars+commandError, 4, 0)
	c.Write(commandVars+commandExpect, 4, r.seq)
	c.Write(ringAnswer, 4, (ringSlots-1)<<24)
	c.Write(ringAddresses, 4, 0x10<<24|0x12<<16)
	r.ringNext = ringSlots
	c.PC = core1Origin
	r.run(t)
	return r
}

// run lets the core go on until it is done with what it was given: nothing
// the host or the devices see changes for a while (longer than its longest
// wait on the bus).
func (r *core1Run) run(t *testing.T) {
	r.cpu.StopAt = 0xffffffff
	last, same := "", 0
	for chunk := 0; chunk < 400 && same < 12; chunk++ {
		if err := r.cpu.Run(1_000_000); err != nil && !strings.Contains(err.Error(), "still running") {
			t.Fatal(err)
		}
		now := r.state() + fmt.Sprint(len(r.bus.log), r.cpu.Read(commandVars+commandExpect, 4))
		if now == last {
			same++
		} else {
			last, same = now, 0
		}
	}
}

// ring posts accesses in the ring.
func (r *core1Run) ring(accesses ...uint32) {
	for _, a := range accesses {
		if r.ringNext/ringSlots%2 == 1 {
			a |= ringLap
		}
		r.cpu.Write(ring+r.ringNext%ringSlots*4, 4, a)
		r.ringNext++
	}
}

// packet sends a command packet.
func (r *core1Run) packet(answer bool, words ...uint32) {
	p := make([]byte, commandSize)
	binary.BigEndian.PutUint32(p, r.seq)
	flags := uint32(len(words))
	if answer {
		flags |= commandAnswer
	}
	binary.BigEndian.PutUint32(p[4:], flags)
	for i, w := range words {
		binary.BigEndian.PutUint32(p[8+4*i:], w)
	}
	binary.BigEndian.PutUint32(p[commandSize-4:], r.seq)
	r.cpu.Load(commandBase+r.slot%commandSlots*commandSize, p)
	r.seq++
	r.slot++
}

// state is everything the host and the devices see.
func (r *core1Run) state() string {
	c := r.cpu
	s := fmt.Sprintf("ring answer %08x failure %08x mail answer %08x read % x\nanswer %x\n",
		c.Read(ringAnswer, 4), c.Read(ringFailure, 4), c.Read(mailbox+mailboxAnswer, 4), c.Bytes(mailbox+mailboxRead, 32), r.answer)
	for _, a := range []byte{0x10, 0x12} {
		s += fmt.Sprintf("device %02x: %v\n", a, r.bus.devices[a].regs)
	}
	s += fmt.Sprintf("memory % x\n", c.Bytes(0x20000, 64))
	return s
}

// Our core 1 program serves the ring and the command packets: accesses
// reach the devices and come back.
func TestCore1Model(t *testing.T) {
	r := newCore1Run(t, core1Program)
	r.ring(0x0005<<16 | 0xbeef)
	r.ring(ringRead | 0x0005<<16)
	r.run(t)
	if got := r.cpu.Read(ringAnswer, 4) & 0xffff; got != 0xbeef {
		t.Fatalf("ring read %x, want beef; %s bus %v", got, r.state(), r.bus.log)
	}
	r.packet(true, ringShort|0x03<<16|0x1234, ringShort|ringRead|0x03<<16, memoryAccess, 0x20000, 0xcafef00d, memoryAccess|memoryRead, 0x20000)
	r.run(t)
	if len(r.answer) < 5 || r.answer[2] != 0x1234 || r.answer[3] != 0xcafef00d {
		t.Fatalf("packet answer %x; %s", r.answer, r.state())
	}
}

// captrimReference is the search as the DiB0090's state machine runs it from
// the host (dib0090_captrim_search before P1G), with the tuner's int8 trim.
func captrimReference(start, step, target int, adcOf func(trim int8) uint16) (int, int) {
	captrim, fcaptrim, s := int8(start), int8(start), int8(step)
	adcDiff := int16(3000)
	for {
		s /= 2
		adc := adcOf(captrim)
		var sign int8
		if int(adc) >= target {
			adc -= uint16(target)
			sign = -1
		} else {
			adc = uint16(target) - adc
			sign = 1
		}
		if int(adc) < int(adcDiff) {
			adcDiff = int16(adc)
			fcaptrim = captrim
		}
		captrim += sign * s
		if s < 1 {
			return int(fcaptrim), int(adcDiff)
		}
	}
}

// runJob gives core 1 a job and runs it, returning its results.
func runJob(t *testing.T, r *core1Run, sequence, kind uint32, params ...uint32) []uint32 {
	t.Helper()
	c := r.cpu
	c.Write(jobArea+jobKind, 4, kind)
	for i, p := range params {
		c.Write(jobArea+jobParam+uint32(i)*4, 4, p)
	}
	c.Write(jobArea+jobSequence, 4, sequence)
	r.run(t)
	if done := c.Read(jobArea+jobDone, 4); done != sequence {
		t.Fatalf("job done %d, want %d", done, sequence)
	}
	if code := c.Read(jobArea+jobError, 4); code != i2cOK {
		t.Fatalf("job error %d", code)
	}
	results := make([]uint32, jobResults)
	for i := range results {
		results[i] = c.Read(jobArea+jobResult+uint32(i)*4, 4)
	}
	return results
}

// trimLo4 is the top bits written with every trim.
const trimLo4 = 0xe900

// runCaptrim runs a trim search with waits of 5, 7 and 11 passes, the ADC
// answering adcOf of the trim set. It gives the results and the passes waited.
func runCaptrim(t *testing.T, r *core1Run, sequence, key uint32, start, step, target int, adcOf func(int8) uint16) (best, diff int, remembered bool, spun uint32) {
	t.Helper()
	tuner := r.bus.devices[0x12]
	tuner.computed = func(register uint16) (uint16, bool) {
		if register != 0x1d {
			return 0, false
		}
		return adcOf(int8(tuner.regs[0x18])), true
	}
	before := r.cpu.Read(jobArea+jobSpin, 4)
	results := runJob(t, r, sequence, jobCaptrimKind,
		0x12, trimLo4, uint32(int32(start)), uint32(int32(step)), uint32(int32(target)), 5, 7, 11, key)
	return int(int32(results[0])), int(int32(results[1])), results[2] != 0, r.cpu.Read(jobArea+jobSpin, 4) - before
}

// The capacitor trim search core 1 runs ends where the host's would, sets
// the trim it found, and takes the waits it was given.
func TestCore1Captrim(t *testing.T) {
	for _, tc := range []struct {
		name                string
		start, step, target int
		best                int // the trim the ADC reads target at
	}{
		{"full search", 64, 64, 200, 41},
		{"full search, other target", 64, 64, 400, 17},
		{"around the trim found before", 41, 4, 200, 43},
		{"past the top of the int8", 126, 4, 200, 127},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newCore1Run(t, core1Program)
			adcOf := func(trim int8) uint16 {
				d := int(trim) - tc.best
				if d < 0 {
					d = -d
				}
				return uint16(tc.target + 7*d*(1-2*(int(trim)&1)) + 3*int(trim) - 3*tc.best)
			}
			best, diff, _, spun := runCaptrim(t, r, 1, 0, tc.start, tc.step, tc.target, adcOf)
			wantBest, wantDiff := captrimReference(tc.start, tc.step, tc.target, adcOf)
			if best != wantBest || diff != wantDiff {
				t.Errorf("trim %d (%d from target), want %d (%d)", best, diff, wantBest, wantDiff)
			}
			if set, want := r.bus.devices[0x12].regs[0x18], uint16(trimLo4)|uint16(int16(int8(wantBest))); set != want {
				t.Errorf("register 0x18 left at %#04x, want %#04x", set, want)
			}
			steps := 0
			for s := tc.step / 2; ; s /= 2 {
				steps++
				if s < 1 {
					break
				}
			}
			if want := uint32(5 + steps*(7+11)); spun != want {
				t.Errorf("waited %d passes, want %d", spun, want)
			}
		})
	}
}

// The wait loop the host measures core 1's rate on runs until the host gives
// another job, and core 1 then takes that one as done.
func TestCore1SpinStops(t *testing.T) {
	r := newCore1Run(t, core1Program)
	c := r.cpu
	c.Write(jobArea+jobKind, 4, jobSpinKind)
	c.Write(jobArea+jobParam, 4, 100_000_000)
	c.Write(jobArea+jobSequence, 4, 1)
	if err := c.Run(200_000); err != nil && !strings.Contains(err.Error(), "still running") {
		t.Fatal(err)
	}
	spun := c.Read(jobArea+jobSpin, 4)
	if spun == 0 || c.Read(jobArea+jobDone, 4) != 0 {
		t.Fatalf("after a while: %d passes, done %d; want passes and not done", spun, c.Read(jobArea+jobDone, 4))
	}
	c.Write(jobArea+jobSequence, 4, 2)
	r.run(t)
	if done := c.Read(jobArea+jobDone, 4); done != 2 {
		t.Fatalf("done %d after the stop, want 2", done)
	}
}

// A second search at a frequency starts from the trim the first ended at,
// with Linux's minimal step, and ends where the host's minimal search would.
func TestCore1CaptrimRemembers(t *testing.T) {
	r := newCore1Run(t, core1Program)
	adcOf := func(trim int8) uint16 { return uint16(200 + 9*int(trim) - 9*41) }
	const key = 527142

	best, _, remembered, _ := runCaptrim(t, r, 1, key, 64, 64, 200, adcOf)
	if remembered {
		t.Fatal("the first search at a frequency started from a remembered trim")
	}
	wantBest, _ := captrimReference(64, 64, 200, adcOf)
	if best != wantBest {
		t.Fatalf("first search ended at %d, want %d", best, wantBest)
	}

	// The trim has drifted since: the ADC now reads target one step higher.
	drifted := func(trim int8) uint16 { return uint16(200 + 9*int(trim) - 9*42) }
	again, diff, remembered, spun := runCaptrim(t, r, 2, key, 64, 64, 200, drifted)
	if !remembered {
		t.Fatal("the second search did not start from the remembered trim")
	}
	wantAgain, wantDiff := captrimReference(best, rememberedStep, 200, drifted)
	if again != wantAgain || diff != wantDiff {
		t.Errorf("second search ended at %d (%d), want %d (%d)", again, diff, wantAgain, wantDiff)
	}
	if want := uint32(5 + 3*(7+11)); spun != want {
		t.Errorf("second search waited %d passes, want the %d of three steps", spun, want)
	}

	// Another frequency is a search of its own.
	if _, _, remembered, _ := runCaptrim(t, r, 3, key+6000, 64, 64, 200, adcOf); remembered {
		t.Error("a frequency the table does not have started from a remembered trim")
	}
}

// A table with no free entry gives up its entries in turn.
func TestCore1TableTakesEntriesInTurn(t *testing.T) {
	r := newCore1Run(t, core1Program)
	c := r.cpu
	for i := uint32(0); i < tableEntries; i++ {
		c.Write(tableBase+i*tableEntry, 4, 1000+i)
		c.Write(tableBase+i*tableEntry+4, 4, 50)
	}
	adcOf := func(trim int8) uint16 { return uint16(200 + 9*int(trim) - 9*41) }
	for i, key := range []uint32{5000, 6000} {
		if _, _, remembered, _ := runCaptrim(t, r, uint32(i+1), key, 64, 64, 200, adcOf); remembered {
			t.Fatalf("key %d started from a remembered trim", key)
		}
		if got := c.Read(tableBase+uint32(i)*tableEntry, 4); got != key {
			t.Errorf("entry %d holds key %d, want %d", i, got, key)
		}
	}
	if next := c.Read(tableNext, 4); next != 2 {
		t.Errorf("next entry %d, want 2", next)
	}
	if got := c.Read(tableBase+2*tableEntry, 4); got != 1002 {
		t.Errorf("entry 2 holds key %d, want the 1002 it had", got)
	}
}

// The program ends before the table, and the table before the stack's
// lowest reach.
func TestCore1LeavesTheTableAlone(t *testing.T) {
	if end := core1Origin + uint32(len(core1Program))*4; end > programLimit {
		t.Errorf("core 1's program ends at %#x, past the table at %#x", end, programLimit)
	}
	if end := uint32(tableNext + 4); end > core1Stack-0x400 {
		t.Errorf("the table ends at %#x, within a kilobyte of the stack at %#x", end, core1Stack)
	}
}

// Parameters are kept per frequency and given back, a change is reported, and
// the trim kept for the frequency is left alone.
func TestCore1RemembersTMCC(t *testing.T) {
	r := newCore1Run(t, core1Program)
	demod := r.bus.devices[0x10]
	// Mode 3, guard 1/8, partial reception; layer A one segment of QPSK 2/3
	// with interleaving 4, layer B twelve of 64QAM 3/4 with interleaving 2.
	for register, value := range map[uint16]uint16{
		570: 0x0032 | 0xff00, 505: 1,
		493: 1, 494: 12, 495: 0x0f,
		499: 3, 500: 2, 501: 0,
		481: 2, 482: 3, 483: 1,
		487: 1, 488: 3, 489: 3,
	} {
		demod.regs[register] = value
	}
	const key = 551143
	layer := func(segments, interleaving, rate, modulation uint32) uint32 {
		return segments | interleaving<<4 | rate<<6 | modulation<<9
	}
	want := [2]uint32{0x32 | 1<<8 | layer(1, 3, 2, 1)<<12, layer(12, 2, 3, 3) | layer(15, 0, 1, 3)<<16}

	if got := runJob(t, r, 1, jobTMCCRecallKind, key); got[0] != 0 {
		t.Fatal("recalled parameters for a frequency nothing was remembered for")
	}
	got := runJob(t, r, 2, jobTMCCRememberKind, 0x10, key)
	if got[0] != 0 || got[1] != want[0] || got[2] != want[1] {
		t.Fatalf("remembered changed=%d %08x %08x, want 0 %08x %08x", got[0], got[1], got[2], want[0], want[1])
	}
	got = runJob(t, r, 3, jobTMCCRecallKind, key)
	if got[0] != 1 || got[1] != want[0] || got[2] != want[1] {
		t.Fatalf("recalled %d %08x %08x, want 1 %08x %08x", got[0], got[1], got[2], want[0], want[1])
	}

	// A trim search there keeps the parameters, and they keep the trim.
	adcOf := func(trim int8) uint16 { return uint16(200 + 9*int(trim) - 9*41) }
	best, _, _, _ := runCaptrim(t, r, 4, key, 64, 64, 200, adcOf)
	if got = runJob(t, r, 5, jobTMCCRecallKind, key); got[0] != 1 || got[1] != want[0] {
		t.Fatal("a trim search lost the parameters remembered with it")
	}
	if got = runJob(t, r, 6, jobTMCCRememberKind, 0x10, key); got[0] != 0 {
		t.Fatal("the same parameters again were reported as changed")
	}
	if _, _, remembered, _ := runCaptrim(t, r, 7, key, 64, 64, 200, adcOf); !remembered {
		t.Fatalf("remembering parameters lost the trim %d", best)
	}

	// The multiplex changes: layer B's code rate is now 2/3.
	demod.regs[482] = 2
	got = runJob(t, r, 8, jobTMCCRememberKind, 0x10, key)
	if got[0] != 1 || got[2]&0x7ff != layer(12, 2, 2, 3) {
		t.Fatalf("after a change: changed=%d layer B %03x, want 1 %03x", got[0], got[2]&0x7ff, layer(12, 2, 2, 3))
	}
	if got = runJob(t, r, 9, jobTMCCRecallKind, key); got[2]&0x7ff != layer(12, 2, 2, 3) {
		t.Fatal("the changed parameters were not kept")
	}
}
