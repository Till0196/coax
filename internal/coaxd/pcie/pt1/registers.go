// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package pt1 drives an Earthsoft PT1 or PT2 on Windows.
//
// Neither card has a BDA driver. What they have is Jungo WinDriver, which maps
// the card's one register window into user space and takes no decisions of its
// own, so bring-up, the I2C sequencer, tuning, the DMA and the demultiplexing
// are all done here.
//
// The PT3 shares only the demodulator: different FPGA register map, an I2C
// sequencer taking packed four-bit opcodes where this one takes whole
// instructions carrying a next address, a descriptor ring per channel where
// this one has one page table for all four, and four interleaved streams out of
// the engine (see micropacket).
//
// One card carries four receivers (two ISDB-S, two ISDB-T), one I2C sequencer,
// one aerial supply and one transfer engine, served from one shared handle.
package pt1

// The two cards this drives. The vendor identifier is Xilinx, whose FPGA both
// are built around; the subsystem is Earthsoft's, but the scan matches these.
// The register maps are identical: the cards differ only in demodulator clock
// and memory training time.
const (
	pciVendor    = 0x10ee
	pciDevicePT1 = 0x211a
	pciDevicePT2 = 0x222a

	// cardRevision is what configuration space must say.
	cardRevision = 1
)

// Model is which of the two cards this is.
type Model int

const (
	ModelPT1 Model = iota
	ModelPT2
)

func (model Model) String() string {
	if model == ModelPT1 {
		return "PT1"
	}
	return "PT2"
}

// demodulatorClock is the crystal the demodulators run from. It decides which
// of two initialisation tables each demodulator core is sent.
func (model Model) demodulatorClock() string {
	if model == ModelPT1 {
		return "20 MHz"
	}
	return "25 MHz"
}

// trainingRounds is how many times the memory interface is stepped round at
// bring-up. The card calls it a phase; what it counts is undocumented. The
// count is per card and is the other place the two cards differ.
func (model Model) trainingRounds() int {
	if model == ModelPT1 {
		return 128
	}
	return 166
}

// The registers. One memory window, 32-bit registers addressed by index: n is
// at 4n. Reading and writing the same number reaches different things.
const (
	// regCommand is written to make the card do something, and read to find
	// out what it has done. The two are different registers behind one number.
	regCommand = 0
	// regPower carries the supply for the tuners, the supply on the aerial
	// lead, the reset line and which receivers are asleep.
	regPower = 1
	// regStream is written to start and stop each receiver's share of the
	// transfer, and read for the bytes the I2C sequencer captured.
	regStream = 2
	// regI2C is written one instruction at a time to load the sequencer's
	// program memory.
	regI2C = 4
	// regTable is written with the page frame number of the first page table,
	// which is where the transfer engine starts walking.
	regTable = 5
)

func registerAt(register int) uint64 { return uint64(register) * 4 }

// What a write to the command register asks for. These are whole words rather
// than bits: several carry two bytes set where one would do, and they are kept
// whole rather than reduced to the bits that appear to matter.
const (
	cmdEnableRAM      = 0x00000002 // step the memory interface round once
	cmdRunI2C         = 0x00000004 // run the sequencer's program
	cmdClock          = 0x00000008 // one clock of the serial identity line
	cmdInitTableCount = 0x00000010 // forget how many page tables there are
	cmdAddTable       = 0x00000020 // and count one more
	cmdResetPCI       = 0x01010000
	cmdReleasePCI     = 0x01000000
	cmdResetRAM       = 0x02020000
	cmdReleaseRAM     = 0x02000000
	cmdRegisterTables = 0x0c000040 // start walking from the register above
	cmdForgetTables   = 0x08080000
	cmdDisableRAM     = 0x0b0b0000
)

// What a read of the command register says.
const (
	statusPCIReset = 0x00000001 // the PCI interface has come back
	statusRAMReset = 0x00000002 // and so has the memory interface
	statusRAMPhase = 0x00000004 // toggles as the memory interface is trained
	statusI2CBusy  = 0x00000080 // the sequencer is still running
	statusSynced   = 0x20000000 // the serial identity line has found its start
	statusIdentity = 0x40000000 // one bit of the card's serial number
	statusUnlocked = 0x80000000 // the card will take commands
)

// The bits of the power register. Everything the card powers is here,
// including the supply on the aerial lead -- one register and one write, which
// is why no single receiver owns the supply.
const (
	powerTunersOn = 1 << 0
	// powerSupply11V is the aerial lead fed from the slot's own twelve volts less
	// the drop across the feed; the vendor documents it as eleven. powerSupply15V
	// is the regulated fifteen, and needs the eleven volt bit set beside it.
	powerSupply11V  = 1 << 2
	powerSupply15V  = 1 << 1
	powerNotInReset = 1 << 3
)

// sleepBits is what each receiver contributes to the power register. All four
// are set unconditionally although they ought to follow whether the receiver is
// asleep: what they mean is not known well enough to turn them off, and a
// receiver left powered costs heat.
var sleepBits = [receivers]uint32{
	1 << 4,
	1<<6 | 1<<7,
	1 << 5,
	1<<6 | 1<<8,
}

// Which standard each receiver is for, and where its demodulator answers. Fixed in the card
// and, unlike the PT3's, not derivable from the receiver number: the addresses run downwards
// and the two standards alternate.
//
//	0  satellite    demodulator 0x1b, tuner 0x60
//	1  terrestrial  demodulator 0x1a, tuner 0x61
//	2  satellite    demodulator 0x19, tuner 0x60
//	3  terrestrial  demodulator 0x18, tuner 0x61
const receivers = 4

var demodAddresses = [receivers]byte{0x1b, 0x1a, 0x19, 0x18}

// Four tuners share two addresses: each sits behind its own demodulator's
// repeater rather than on the card's own bus.
var tunerAddresses = [receivers]byte{0x60, 0x61, 0x60, 0x61}

func satelliteReceiver(receiver int) bool { return receiver%2 == 0 }
