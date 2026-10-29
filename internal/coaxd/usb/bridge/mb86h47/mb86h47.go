// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package mb86h47 drives the Fujitsu MB86H47A USB bridge of the CECH-ZD1 J
// as it comes up at power-on. The host reaches every block of the
// chip through vendor request 0xbc, which reads and writes chip memory, and
// builds the stream path from the hardware alone:
//
//   - the demodulator is powered through GPIO 0 and 6 and reached over the
//     bridge's I2C master, whose transfers codec core 1, an FR CPU of the
//     chip's own, runs from a mailbox (i2c.go);
//   - the TS engine at 0xD00000 takes the demodulator's parallel output and
//     writes it, unfiltered and untouched, into an SDRAM ring as 192-byte
//     units (a four-byte arrival timestamp and the packet);
//   - codec core 0, another such CPU, runs core0Program, which moves the
//     units from the ring to its packetizer and out to endpoint 0x81;
//   - endpoint 0x81 is switched to DMA mode.
//
// What arrives is the broadcast transport stream as the demodulator delivered
// it. The card slot is a smart-card UART the host drives as well (card.go).
package mb86h47

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/usb/bridge/mb86h47/regs"
	"github.com/Till0196/coax/pkg/usbio"
)

// Device is the subset of usbio.Transport the bridge needs.
type Device interface {
	Control(requestType, request byte, value, index uint16, buffer []byte, timeoutMS uint32) (int, error)
	Bulk(endpoint byte, buffer []byte, timeoutMS uint32) (int, error)
}

const (
	endpointStream = 0x81
	requestMemory  = 0xbc

	vendorOut = 0x40
	vendorIn  = 0xc0

	controlTimeoutMS = 1000
)

// Registers the driver reaches for by name. They are 16 bits, in the high
// half of their word.
const (
	registerGPIODirection = 0x90014
	registerGPIOValue     = 0x90018

	// addressState reads 0 on a chip that has run nothing since power-up.
	addressState = 0x82008
)

// Bridge is an MB86H47A. Every method is safe to call from several
// goroutines.
type Bridge struct {
	device Device

	mu  sync.Mutex
	err error
	// sequence numbers the transfers handed to codec core 1's mailbox;
	// ringNext is the next ring slot, ringDone the last one core 1 has
	// finished, ringAddresses the slave addresses the ring serves,
	// ringSent the address each recent slot went to and ringHeld the slots
	// not sent yet.
	sequence      byte
	ringNext      uint32
	ringDone      uint32
	ringAddresses [2]byte
	ringSent      [256]byte
	ringHeld      []uint32

	// command is set while the command channel carries the ring's
	// accesses (command.go): commandSequence numbers the next packet,
	// commandSlot is its slot and commandHeld the accesses for it.
	// commandUnanswered is set while a packet sent without asking for an
	// answer may still be being carried out.
	command           bool
	commandSequence   uint32
	commandSlot       uint32
	commandHeld       []uint32
	commandUnanswered bool

	// jobSequence numbers the last job given to core 1 (job.go); spinRate is its
	// wait loop's passes a second, once measured.
	jobSequence uint32
	spinRate    float64

	// steps is stream setup queued by PrepareStream, stepsErr the first
	// failure of a step run by Idle; core0Loaded is set once core 0's
	// program is known to be in place.
	steps       []streamStep
	stepsErr    error
	core0Loaded bool

	streamMu  sync.Mutex
	streaming bool
	reader    usbio.EndpointReader // ReadStream's, once it has read
	framer    *framer
	raw       []byte
}

// New wraps an opened USB device. Nothing is sent until Start.
func New(device Device) *Bridge { return &Bridge{device: device} }

// errNotFresh is Start on a chip that has run something else since power-up.
var errNotFresh = errors.New("MB86H47A is not in its power-up state; unplug the CECH-ZD1 J and plug it back in")

// Start widens the chip's memory request to 32-bit addresses, turns on the
// clocks and the I2C master, and starts codec core 1, which runs the I2C
// transfers. It is safe to call
// again on a chip it already started.
func (bridge *Bridge) Start() error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.err = nil
	if state := bridge.read16(addressState); bridge.err != nil {
		return fmt.Errorf("MB86H47A state: %w", bridge.err)
	} else if state != 0 {
		return errNotFresh
	}
	if err := bridge.widenLocked(); err != nil {
		return err
	}
	bridge.clocksLocked()
	bridge.trapInterruptsLocked()
	bridge.i2cInitLocked()
	if bridge.err != nil {
		return bridge.err
	}
	if err := bridge.core1StartLocked(); err != nil {
		return err
	}
	bridge.commandSetupLocked()
	return nil
}

// Chip memory: wValue carries address bits 16-23 in its high byte and, once
// the request is widened, bits 24-31 in its low byte. The first error is kept
// in bridge.err, so a sequence of accesses is checked once at its end.
func memorySetup(address uint32) (value, index uint16) {
	return uint16(address>>8)&0xff00 | uint16(address>>24), uint16(address)
}

func (bridge *Bridge) read32(address uint32) uint32 {
	buffer := make([]byte, 4)
	value, index := memorySetup(address)
	if n, err := bridge.device.Control(vendorIn, requestMemory, value, index, buffer, controlTimeoutMS); err != nil || n != 4 {
		if bridge.err == nil {
			bridge.err = fmt.Errorf("MB86H47A read %#08x: %d bytes, %v", address, n, err)
		}
		return 0
	}
	return binary.BigEndian.Uint32(buffer)
}

func (bridge *Bridge) write32(address, data uint32) {
	value, index := memorySetup(address)
	if _, err := bridge.device.Control(vendorOut, requestMemory, value, index, binary.BigEndian.AppendUint32(nil, data), controlTimeoutMS); err != nil && bridge.err == nil {
		bridge.err = fmt.Errorf("MB86H47A write %#08x: %w", address, err)
	}
}

// readBlock reads up to 64 bytes in one request.
func (bridge *Bridge) readBlock(address uint32, buffer []byte) {
	value, index := memorySetup(address)
	if n, err := bridge.device.Control(vendorIn, requestMemory, value, index, buffer, controlTimeoutMS); err != nil || n != len(buffer) {
		if bridge.err == nil {
			bridge.err = fmt.Errorf("MB86H47A read %#08x: %d bytes, %v", address, n, err)
		}
	}
}

// readLimit is the most a read returns.
const readLimit = 64

// writeLimit is the most a write carries: the handler buffers the data stage
// in 64 bytes.
const writeLimit = 64

// writeBlock writes up to writeLimit bytes in one request.
func (bridge *Bridge) writeBlock(address uint32, data []byte) {
	value, index := memorySetup(address)
	if _, err := bridge.device.Control(vendorOut, requestMemory, value, index, data, controlTimeoutMS); err != nil && bridge.err == nil {
		bridge.err = fmt.Errorf("MB86H47A write %#08x: %w", address, err)
	}
}

// write16 stores a 16-bit register, which sits in the high half of its word.
func (bridge *Bridge) write16(address uint32, data uint16) {
	bridge.write32(address, uint32(data)<<16|bridge.read32(address)&0xffff)
}

func (bridge *Bridge) read16(address uint32) uint16 { return uint16(bridge.read32(address) >> 16) }

func (bridge *Bridge) set32(address, bits uint32) {
	bridge.write32(address, bridge.read32(address)|bits)
}

func (bridge *Bridge) clear32(address, bits uint32) {
	bridge.write32(address, bridge.read32(address)&^bits)
}

// standbyBlocks are the engine blocks whose control block (the layout of
// regs.Cluster) the clock set-up gives the standby mode: the four clusters,
// then the audio and codec cores.
var standbyBlocks = []uint32{
	regs.ClusterCoreBase, regs.ClusterHelperBase, regs.ClusterDecoderBase, regs.ClusterSpareBase,
	0xe70000, 0xc70000, 0xd00000, 0x1600000, core1Base,
}

// clocksLocked sets the clock, reset and SDRAM registers. One that holds its
// value already, as an earlier Start leaves it while the chip stays powered, is
// not written again.
func (bridge *Bridge) clocksLocked() {
	clock, memory := regs.Clock, regs.Memory
	held := map[uint32]uint32{}
	readRun := func(first, last uint32) {
		data := make([]byte, last+4-first)
		bridge.readBlock(first, data)
		for at := uint32(0); at < uint32(len(data)); at += 4 {
			held[first+at] = binary.BigEndian.Uint32(data[at:])
		}
	}
	readRun(clock.PLL.Addr(), clock.Unk12C.Addr())
	readRun(memory.Format2[1].Addr(), memory.Format2[1].Addr())
	readRun(memory.Planes2[2].Chroma[0].Addr(), memory.Planes2[4].Chroma[1].Addr())
	set16 := func(address uint32, data uint16) {
		word, ok := held[address]
		if !ok {
			word = bridge.read32(address)
		}
		if uint16(word>>16) != data {
			bridge.write32(address, uint32(data)<<16|word&0xffff)
		}
	}
	set16(clock.PLL.Addr(), 0x8201)
	set16(clock.Unk114.Addr(), 0x8008)
	set16(clock.Gates.Addr(), 0xffff)
	// Bit 2 of each engine block's standby register (16 bits, in the
	// high half of the word, for the clusters and the audio blocks).
	for _, base := range standbyBlocks {
		address := regs.ClusterAt(base).StandbyMode.Addr()
		if word := bridge.read32(address); word&(regs.ClusterStandbyWait<<16) == 0 {
			bridge.write32(address, word|regs.ClusterStandbyWait<<16)
		}
	}
	set16(clock.Unk11C.Addr(), 6)
	set16(clock.Unk128.Addr(), 0x6000)
	set16(clock.Unk12C.Addr(), 0x4007)
	set16(memory.Format2[1].Addr(), 3)
	for _, reg := range []regs.Reg{memory.Planes2[2].Chroma[0], memory.Planes2[2].Chroma[1], memory.Planes2[4].Chroma[0], memory.Planes2[4].Chroma[1]} {
		set16(reg.Addr(), 0x0880)
	}
}

// The power-up code sets up only the first 20 of the main CPU's interrupt
// vectors (0xFFC00 + 0x3FC - 4n): reset, the exceptions (all to one handler
// that starts the power-up code over) and the four external lines it serves
// USB through. The rest of the table is whatever SRAM held at power-up, and
// some interrupt levels (ICR at 0x440 + source) come up enabled as well.
//
// trapInterruptsLocked masks the sources that come up enabled, and points
// the exceptions and every vector from 20 on at a recorder: it notes the
// vector, the PC and PS the CPU was interrupted at, RP and R15 at TrapRecord
// and then waits with interrupts on, so that USB, and with it the record,
// stays reachable.
const (
	vectorTable     = 0xffc00
	firstFreeVector = 20
	trapStubs       = 0xe3000 // SRAM the power-up code leaves unused
	trapCommon      = 0xe3800
	interruptLevels = 0x440
	// TrapRecord is where a trapped exception or interrupt is noted:
	// magic 0x7ea9, vector, PC, PS, RP, R15, count.
	TrapRecord = 0x41ff0000
)

var (
	trapRecorder = mustBuild(buildTrapRecorder)
	trapStubCode = mustBuild(buildTrapStubs)
)

func (bridge *Bridge) trapInterruptsLocked() {
	// ICR00-ICR63: level 31 masks a source.
	levels := make([]byte, 0x40)
	bridge.readBlock(interruptLevels, levels)
	for offset := uint32(0); offset < 0x40; offset += 4 {
		word := binary.BigEndian.Uint32(levels[offset:])
		if word|0x1f1f1f1f != word {
			bridge.write32(interruptLevels+offset, word|0x1f1f1f1f)
		}
	}
	bridge.writeWords(trapCommon, trapRecorder)
	bridge.writeWords(trapStubs, trapStubCode)
	// Vectors 16-19 are the USB lines the power-up code serves.
	stub := func(vector int) uint32 { return trapStubs + uint32(vector)*trapStubStride }
	for _, run := range [][2]int{{firstFreeVector, 255}, {1, 15}} {
		words := make([]uint32, 0, run[1]-run[0]+1)
		for vector := run[1]; vector >= run[0]; vector-- {
			words = append(words, stub(vector))
		}
		bridge.writeWords(vectorTable+0x3fc-4*uint32(run[1]), words)
	}
}

func (bridge *Bridge) writeWords(address uint32, words []uint32) {
	data := make([]byte, 0, len(words)*4)
	for _, word := range words {
		data = binary.BigEndian.AppendUint32(data, word)
	}
	for offset := 0; offset < len(data) && bridge.err == nil; offset += writeLimit {
		bridge.writeBlock(address+uint32(offset), data[offset:min(offset+writeLimit, len(data))])
	}
}

// ResetDemodulator powers the DiB8096 (GPIO 6) and pulses its reset (GPIO 0).
func (bridge *Bridge) ResetDemodulator() error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.err = nil
	if err := bridge.ringDrainLocked(); err != nil {
		return err
	}
	value, direction := bridge.read16(registerGPIOValue), bridge.read16(registerGPIODirection)
	bridge.write16(registerGPIOValue, value&^1|1<<6)
	bridge.write16(registerGPIODirection, direction|1|1<<6)
	time.Sleep(50 * time.Millisecond)
	bridge.write16(registerGPIOValue, value|1|1<<6)
	time.Sleep(10 * time.Millisecond)
	return bridge.err
}

// loadLocked holds the engine core at base in reset and loads program at
// origin. The core's reset vector is the last word of its instruction RAM
// (base+0xFFFFC), which holds nothing at power-up; without it a reset release
// does not start the core.
//
// The chip keeps its RAM while it is powered, so what an earlier Start or
// stream loaded is usually there still, and a request takes milliseconds on
// some hosts. A program noted at loadedSignature is not loaded again, and
// otherwise only what differs from what is read back is written.
func (bridge *Bridge) loadLocked(base, origin uint32, program []uint32) {
	bridge.write32(base+0xffffc, origin)
	bridge.write32(base, 0xffffffff)
	signature := signatureOf(origin, program)
	noted := make([]byte, 8)
	bridge.readBlock(base+loadedSignature, noted)
	if bridge.err == nil && binary.BigEndian.Uint32(noted) == signatureMagic && binary.BigEndian.Uint32(noted[4:]) == signature {
		return
	}
	// Unnoted while it changes, so a load cut short is not taken for done.
	bridge.write32(base+loadedSignature, 0)
	loaded := make([]byte, 64)
	for first := 0; first < len(program); first += len(loaded) / 4 {
		words := program[first:min(first+len(loaded)/4, len(program))]
		address := base + origin + uint32(first)*4
		bridge.readBlock(address, loaded[:len(words)*4])
		for index, word := range words {
			if binary.BigEndian.Uint32(loaded[index*4:]) != word {
				data := make([]byte, 0, len(words)*4)
				for _, word := range words {
					data = binary.BigEndian.AppendUint32(data, word)
				}
				bridge.writeBlock(address, data)
				break
			}
		}
	}
	if bridge.err == nil {
		bridge.writeWords(base+loadedSignature, []uint32{signatureMagic, signature})
	}
}

// loadedSignature is where loadLocked notes the program a core holds, in its
// data RAM: above its stack, which starts at 0xA37FC and runs down, and below
// its variables. signatureMagic marks the note as one.
const (
	loadedSignature = 0xa3800
	signatureMagic  = 0x10ad5160
)

func signatureOf(origin uint32, program []uint32) uint32 {
	hash := uint32(2166136261)
	for _, word := range append([]uint32{origin}, program...) {
		for shift := 24; shift >= 0; shift -= 8 {
			hash ^= word >> shift & 0xff
			hash *= 16777619
		}
	}
	return hash
}

// mustBuild builds a program of our own, which cannot fail but through a
// mistake in it.
func mustBuild(build func() ([]uint32, error)) []uint32 {
	words, err := build()
	if err != nil {
		panic(err)
	}
	return words
}
