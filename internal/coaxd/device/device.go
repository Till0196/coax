// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package device is the contract a tuner driver satisfies, whatever it is
// attached by: a board is one piece of hardware and a tuner one receiver on it.
// The Linux DVB and Windows BDA backends and the directly driven PCIe cards all
// wear it.
package device

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
)

// ErrWouldBlock reports that no stream data was available within the read
// timeout.
var ErrWouldBlock = errors.New("the receiver would block")

// ErrClosed reports a read of a stream that has been closed. It is not the
// board going away: a stream closed under a reader that had not stopped is that
// reader's mistake, and the board and its other tuners carry on.
var ErrClosed = errors.New("the stream has been closed")

// ErrTunerBusy reports that the requested tuner is already open. Every backend
// answers with it, and the message is shown to whoever asked.
var ErrTunerBusy = errors.New("tuner is already in use")

// Options selects and configures a board.
type Options struct {
	// Selector picks between identical boards: empty for the first match, a
	// "bus:address" string, or a match index.
	Selector string
	// Tuner is the index of the tuner to use. Boards carrying one tuner ignore it.
	Tuner int
	// FirmwareDir holds the blobs firmware-loading ICs need. Empty means
	// DefaultFirmwareDir.
	FirmwareDir string
}

// Measurement is a signal reading a frontend may not be able to make. The flag is
// kept apart from the value because a reading of zero is a real measurement.
type Measurement struct {
	Value float64
	Known bool
}

// Measured wraps a reading a driver did make.
func Measured(value float64) Measurement { return Measurement{Value: value, Known: true} }

// Status is the demodulator lock state plus the frontend status names the scanner
// reports, in the order a DVB frontend would list them.
type Status struct {
	Locked bool
	Names  []string
	// CNR is the carrier-to-noise ratio in dB, measurable only once locked on most
	// demodulators.
	CNR Measurement
	// Strength is the input level in dBm, a tuner rather than a demodulator
	// measurement and so often available without a lock.
	Strength Measurement
	// Quality is a driver-defined figure between 0 and 100, which only the Windows
	// BDA drivers report. Kept apart from CNR because it is not a decibel
	// measurement.
	Quality Measurement
}

// LockNames are the frontend status names a full demodulator lock implies. Boards
// that report partial acquisition build their own list; a caller that wants to
// append must copy first, the slice being shared.
var LockNames = []string{"SIGNAL", "CARRIER", "VITERBI", "SYNC", "LOCK"}

// TunerInfo describes one tuner of a board before it is opened. Deliveries is
// per tuner rather than per board, boards often carrying tuners of differing
// capability.
type TunerInfo struct {
	Index      int
	Name       string
	Deliveries []string
	// Mode is what the hardware is currently set to, for hardware with a setting of
	// its own beside the channel -- a TBS card's demodulator mode. Empty for most.
	Mode string
	// Settings is everything the hardware will say about how it is currently tuned:
	// what the driver found rather than what it was asked for.
	Settings []Setting
	// Controls is what a channel may set on this tuner beside the channel itself:
	// the supply on its aerial lead and its gain. Known from the model before the
	// board is opened, so that a client can offer only what will be honoured.
	Controls Controls
}

// Controls is what a tuner lets a channel set beside where to tune.
type Controls struct {
	// LNB is the supply a tuner can put on its lead for a dish's block. Nil where it
	// has none a channel can set: a terrestrial tuner, or a driver that decides the
	// supply itself (a plain Windows BDA driver).
	LNB *LNBControl
	// Gain is the tuner's amplifier setting. Nil where it has none, which a channel
	// asking for one is refused for (tuning.ErrNoGain).
	Gain *GainControl
}

// LNBControl is what a tuner's LNB supply can be set to.
type LNBControl struct {
	// Off says the supply can be switched off. Where it cannot, a channel that asks
	// for off is refused (tuning.ErrNoLNBSwitch).
	Off bool
	// Low and High are what the two levels are worth ("11V", "13V", "15V",
	// "18V"). Both empty for a supply that is only on or off.
	Low, High string
	// Shared says the supply is one line for more than the tuner: "board" (a USB
	// board's every satellite tuner), "card" (a PCIe card's). A channel on one
	// tuner switching it on feeds the others' leads too. Empty for a supply of
	// the tuner's own.
	Shared string
}

// GainControl is what a tuner's amplifier can be set to.
type GainControl struct {
	// Decibels is each step's gain, least first, where the hardware says; nil for
	// an amplifier that is only in or out of circuit (min and max, no levels).
	Decibels []float64
	// Deliveries narrows the setting to these delivery systems, for a tuner whose
	// amplifier sits on one input only. Empty means every system it tunes.
	Deliveries []string
}

// Switch reports whether the amplifier is only in or out, with no levels.
func (gain GainControl) Switch() bool { return len(gain.Decibels) == 0 }

// Setting is one of a tuner's settings, named the way a channel file names it.
type Setting struct {
	Name  string
	Value string
}

// Supports reports whether this tuner can tune a delivery system.
func (tuner TunerInfo) Supports(deliverySystem string, normalize func(string) string) bool {
	for _, delivery := range tuner.Deliveries {
		if normalize(delivery) == normalize(deliverySystem) {
			return true
		}
	}
	return false
}

// Stream is an opened transport stream. Read returns ErrWouldBlock when the tuner
// had nothing to hand over before its timeout.
type Stream interface {
	Read(ctx context.Context, buffer []byte) (int, error)
	Close() error
}

// StreamStats is the optional capability of a Stream that can say what it threw
// away, asked for with a type assertion. Without a count, a recording from a
// board whose tuners share one endpoint looks exactly like one fed a poor
// signal.
type StreamStats interface {
	// Dropped is transport packets discarded since the stream was opened, because
	// this consumer was not reading fast enough.
	Dropped() uint64
}

// StreamAnalysis is the optional capability of a Stream that looks at the
// transport packets going through it, asked for with a type assertion. It
// answers what no demodulator can: a lead with nothing on it can lock and hand
// back noise for as long as anybody records it, and what tells the two apart is
// whether packets come out with their sync bytes 188 apart.
type StreamAnalysis interface {
	// Synced reports whether the stream is arriving as whole packets now.
	Synced() bool
	// StreamCounts is what has been seen since the stream was opened.
	StreamCounts() StreamCounts
}

// StreamCounts is what was seen in the multiplex leaving a tuner. Every field
// counts from when the stream was opened and none is reset, so a caller
// subtracts.
type StreamCounts struct {
	// Format is which multiplex this was read as, "ts" or "tlv". It decides which of
	// the fields below mean anything.
	Format string
	// Bytes is everything the tuner produced, packet or not.
	Bytes uint64
	// Packets is what was recognised as a packet, stuffing included.
	Packets uint64
	// Skipped is bytes that were not part of a packet.
	Skipped uint64
	// SyncLosses counts breaks in packet-boundary alignment, not packets.
	SyncLosses uint64
	// Discontinuities is packets that did not follow the last one in their own
	// run: a continuity counter, or an MMTP sequence number. Data that went
	// missing rather than data that arrived damaged.
	Discontinuities uint64
	// Nulls is stuffing.
	Nulls uint64

	// Errors is packets the demodulator failed to correct. TS only.
	Errors uint64
	// Scrambled is packets still carrying their scrambling bits. TS only.
	Scrambled uint64

	// Control is the TLV signalling that describes the multiplex, and IPv4, IPv6 and
	// CompressedIP how the datagrams inside are carried. TLV only.
	Control      uint64
	IPv4         uint64
	IPv6         uint64
	CompressedIP uint64
	// Undecodable is TLV packets whose MMTP header could not be reached. TLV only.
	Undecodable uint64
	// Forgotten is how many times the sequence table was emptied because more runs
	// appeared at once than any multiplex has. Anything but zero means
	// Discontinuities is not to be believed. TLV only.
	Forgotten uint64
}

// Tuner is one tuner of a board. Implementations must be safe for concurrent use,
// and must not disturb the other tuners of the same board.
type Tuner interface {
	Name() string
	Supports(deliverySystem string) bool
	Tune(properties map[string]string) error
	Status() (Status, error)
	OpenStream() (Stream, error)
	Close() error
}

// Board is one opened piece of hardware and the tuners on it. A board stays alive
// while any of its tuners is open.
type Board interface {
	Name() string
	Tuners() []TunerInfo
	// OpenTuner claims one tuner. It may be called for several indices, and the
	// resulting Tuners may be used concurrently.
	OpenTuner(index int) (Tuner, error)
	Close() error
}

// FirmwareDirOrDefault resolves the configured firmware directory.
func (options Options) FirmwareDirOrDefault() string {
	if options.FirmwareDir != "" {
		return options.FirmwareDir
	}
	return DefaultFirmwareDir()
}

// DefaultFirmwareDir is where firmware blobs live when none was configured:
// /lib/firmware on Linux, and elsewhere nothing, which leaves FirmwareDirs to
// look in the places that system has.
func DefaultFirmwareDir() string {
	if runtime.GOOS == "linux" {
		return "/lib/firmware"
	}
	return ""
}

// Owner shares one underlying device between a board handle and the tuners
// taken from it, releasing the hardware once the last holder is done: closing a
// tuner must not pull the device out from under the ones still streaming. A new
// Owner starts with one reference, the board handle itself.
type Owner struct {
	mu      sync.Mutex
	refs    int
	release func() error
}

// NewOwner returns an Owner holding one reference, calling release at zero.
func NewOwner(release func() error) *Owner {
	return &Owner{refs: 1, release: release}
}

// Retain takes an additional reference.
func (owner *Owner) Retain() error {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.refs == 0 {
		return errors.New("USB device is closed")
	}
	owner.refs++
	return nil
}

// Release drops one reference, releasing the hardware at zero. Releasing more
// times than retained is a no-op, so a double Close is harmless.
func (owner *Owner) Release() error {
	owner.mu.Lock()
	if owner.refs == 0 {
		owner.mu.Unlock()
		return nil
	}
	owner.refs--
	if owner.refs != 0 {
		owner.mu.Unlock()
		return nil
	}
	// The count can never rise again — Retain refuses once it has hit zero — so this
	// branch runs exactly once.
	owner.mu.Unlock()
	return owner.release()
}

// Latch turns a resource shared by the tuners of a board on for the first
// holder and off for the last: LNB power, a shared stream endpoint, a clock.
type Latch struct {
	mu    sync.Mutex
	count int
	on    func() error
	off   func() error
}

// NewLatch returns a Latch that is currently off.
func NewLatch(on, off func() error) *Latch {
	return &Latch{on: on, off: off}
}

// Acquire turns the resource on if this is the first holder.
func (latch *Latch) Acquire() error {
	latch.mu.Lock()
	defer latch.mu.Unlock()
	if latch.count == 0 && latch.on != nil {
		if err := latch.on(); err != nil {
			return err
		}
	}
	latch.count++
	return nil
}

// Release turns the resource off once the last holder lets go.
func (latch *Latch) Release() error {
	latch.mu.Lock()
	defer latch.mu.Unlock()
	if latch.count == 0 {
		return nil
	}
	latch.count--
	if latch.count != 0 || latch.off == nil {
		return nil
	}
	return latch.off()
}

// Held reports how many holders the latch currently has.
func (latch *Latch) Held() int {
	latch.mu.Lock()
	defer latch.mu.Unlock()
	return latch.count
}

// CheckTuner validates a tuner index against a board's tuner list.
func CheckTuner(index int, tuners []TunerInfo) error {
	if index < 0 || index >= len(tuners) {
		return fmt.Errorf("tuner %d is outside the range 0..%d", index, len(tuners)-1)
	}
	return nil
}

// CardStatus is what a board's on-board card reader can say about the card in it
// without disturbing an ongoing session.
type CardStatus struct {
	// Present reports whether a card is seated.
	Present bool
	// Reader names the card interface hardware, e.g. "ASIE5606 rev 15".
	Reader string
}

// CardReader is the optional capability of a board that carries a smart card
// reader of its own, asked for with a type assertion since most boards have
// none. It belongs to the board rather than to a tuner because there is one
// slot per enclosure.
//
// A session is a single conversation with sequence numbers at both ends, so
// implementations serialise their own access; callers still have to keep a
// Reset and the Transmit calls that follow it together to make sense.
type CardReader interface {
	// CardStatus reports whether a card is seated.
	CardStatus() (CardStatus, error)
	// ResetCard powers the card up and returns its ATR, starting a fresh session. It
	// must be called before the first TransmitAPDU.
	ResetCard() ([]byte, error)
	// TransmitAPDU sends one command APDU and returns the card's response, including
	// its two status bytes.
	TransmitAPDU(apdu []byte) ([]byte, error)
}
