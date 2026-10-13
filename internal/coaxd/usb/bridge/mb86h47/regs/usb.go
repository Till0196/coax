// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package regs

// USBBase is the base of the USB unit: its block enables and resets, its
// events, the DMA channels between SDRAM and the endpoints, the I2C master
// (FD8FE0) and, from FDBC00, the USB core with its endpoints.
const USBBase = 0xfd8000

// USBUnit is the USB unit at FD8000. Its registers are 32 bits wide.
// The USB core's registers (from FDBC00) take their byte lanes from
// Lanes, which the power-up code sets to all four (0xF) after each access
// it makes and which is back at all lanes on every host request (mb86h47
// stream.go): whole words are written.
type USBUnit struct {
	// Unk008 is 0xFF at start, 0 after a USB event; bit 4 goes with
	// DMA[9].
	Unk008 Reg // 0x008
	// Unk00C picks what EP2DMA serves: 4 for endpoint 0x02's transfers,
	// 8 for the power-up code's own transfers, 0 when stopped. Our code
	// leaves it alone.
	Unk00C Reg // 0x00c
	// Enable and Reset are the unit's blocks (USBBlock...): a block runs
	// with its Enable bit set and its Reset bit clear, and is stopped the
	// other way round.
	Enable Reg // 0x010
	Reset  Reg // 0x014
	// Status is the unit's events (group B bit 11 of the interrupt
	// unit): bit 0 EPStatus/IRQStatus have one, bit 2 the core's
	// (CoreStatus), bit 3 another (Unk084 bit 29, FDB424).
	Status Reg // 0x080
	// Unk084: bit 29 goes with Status bit 3.
	Unk084 Reg // 0x084
	// Unk08C: bit 30 a USB reset, which ends the power-up code's
	// control transfers and is written back to clear it.
	Unk08C Reg // 0x08c
	// IRQStatus is the I2C master's event (bit 0), enabled by IRQEnable.
	IRQStatus Reg // 0x090
	// EPStatus is the endpoints' DMA events: bit 2 EP2DMA's transfer
	// done, write 1 to clear (the next transfer then needs Dest moved on
	// by Length and a new Go), bits 18-19 events of the other channels.
	EPStatus Reg // 0x094
	Unk0A4   Reg // 0x0a4
	Unk0AC   Reg // 0x0ac
	// IRQEnable enables IRQStatus (1).
	IRQEnable Reg // 0x0b0
	// EPEnable enables EPStatus to the power-up code's interrupt; our
	// code writes 0 because our loop takes the event.
	EPEnable Reg // 0x0b4
	// Unk0C0 and Unk0C4 are part of the I2C master's set-up; our code
	// pulses Unk0C0 1 then 0.
	Unk0C0, Unk0C4 Reg // 0x0c0, 0x0c4
	Unk140         Reg // 0x140
	// Lanes is the byte lanes of the next access to the USB core's
	// registers, a bit a byte (0xF all four; narrowed for the last word
	// of a packet).
	Lanes Reg // 0x14c

	// DMA are the DMA channels between SDRAM and the endpoints
	// (0x200 + 0x20*i): DMA[0] and DMA[1] feed endpoint 0x81 (the
	// stream), DMA[8] takes what arrives on endpoint 0x02 (our command
	// channel needs endpoint 0x02, so our code holds DMA[8]); DMA[3], [4]
	// and [9] serve other configurations.
	DMA [10]USBDMA
	// EP2DMA is the DMA of endpoint 0x02 into SDRAM (0x500).
	EP2DMA USBEP2DMA
	// I2C is the I2C master (0xFE0).
	I2C I2CUnit

	Unk3C04 Reg // 0x3c04 (FDBC04)
	// CoreStatus is the core's state and events: bit 16 high speed,
	// bits 20-23 read at a reset, bits 27, 29, 30 end a control transfer,
	// 0x6B000000 events (written back 0).
	CoreStatus Reg // 0x3c08 (FDBC08)
	// Unk3C0C must not be written: with 0x72 the power-up code's USB
	// interrupt no longer returns to its wait loop.
	Unk3C0C Reg // 0x3c0c (FDBC0C)
	// EPMode is the endpoints' modes: bit 16+n puts endpoint n in DMA
	// mode, bit n enables its interrupt (EP1 comes up in interrupt
	// mode; without DMA mode its packetizer stalls once the buffer is
	// full).
	EPMode  Reg // 0x3c14 (FDBC14)
	Unk3C70 Reg // 0x3c70 (FDBC70)
	// Endpoint is each endpoint's control pair (FDBD00 + 8*n).
	Endpoint [6]USBEndpoint
	// FIFO is each endpoint's FIFO (FDBD80 + 4*n): the power-up code
	// puts endpoint 0's answers in FIFO[0] and endpoint 0x83's in
	// FIFO[3], as our core 1 does.
	FIFO    [6]Reg
	Unk3DC0 Reg // 0x3dc0 (FDBDC0)
	// EPConfig is each endpoint's set-up (FDBE04 + 4*n), the packet size
	// in the high bits and a code in the low byte: 0x2000000 for
	// endpoint 0, then 0xD1, 0xC2, 0xF3, 0xD4, 0xD5 with 0x200 (high
	// speed) or 0x40 byte packets.
	EPConfig [6]Reg
}

// USBDMA is a DMA channel between SDRAM and an endpoint.
type USBDMA struct {
	// Control: 1 holds the channel, 0 stops it, 2 runs it with Config.
	// Our code writes 1, 0, the Config, 2.
	Control Reg // +0x00
	// Config is the channel's set-up: bit 31 a mode, bits 28-29 set in
	// this configuration, the low bits the channel's buffer (0x9F0000,
	// 0x9F2000 for DMA[0]/[1], 0x1000 for DMA[8]). Our code writes
	// 0xB09F0000 and 0xB09F2000.
	Config Reg // +0x04
}

// USBEP2DMA is endpoint 0x02's DMA into SDRAM. Its destination is an
// offset into window MemoryWindowEP2 of the frame-memory controller.
type USBEP2DMA struct {
	// Control: 2 runs, 0 stops.
	Control Reg // 0x500
	// Go starts a transfer (1).
	Go Reg // 0x504
	// Dest is where the next transfer goes: an offset into the window
	// (20 bits; the DMA takes its low 7 bits as 0). The power-up code
	// writes a full address there (0x80002400) for its own transfers.
	Dest   Reg // 0x508
	Unk50C Reg // 0x50c
	// Length is a transfer's bytes (our code uses a chunk).
	Length Reg // 0x510
	// Unk514 is 0x70002000 or 0x40001000 by the configuration.
	Unk514 Reg // 0x514
}

// USBEndpoint is an endpoint's pair of control registers.
type USBEndpoint struct {
	// Control: bit 0 resets the endpoint's buffer and reads back 1 until
	// done (endpoints 1-5 are reset at a bus reset; our code writes
	// 0x00FF6001 then 0x00FF6000 to EP1); endpoint 0 takes 4 to stall.
	Control Reg // +0x00
	// Command: 4 sends what is in the FIFO (endpoint 0x83: the power-up
	// code and our core 1), 8 and 0x10 end endpoint 0's data and status
	// stages; reads bit 16 and 17 as a packet's events. Endpoints 1-5
	// get 0 after a reset.
	Command Reg // +0x04
}

// I2CUnit is the I2C master (FD8FE0). The host sets it up; codec core 1
// runs the transfers. The power-up code runs it from its interrupt.
type I2CUnit struct {
	// Status: bit 7 the bus busy, bit 4 no acknowledge, bit 3 which
	// part of a transfer failed.
	Status Reg // +0x00
	// Control: bit 0 a byte done (write 0 to go on), bit 1 its
	// interrupt, bit 3 acknowledge the byte read, bit 4 start (and stay
	// master), bit 5 repeated start, bit 6 the master on, bit 7 a bus
	// error. 0x42 idle with the interrupt; our code uses 0x40, 0x50 a
	// byte after a start, 0x58 a byte read with acknowledge, 0x70 after a
	// repeated start.
	Control Reg // +0x04
	// Clock is 0x3C.
	Clock Reg // +0x08
	// Data is the byte to send or received.
	Data Reg // +0x10
	// Divider is the bus clock's divider: 0x10 about 100 kHz; our code
	// writes 0x08.
	Divider Reg // +0x14
	// Unk18 is 6.
	Unk18 Reg // +0x18
}

// I2CAt is the I2C master at base.
func I2CAt(base uint32) I2CUnit {
	return I2CUnit{Status: Reg(base), Control: Reg(base + 4), Clock: Reg(base + 8), Data: Reg(base + 0x10),
		Divider: Reg(base + 0x14), Unk18: Reg(base + 0x18)}
}

// USBAt is the USB unit at base.
func USBAt(base uint32) USBUnit {
	r := func(o uint32) Reg { return Reg(base + o) }
	u := USBUnit{
		Unk008: r(0x008), Unk00C: r(0x00c), Enable: r(0x010), Reset: r(0x014),
		Status: r(0x080), Unk084: r(0x084), Unk08C: r(0x08c), IRQStatus: r(0x090), EPStatus: r(0x094),
		Unk0A4: r(0x0a4), Unk0AC: r(0x0ac), IRQEnable: r(0x0b0), EPEnable: r(0x0b4),
		Unk0C0: r(0x0c0), Unk0C4: r(0x0c4), Unk140: r(0x140), Lanes: r(0x14c),
		EP2DMA: USBEP2DMA{Control: r(0x500), Go: r(0x504), Dest: r(0x508), Unk50C: r(0x50c), Length: r(0x510), Unk514: r(0x514)},
		I2C:    I2CAt(base + 0xfe0),

		Unk3C04: r(0x3c04), CoreStatus: r(0x3c08), Unk3C0C: r(0x3c0c), EPMode: r(0x3c14), Unk3C70: r(0x3c70),
		Unk3DC0: r(0x3dc0),
	}
	for i := range u.DMA {
		u.DMA[i] = USBDMA{Control: r(0x200 + 0x20*uint32(i)), Config: r(0x204 + 0x20*uint32(i))}
	}
	for n := range u.Endpoint {
		u.Endpoint[n] = USBEndpoint{Control: r(0x3d00 + 8*uint32(n)), Command: r(0x3d04 + 8*uint32(n))}
		u.FIFO[n] = r(0x3d80 + 4*uint32(n))
		u.EPConfig[n] = r(0x3e04 + 4*uint32(n))
	}
	return u
}

// USB is the USB unit.
var USB = USBAt(USBBase)

// Blocks of Enable and Reset.
const (
	USBBlockI2C    = 1 << 4
	USBBlockStream = 0xf0000 // DMA[0], DMA[1], DMA[8] (and a fourth)
	USBBlockEP2DMA = 1 << 20 // EP2DMA
	USBBlockDMA3   = 1 << 24 // DMA[3]
	USBBlockDMA4   = 1 << 25 // DMA[4]
	USBBlockDMA9   = 1 << 27 // DMA[9]
)

// Bits and values.
const (
	USBEP2Done       = 1 << 2 // EPStatus, EPEnable: EP2DMA's transfer done
	USBDMAHold       = 1      // USBDMA.Control
	USBDMARun        = 2      // USBDMA.Control, USBEP2DMA.Control
	USBEndpointSend  = 4      // USBEndpoint.Command: send the FIFO's packet
	USBEndpointFlush = 1      // USBEndpoint.Control: reset the endpoint's buffer
)

// USBEPModeDMA is EPMode's bit that puts endpoint n in DMA mode.
func USBEPModeDMA(n int) uint32 { return 1 << (16 + n) }
