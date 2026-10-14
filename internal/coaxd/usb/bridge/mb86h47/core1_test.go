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
			v := m.dev.regs[m.register]
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
