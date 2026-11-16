// Copyright (c) 2008 FUKAUMI Naoki; 2012 tslroom; 2016 stz2012; 2026 KASHIWAYA Rikuto
// SPDX-License-Identifier: (Apache-2.0 OR GPL-2.0-or-later) AND BSD-2-Clause
//
// The transform and its keys are those recfriio's Hdp.cpp undoes for the same
// board (BSD-2-Clause; see NOTICE). The rest is this module's own.

package ut100b

import (
	"crypto/cipher"
	"crypto/des"
)

// The card interface IC on this board puts every transport packet's payload
// through DES before the bridge sees it, and no setting this driver has found
// turns that off: register 5 enables the output only with bit 7 set, and with
// bit 7 set the payload is always enciphered. Registers 0x10 to 0x1f, written
// through the bridge's guarded write, change the key but do not take the
// transform away. The PLEX boards' IC of the same revision streams in the clear
// with register 5 at 0x20, which on this board stops the stream, so the
// difference is taken to be in how the board wires the IC's outputs.
//
// The keys below are what the IC uses when registers 0x10 to 0x1f are not
// written through the guarded write: the bring-up writes them with ordinary
// writes, which the output needs but the key does not notice. The four
// header bytes pass through untouched; the 184 payload bytes are 23 blocks, the
// first sixteen under one key and the last seven under the other, each whitened
// by the same eight bytes before it is enciphered.
var (
	desWhitening = [8]byte{0x00, 0x00, 0xb1, 0xf2, 0x00, 0x04, 0xf2, 0x04}
	desKeyHead   = []byte{0xbc, 0x8c, 0x73, 0xe9, 0x0e, 0x68, 0x31, 0x23}
	desKeyTail   = []byte{0xec, 0x15, 0x80, 0x01, 0x80, 0x7f, 0x80, 0x80}
)

const (
	packetSize = 188
	syncByte   = 0x47
	// headBlocksEnd is where the payload stops being enciphered under the head key.
	headBlocksEnd = 4 + 128
)

// descrambler undoes the IC's transform one packet at a time.
type descrambler struct {
	head, tail cipher.Block
}

func newDescrambler() *descrambler {
	head, err := des.NewCipher(desKeyHead)
	if err != nil {
		panic(err)
	}
	tail, err := des.NewCipher(desKeyTail)
	if err != nil {
		panic(err)
	}
	return &descrambler{head: head, tail: tail}
}

// packet deciphers one whole packet in place.
func (descrambler *descrambler) packet(packet []byte) {
	_ = packet[packetSize-1]
	for at := 4; at < packetSize; at += des.BlockSize {
		block := packet[at : at+des.BlockSize]
		for index := range block {
			block[index] ^= desWhitening[index]
		}
		if at < headBlocksEnd {
			descrambler.head.Decrypt(block, block)
		} else {
			descrambler.tail.Decrypt(block, block)
		}
	}
}

// staleBytes is what a freshly started stream carries from before the tune: the
// bridge hands over what it still held, two transfers' worth, ahead of anything
// from the new channel. recfriio drops the same amount.
const staleBytes = 2 * 16384

// syncRun is how many packets in a row have to line up before a position is
// taken for the packet boundary, after a start or a loss of sync.
const syncRun = 3

// packetizer turns the bridge's reads, which need not be whole packets, into
// whole deciphered packets. The header passes through the IC in the clear, so
// the sync bytes can be looked for before deciphering.
type packetizer struct {
	descrambler *descrambler
	// stale is how much of the start of the stream is still to be dropped.
	stale int
	// held is what has arrived but not yet made a whole packet, or not yet been
	// lined up.
	held []byte
	// ready is whole deciphered packets waiting for a reader.
	ready []byte
	// resyncs counts how often the boundary had to be looked for again.
	resyncs int
}

func newPacketizer() *packetizer {
	return &packetizer{descrambler: newDescrambler(), stale: staleBytes}
}

// reset starts over, as a new stream does.
func (packetizer *packetizer) reset() {
	packetizer.stale = staleBytes
	packetizer.held = packetizer.held[:0]
	packetizer.ready = packetizer.ready[:0]
	packetizer.resyncs = 0
}

// feed takes what the bridge delivered.
func (packetizer *packetizer) feed(data []byte) {
	if packetizer.stale > 0 {
		drop := min(len(data), packetizer.stale)
		packetizer.stale -= drop
		data = data[drop:]
	}
	packetizer.held = append(packetizer.held, data...)
	held := packetizer.held
	at := 0
	for len(held)-at >= packetSize {
		if !lined(held[at:]) {
			// Out of step: look for the next place where syncRun packets line up,
			// keeping what might yet turn out to be one.
			next := findBoundary(held[at:])
			if next < 0 {
				at = max(at, len(held)-(syncRun*packetSize-1))
				break
			}
			packetizer.resyncs++
			at += next
			continue
		}
		start := len(packetizer.ready)
		packetizer.ready = append(packetizer.ready, held[at:at+packetSize]...)
		packetizer.descrambler.packet(packetizer.ready[start:])
		at += packetSize
	}
	packetizer.held = append(packetizer.held[:0], held[at:]...)
}

// lined reports whether data starts on a packet boundary: its first byte is a
// sync byte, and so is the one a packet on when that much has arrived.
func lined(data []byte) bool {
	if data[0] != syncByte {
		return false
	}
	return len(data) <= packetSize || data[packetSize] == syncByte
}

// findBoundary finds the first position where syncRun packets line up, or -1.
func findBoundary(data []byte) int {
	for start := 0; start+(syncRun-1)*packetSize < len(data); start++ {
		aligned := true
		for packet := 0; packet < syncRun; packet++ {
			if data[start+packet*packetSize] != syncByte {
				aligned = false
				break
			}
		}
		if aligned {
			return start
		}
	}
	return -1
}

// take moves up to len(buffer) bytes of ready packets into buffer.
func (packetizer *packetizer) take(buffer []byte) int {
	moved := copy(buffer, packetizer.ready)
	packetizer.ready = append(packetizer.ready[:0], packetizer.ready[moved:]...)
	return moved
}
