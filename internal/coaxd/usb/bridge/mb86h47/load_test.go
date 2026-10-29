// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mb86h47

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/usb/bridge/mb86h47/regs"
)

type countingChip struct {
	memoryChip
	requests int
}

func (chip *countingChip) Control(requestType, request byte, value, index uint16, buffer []byte, timeout uint32) (int, error) {
	chip.requests++
	return chip.memoryChip.Control(requestType, request, value, index, buffer, timeout)
}

func TestLoadSkipsAProgramNotedAsLoaded(t *testing.T) {
	chip := &countingChip{memoryChip: memoryChip{words: map[uint32]uint32{}}}
	bridge := &Bridge{device: chip}
	program := core1Program

	bridge.loadLocked(core1Base, core1Origin, program)
	if bridge.err != nil {
		t.Fatal(bridge.err)
	}
	for i, word := range program {
		if chip.words[core1Base+core1Origin+uint32(i)*4] != word {
			t.Fatalf("program word %d not loaded", i)
		}
	}

	chip.requests = 0
	bridge.loadLocked(core1Base, core1Origin, program)
	if chip.requests != 3 {
		t.Fatalf("loading it again took %d requests, want 3: the reset vector, the reset and the note", chip.requests)
	}

	chip.words[core1Base+loadedSignature+4]++
	chip.words[core1Base+core1Origin+40]++
	chip.requests = 0
	bridge.loadLocked(core1Base, core1Origin, program)
	if chip.requests < len(program)/16 {
		t.Fatalf("a mismatched note took %d requests, too few to check the program", chip.requests)
	}
	if chip.words[core1Base+core1Origin+40] != program[10] {
		t.Fatal("the changed word was not loaded again")
	}
}

func TestLoadedSignatureIsClear(t *testing.T) {
	for _, core := range []struct {
		name   string
		end    uint32
		stack  uint32
		lowest uint32
	}{
		{"core 1", core1Origin + 4*uint32(len(core1Program)), core1Stack, jobArea},
		{"core 0", core0Origin + 4*uint32(len(core0Program)), core0Stack, core0Counters},
	} {
		if core.end > core.stack || loadedSignature <= core.stack || loadedSignature+8 > core.lowest {
			t.Errorf("%s: program ends %#x, stack starts %#x, variables from %#x; the note at %#x does not fit",
				core.name, core.end, core.stack, core.lowest, loadedSignature)
		}
	}
}

type writeCountingChip struct {
	memoryChip
	reads, writes int
}

func (chip *writeCountingChip) Control(requestType, request byte, value, index uint16, buffer []byte, timeout uint32) (int, error) {
	if requestType == vendorOut {
		chip.writes++
	} else {
		chip.reads++
	}
	return chip.memoryChip.Control(requestType, request, value, index, buffer, timeout)
}

func TestClocksWriteOnlyWhatDiffers(t *testing.T) {
	chip := &writeCountingChip{memoryChip: memoryChip{words: map[uint32]uint32{}}}
	clock, memory := regs.Clock, regs.Memory
	chip.words[clock.Unk11C.Addr()] = 0x1234abcd
	bridge := &Bridge{device: chip}
	bridge.clocksLocked()
	if bridge.err != nil {
		t.Fatal(bridge.err)
	}
	for address, want := range map[uint32]uint32{
		clock.PLL.Addr(): 0x8201 << 16, clock.Unk114.Addr(): 0x8008 << 16, clock.Gates.Addr(): 0xffff << 16,
		clock.Unk11C.Addr(): 6<<16 | 0xabcd, clock.Unk128.Addr(): 0x6000 << 16, clock.Unk12C.Addr(): 0x4007 << 16,
		memory.Format2[1].Addr(): 3 << 16, memory.Planes2[2].Chroma[0].Addr(): 0x880 << 16, memory.Planes2[4].Chroma[1].Addr(): 0x880 << 16,
	} {
		if got := chip.words[address]; got != want {
			t.Errorf("%#x: %#x, want %#x", address, got, want)
		}
	}
	for _, base := range standbyBlocks {
		if chip.words[regs.ClusterAt(base).StandbyMode.Addr()]&(regs.ClusterStandbyWait<<16) == 0 {
			t.Errorf("block %#x not in standby", base)
		}
	}

	chip.reads, chip.writes = 0, 0
	bridge.clocksLocked()
	if chip.writes != 0 || chip.reads != 3+len(standbyBlocks) {
		t.Fatalf("set up again: %d reads, %d writes; want %d reads and none", chip.reads, chip.writes, 3+len(standbyBlocks))
	}
}
