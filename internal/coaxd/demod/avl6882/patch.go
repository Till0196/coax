// Copyright (c) 2015 Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package avl6882

import (
	"errors"
	"fmt"
)

// runPatch interprets a firmware image's patch script (avl6882_patch_demod).
// The image is a word array: a header, then commands each prefixed by their own
// word count and a list of condition words; conditions fold into a stack-less
// accumulator plus a scratch variable array, and a false command is skipped by
// jumping to the next command's index. Every index is bounds-checked so a
// malformed image errors out instead of panicking the scan.
func (demod *Demod) runPatch(patch []uint32) error {
	var variables [patchVarArraySize]uint32

	cursor := &patchCursor{patch: patch, index: 3}

	argsAddr, err := cursor.next()
	if err != nil {
		return err
	}
	dataSectionOffset, err := cursor.next()
	if err != nil {
		return err
	}
	reservedLength, err := cursor.next()
	if err != nil {
		return err
	}
	if cursor.index, err = cursor.ahead(cursor.index, uint64(reservedLength)); err != nil {
		return err
	}

	scriptLength, err := cursor.next()
	if err != nil {
		return err
	}
	end, err := cursor.ahead(cursor.index, uint64(scriptLength))
	if err != nil {
		return err
	}

	for cursor.index < end {
		commandWords, err := cursor.next()
		if err != nil {
			return err
		}
		// The count includes its own word, so a command of none would be read
		// again for ever.
		if commandWords == 0 {
			return fmt.Errorf("AVL6882 patch: a command of no words at %d", cursor.index-1)
		}
		nextCommandIndex, err := cursor.ahead(cursor.index-1, uint64(commandWords))
		if err != nil {
			return err
		}

		condition, err := evaluateConditions(cursor, &variables)
		if err != nil {
			return err
		}
		if condition == 0 {
			cursor.index = nextCommandIndex
			continue
		}

		returnValues, err := demod.runPatchCommand(cursor, &variables, argsAddr, dataSectionOffset)
		if err != nil {
			return err
		}
		if cursor.index, err = cursor.ahead(cursor.index, uint64(returnValues)); err != nil {
			return err
		}
	}
	return nil
}

// patchCursor is a bounds-checked reader over the patch word array.
type patchCursor struct {
	patch []uint32
	index int
}

func (cursor *patchCursor) next() (uint32, error) {
	if cursor.index >= len(cursor.patch) {
		return 0, fmt.Errorf("AVL6882 patch: read past the end of the image at word %d", cursor.index)
	}
	value := cursor.patch[cursor.index]
	cursor.index++
	return value, nil
}

// peek reads the word at the cursor without consuming it, for reading the first
// return-value slot before all of them are skipped at once.
func (cursor *patchCursor) peek() (uint32, error) {
	if cursor.index >= len(cursor.patch) {
		return 0, fmt.Errorf("AVL6882 patch: read past the end of the image at word %d", cursor.index)
	}
	return cursor.patch[cursor.index], nil
}

func (cursor *patchCursor) slice(length uint64) ([]uint32, error) {
	end, err := cursor.ahead(cursor.index, length)
	if err != nil {
		return nil, err
	}
	return cursor.patch[cursor.index:end], nil
}

// ahead is the index count words on from base, refused unless it is still
// inside the image (or just at its end). Worked out in 64 bits: the counts come
// out of the image as 32-bit words, which a 32-bit int cannot always hold, and
// a sum that wrapped round would pass any bound.
func (cursor *patchCursor) ahead(base int, count uint64) (int, error) {
	if base < 0 || uint64(base)+count > uint64(len(cursor.patch)) {
		return 0, fmt.Errorf("AVL6882 patch: %d words on from word %d runs past the image", count, base)
	}
	return base + int(count), nil
}

// evaluateConditions folds a command's condition words into an accumulator;
// zero of them means the command runs unconditionally.
func evaluateConditions(cursor *patchCursor, variables *[patchVarArraySize]uint32) (uint32, error) {
	count, err := cursor.next()
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 1, nil
	}
	var condition uint32
	for range count {
		operation, err := cursor.next()
		if err != nil {
			return 0, err
		}
		value, err := cursor.next()
		if err != nil {
			return 0, err
		}
		unary := (operation >> 8) & 0xff
		binary := operation & 0xff
		addrMode := (operation >> 16) & 0x03

		if addrMode == patchAddrModeVarIndex && binary != patchBinaryStore {
			if value >= patchVarArraySize {
				return 0, fmt.Errorf("AVL6882 patch: variable index %d is out of range", value)
			}
			value = variables[value]
		}
		switch unary {
		case patchUnaryLogicalNegate:
			value = boolWord(value == 0)
		case patchUnaryBitwiseNegate:
			value = ^value
		}
		switch binary {
		case patchBinaryLoad:
			condition = value
		case patchBinaryStore:
			if value >= patchVarArraySize {
				return 0, fmt.Errorf("AVL6882 patch: variable index %d is out of range", value)
			}
			variables[value] = condition
		case patchBinaryAnd:
			condition = boolWord(condition != 0 && value != 0)
		case patchBinaryOr:
			condition = boolWord(condition != 0 || value != 0)
		case patchBinaryBitwiseAnd:
			condition &= value
		case patchBinaryBitwiseOr:
			condition |= value
		case patchBinaryEquals:
			condition = boolWord(condition == value)
		case patchBinaryNotEquals:
			condition = boolWord(condition != value)
		}
	}
	return condition, nil
}

func boolWord(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}

// runPatchCommand runs one command and returns the count of return-value slots
// following it, which the caller skips.
func (demod *Demod) runPatchCommand(
	cursor *patchCursor,
	variables *[patchVarArraySize]uint32,
	argsAddr, dataSectionOffset uint32,
) (uint32, error) {
	command, err := cursor.next()
	if err != nil {
		return 0, err
	}

	// store writes into the variable the first return-value slot names, without
	// consuming it: the caller skips all the slots at once.
	store := func(value uint32) error {
		index, err := cursor.peek()
		if err != nil {
			return err
		}
		if index >= patchVarArraySize {
			return fmt.Errorf("AVL6882 patch: variable index %d is out of range", index)
		}
		variables[index] = value
		return nil
	}

	switch command {
	case patchCmdPing:
		pingErr := demod.execute(fwCmdPing)
		returnValues, err := cursor.next()
		if err != nil {
			return 0, err
		}
		return returnValues, store(boolWord(pingErr == nil))

	case patchCmdValidateCRC:
		expected, err := cursor.next()
		if err != nil {
			return 0, err
		}
		sourceAddr, err := cursor.next()
		if err != nil {
			return 0, err
		}
		length, err := cursor.next()
		if err != nil {
			return 0, err
		}
		if err := demod.write32(0x200+rcFWCommandArgsAddr, argsAddr); err != nil {
			return 0, err
		}
		if err := demod.write32(argsAddr+0, sourceAddr); err != nil {
			return 0, err
		}
		if err := demod.write32(argsAddr+4, length); err != nil {
			return 0, err
		}
		if err := demod.execute(fwCmdCalcCRC); err != nil {
			return 0, err
		}
		result, err := demod.read32(argsAddr + 8)
		if err != nil {
			return 0, err
		}
		returnValues, err := cursor.next()
		if err != nil {
			return 0, err
		}
		return returnValues, store(boolWord(result == expected))

	case patchCmdLoadToDevice:
		length, err := cursor.next()
		if err != nil {
			return 0, err
		}
		destAddr, err := cursor.next()
		if err != nil {
			return 0, err
		}
		dataOffset, err := cursor.next()
		if err != nil {
			return 0, err
		}
		start := uint64(dataOffset) + uint64(dataSectionOffset)
		if start+uint64(length) > uint64(len(cursor.patch)) {
			return 0, fmt.Errorf("AVL6882 patch: a load payload at %d runs past the image", start)
		}
		if err := demod.writeData(destAddr, cursor.patch[start:start+uint64(length)]); err != nil {
			return 0, err
		}
		return cursor.next()

	case patchCmdLoadToDeviceImm:
		length, err := cursor.next()
		if err != nil {
			return 0, err
		}
		destAddr, err := cursor.next()
		if err != nil {
			return 0, err
		}
		value, err := cursor.next()
		if err != nil {
			return 0, err
		}
		if err := demod.write(destAddr, value, int(length)); err != nil {
			return 0, err
		}
		return cursor.next()

	case patchCmdReadFromDevice:
		length, err := cursor.next()
		if err != nil {
			return 0, err
		}
		sourceAddr, err := cursor.next()
		if err != nil {
			return 0, err
		}
		returnValues, err := cursor.next()
		if err != nil {
			return 0, err
		}
		value, err := demod.read(sourceAddr, int(length))
		if err != nil {
			return 0, err
		}
		return returnValues, store(value)

	case patchCmdDMA:
		destAddr, err := cursor.next()
		if err != nil {
			return 0, err
		}
		length, err := cursor.next()
		if err != nil {
			return 0, err
		}
		// A DMA descriptor is three words wide.
		words := uint64(length) * 3
		if length > 0 {
			payload, err := cursor.slice(words)
			if err != nil {
				return 0, err
			}
			if err := demod.writeData(destAddr, payload); err != nil {
				return 0, err
			}
		}
		if err := demod.write32(0x200+rcFWCommandArgsAddr, destAddr); err != nil {
			return 0, err
		}
		if err := demod.execute(fwCmdDMA); err != nil {
			return 0, err
		}
		cursor.index += int(words) // inside the image: slice checked it
		return cursor.next()

	case patchCmdDecompress:
		compression, err := cursor.next()
		if err != nil {
			return 0, err
		}
		sourceAddr, err := cursor.next()
		if err != nil {
			return 0, err
		}
		destAddr, err := cursor.next()
		if err != nil {
			return 0, err
		}
		var refAddr, refSize uint32
		if compression == patchCompressionZlib {
			if refAddr, err = cursor.next(); err != nil {
				return 0, err
			}
			if refSize, err = cursor.next(); err != nil {
				return 0, err
			}
		}
		if err := demod.write32(0x200+rcFWCommandArgsAddr, argsAddr); err != nil {
			return 0, err
		}
		for offset, value := range map[uint32]uint32{0: compression, 4: sourceAddr, 8: destAddr} {
			if err := demod.write32(argsAddr+offset, value); err != nil {
				return 0, err
			}
		}
		if compression == patchCompressionZlib {
			if err := demod.write32(argsAddr+12, refAddr); err != nil {
				return 0, err
			}
			if err := demod.write32(argsAddr+16, refSize); err != nil {
				return 0, err
			}
		}
		if err := demod.execute(fwCmdDecompress); err != nil {
			return 0, err
		}
		return cursor.next()

	case patchCmdAssertCPUReset:
		if err := demod.write32(0x110840, 1); err != nil {
			return 0, err
		}
		return cursor.next()

	case patchCmdReleaseCPUReset:
		if err := demod.write32(0x110840, 0); err != nil {
			return 0, err
		}
		return cursor.next()

	case patchCmdDMAHardware:
		destAddr, err := cursor.next()
		if err != nil {
			return 0, err
		}
		length, err := cursor.next()
		if err != nil {
			return 0, err
		}
		words := uint64(length) * 3
		if length > 0 {
			payload, err := cursor.slice(words)
			if err != nil {
				return 0, err
			}
			if err := demod.writeData(destAddr, payload); err != nil {
				return 0, err
			}
		}
		const dmaMaxTries = 20
		var status uint32
		ready := false
		for range dmaMaxTries {
			status, err = demod.read32(0x110048)
			if err != nil {
				return 0, err
			}
			if status&0x01 != 0 {
				ready = true
				break
			}
		}
		if !ready {
			return 0, errors.New("AVL6882 patch: the DMA engine never became ready")
		}
		if err := demod.write32(0x110050, destAddr); err != nil {
			return 0, err
		}
		cursor.index += int(words) // inside the image: slice checked it
		return cursor.next()

	case patchCmdSetConditionImm:
		value, err := cursor.next()
		if err != nil {
			return 0, err
		}
		returnValues, err := cursor.next()
		if err != nil {
			return 0, err
		}
		return returnValues, store(value)

	default:
		return 0, fmt.Errorf("AVL6882 patch: unknown command %d", command)
	}
}
