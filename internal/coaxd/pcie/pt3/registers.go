// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package pt3 drives an Earthsoft PT3 on Windows.
//
// The card has no BDA driver, only Jungo WinDriver, which maps its registers
// into user space and takes no decisions of its own; bring-up, the I2C master,
// tuning and the DMA are all done here. A port of the pt3 driver kept in the
// pt3 directory of this repository.
//
// One card carries four channels: two ISDB-S and two ISDB-T, sharing two
// tuners, one I2C master and one supply for the aerial. Channel n uses tuner
// n&1. WinDriver allows a single open of a card, so the four share one handle.
package pt3

// The FPGA revision is checked because the register map below is that
// revision's; anything else is refused.
const (
	// pciVendor is Altera, whose FPGA the card is built around; the subsystem
	// is Earthsoft's but the scan matches on these two.
	pciVendor = 0x1172
	pciDevice = 0x4c15

	cardModel    = 3    // the top byte of the version register
	cardFPGA     = 0x04 // the register map this package speaks
	cardRevision = 1    // the PCI revision, from configuration space
)

// The card's two memory windows, in the order it declares them: the registers,
// then the memory the I2C master reads its instructions out of and writes its
// captured bytes into. The second is byte-addressed: a 32-bit access there
// writes the three bytes beside the one meant.
//
// These are positions in that list, not address space numbers: the windows come
// from base address registers 0 and 2. See windriver.Device.Spaces.
const (
	registerWindow    = 0
	instructionWindow = 1
)

// The registers, all in the first window.
const (
	regVersion  = 0x00 // model, register map and FPGA revision
	regBus      = 0x04
	regSystemW  = 0x08 // written: the supply on the aerial lead
	regSystemR  = 0x0c
	regI2CWrite = 0x10 // written: where to start, and go
	regI2CRead  = 0x14 // read: busy, and how it went
)

// The per-channel block, which repeats every channelStride from channelBase.
const (
	channelBase   = 0x40
	channelStride = 0x18

	regDescriptorLow  = 0x00 // where the descriptor ring starts
	regDescriptorHigh = 0x04
	regDMAControl     = 0x08 // bit 0 run, bit 1 stop
	regTSControl      = 0x0c // test pattern, and the error-counter reset
	regStatus         = 0x10 // bit 0: the transfer is running
	regTSErrors       = 0x14 // a Gray-coded count of unusable packets
)

// channelRegister is where a channel's copy of one of the registers above sits.
// The blocks are in this package's channel order -- both satellite receivers
// then both terrestrial ones -- so nothing is translated here. Proved on the
// card: with one receiver awake and three asleep, only that receiver's transfer
// carries anything.
func channelRegister(channel int, register uint64) uint64 {
	return channelBase + channelStride*uint64(channel) + register
}

// What the I2C register bits mean.
const (
	i2cAddressMask = 0x1fff // the start address, counted in instructions
	i2cGo          = 1 << 16
	i2cReset       = 1 << 17

	i2cBusy  = 1 << 0
	i2cError = 0x6 // bits 1 and 2: the slave did not answer as expected
	i2cClean = 1 << 3
)

// Where things live in the second window. Instructions are addressed in
// instructions rather than bytes -- two to a byte -- so halving an instruction
// address gives a byte offset. The card writes captured bytes to the same
// window starting at the base, which is only safe because the capturing program
// has finished by then.
const (
	instructionBase = 2048
	// instructionRoom bounds any one transfer: the card's address field is
	// thirteen bits wide.
	instructionRoom = 4096
)

// throughRegister is the demodulator register that repeats what follows it to
// the tuner behind it. Neither tuner is on the card's own bus.
const throughRegister = 0xfe

// qmIdentity is what the satellite tuner's first register reads: the one value on
// this card known in advance, so a newly written I2C path is checked against it.
const qmIdentity = 0x48

// The supply on the aerial lead, as the system register wants it. Bit 0 is the
// supply and bit 1 chooses the voltage; two further bits are set in all three
// settings for no documented reason and are kept exactly as they stand.
const (
	supplyOff  uint32 = 0x0c
	supplyLow  uint32 = 0x0d // 11 V
	supplyHigh uint32 = 0x0f // 15 V
)

// channels is how many receivers a card has. The first two are satellite and the
// last two terrestrial; each of the two tuners serves one of each.
const channels = 4

func satelliteChannel(channel int) bool { return channel < 2 }

func tunerOf(channel int) int { return channel & 1 }

// The two programs the card holds in its own memory, as instruction addresses;
// neither can be read out. They are the demodulator and terrestrial tuner
// initialisation and carry this board's configuration as well as the chips',
// which is why no driver for this card has those sequences in its source. See
// bringUp.
const (
	romInitDemods = 4096
	romInitTuners = 4096 + 2042
)

// The direct memory access, as the card arranges it. The card walks a ring of
// descriptors itself; there is no register saying how far it has got and no
// interrupt, and it just overwrites what is there. The way to tell is a byte in
// front of every block that transport stream data cannot begin with, watched
// for change. See ring.
const (
	// transferSize is what one descriptor covers. A transfer must not cross a
	// four gigabyte boundary, and one page cannot.
	transferSize = 4096
	// ringTransfers has to make a whole number of transport packets and a whole
	// number of blocks: 47 pages is exactly 1024 packets, and this is four of
	// those.
	ringTransfers = 188
	ringSize      = ringTransfers * transferSize

	// blockSize is the unit the sentinel below guards: 128 transport packets.
	blockSize  = packetSize * 128
	ringBlocks = ringSize / blockSize

	packetSize = 188

	// sentinel starts every block and is what the card overwrites. It cannot be
	// mistaken for data: a block begins with a packet, and a packet begins 0x47.
	sentinel = 0x74

	// startBlocks is how many blocks after the card starts are not handed on.
	// What it writes first is still what the demodulator produced before the
	// transfer began, packets flagged with transport errors among it.
	startBlocks = 4

	// descriptorSize is one ring entry: where the transfer goes, how much of it,
	// and where the next entry is.
	descriptorSize = 20

	descriptorAddressLow  = 0x00
	descriptorAddressHigh = 0x04
	descriptorBytes       = 0x08
	descriptorNextLow     = 0x0c
	descriptorNextHigh    = 0x10
)

// What the transfer control register does.
const (
	dmaStart   = 0x01
	dmaStop    = 0x02
	dmaRunning = 0x01 // in the status register
)
