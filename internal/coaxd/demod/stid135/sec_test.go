// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stid135

import "testing"

func TestSetToneHoldsTheTransmitterMode(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	// Something else is already using the register's other bits.
	bus.registers[demod.diseqcReg(regDISTXCFG)] = 0xf0
	if err := demod.SetTone(true); err != nil {
		t.Fatalf("SetTone: %v", err)
	}
	if got := bus.registers[demod.diseqcReg(regDISTXCFG)]; got != 0xf0|diseqcModeContinuous22kHz {
		t.Fatalf("DISTXCFG became %#02x, want the mode field at the tone value", got)
	}
	if err := demod.SetTone(false); err != nil {
		t.Fatalf("SetTone: %v", err)
	}
	if got := bus.registers[demod.diseqcReg(regDISTXCFG)]; got != 0xf0|diseqcModePWM {
		t.Fatalf("DISTXCFG became %#02x, want the mode field back at the message value", got)
	}
}

func TestSetToneAddressesTheRightTransmitter(t *testing.T) {
	// The four transmitters belong to the four RF inputs, so a demodulator on
	// input 3 drives transmitter 3.
	bus := newFakeBus()
	demod := newTestDemodIn(bus, Config{DemodAddress: testAddress, AlreadyBooted: true,
		Multiswitch: Direct, Demod: 7, RFInput: 3})
	if err := demod.SetTone(true); err != nil {
		t.Fatalf("SetTone: %v", err)
	}
	if _, ok := bus.lastWriteTo(diseqcBase[3] + regDISTXCFG); !ok {
		t.Fatal("nothing was written to the fourth transmitter")
	}
	if _, ok := bus.lastWriteTo(diseqcBase[0] + regDISTXCFG); ok {
		t.Fatal("the first transmitter was written to")
	}
}

func TestSendDiSEqC(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	// The transmitter reports an empty FIFO and an idle line.
	bus.registers[demod.diseqcReg(regDISTXSTATUS)] = fldTXIDLE
	message := []byte{0xe0, 0x10, 0x38, 0xf0}
	if err := demod.SendDiSEqC(message); err != nil {
		t.Fatalf("SendDiSEqC: %v", err)
	}
	// Every byte went to the FIFO register, in order, as single-byte writes:
	// the FIFO must never be read, so a read-modify-write would be wrong.
	var sent []byte
	for _, frame := range bus.writes {
		address := uint16(frame[1])<<8 | uint16(frame[2])
		if address == demod.diseqcReg(regDISTXFIFO) {
			if len(frame) != 4 {
				t.Fatalf("a FIFO write carried %d payload bytes, want one", len(frame)-3)
			}
			sent = append(sent, frame[3])
		}
	}
	if len(sent) != len(message) {
		t.Fatalf("%d bytes reached the FIFO, want %d", len(sent), len(message))
	}
	for index := range message {
		if sent[index] != message[index] {
			t.Fatalf("the FIFO received %v, want %v", sent, message)
		}
	}
	// Precharge is raised before the burst and dropped after it.
	if got := bus.registers[demod.diseqcReg(regDISTXCFG)]; got&fldDISPRECHARGE != 0 {
		t.Fatalf("DISTXCFG ended at %#02x, want precharge released", got)
	}
}

func TestSendDiSEqCHoldsOffTheReceiverOnTheFirstInput(t *testing.T) {
	// Only the first input can receive, and it cannot while it transmits.
	bus := newFakeBus()
	demod := newTestDemod(bus)
	bus.registers[demod.diseqcReg(regDISTXSTATUS)] = fldTXIDLE
	bus.registers[demod.diseqcReg(regDISRXCFG)] = fldDISRXON
	if err := demod.SendDiSEqC([]byte{0xe0, 0x10, 0x38, 0xf0}); err != nil {
		t.Fatalf("SendDiSEqC: %v", err)
	}
	if got := bus.registers[demod.diseqcReg(regDISRXCFG)]; got&fldDISRXON == 0 {
		t.Fatalf("DISRXCFG ended at %#02x, want the receiver switched back on", got)
	}
	var sawHoldOff bool
	for _, frame := range bus.writes {
		address := uint16(frame[1])<<8 | uint16(frame[2])
		if address == demod.diseqcReg(regDISRXCFG) && len(frame) == 4 && frame[3]&fldDISRXON == 0 {
			sawHoldOff = true
		}
	}
	if !sawHoldOff {
		t.Fatal("the receiver was never held off")
	}
}

func TestSendDiSEqCRejectsAnImpossibleMessage(t *testing.T) {
	demod := newTestDemod(newFakeBus())
	for _, message := range [][]byte{nil, make([]byte, 9)} {
		if err := demod.SendDiSEqC(message); err == nil {
			t.Errorf("a %d-byte message was accepted", len(message))
		}
	}
}

// TestSECRefusesAnUninitializedChip covers every entry point that needs a
// woken chip, the two readback calls included, not only the SEC ones.
func TestSECRefusesAnUninitializedChip(t *testing.T) {
	demod := New(newFakeBus(), Config{DemodAddress: testAddress, AlreadyBooted: true})
	if err := demod.SetTone(true); err != ErrNotInitialized {
		t.Errorf("SetTone answered %v, want ErrNotInitialized", err)
	}
	if err := demod.SendDiSEqC([]byte{0xe0}); err != ErrNotInitialized {
		t.Errorf("SendDiSEqC answered %v, want ErrNotInitialized", err)
	}
	if _, err := demod.GainIntegrator(); err != ErrNotInitialized {
		t.Errorf("GainIntegrator answered %v, want ErrNotInitialized", err)
	}
	if _, err := demod.Status(); err != ErrNotInitialized {
		t.Errorf("Status answered %v, want ErrNotInitialized", err)
	}
}
