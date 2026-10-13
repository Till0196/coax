// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mb86h47

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/usb/bridge/mb86h47/fr30"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/mb86h47/fr30/sim"
)

// mustAssemble finishes a reference program.
func mustAssemble(a *fr30.Assembler) []uint32 {
	words, err := a.Words()
	if err != nil {
		panic(err)
	}
	return words
}

// trapReference is the recorder and stubs written instruction by
// instruction.
func trapReference() (common, stubs []uint32) {
	a := fr30.New(trapCommon)
	a.LDI32(TrapRecord, fr30.R12)
	a.LDI32(0x7ea9, fr30.R13)
	a.ST(fr30.R13, fr30.R12)
	a.ADDI(4, fr30.R12)
	a.ST(fr30.R0, fr30.R12)
	a.ADDI(4, fr30.R12)
	a.LD(fr30.R15, fr30.R13)
	a.ST(fr30.R13, fr30.R12)
	a.ADDI(4, fr30.R12)
	a.MOV(fr30.R15, fr30.R13)
	a.ADDI(4, fr30.R13)
	a.LD(fr30.R13, fr30.R13)
	a.ST(fr30.R13, fr30.R12)
	a.ADDI(4, fr30.R12)
	a.MOVFromS(fr30.RP, fr30.R13)
	a.ST(fr30.R13, fr30.R12)
	a.ADDI(4, fr30.R12)
	a.ST(fr30.R15, fr30.R12)
	a.ADDI(4, fr30.R12)
	a.LD(fr30.R12, fr30.R13)
	a.ADDI(1, fr30.R13)
	a.ST(fr30.R13, fr30.R12)
	a.STILM(0x1f)
	a.ORCCR(0x10)
	wait := a.Here("wait")
	a.B(fr30.Always, wait)
	common = mustAssemble(a)
	for vector := 0; vector < 256; vector++ {
		s := fr30.New(trapStubs + uint32(vector)*8)
		s.LDI8(uint32(vector), fr30.R0)
		s.LDI20(trapCommon, fr30.R12)
		s.JMP(fr30.R12)
		stubs = append(stubs, mustAssemble(s)...)
	}
	return common, stubs
}

// TestTrapRecorder takes interrupts through both recorders: the same record,
// and the CPU left waiting with interrupts on, the recorder fitting before
// the stubs' end and the stubs in their stride.
func TestTrapRecorder(t *testing.T) {
	refCommon, refStubs := trapReference()
	if len(trapStubCode) != len(refStubs) {
		t.Fatalf("stubs take %d words, %d before", len(trapStubCode), len(refStubs))
	}
	if trapStubs+4*uint32(len(trapStubCode)) > trapCommon {
		t.Fatal("stubs run into the recorder")
	}
	for _, vector := range []int{1, 7, 20, 63, 128, 255} {
		var records [2][7]uint32
		var states [2]uint32
		for i, code := range [][2][]uint32{{refCommon, refStubs}, {trapRecorder, trapStubCode}} {
			cpu := sim.New()
			cpu.LoadWords(trapCommon, code[0])
			cpu.LoadWords(trapStubs, code[1])
			cpu.TBR = vectorTable
			for v := 0; v < 256; v++ {
				cpu.Write(vectorTable+0x3fc-4*uint32(v), 4, trapStubs+uint32(v)*trapStubStride)
			}
			cpu.R[15] = 0x7f000
			cpu.RP = 0x12344
			cpu.PC = 0x45670
			cpu.PS = 0x10 | 0x1f<<16 | 0x5
			if !cpu.Interrupt(vector) {
				t.Fatal("interrupt not taken")
			}
			cpu.Run(500) // the recorder waits for ever
			for k := range records[i] {
				records[i][k] = cpu.Read(TrapRecord+4*uint32(k), 4)
			}
			states[i] = cpu.PS &^ 0xf
		}
		if records[0] != records[1] || states[0] != states[1] {
			t.Fatalf("vector %d: reference %#x (PS %#x), fc %#x (PS %#x)", vector, records[0], states[0], records[1], states[1])
		}
		want := [7]uint32{0x7ea9, uint32(vector), 0x45670, 0x10 | 0x1f<<16 | 0x5, 0x12344, 0x7eff8, 1}
		if records[1] != want {
			t.Fatalf("vector %d: record %#x, want %#x", vector, records[1], want)
		}
	}
}
