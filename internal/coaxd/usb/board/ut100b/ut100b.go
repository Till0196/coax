// Copyright (c) 2008 FUKAUMI Naoki; 2012 tslroom; 2016 stz2012; 2026 KASHIWAYA Rikuto
// SPDX-License-Identifier: (Apache-2.0 OR GPL-2.0-or-later) AND BSD-2-Clause
//
// The power-up sequence -- the GPIO steps and their waits, the demodulator's
// registers and the card interface's -- is recfriio's Hdp.cpp for this board
// (BSD-2-Clause; see NOTICE). The rest is this module's own.

// Package ut100b drives the Quixun QRS-UT100B, a terrestrial-only USB stick.
//
// It is the PLEX boards' silicon put together another way: an ASICEN ASV5211
// bridge, a demodulator with the TC90522's terrestrial register map at 0x20, an
// RF tuner behind the demodulator's repeater, and an ASIE5606 card interface IC
// that both hosts the on-board card slot and sits in the TS path. So the bridge,
// the demodulator, the card's T=1 session and the firmware upload are the ones
// px3 uses, and what is the board's own is the tuner (tuner.go), the order the
// board is powered up in, and the IC's DES transform on the stream
// (descramble.go).
//
// The bridge boots from the same ROM as the PLEX boards, under the same cold
// identifier, and comes back under Quixun's own once the firmware is in. px3
// does the upload for both.
package ut100b

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/tc90522"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/smartcard/asie5606"
	"github.com/Till0196/coax/internal/coaxd/trace"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/board/px3"
	"github.com/Till0196/coax/internal/coaxd/usb/boardkit"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/asv5211"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/pkg/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "ut100b",
	Aliases:     []string{"qrs-ut100b", "qrsut100b"},
	Description: "Quixun QRS-UT100B (ASV5211 + TC90522 + ASIE5606)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// WarmIDs is what the board enumerates as once its firmware is in. The EEPROM
// on the board decides it, whichever firmware image was uploaded.
var WarmIDs = []usbio.ID{{Vendor: 0x3275, Product: 0x7020}}

var supportedIDs = WarmIDs

// deliveries is what the board's one tuner reaches.
var deliveries = []string{"ISDB-T"}

// Supports reports whether the board can tune a delivery system.
func Supports(deliverySystem string) bool {
	return tuning.Normalize(deliverySystem) == "ISDBT"
}

// Layout is the board's one terrestrial tuner.
func Layout(usbio.ID) []device.TunerInfo {
	return []device.TunerInfo{{Index: 0, Name: "ISDB-T", Deliveries: deliveries}}
}

// I2C addresses on the bridge's bus.
const (
	demodAddress = 0x20
)

// cardAddresses are where the card interface IC may answer, in the order the
// vendor driver looks: 0x5a, where the PLEX boards and the MonsterTV HDUS have
// it, then 0x4a, where it answers on this board. px3 looks at the same two.
var cardAddresses = []byte{0x5a, 0x4a}

// Card interface registers the bring-up writes.
const (
	cardCommand  = 0x00
	cardStatus   = 0x04
	cardTSMode   = 0x05
	cardIdentity = 0x09
	// cardTSSetup is the first of the registers the TS output needs written
	// before it starts.
	cardTSSetup       = 0x10
	cardTSSetupLength = 0x10
	card80            = 0x80
	// cardTSOutput enables the IC's TS output, which on this board always comes
	// enciphered (descramble.go).
	cardTSOutput = 0x80
)

// GPIO lines of the bridge's main port. What each does is inferred from where
// the vendor driver steps it.
const (
	gpioTunerPower = 0x08 // high cuts the tuner's supply
	gpioCardReset  = 0x11 // the card interface IC's reset, active low
	gpioPowerDown  = 0xd4 // cleared when powering down
	gpioPowerUp    = 0xd5 // pulsed low when powering up
)

// demodInit is the demodulator's power-up register set, as the vendor driver
// writes it, in register and value pairs.
var demodInit = []byte{
	0x01, 0x00, 0x02, 0x00, 0x03, 0x00, 0x04, 0x00, 0x05, 0x00, 0x06, 0x00, 0x07, 0x00, 0x08, 0x00,
	0x0c, 0x00, 0x11, 0x21, 0x12, 0x10, 0x13, 0x03, 0x14, 0xe0, 0x15, 0x42, 0x16, 0x09, 0x17, 0x20,
	0x18, 0x29, 0x19, 0x13, 0x1a, 0x29, 0x1b, 0x13, 0x1c, 0x2a, 0x1d, 0xaa, 0x1e, 0xaa, 0x1f, 0xa8,
	0x20, 0x00, 0x21, 0xff, 0x22, 0x80, 0x23, 0x4c, 0x24, 0x4c, 0x25, 0x80, 0x26, 0x00, 0x27, 0x0c,
	0x28, 0x60, 0x29, 0x6b, 0x2a, 0x40, 0x2b, 0x40, 0x2c, 0xff, 0x2d, 0x00, 0x2e, 0xff, 0x2f, 0x00,
	0x30, 0x20, 0x31, 0x0f, 0x32, 0x84, 0x34, 0x0f, 0x38, 0x00, 0x39, 0x0f, 0x3a, 0x20, 0x3b, 0x21,
	0x3c, 0x3f, 0x3d, 0x10, 0x3e, 0x08, 0x3f, 0x0c, 0x40, 0x0c, 0x41, 0x00, 0x42, 0x00, 0x43, 0x4f,
	0x44, 0xff, 0x46, 0x20, 0x47, 0x00, 0x48, 0x90, 0x49, 0xe6, 0x4a, 0x02, 0x4b, 0x54, 0x4c, 0x00,
	0x50, 0x04, 0x51, 0x58, 0x52, 0x20, 0x54, 0x57, 0x55, 0xf1, 0x56, 0x20, 0x57, 0x70, 0x58, 0x60,
	0x5c, 0x50, 0x5d, 0x00, 0x5e, 0x01, 0x5f, 0x80, 0x70, 0x18, 0x71, 0x00, 0x72, 0x00, 0x75, 0x22,
	0x76, 0x00, 0x77, 0x01, 0x7c, 0x00, 0x7d, 0x52, 0xba, 0x00, 0xbb, 0x00, 0xbc, 0x00, 0xc2, 0x10,
	0xc7, 0x00, 0xe4, 0x1a, 0xe6, 0x2f, 0xe9, 0x08, 0xec, 0x00, 0xef, 0x00,
}

// Demodulator register 0x1c: the vendor driver sets bits 3 and 5 and clears bit
// 2 and bit 4 at power-up, and sets bit 4 as well before every tune.
const (
	demodControl     = 0x1c
	demodControlMask = 0x3c
	demodControlOpen = 0x28
	demodControlTune = 0x38
)

// Around the tuner moving, the vendor driver writes registers 0x1e and 0x1f, the
// first pair while the gain control is held and the second before it resumes.
var (
	demodBeforeTune = []byte{0x1e, 0xaa, 0x1f, 0xa8}
	demodAfterTune  = []byte{0x1e, 0xa2, 0x1f, 0x08}
)

// The stream comes in on the bridge's first channel, endpoint 0x81, through its
// channel filter, which drops the null packets: 0x1fff in both ends of its
// excluded PID range.
const (
	streamChannel  = 0
	filterExcludeL = 0x41
	filterExcludeH = 0x43
	nullPID        = 0x1fff
)

// control is the part of *asv5211.Bridge the board uses, as an interface so the
// sequences can be checked without hardware.
type control interface {
	Name() string
	Close() error
	CustomerInfo() ([]byte, error)
	SetGPIO(value, mask byte) error
	SetGPIOEx(value, mask byte) error
	ResetFilter(channel int, mode byte) error
	SetFilterWord(channel int, register byte, value uint16) error
	SetFilterInput(channel int, on bool) error
	SetFilterRun(channel int, on bool) error
	StartStream(channel int) error
	StopStream(channel int) error
	ReadStream(channel int, buffer []byte, timeout uint32) (int, error)
}

// hardware is the board, brought up.
type hardware struct {
	control control
	// cardBus reaches the card interface IC's registers at an address.
	cardBus func(address byte) asie5606.Bus
	demod   *tc90522.Demod
	tuner   *tuner
	// cardAddress is where the card interface IC answered.
	cardAddress byte
	sleep       func(time.Duration)

	card struct {
		sync.Mutex
		reader *asie5606.Reader
	}
}

func (hardware *hardware) wait(milliseconds int) {
	hardware.sleep(time.Duration(milliseconds) * time.Millisecond)
}

// gpioStep is one write to the main GPIO port and the wait after it.
type gpioStep struct {
	value, mask byte
	wait        int
}

func (hardware *hardware) gpio(steps ...gpioStep) error {
	for _, step := range steps {
		if err := hardware.control.SetGPIO(step.value, step.mask); err != nil {
			return err
		}
		if step.wait > 0 {
			hardware.wait(step.wait)
		}
	}
	return nil
}

// enumerationTimeout bounds the wait for a board px3 booted to come back.
const (
	enumerationTimeout = 5 * time.Second
	enumerationPoll    = 100 * time.Millisecond
)

// open claims the stick, booting it first if it is still waiting for its
// firmware, and powers it up.
func open(options device.Options) (device.Board, error) {
	transport, err := usbio.Open(options.Selector, supportedIDs)
	if err != nil {
		transport, err = bootAndOpen(options, err)
		if err != nil {
			return nil, err
		}
	}
	bridge := asv5211.New(transport)
	hardware := &hardware{
		control: bridge,
		cardBus: func(address byte) asie5606.Bus { return bridge.OpenCardBus(address) },
		demod:   tc90522.New(bridge.I2C(demodAddress), demodAddress, false),
		sleep:   time.Sleep,
	}
	hardware.tuner = &tuner{bus: hardware.demod.TunerBus()}
	if err := hardware.powerUp(); err != nil {
		_ = bridge.Close()
		return nil, fmt.Errorf("power up the QRS-UT100B: %w", err)
	}
	inner, err := boardkit.New(boardkit.Config{
		Model:    "Quixun QRS-UT100B",
		Location: bridge.Name(),
		Tuners:   Layout(WarmIDs[0]),
		Release:  hardware.powerDown,
		Open: func(int) (boardkit.Tuner, error) {
			return &frontend{hardware: hardware, packets: newPacketizer()}, nil
		},
	})
	if err != nil {
		_ = hardware.powerDown()
		return nil, err
	}
	return &board{Board: inner, hardware: hardware}, nil
}

// bootAndOpen has px3 upload the firmware to any board still under the bridge
// vendor's cold identifier, which is how this board starts, and opens it once it
// is back under its own.
func bootAndOpen(options device.Options, cause error) (*usbio.Transport, error) {
	if px3.Backend.Boot == nil {
		return nil, cause
	}
	if err := px3.Backend.Boot(options); err != nil {
		return nil, fmt.Errorf("%w (and booting a cold ASV52xx board failed: %v)", cause, err)
	}
	deadline := time.Now().Add(enumerationTimeout)
	for {
		transport, err := usbio.Open(options.Selector, supportedIDs)
		if err == nil || !time.Now().Before(deadline) {
			return transport, err
		}
		time.Sleep(enumerationPoll)
	}
}

// powerUp is the vendor driver's bring-up: the supplies, the tuner's reset, the
// demodulator's and tuner's registers, then the card interface IC's reset and
// its TS output.
func (hardware *hardware) powerUp() error {
	// The block of board information is read first, as the vendor driver does.
	// Nothing in it is needed here.
	if _, err := hardware.control.CustomerInfo(); err != nil {
		trace.Printf("ut100b %s: read the board information: %v", hardware.control.Name(), err)
	}
	if err := hardware.control.SetGPIO(0x07, 0xfb); err != nil {
		return err
	}
	if err := hardware.control.SetGPIOEx(0x00, 0x02); err != nil {
		return err
	}
	// Everything off, then the tuner's supply on, then the rest.
	if err := hardware.gpio(
		gpioStep{0x00, gpioPowerDown, 0},
		gpioStep{gpioTunerPower, gpioTunerPower, 50},
		gpioStep{gpioTunerPower, gpioTunerPower, 50},
		gpioStep{0x00, gpioTunerPower, 150},
		gpioStep{0xff, gpioCardReset, 50},
		gpioStep{0x00, gpioPowerUp, 50},
		gpioStep{0xff, gpioPowerUp, 100},
	); err != nil {
		return fmt.Errorf("power the board up: %w", err)
	}
	if err := hardware.tuner.reset(); err != nil {
		return err
	}
	if err := hardware.demod.WritePairs(demodInit); err != nil {
		return fmt.Errorf("initialize the demodulator: %w", err)
	}
	if err := hardware.demod.WriteRegisterMask(demodControl, demodControlOpen, demodControlMask); err != nil {
		return fmt.Errorf("initialize the demodulator: %w", err)
	}
	if err := hardware.tuner.load(); err != nil {
		return err
	}
	if err := hardware.gpio(
		gpioStep{0xff, gpioCardReset, 50},
		gpioStep{0x00, gpioCardReset, 50},
		gpioStep{0xff, gpioCardReset, 100},
	); err != nil {
		return fmt.Errorf("reset the card interface: %w", err)
	}
	address, identity, err := hardware.findCard()
	if err != nil {
		return err
	}
	// The low three bits of the identity read 6 on the IC whose transform is
	// the DES descramble.go undoes; the vendor driver selects another, which
	// this driver does not know, for anything else.
	if identity&0x07 != 0x06 {
		return fmt.Errorf("the card interface at %#02x reports identity %#02x, not the DES variant this driver undoes", address, identity)
	}
	hardware.cardAddress = address
	trace.Printf("ut100b %s: card interface at %#02x, identity %#02x", hardware.control.Name(), address, identity)
	hardware.wait(30)
	card := hardware.cardBus(address)
	if err := card.Write(cardTSMode, []byte{cardTSOutput}); err != nil {
		return fmt.Errorf("enable the card interface's TS output: %w", err)
	}
	// Then registers 0x10 to 0x1f are written with zeros, one at a time and as
	// ordinary I2C writes. These are the registers px3 sets through the
	// guarded write, and written this way they leave the transform's key where
	// it was; but without them nothing comes out of the IC at all.
	for register := byte(cardTSSetup); register < cardTSSetup+cardTSSetupLength; register++ {
		if err := card.Write(register, []byte{0}); err != nil {
			return fmt.Errorf("set the card interface's TS output up: %w", err)
		}
	}
	return nil
}

// findCard finds the card interface IC at the first address that answers.
func (hardware *hardware) findCard() (byte, byte, error) {
	var last error
	for _, address := range cardAddresses {
		identity, err := hardware.cardBus(address).Read(cardIdentity, 1)
		if err == nil && len(identity) == 1 {
			return address, identity[0], nil
		}
		last = err
	}
	return 0, 0, fmt.Errorf("no card interface answered at % #02x; the stream passes through it: %v", cardAddresses, last)
}

// powerDown parks the card interface, cuts the supplies and lets the bridge go,
// once the last tuner is closed.
func (hardware *hardware) powerDown() error {
	hardware.card.Lock()
	hardware.card.reader = nil
	hardware.card.Unlock()
	var failed []error
	card := hardware.cardBus(hardware.cardAddress)
	for _, register := range []byte{card80, cardStatus, cardCommand} {
		if err := card.Write(register, []byte{0}); err != nil {
			failed = append(failed, err)
		}
	}
	hardware.wait(50)
	if err := hardware.gpio(
		gpioStep{0x00, gpioPowerDown, 0},
		gpioStep{gpioTunerPower, gpioTunerPower, 50},
	); err != nil {
		failed = append(failed, err)
	}
	if err := hardware.control.Close(); err != nil {
		failed = append(failed, err)
	}
	return errors.Join(failed...)
}

// board is the boardkit handle with the card slot alongside it.
type board struct {
	device.Board
	hardware *hardware
}

var _ device.CardReader = (*board)(nil)

// cardReader brings the T=1 session up on first use. The caller holds
// hardware.card.
func (hardware *hardware) cardReader() (*asie5606.Reader, error) {
	if hardware.card.reader == nil {
		reader, err := asie5606.New(hardware.cardBus(hardware.cardAddress))
		if err != nil {
			return nil, err
		}
		hardware.card.reader = reader
	}
	return hardware.card.reader, nil
}

// CardStatus reports whether a card is seated in the slot.
func (board *board) CardStatus() (device.CardStatus, error) {
	board.hardware.card.Lock()
	defer board.hardware.card.Unlock()
	reader, err := board.hardware.cardReader()
	if err != nil {
		return device.CardStatus{}, err
	}
	present, err := reader.Present()
	if err != nil {
		return device.CardStatus{}, err
	}
	return device.CardStatus{Present: present, Reader: fmt.Sprintf("ASIE5606 rev %d", reader.Version())}, nil
}

// ResetCard powers the card up and returns its ATR.
func (board *board) ResetCard() ([]byte, error) {
	board.hardware.card.Lock()
	defer board.hardware.card.Unlock()
	reader, err := board.hardware.cardReader()
	if err != nil {
		return nil, err
	}
	return reader.Reset()
}

// TransmitAPDU sends one command APDU to the card and returns its answer.
func (board *board) TransmitAPDU(apdu []byte) ([]byte, error) {
	board.hardware.card.Lock()
	defer board.hardware.card.Unlock()
	reader, err := board.hardware.cardReader()
	if err != nil {
		return nil, err
	}
	return reader.Transmit(apdu)
}

// frontend is the board's one tuner.
type frontend struct {
	hardware *hardware
	tuned    bool
	packets  *packetizer
	// raw is where the bridge's reads land before they become packets.
	raw []byte
	// streamed and streamStarted are for the log.
	streamed      int64
	streamStarted time.Time
}

// Tune locks onto the channel described by the given channel-file properties.
func (front *frontend) Tune(properties map[string]string) error {
	if !Supports(properties["DELIVERY_SYSTEM"]) {
		return fmt.Errorf("the QRS-UT100B does not support %q", properties["DELIVERY_SYSTEM"])
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	mhz := uint32((tuning.FrequencyKHz(frequency) + 500) / 1000)
	front.tuned = false
	demod := front.hardware.demod
	if err := demod.WriteRegisterMask(demodControl, demodControlTune, demodControlMask); err != nil {
		return err
	}
	// The gain control is held while the tuner moves, as with the FC0012: the
	// same registers in the same order.
	err = demod.Tune(tc90522.TunerFC0012, func() error {
		if err := demod.WritePairs(demodBeforeTune); err != nil {
			return err
		}
		return front.hardware.tuner.tune(mhz)
	}, func() error {
		return demod.WritePairs(demodAfterTune)
	})
	if err != nil {
		trace.Printf("ut100b %s: tune %d MHz: %v", front.hardware.control.Name(), mhz, err)
		return err
	}
	trace.Printf("ut100b %s: tuned %d MHz", front.hardware.control.Name(), mhz)
	front.tuned = true
	return nil
}

// Status reports the demodulator lock state for the last tuned channel.
func (front *frontend) Status() (device.Status, error) {
	if !front.tuned {
		return device.Status{}, nil
	}
	locked, err := front.hardware.demod.LockedT()
	if err != nil || !locked {
		return device.Status{}, err
	}
	status := device.Status{Locked: true, Names: device.LockNames}
	if cnr, measured, err := front.hardware.demod.CNRT(); err == nil && measured {
		status.CNR = device.Measured(cnr)
	}
	return status, nil
}

// StartStream sets the channel filter up the way px3 does and starts the
// bridge's first channel.
func (front *frontend) StartStream() (err error) {
	bridge := front.hardware.control
	defer func() {
		if err != nil {
			_ = front.StopStream()
		}
	}()
	if err := bridge.ResetFilter(streamChannel, 1); err != nil {
		return err
	}
	for _, register := range []byte{filterExcludeL, filterExcludeH} {
		if err := bridge.SetFilterWord(streamChannel, register, nullPID); err != nil {
			return err
		}
	}
	if err := bridge.SetFilterInput(streamChannel, true); err != nil {
		return err
	}
	if err := bridge.SetFilterRun(streamChannel, true); err != nil {
		return err
	}
	if err := bridge.StartStream(streamChannel); err != nil {
		return err
	}
	front.packets.reset()
	front.streamed, front.streamStarted = 0, time.Now()
	trace.Printf("ut100b %s: stream started", bridge.Name())
	return nil
}

// StopStream unwinds StartStream, reporting the first failure but always
// finishing.
func (front *frontend) StopStream() error {
	bridge := front.hardware.control
	if !front.streamStarted.IsZero() {
		trace.Printf("ut100b %s: stream stopped after %d bytes in %.1fs, %d resyncs", bridge.Name(),
			front.streamed, time.Since(front.streamStarted).Seconds(), front.packets.resyncs)
		front.streamStarted = time.Time{}
	}
	err := bridge.SetFilterRun(streamChannel, false)
	if stop := bridge.StopStream(streamChannel); err == nil {
		err = stop
	}
	return err
}

// Read hands over whole deciphered packets.
func (front *frontend) Read(_ context.Context, buffer []byte) (int, error) {
	if moved := front.packets.take(buffer); moved > 0 {
		return moved, nil
	}
	if cap(front.raw) < len(buffer) {
		front.raw = make([]byte, len(buffer))
	}
	raw := front.raw[:len(buffer)]
	read, err := front.hardware.control.ReadStream(streamChannel, raw, 200)
	front.streamed += int64(read)
	front.packets.feed(raw[:read])
	moved := front.packets.take(buffer)
	if moved > 0 {
		return moved, nil
	}
	if err == nil {
		// Everything that came was dropped as stale or held back as part of a
		// packet; the caller tries again as after a timeout.
		return 0, device.ErrWouldBlock
	}
	return boardkit.WouldBlock(0, err)
}
