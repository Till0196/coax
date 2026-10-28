// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt3

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/pcie/windriver"
)

// ring is one channel's transfer buffer and the descriptors that point at it.
// The card walks a closed ring for ever and never says where it has got to; what
// it does say is what it has written, since every block begins with a byte the
// card overwrites with the start of a transport packet. A block is only handed on
// once the block after it has started, which is what makes "filled" mean
// "finished".
type ring struct {
	card    *card
	channel int

	data        *windriver.DMABuffer
	descriptors *windriver.DMABuffer

	// block is the next block to be handed on.
	block int
	// skip is how many of the blocks still to come are dropped rather than
	// handed on. See startBlocks.
	skip int
	// overruns counts the times the card came all the way round unwatched,
	// which means data was lost.
	overruns int
	running  bool
}

// newRing allocates a channel's buffer and describes it to the card.
func newRing(held *card, channel int) (*ring, error) {
	r := &ring{card: held, channel: channel}
	var data *windriver.DMABuffer
	var err error
	if held.lowMemory {
		data, err = held.device.LockContiguousLow(ringSize)
	} else {
		data, err = held.device.LockScatterGather(pageAligned(ringSize))
	}
	if err != nil {
		return nil, fmt.Errorf("lock the buffer for channel %d: %w", channel, err)
	}
	r.data = data

	descriptors, err := held.device.LockScatterGather(pageAligned(descriptorSize * ringTransfers))
	if err != nil {
		data.Unlock()
		return nil, fmt.Errorf("lock the descriptors for channel %d: %w", channel, err)
	}
	r.descriptors = descriptors
	// One piece: the card is given one address to start walking from and finds
	// every entry after that by following the one before it.
	if len(descriptors.Pages) != 1 {
		described := descriptors.Describe()
		_ = r.close()
		return nil, fmt.Errorf("the descriptors for channel %d came back as %d pieces: %s",
			channel, len(descriptors.Pages), described)
	}
	if err := r.describe(); err != nil {
		described := data.Describe()
		_ = r.close()
		return nil, fmt.Errorf("describe the buffer for channel %d to the card: %w (%s)", channel, err, described)
	}
	return r, nil
}

// describe fills in the descriptor ring: one entry per transfer, each pointing
// at the next and the last at the first. The pieces the buffer was locked as
// are not transfers -- the library reports what is physically contiguous, so
// 188 pages may come back as, say, 23 runs of varying length -- and the card
// wants one page per transfer, because a transfer must not cross a four
// gigabyte boundary and a page cannot. So each run is cut up.
func (r *ring) describe() error {
	base := r.descriptors.Pages[0].Physical
	entries := r.descriptors.Bytes

	index := 0
	for _, run := range r.data.Pages {
		if run.Bytes%transferSize != 0 {
			return fmt.Errorf("a piece of %d bytes is not a whole number of transfers", run.Bytes)
		}
		for at := uint32(0); at < run.Bytes; at += transferSize {
			if index >= ringTransfers {
				return fmt.Errorf("the buffer needs more than %d transfers", ringTransfers)
			}
			address := run.Physical + uint64(at)
			put := index * descriptorSize
			next := base + uint64((index+1)%ringTransfers*descriptorSize)
			put32(entries, put+descriptorAddressLow, uint32(address))
			put32(entries, put+descriptorAddressHigh, uint32(address>>32))
			put32(entries, put+descriptorBytes, transferSize)
			put32(entries, put+descriptorNextLow, uint32(next))
			put32(entries, put+descriptorNextHigh, uint32(next>>32))
			index++
		}
	}
	if index != ringTransfers {
		return fmt.Errorf("the buffer came to %d transfers rather than %d", index, ringTransfers)
	}
	return nil
}

// arm marks every block as not yet written and sets the card going.
func (r *ring) arm() error {
	if err := r.stop(); err != nil {
		return err
	}
	for block := range ringBlocks {
		r.data.Bytes[block*blockSize] = sentinel
	}
	r.block = 0
	r.skip = startBlocks

	base := r.descriptors.Pages[0].Physical
	if err := r.write(regDescriptorLow, uint32(base)); err != nil {
		return err
	}
	if err := r.write(regDescriptorHigh, uint32(base>>32)); err != nil {
		return err
	}
	if err := r.write(regDMAControl, dmaStart); err != nil {
		return err
	}
	r.running = true
	return nil
}

// stopBudget is how long the card is given to finish what it was doing. The
// transfer in flight is at most one page, so anything beyond a few milliseconds
// means the card is not answering at all.
const stopBudget = 250 * time.Millisecond

func (r *ring) stop() error {
	if err := r.write(regDMAControl, dmaStop); err != nil {
		return err
	}
	// Running until the card says otherwise: told to stop is not stopped, and
	// close lets go of the memory only once the card is no longer writing to it.
	deadline := time.Now().Add(stopBudget)
	for {
		status, err := r.read(regStatus)
		if err != nil {
			return err
		}
		if status&dmaRunning == 0 {
			r.running = false
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("channel %d was still transferring %v after being told to stop", r.channel, stopBudget)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// collect hands on every block the card has finished with and puts the sentinel
// back so the block can be recognised again next time round. A block counts as
// finished when the block after it has been started, so the last block the card
// touched is left until the next call.
func (r *ring) collect(hand func([]byte)) {
	ready := 0
	for {
		next := (r.block + 1) % ringBlocks
		if r.data.Bytes[next*blockSize] == sentinel {
			break
		}
		if r.skip > 0 {
			r.skip--
		} else {
			hand(r.data.Bytes[r.block*blockSize : (r.block+1)*blockSize])
		}
		r.data.Bytes[r.block*blockSize] = sentinel
		r.block = next
		ready++
		if ready == ringBlocks-1 {
			// The whole ring was waiting, so the card had nowhere left to
			// write and has been overwriting untaken blocks. What was handed
			// on above is whole; what is missing came before it.
			r.overruns++
			break
		}
	}
}

// stranded keeps, for the life of the process, the memory of a ring whose card
// could not be seen to stop. Unlocked and let go, its pages would be handed out
// again while the card may still be writing to them.
var (
	strandedMu sync.Mutex
	stranded   []*ring
)

func (r *ring) close() error {
	var stopped error
	if r.running {
		stopped = r.stop()
	}
	if r.running {
		// Neither the error nor the memory is let go: the card is closed
		// with bus mastering taken away, and until then these pages are the
		// only ones it can be writing to.
		strandedMu.Lock()
		stranded = append(stranded, r)
		strandedMu.Unlock()
		r.card.device.Untrusted()
		if stopped == nil {
			stopped = fmt.Errorf("channel %d could not be seen to stop", r.channel)
		}
		return fmt.Errorf("%w; its buffer is kept rather than given back", stopped)
	}
	if r.descriptors != nil {
		_ = r.descriptors.Unlock()
		r.descriptors = nil
	}
	if r.data != nil {
		_ = r.data.Unlock()
		r.data = nil
	}
	return nil
}

// leaveSafe has the driver stop every channel's transfer when the card is let
// go, including when this process dies without closing it. The rings are this
// process's memory, given back to the system when it goes, and the card walks a
// ring for ever: left running, it would write into whatever the pages are
// handed to next. A card the driver cannot do this for is not used.
//
// The driver cannot wait for a channel to say it has stopped. The transfer in
// flight is at most one page and is over before the memory is let go.
//
// After the transfers, the I2C master is reset, stopping any program it is
// part way through, and the supply on the aerial lead is turned off. On an
// ordinary close the same writes are made again after Close's own, and find
// everything already stopped and off.
func (c *card) leaveSafe() error {
	var writes []windriver.RegisterWrite
	for channel := range channels {
		writes = append(writes, windriver.RegisterWrite{
			Space: c.registers, Offset: channelRegister(channel, regDMAControl), Value: dmaStop,
		})
	}
	writes = append(writes,
		windriver.RegisterWrite{Space: c.registers, Offset: regI2CWrite, Value: i2cReset},
		windriver.RegisterWrite{Space: c.registers, Offset: regSystemW, Value: supplyOff},
	)
	return c.device.SetupCleanup(writes)
}

// read and write reach this channel's copy of a transfer register.
func (r *ring) read(register uint64) (uint32, error) {
	return r.card.device.ReadU32(r.card.registers, channelRegister(r.channel, register))
}

func (r *ring) write(register uint64, value uint32) error {
	return r.card.device.WriteU32(r.card.registers, channelRegister(r.channel, register), value)
}

// pageAligned allocates memory a card can be given page by page: the allocator
// knows nothing of pages, so this asks for one page more and cuts the aligned
// part out of the middle. Both are returned -- the second has to stay
// reachable, because holding a slice of the middle is not holding the whole.
func pageAligned(size int) ([]byte, any) {
	raw := make([]byte, size+transferSize)
	skip := (transferSize - int(uintptr(unsafe.Pointer(&raw[0]))%transferSize)) % transferSize
	return raw[skip : skip+size], raw
}

func put32(into []byte, at int, value uint32) { binary.LittleEndian.PutUint32(into[at:], value) }

// errorPackets reports the card's own count of packets it could not use. The
// card keeps it Gray coded, one bit changing at a time, so a read landing
// mid-increment cannot see a number that was never there. It counts from the
// last reset and is only useful as a difference.
func (r *ring) errorPackets() (uint32, error) {
	gray, err := r.read(regTSErrors)
	if err != nil {
		return 0, err
	}
	binary := gray
	for shift := uint(1); shift < 32; shift *= 2 {
		binary ^= binary >> shift
	}
	return binary, nil
}
