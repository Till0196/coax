// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package boardkit supplies the half of a board driver that never varies: one
// Device per tuner, refusing a tuner already taken, keeping the USB device
// alive while any tuner is open, stopping the stream on close, and turning a
// read timeout into device.ErrWouldBlock.
//
// A board driver implements Tuner and New wraps it into the device.Board and
// device.Tuner contracts, leaving the board package only the USB IDs, the
// bring-up sequence, and how a channel is acquired.
package boardkit

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/pkg/usbio"
)

// Tuner is the hardware half of one tuner of a board. The kit calls these under
// its own per-tuner lock, so an implementation never needs a mutex of its own
// for them -- it does still need one for state it shares with the other tuners
// of the same board.
type Tuner interface {
	// Tune acquires the channel described by the channel-file properties.
	Tune(properties map[string]string) error
	// Status reports the demodulator lock state for the last tuned channel.
	Status() (device.Status, error)
	// StartStream begins delivering transport packets to Read.
	StartStream() error
	// StopStream stops delivery. The kit calls it once per StartStream.
	StopStream() error
	// Read returns the next packets, or device.ErrWouldBlock when none arrived
	// before the board's read timeout. WouldBlock converts the usual case.
	Read(ctx context.Context, buffer []byte) (int, error)
}

// Amplifier is implemented by a Tuner whose front end has a gain setting. The
// kit reads the channel's GAIN property, picks the step from the ladder the
// tuner describes, and applies it before the tune. A tuner without one has the
// property refused.
type Amplifier interface {
	// GainLadder is what this tuner's amplifier can be set to for the delivery
	// system about to be tuned: per tuner where the two tuners carry different
	// silicon, and per delivery system where one tuner feeds two chips. An empty
	// ladder means nothing to set.
	GainLadder(deliverySystem string) tuning.GainLadder
	// SetGain moves the amplifier to one of that ladder's steps, counted from
	// the least gain.
	SetGain(step int) error
}

// Releaser is implemented by a Tuner whose hardware must be powered down when
// the tuner is closed, rather than only when the whole board is.
type Releaser interface {
	// Release runs after the stream is stopped and before the board's
	// reference is dropped.
	Release() error
}

// Config describes a board to the kit.
type Config struct {
	// Model is the board's product name. Name appends the USB bus:address.
	Model string
	// Location is the USB bus:address the board was found at.
	Location string
	// Tuners describes each tuner before it is opened. Its length sets how many
	// tuners the board has.
	Tuners []device.TunerInfo
	// Owner keeps the USB device alive while the board handle or any tuner
	// taken from it is open. Boards built on a shared frame pass the frame's
	// Owner; the rest leave it nil and set Release instead.
	Owner *device.Owner
	// Release runs once the board handle and every tuner are closed. It is used
	// only when Owner is nil.
	Release func() error
	// Open supplies the hardware half of a tuner when it is first claimed.
	Open func(index int) (Tuner, error)
	// Supports overrides the delivery-system test. Leaving it nil matches
	// against the tuner's own Deliveries, which is what most boards want.
	Supports func(deliverySystem string) bool
}

// Board is an opened USB board wearing the device.Board contract.
type Board struct {
	config Config
	owner  *device.Owner
	mu     sync.Mutex
	taken  []bool
}

// New wraps a board's configuration into the device.Board contract. Exactly one
// of Config.Owner and Config.Release must be set.
func New(config Config) (*Board, error) {
	if len(config.Tuners) == 0 {
		return nil, errors.New("the board has no tuners")
	}
	if config.Open == nil {
		return nil, errors.New("Config.Open is required")
	}
	if (config.Owner == nil) == (config.Release == nil) {
		return nil, errors.New("set exactly one of Config.Owner and Config.Release")
	}
	owner := config.Owner
	if owner == nil {
		owner = device.NewOwner(config.Release)
	}
	return &Board{config: config, owner: owner, taken: make([]bool, len(config.Tuners))}, nil
}

// Name reports the model and the USB bus:address the board was found at.
func (board *Board) Name() string {
	if board.config.Location == "" {
		return board.config.Model
	}
	return board.config.Model + " " + board.config.Location
}

// Tuners describes the board's tuners.
func (board *Board) Tuners() []device.TunerInfo {
	return append([]device.TunerInfo(nil), board.config.Tuners...)
}

// Owner exposes the reference count, for board code that has to retain the USB
// device outside a tuner's lifetime.
func (board *Board) Owner() *device.Owner { return board.owner }

// OpenTuner claims one tuner, returning device.ErrTunerBusy if it is already
// out. The returned Device may be used concurrently with the board's others.
func (board *Board) OpenTuner(index int) (device.Tuner, error) {
	if err := device.CheckTuner(index, board.config.Tuners); err != nil {
		return nil, err
	}
	board.mu.Lock()
	if board.taken[index] {
		board.mu.Unlock()
		return nil, device.ErrTunerBusy
	}
	if err := board.owner.Retain(); err != nil {
		board.mu.Unlock()
		return nil, err
	}
	board.taken[index] = true
	board.mu.Unlock()

	tuner, err := board.config.Open(index)
	if err != nil {
		board.release(index)
		_ = board.owner.Release()
		return nil, fmt.Errorf("open tuner %d: %w", index, err)
	}
	return &claimedTuner{board: board, index: index, tuner: tuner}, nil
}

// Close drops the board handle's own reference. The USB device stays open while
// a tuner taken from it is still in use.
func (board *Board) Close() error { return board.owner.Release() }

func (board *Board) release(index int) {
	board.mu.Lock()
	board.taken[index] = false
	board.mu.Unlock()
}

// supports falls back to the tuner's own delivery list when the board did not
// override the test.
func (board *Board) supports(index int, deliverySystem string) bool {
	if board.config.Supports != nil {
		return board.config.Supports(deliverySystem)
	}
	return board.config.Tuners[index].Supports(deliverySystem, tuning.Normalize)
}

// claimedTuner is one claimed tuner, wearing the device.Tuner contract.
type claimedTuner struct {
	board *Board
	index int
	tuner Tuner

	mu        sync.Mutex
	streaming bool
	closed    bool
	// open is the stream last handed out, ended when the tuner is closed so
	// that nothing reads it after the tuner is someone else's.
	open *stream
	// gain is the amplifier step the last tune settled on, spelled for
	// reporting. Empty when nothing asked for one.
	gain string
}

func (claimed *claimedTuner) Name() string { return claimed.board.Name() }

func (claimed *claimedTuner) Supports(deliverySystem string) bool {
	return claimed.board.supports(claimed.index, deliverySystem)
}

// Tune acquires a channel. A stream already running is stopped first, since no
// board delivers a coherent stream across a retune.
func (claimed *claimedTuner) Tune(properties map[string]string) error {
	claimed.mu.Lock()
	defer claimed.mu.Unlock()
	if claimed.closed {
		return errors.New("USB tuner is closed")
	}
	if err := claimed.setGainLocked(properties); err != nil {
		return err
	}
	_ = claimed.stopStreamLocked()
	return claimed.tuner.Tune(properties)
}

// setGainLocked applies the channel's gain setting, if it gave one. "auto" is
// honoured by every board, including those with no amplifier -- it means
// whoever drives the front end decides. A level or an end is refused where
// there is nothing to move.
func (claimed *claimedTuner) setGainLocked(properties map[string]string) error {
	gain, err := tuning.ParseGain(properties)
	if err != nil {
		return err
	}
	if !gain.Given() || gain.Kind == tuning.GainAuto {
		return nil
	}
	amplifier, ok := claimed.tuner.(Amplifier)
	if !ok {
		return tuning.RefuseGain(properties, claimed.board.Name())
	}
	ladder := amplifier.GainLadder(properties["DELIVERY_SYSTEM"])
	if !ladder.Usable() {
		return tuning.RefuseGain(properties, claimed.board.Name())
	}
	step, err := ladder.Choose(gain)
	if err != nil {
		return fmt.Errorf("%s: %w", claimed.board.Name(), err)
	}
	if err := amplifier.SetGain(step); err != nil {
		return err
	}
	claimed.gain = ladder.Describe(step)
	return nil
}

// Settings reports how the tuner is set beyond the channel itself: the gain
// step chosen, which matters because a level is matched to the nearest step the
// hardware has rather than taken as written.
func (claimed *claimedTuner) Settings() []device.Setting {
	claimed.mu.Lock()
	defer claimed.mu.Unlock()
	var settings []device.Setting
	if reporter, ok := claimed.tuner.(interface{ Settings() []device.Setting }); ok {
		settings = append(settings, reporter.Settings()...)
	}
	if claimed.gain != "" {
		settings = append(settings, device.Setting{Name: tuning.GainProperty, Value: claimed.gain})
	}
	return settings
}

func (claimed *claimedTuner) Status() (device.Status, error) {
	claimed.mu.Lock()
	defer claimed.mu.Unlock()
	if claimed.closed {
		return device.Status{}, errors.New("USB tuner is closed")
	}
	return claimed.tuner.Status()
}

func (claimed *claimedTuner) OpenStream() (device.Stream, error) {
	claimed.mu.Lock()
	defer claimed.mu.Unlock()
	if claimed.closed {
		return nil, errors.New("USB tuner is closed")
	}
	if claimed.streaming {
		return nil, errors.New("USB tuner is already streaming")
	}
	if err := claimed.tuner.StartStream(); err != nil {
		return nil, err
	}
	claimed.streaming = true
	claimed.open = &stream{claimed: claimed}
	return claimed.open, nil
}

// Close stops the stream, powers the tuner down if it asked to be, and hands it
// back to the board.
func (claimed *claimedTuner) Close() error {
	claimed.mu.Lock()
	if claimed.closed {
		claimed.mu.Unlock()
		return nil
	}
	claimed.closed = true
	if claimed.open != nil {
		// Waits for a read under way. See stream.Read.
		claimed.open.mu.Lock()
		claimed.open.closed = true
		claimed.open.mu.Unlock()
		claimed.open = nil
	}
	_ = claimed.stopStreamLocked()
	if releaser, ok := claimed.tuner.(Releaser); ok {
		_ = releaser.Release()
	}
	claimed.mu.Unlock()

	claimed.board.release(claimed.index)
	return claimed.board.owner.Release()
}

// stopStream tolerates a second call.
func (claimed *claimedTuner) stopStream() error {
	claimed.mu.Lock()
	defer claimed.mu.Unlock()
	return claimed.stopStreamLocked()
}

func (claimed *claimedTuner) stopStreamLocked() error {
	if !claimed.streaming {
		return nil
	}
	claimed.streaming = false
	return claimed.tuner.StopStream()
}

type stream struct {
	claimed *claimedTuner
	mu      sync.Mutex
	closed  bool
}

// Read holds the stream's lock throughout, so Close waits for a read under way
// and nothing reads the tuner once Close has returned. Several boards hand the
// same front end to whoever opens a tuner next, and a reader left over from the
// last holder would be taking the new holder's packets.
func (stream *stream) Read(ctx context.Context, buffer []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed {
		return 0, device.ErrClosed
	}
	return stream.claimed.tuner.Read(ctx, buffer)
}

// Dropped reports the packets lost because the caller was not reading fast
// enough, for a tuner that can say. One that cannot reports zero, which is what
// a board with an endpoint per tuner genuinely drops.
func (stream *stream) Dropped() uint64 {
	counter, ok := stream.claimed.tuner.(interface{ Dropped() uint64 })
	if !ok {
		return 0
	}
	return counter.Dropped()
}

// Close stops the stream but leaves the tuner open for a retune.
func (stream *stream) Close() error {
	stream.mu.Lock()
	if stream.closed {
		stream.mu.Unlock()
		return nil
	}
	stream.closed = true
	stream.mu.Unlock()
	return stream.claimed.stopStream()
}

// WouldBlock translates a USB read timeout into device.ErrWouldBlock, which the
// scanner retries rather than reporting as a failure.
func WouldBlock(read int, err error) (int, error) {
	if errors.Is(err, usbio.ErrTimeout) {
		return read, device.ErrWouldBlock
	}
	return read, err
}

// CardHardware is the smart card slot of a board as the hardware layer offers
// it: whichever of the bridge, the chip or the frame owns the slot implements
// these three. It is narrower than device.CardReader because the reader's name
// is a fact about the product.
type CardHardware interface {
	// CardPresent reports whether a card is seated in the slot.
	CardPresent() (bool, error)
	// ResetCard powers the card up and returns its answer to reset.
	ResetCard() ([]byte, error)
	// TransmitAPDU sends one command APDU to the card and returns its answer.
	TransmitAPDU(apdu []byte) ([]byte, error)
}

// CardBoard adds a board's smart card reader to its handle, fulfilling the
// optional card capability of the usbdev contract. It wraps the board rather
// than a Tuner because one slot serves all the board's tuners.
func CardBoard(inner device.Board, hardware CardHardware, reader string) device.Board {
	return &cardBoard{Board: inner, hardware: hardware, reader: reader}
}

type cardBoard struct {
	device.Board
	hardware CardHardware
	reader   string
}

var _ device.CardReader = (*cardBoard)(nil)

func (board *cardBoard) CardStatus() (device.CardStatus, error) {
	present, err := board.hardware.CardPresent()
	if err != nil {
		return device.CardStatus{}, err
	}
	return device.CardStatus{Present: present, Reader: board.reader}, nil
}

func (board *cardBoard) ResetCard() ([]byte, error) { return board.hardware.ResetCard() }

func (board *cardBoard) TransmitAPDU(apdu []byte) ([]byte, error) {
	return board.hardware.TransmitAPDU(apdu)
}
