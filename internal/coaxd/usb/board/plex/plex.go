// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Common scaffolding for the px4_drv boards, distilled from px4_device.c,
// pxmlt_device.c, isdb2056_device.c, m1ur_device.c and s1ur_device.c by nns779.

// Package plex holds what every PLEX IT930x board does the same way: bring the
// bridge up, load its firmware, gate the tuner-board supply and the LNB supply
// behind reference counts, and split the shared stream endpoint per tuner. The
// board packages beside it supply the parts that differ: which demodulators and
// tuners are fitted, and how a channel is acquired.
package plex

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/boardkit"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/it930x"
	"github.com/Till0196/coax/internal/coaxd/usb/tsdemux"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// firmwareFile is the IT930x blob every board in this family needs.
const firmwareFile = "it930x-firmware.bin"

// Config describes a board to the shared scaffolding.
type Config struct {
	// IDs are the USB vendor and product pairs the board enumerates as.
	IDs []usbio.ID
	// Inputs configures the bridge's TS input ports.
	Inputs [it930x.Inputs]it930x.Input
	// Tuners is how many tuners feed the stream endpoint.
	Tuners int
	// Tag identifies the owning tuner of each packet. Boards with one tuner
	// leave the sync byte alone and use tsdemux.PlainSync.
	Tag tsdemux.Tag
	// XferSize is the bridge's stream transfer threshold in bytes.
	XferSize uint32
	// PowerGPIO and PowerEnableGPIO gate the tuner board's supply: the first is
	// held low and the second high while the tuners run.
	PowerGPIO       int
	PowerEnableGPIO int
	// LNBGPIO gates the satellite LNB supply, or zero on boards without one.
	LNBGPIO int
	// PowerOn runs once the supply is up, to initialise the tuner ICs.
	PowerOn func() error
	// PowerOff runs before the supply goes down.
	PowerOff func() error
}

// Frame is an opened board: the bridge plus the shared resources its tuners
// contend for.
type Frame struct {
	Bridge *it930x.Bridge
	// Owner keeps the USB device alive while any tuner is open.
	Owner *device.Owner
	// Power counts the tuners holding the analogue supply up.
	Power *device.Latch
	// LNB counts the tuners asking for satellite power. It is nil on boards
	// without an LNB supply.
	LNB *device.Latch
	// Demux splits the shared stream endpoint per tuner.
	Demux *tsdemux.Demux

	// card is the enclosure's smart card reader, brought up on first use.
	card cardSlot

	config Config
}

// Open claims the board, brings the bridge up and leaves the tuner supply off.
func Open(options device.Options, config Config) (*Frame, error) {
	if config.Tuners <= 0 || config.Tag == nil {
		return nil, errors.New("plex: Tuners and Tag are required")
	}
	if config.XferSize == 0 {
		// 816 packets is the default transfer size.
		config.XferSize = 188 * 816
	}
	transport, err := usbio.Open(options.Selector, config.IDs)
	if err != nil {
		return nil, err
	}
	fail := func(operation string, cause error) (*Frame, error) {
		_ = transport.Close()
		return nil, fmt.Errorf("%s: %w", operation, cause)
	}

	bridge := it930x.New(transport, it930x.Config{
		XferSize:    config.XferSize,
		I2CSpeed:    0x07,
		MaxBulkSize: 512,
		Inputs:      config.Inputs,
	})
	if err := bridge.Raise(); err != nil {
		return fail("wake IT930x", err)
	}
	// A board whose configuration EEPROM is silent cannot be trusted to match
	// the tuner layout compiled in here.
	eeprom, err := bridge.ReadRegister(0x4979)
	if err != nil {
		return fail("read the board's EEPROM", err)
	}
	if eeprom == 0 {
		return fail("read the board's EEPROM", errors.New("no response"))
	}
	firmware, err := device.FindFirmware(options.FirmwareDirOrDefault(), firmwareFile)
	if err != nil {
		return fail("load IT930x firmware", err)
	}
	if err := bridge.LoadFirmware(firmware); err != nil {
		return fail("load IT930x firmware", err)
	}
	if err := bridge.InitWarm(); err != nil {
		return fail("configure IT930x", err)
	}

	pins := []int{config.PowerGPIO, config.PowerEnableGPIO}
	if config.LNBGPIO != 0 {
		pins = append(pins, config.LNBGPIO)
	}
	for _, pin := range pins {
		if err := bridge.SetGPIOMode(pin, it930x.GPIOOut, true); err != nil {
			return fail("configure IT930x GPIO", err)
		}
	}
	frame := &Frame{Bridge: bridge, config: config}
	// InitWarm leaves the tuner board powered; start from a known-off state.
	if err := frame.supply(false); err != nil {
		return fail("power the tuners down", err)
	}
	if config.LNBGPIO != 0 {
		if err := bridge.WriteGPIO(config.LNBGPIO, false); err != nil {
			return fail("switch LNB power off", err)
		}
		frame.LNB = device.NewLatch(
			func() error { return bridge.WriteGPIO(config.LNBGPIO, true) },
			func() error { return bridge.WriteGPIO(config.LNBGPIO, false) },
		)
	}

	frame.Power = device.NewLatch(frame.powerUp, frame.powerDown)
	demux, err := tsdemux.New(tsdemux.Config{
		Read: bridge.ReadStream,
		Start: func() error {
			if err := bridge.PurgePSB(2000); err != nil {
				return err
			}
			return bridge.StartReading()
		},
		Stop:     bridge.StopReading,
		Tag:      config.Tag,
		Tuners:   config.Tuners,
		ReadSize: int(config.XferSize),
	})
	if err != nil {
		return fail("prepare stream demultiplexer", err)
	}
	frame.Demux = demux
	frame.Owner = device.NewOwner(func() error {
		frame.releaseCard()
		if config.LNBGPIO != 0 {
			_ = bridge.WriteGPIO(config.LNBGPIO, false)
		}
		_ = frame.supply(false)
		return bridge.Close()
	})
	return frame, nil
}

// supply drives the two pins that gate the tuner board's analogue supply.
func (frame *Frame) supply(on bool) error {
	if on {
		if err := frame.Bridge.WriteGPIO(frame.config.PowerGPIO, false); err != nil {
			return err
		}
		time.Sleep(80 * time.Millisecond)
		if err := frame.Bridge.WriteGPIO(frame.config.PowerEnableGPIO, true); err != nil {
			return err
		}
		time.Sleep(20 * time.Millisecond)
		return nil
	}
	_ = frame.Bridge.WriteGPIO(frame.config.PowerEnableGPIO, false)
	return frame.Bridge.WriteGPIO(frame.config.PowerGPIO, true)
}

func (frame *Frame) powerUp() error {
	if err := frame.supply(true); err != nil {
		return err
	}
	if frame.config.PowerOn == nil {
		return nil
	}
	if err := frame.config.PowerOn(); err != nil {
		_ = frame.supply(false)
		return err
	}
	return nil
}

func (frame *Frame) powerDown() error {
	if frame.config.PowerOff != nil {
		_ = frame.config.PowerOff()
	}
	return frame.supply(false)
}

// Name reports the USB bus:address the board was found at.
func (frame *Frame) Name() string { return frame.Bridge.Name() }

// Close releases the frame's own reference to the USB device.
func (frame *Frame) Close() error { return frame.Owner.Release() }

// Tuner is the per-tuner half of an IT930x board driver: the part that depends
// on which demodulator and tuner IC the product fits. The frame owns the stream
// itself, so starting one here means no more than switching the demodulator's
// TS output on.
type Tuner interface {
	// Tune acquires the channel described by the channel-file properties.
	Tune(properties map[string]string) error
	// Status reports the demodulator lock state for the last tuned channel.
	Status() (device.Status, error)
	// EnableTS switches this tuner's demodulator TS output on or off.
	EnableTS(enable bool) error
	// Park powers the tuner's silicon down when the tuner is handed back.
	Park() error
}

// Board wraps a frame and its tuners into the device.Board contract: hold the
// analogue supply up while a tuner is open, take that tuner's share of the
// shared endpoint, and let go of both when it closes.
func (frame *Frame) Board(model string, tuners []device.TunerInfo, open func(index int) (Tuner, error)) (device.Board, error) {
	return boardkit.New(boardkit.Config{
		Model:    model,
		Location: frame.Name(),
		Tuners:   tuners,
		Owner:    frame.Owner,
		Open: func(index int) (boardkit.Tuner, error) {
			if err := frame.Power.Acquire(); err != nil {
				return nil, fmt.Errorf("power up the tuner: %w", err)
			}
			inner, err := open(index)
			if err != nil {
				_ = frame.Power.Release()
				return nil, err
			}
			return &tuner{frame: frame, index: index, inner: inner}, nil
		},
	})
}

// GainLadder and SetGain pass the setting through to the board's own tuner,
// since the frame knows nothing about what silicon is behind it. An empty
// ladder is what the kit refuses the property on.
func (tuner *tuner) GainLadder(deliverySystem string) tuning.GainLadder {
	amplifier, ok := tuner.inner.(boardkit.Amplifier)
	if !ok {
		return tuning.GainLadder{}
	}
	return amplifier.GainLadder(deliverySystem)
}

func (tuner *tuner) SetGain(step int) error {
	amplifier, ok := tuner.inner.(boardkit.Amplifier)
	if !ok {
		return fmt.Errorf("this tuner has no gain setting to change")
	}
	return amplifier.SetGain(step)
}

// tuner adapts a board's Tuner to the boardkit contract.
type tuner struct {
	frame *Frame
	index int
	inner Tuner
	// reader is this tuner's share of the shared endpoint while it streams.
	reader *tsdemux.Reader
}

func (tuner *tuner) Tune(properties map[string]string) error { return tuner.inner.Tune(properties) }

func (tuner *tuner) Status() (device.Status, error) { return tuner.inner.Status() }

// StartStream opens the demodulator's TS output and claims the tuner's share of
// the endpoint.
func (tuner *tuner) StartStream() error {
	if err := tuner.inner.EnableTS(true); err != nil {
		return err
	}
	reader, err := tuner.frame.Demux.Open(tuner.index)
	if err != nil {
		_ = tuner.inner.EnableTS(false)
		return err
	}
	tuner.reader = reader
	return nil
}

// StopStream gives the share back and switches the TS output off again,
// reporting the first failure but always finishing the sequence.
func (tuner *tuner) StopStream() error {
	var err error
	if tuner.reader != nil {
		err = tuner.reader.Close()
		tuner.reader = nil
	}
	if disable := tuner.inner.EnableTS(false); err == nil {
		err = disable
	}
	return err
}

// Read takes the next packets the demultiplexer sorted into this tuner's share.
func (tuner *tuner) Read(ctx context.Context, buffer []byte) (int, error) {
	if tuner.reader == nil {
		return 0, device.ErrWouldBlock
	}
	return tuner.reader.Read(ctx, buffer)
}

// Dropped reports the packets the demultiplexer discarded for this tuner
// because nothing was reading them fast enough. It belongs to the reader, so
// each stream starts again at zero.
func (tuner *tuner) Dropped() uint64 {
	if tuner.reader == nil {
		return 0
	}
	return tuner.reader.Dropped()
}

// Release parks the tuner and lets the analogue supply drop if this was the
// last tuner holding it up.
func (tuner *tuner) Release() error {
	_ = tuner.inner.Park()
	return tuner.frame.Power.Release()
}

// LNBClaim is one tuner's claim on a frame's shared LNB supply. The supply is
// reference counted so the other satellite tuners are not cut off, and a claim
// records whether it is already held so a retune does not count twice.
type LNBClaim struct {
	frame *Frame
	held  bool
}

// NewLNBClaim returns a claim on this frame's LNB supply, not yet held.
func (frame *Frame) NewLNBClaim() LNBClaim { return LNBClaim{frame: frame} }

// Acquire switches the LNB supply on for this tuner. A claim already held is
// left alone.
func (claim *LNBClaim) Acquire() error {
	if claim.held {
		return nil
	}
	if err := claim.frame.LNB.Acquire(); err != nil {
		return fmt.Errorf("switch LNB power on: %w", err)
	}
	claim.held = true
	return nil
}

// Want takes or drops this tuner's claim to match what the channel asked for. A
// channel that does not want the supply is honoured by dropping the claim
// rather than switching the shared line off under another tuner, so an
// installation with its own power inserter never raises the board's supply at
// all.
func (claim *LNBClaim) Want(supply bool) error {
	if supply {
		return claim.Acquire()
	}
	if !claim.held {
		return nil
	}
	claim.held = false
	if err := claim.frame.LNB.Release(); err != nil {
		return fmt.Errorf("switch LNB power off: %w", err)
	}
	return nil
}

// Release drops the claim on the way out of a tuner's Park, keeping err as the
// primary failure the way Park reports it.
func (claim *LNBClaim) Release(err error) error {
	if !claim.held {
		return err
	}
	claim.held = false
	if release := claim.frame.LNB.Release(); err == nil {
		err = release
	}
	return err
}

// InputSpec is one enabled TS input of a board.
type InputSpec struct {
	// Port is the bridge's internal port number for this input.
	Port byte
	// Bus is the bridge I2C master the demodulator sits on, 1 to 3.
	Bus byte
	// Address is the demodulator's 7-bit I2C address.
	Address byte
}

// Inputs builds the bridge's input configuration. Enabled inputs get serial TS
// and 188-byte packets; when tagged, each also gets a sync byte carrying its
// number so one endpoint can carry them all. unusedPorts names the inputs the
// board does not fit, which still have to be named so they can be switched off.
func Inputs(specs []InputSpec, tagged bool, unusedPorts []byte) [it930x.Inputs]it930x.Input {
	var inputs [it930x.Inputs]it930x.Input
	for index, spec := range specs {
		if index >= it930x.Inputs {
			break
		}
		syncByte := byte(0x47)
		if tagged {
			syncByte = byte((index+1)<<4) | 0x07
		}
		inputs[index] = it930x.Input{
			Enable:       true,
			IsParallel:   false,
			PortNumber:   spec.Port,
			SlaveNumber:  byte(index),
			I2CBus:       spec.Bus,
			I2CAddr:      spec.Address,
			PacketLength: 188,
			SyncByte:     syncByte,
		}
	}
	for offset, port := range unusedPorts {
		index := len(specs) + offset
		if index >= it930x.Inputs {
			break
		}
		inputs[index] = it930x.Input{Enable: false, PortNumber: port}
	}
	return inputs
}

// WaitForLock polls a synthesiser's lock flag for half a second.
func WaitForLock(locked func() (bool, error)) error {
	var err error
	for attempt := 0; attempt < 50; attempt++ {
		var lock bool
		if lock, err = locked(); err == nil && lock {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	return errors.New("did not lock")
}
