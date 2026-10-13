// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package tsdemux splits one USB endpoint that carries the transport streams of
// several tuners at once back into a stream per tuner.
//
// Bridges such as the ITE IT930x feed every tuner into a single bulk endpoint
// and mark each 188-byte packet by rewriting its sync byte with the source
// tuner's number. One reader goroutine fans the packets out to per-tuner
// buffers.
package tsdemux

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
)

// Tag reports which tuner a packet belongs to, given its leading sync byte.
type Tag func(sync byte) (index int, ok bool)

// PlainSync tags an untagged stream: every packet belongs to tuner 0.
func PlainSync() Tag {
	return func(sync byte) (int, bool) { return 0, sync == 0x47 }
}

// TaggedSync tags the scheme IT930x boards use, where the high nibble of the sync
// byte carries the tuner number plus one: 0x17, 0x27, 0x37, 0x47, 0x57.
func TaggedSync(count int) Tag {
	return func(sync byte) (int, bool) {
		if sync&0x8f != 0x07 {
			return 0, false
		}
		index := int(sync>>4) - 1
		if index < 0 || index >= count {
			return 0, false
		}
		return index, true
	}
}

// Config describes the multiplexed source.
type Config struct {
	// Read pulls raw bytes from the shared endpoint. It should return a timeout
	// error rather than blocking indefinitely; any error is treated as "nothing
	// arrived".
	Read func(buffer []byte, timeoutMS uint32) (int, error)
	// Start and Stop run when the first tuner starts streaming and when the last one
	// stops, for the bridge's stream enable and any buffer purge.
	Start func() error
	Stop  func() error
	// Tag identifies the owning tuner of each packet. Required.
	Tag Tag
	// Tuners is how many tuners feed the endpoint. Required.
	Tuners int
	// PacketLength defaults to 188.
	PacketLength int
	// ReadSize is the byte count per source read. Defaults to 312 packets, the
	// transfer size IT930x boards are usually configured with. Reads shorter than
	// a packet are fine: the remainder is carried into the next one.
	ReadSize int
	// Timeout is the source read timeout in milliseconds. Defaults to 100.
	Timeout uint32
	// Blocks is how many read batches may queue per tuner before packets are
	// dropped.
	Blocks int
	// SyncRun is how many consecutive well-formed packets must line up before a
	// resynchronising demux trusts the alignment.
	SyncRun int
}

func (config *Config) applyDefaults() error {
	if config.Read == nil || config.Tag == nil {
		return errors.New("Read and Tag are required")
	}
	if config.Tuners <= 0 {
		return fmt.Errorf("Tuners must be positive, got %d", config.Tuners)
	}
	if config.PacketLength <= 0 {
		config.PacketLength = 188
	}
	if config.ReadSize <= 0 {
		config.ReadSize = config.PacketLength * 312
	}
	if config.Timeout == 0 {
		config.Timeout = 100
	}
	if config.Blocks <= 0 {
		config.Blocks = 64
	}
	if config.SyncRun <= 0 {
		config.SyncRun = 4
	}
	return nil
}

// Demux fans a multiplexed endpoint out to one Reader per tuner. The source is
// read only while at least one Reader is open.
type Demux struct {
	config  Config
	mu      sync.Mutex
	readers []*Reader
	active  int
	stop    chan struct{}
	done    chan struct{}
	carry   []byte
	// batches is the per-tuner sorting scratch, kept between reads. The batches
	// themselves come from the readers and go back to them.
	batches [][]byte
	// dropped counts packets thrown away before they could be sorted, which is not
	// any one reader's loss.
	dropped atomic.Uint64
}

// New prepares a demultiplexer. No reading happens until Open is called.
func New(config Config) (*Demux, error) {
	if err := config.applyDefaults(); err != nil {
		return nil, err
	}
	return &Demux{config: config, readers: make([]*Reader, config.Tuners)}, nil
}

// Open claims the stream of one tuner. Readers for different tuners are
// independent and may be used concurrently.
func (demux *Demux) Open(index int) (*Reader, error) {
	demux.mu.Lock()
	defer demux.mu.Unlock()
	if index < 0 || index >= len(demux.readers) {
		return nil, fmt.Errorf("tuner %d is outside the range 0..%d", index, len(demux.readers)-1)
	}
	if demux.readers[index] != nil {
		return nil, device.ErrTunerBusy
	}
	reader := &Reader{
		demux:  demux,
		index:  index,
		blocks: make(chan []byte, demux.config.Blocks),
		spare:  make(chan []byte, demux.config.Blocks),
	}
	// Made once, for the same reason the read buffers are.
	for i := 0; i < demux.config.Blocks; i++ {
		reader.spare <- make([]byte, 0, demux.config.ReadSize)
	}
	demux.readers[index] = reader
	demux.active++
	if demux.active == 1 {
		if err := demux.start(); err != nil {
			demux.readers[index] = nil
			demux.active--
			return nil, err
		}
	}
	return reader, nil
}

// start runs with demux.mu held.
func (demux *Demux) start() error {
	if demux.config.Start != nil {
		if err := demux.config.Start(); err != nil {
			return err
		}
	}
	demux.carry = demux.carry[:0]
	demux.stop = make(chan struct{})
	demux.done = make(chan struct{})
	go demux.pump(demux.stop, demux.done)
	return nil
}

// release drops one reader and stops the pump once the last one goes.
func (demux *Demux) release(reader *Reader) error {
	demux.mu.Lock()
	if demux.readers[reader.index] != reader {
		demux.mu.Unlock()
		return nil
	}
	demux.readers[reader.index] = nil
	demux.active--
	if demux.active != 0 {
		demux.mu.Unlock()
		return nil
	}
	stop, done := demux.stop, demux.done
	demux.mu.Unlock()
	close(stop)
	<-done
	if demux.config.Stop != nil {
		return demux.config.Stop()
	}
	return nil
}

// pump takes bytes off the shared endpoint, and does nothing else with them.
//
// Sorting them into packets is left to a goroutine of its own. The card will
// not wait: an endpoint sending isochronously is served only while transfers
// are outstanding, so whatever this goroutine stops to do is time the card
// spends with nowhere to put what it is sending. The sorting is microseconds of
// work, but on a busy machine it was measured taking a hundred and forty
// milliseconds. On two goroutines that is only the sorter's problem, and it
// catches up.
func (demux *Demux) pump(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	defer device.HaltOnPanic()
	prioritise()
	defer runtime.UnlockOSThread()

	raw := make(chan []byte, demux.config.Blocks)
	// The buffers are made here, all of them, and go round from here on.
	//
	// Making one whenever the pool happened to be empty let the heap grow for the
	// first quarter of a minute of every stream, and every collection that growth
	// caused took a quarter of the runtime's processors for the length of its
	// mark. Measured, the goroutine reading the card waited a quarter of a second
	// for a turn, which is as long as the endpoint's outstanding transfers cover.
	free := make(chan []byte, demux.config.Blocks)
	for i := 0; i < demux.config.Blocks; i++ {
		free <- make([]byte, demux.config.ReadSize)
	}
	sorted := make(chan struct{})
	go demux.sort(raw, free, sorted)
	defer func() {
		close(raw)
		<-sorted
	}()

	for {
		select {
		case <-stop:
			return
		default:
		}
		var buffer []byte
		select {
		case buffer = <-free:
		default:
			// Nothing free means the sorter is further behind than the queue is deep. The
			// oldest read waiting for it is taken back rather than waiting for one or
			// making one: waiting stops the card, and making one leaves the collector to
			// take them back at a moment of its own choosing.
			select {
			case buffer = <-raw:
				demux.dropped.Add(uint64(len(buffer) / demux.config.PacketLength))
				buffer = buffer[:cap(buffer)]
			case <-stop:
				return
			}
		}
		read, err := demux.config.Read(buffer, demux.config.Timeout)
		if read == 0 {
			demux.recycle(free, buffer)
		} else {
			select {
			case raw <- buffer[:read]:
			default:
				// The sorter is further behind than the queue is deep. Dropping is what
				// the card would do; waiting for it would lose the same bytes anyway.
				demux.dropped.Add(uint64(read / demux.config.PacketLength))
				demux.recycle(free, buffer)
			}
		}
		if err != nil && read == 0 {
			// A timeout simply means no packets yet. Any other failure would spin.
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
}

// sort turns what the pump read into packets, and hands the buffer back.
func (demux *Demux) sort(raw chan []byte, free chan<- []byte, done chan<- struct{}) {
	defer close(done)
	defer device.HaltOnPanic()
	// Sorting is on the same path as reading and is owed the same turn. A sorter
	// that does not get one leaves the reader nothing to read into: measured, it
	// went two seconds without running, which emptied the pool.
	prioritise()
	defer runtime.UnlockOSThread()
	for buffer := range raw {
		demux.distribute(buffer)
		demux.recycle(free, buffer)
	}
}

// recycle offers a buffer back to the pump, and lets it go if the pump has
// enough.
func (demux *Demux) recycle(free chan<- []byte, buffer []byte) {
	select {
	case free <- buffer[:cap(buffer)]:
	default:
	}
}

// distribute splits a raw read into packets and queues them per tuner. Leftover
// bytes are carried into the next read.
//
// The batches are borrowed from the readers they are bound for and handed back
// once read, because a stream sorted into freshly made batches makes as much
// garbage as it carries. Collecting that garbage stops every thread the runtime
// has: measured, such a stop held the thread reading the card for a third of a
// second.
func (demux *Demux) distribute(data []byte) {
	length := demux.config.PacketLength
	source := data
	if len(demux.carry) != 0 {
		demux.carry = append(demux.carry, data...)
		source = demux.carry
	}
	if len(demux.batches) != len(demux.readers) {
		demux.batches = make([][]byte, len(demux.readers))
	}
	batches := demux.batches
	offset := 0
	for offset+length <= len(source) {
		index, ok := demux.aligned(source[offset:])
		if !ok {
			offset++
			continue
		}
		if batches[index] == nil {
			batches[index] = demux.borrow(index)
		}
		start := len(batches[index])
		batches[index] = append(batches[index], source[offset:offset+length]...)
		// Hand the tuner an ordinary transport stream, not the tagged one.
		batches[index][start] = 0x47
		offset += length
	}
	demux.carry = append(demux.carry[:0], source[offset:]...)
	for index, batch := range batches {
		if len(batch) != 0 {
			demux.deliver(index, batch)
		}
		batches[index] = nil
	}
}

// borrow takes a batch a reader has finished with, or makes one. A batch holds
// at most one read's worth of packets, so one that has been round once never
// has to grow.
func (demux *Demux) borrow(index int) []byte {
	demux.mu.Lock()
	reader := demux.readers[index]
	demux.mu.Unlock()
	if reader != nil {
		select {
		case batch := <-reader.spare:
			return batch[:0]
		default:
		}
	}
	return make([]byte, 0, demux.config.ReadSize)
}

// aligned reports the owning tuner of the packet at the head of data, requiring
// a run of well-formed packets so a mid-packet start is not mistaken for one.
func (demux *Demux) aligned(data []byte) (int, bool) {
	length := demux.config.PacketLength
	index, ok := demux.config.Tag(data[0])
	if !ok {
		return 0, false
	}
	for run := 1; run < demux.config.SyncRun; run++ {
		offset := run * length
		if offset >= len(data) {
			// Not enough bytes left to confirm the run; the carry will retry.
			break
		}
		if _, valid := demux.config.Tag(data[offset]); !valid {
			return 0, false
		}
	}
	return index, true
}

func (demux *Demux) deliver(index int, batch []byte) {
	demux.mu.Lock()
	reader := demux.readers[index]
	demux.mu.Unlock()
	if reader == nil {
		return
	}
	select {
	case reader.blocks <- batch:
	default:
		// The consumer is behind. Dropping is what the hardware would do; the count
		// lets a caller report it.
		reader.dropped.Add(uint64(len(batch) / demux.config.PacketLength))
	}
}

// Reader is the transport stream of one tuner.
type Reader struct {
	demux  *Demux
	index  int
	blocks chan []byte
	// spare holds batches this reader has finished with, for the sorter to fill
	// again. block is the one pending is a view of.
	spare   chan []byte
	block   []byte
	pending []byte
	// timeout is this reader's own, kept between reads. A reader is asked for
	// packets five hundred times a second, and a timer made and thrown away that
	// often is rubbish for the collector to walk at exactly the wrong moment.
	timeout *time.Timer
	dropped atomic.Uint64
	closed  atomic.Bool
}

// Read copies out the next packets for this tuner. It returns
// device.ErrWouldBlock when none arrived before the source timeout, and
// device.ErrClosed once the reader has been closed: nothing will arrive for it
// again, and waiting would only look like a quiet carrier.
func (reader *Reader) Read(ctx context.Context, buffer []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if reader.closed.Load() {
		return 0, device.ErrClosed
	}
	if len(reader.pending) == 0 {
		reader.giveBack()
		wait := time.Duration(reader.demux.config.Timeout) * time.Millisecond
		if reader.timeout == nil {
			reader.timeout = time.NewTimer(wait)
		} else {
			reader.timeout.Reset(wait)
		}
		select {
		case block := <-reader.blocks:
			reader.block, reader.pending = block, block
			reader.timeout.Stop()
		case <-ctx.Done():
			reader.timeout.Stop()
			return 0, ctx.Err()
		case <-reader.timeout.C:
			return 0, device.ErrWouldBlock
		}
	}
	copied := copy(buffer, reader.pending)
	reader.pending = reader.pending[copied:]
	if len(reader.pending) == 0 {
		reader.giveBack()
	}
	return copied, nil
}

// giveBack offers the batch just read back to the sorter.
func (reader *Reader) giveBack() {
	if reader.block == nil {
		return
	}
	select {
	case reader.spare <- reader.block[:cap(reader.block)]:
	default:
	}
	reader.block = nil
}

// Dropped counts the packets discarded because this reader fell behind.
func (reader *Reader) Dropped() uint64 {
	// The pump's own count is included: bytes lost before they were sorted are
	// lost to every reader, and there is no telling whose they were.
	return reader.dropped.Load() + reader.demux.dropped.Load()
}

// Close stops this tuner's stream, and the shared endpoint if it was the last.
func (reader *Reader) Close() error {
	if reader.closed.Swap(true) {
		return nil
	}
	return reader.demux.release(reader)
}
