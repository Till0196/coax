// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package dd

import (
	"fmt"
	"sync"
	"time"
)

// The card's command interface. Newer Digital Devices cards carry no demodulator
// this side can reach -- the MAX M4 registers no I2C bus at all -- and offer a
// mailbox in the register window instead: write a command, ring a bell, read a
// reply. The firmware does the tuning, so a tune is one command with a frequency
// in it.

// The mailbox, at its offsets in the card's register window.
const (
	mciControl     = 0x500 // the bell, and the busy and ready flags
	mciCommandBuf  = 0x600 // where a command is written
	mciResultBuf   = 0x680 // where the reply appears
	mciMessageSize = 0x80  // both buffers are this long
)

// The control register's bits.
const (
	mciStart   = 0x00000001 // set to run the command; the card clears it
	mciDoneIRQ = 0x00000002 // ask for an interrupt as well, which this does not
	mciReset   = 0x00008000
	mciReady   = 0x00010000 // the firmware is up and idle
)

// What the firmware answers with, in the reply's first byte.
const (
	mciStatusOK               = 0x00
	mciStatusUnsupported      = 0x80
	mciStatusPoweredDown      = 0xf0
	mciStatusDisabled         = 0xf9
	mciStatusBusy             = 0xfa
	mciStatusHardwareError    = 0xfb
	mciStatusInvalidParameter = 0xfc
	mciStatusRetry            = 0xfd
	mciStatusNotReady         = 0xfe
	mciStatusError            = 0xff
)

// The demodulator states a query command answers with, in the same byte as the
// status codes above: a command that does something answers with how it went, a
// command that asks something with where the demodulator is.
const (
	demodStopped    = 0
	demodWaitSignal = 2
	demodWaitTS     = 6
	demodTimeout    = 14
	demodLocked     = 15
)

func demodStateName(state byte) string {
	switch state {
	case demodStopped:
		return "stopped"
	case demodWaitSignal:
		return "waiting for a signal"
	case demodWaitTS:
		return "waiting for the transport stream"
	case demodTimeout:
		return "timed out"
	case demodLocked:
		return "locked"
	}
	return fmt.Sprintf("state %d", state)
}

func mciStatusName(status byte) string {
	switch status {
	case mciStatusOK:
		return "ok"
	case mciStatusUnsupported:
		return "unsupported"
	case mciStatusPoweredDown:
		return "powered down"
	case mciStatusDisabled:
		return "disabled"
	case mciStatusBusy:
		return "busy"
	case mciStatusHardwareError:
		return "hardware error"
	case mciStatusInvalidParameter:
		return "invalid parameter"
	case mciStatusRetry:
		return "retry"
	case mciStatusNotReady:
		return "not ready"
	case mciStatusError:
		return "error"
	}
	return fmt.Sprintf("status %#02x", status)
}

// The commands this backend uses; the firmware carries more.
const (
	cmdStop           = 0x01
	cmdSetInputConfig = 0x05
	cmdGetStatus      = 0x02
	cmdGetSignalInfo  = 0x03
	cmdSearchDVBS     = 0x10
	cmdSearchISDBS    = 0x11
	cmdSearchDVBC     = 0x20
	cmdSearchDVBT     = 0x21
	cmdSearchDVBT2    = 0x22
	cmdSearchDVBC2    = 0x23
	cmdSearchISDBT    = 0x24
	cmdSearchISDBC    = 0x25
	cmdSearchJ83B     = 0x26
)

// The bandwidth codes a search takes: the channel width in whole megahertz,
// which reads like an enumeration and is not one. Zero means the firmware was
// not told, and a search that is not told times out rather than guessing.
const (
	bandwidthUnknown = 0
	bandwidth1_7MHz  = 1
	bandwidth5MHz    = 5
	bandwidth6MHz    = 6
	bandwidth7MHz    = 7
	bandwidth8MHz    = 8
	bandwidth10MHz   = 10
	// bandwidthExtension is set alongside a width for J.83B as Japan carries
	// it, which is the one place it applies.
	bandwidthExtension = 0x80
)

// mciCommand is one message to the firmware: four bytes naming the command and
// which tuner, demodulator and output it is for, then 124 bytes of parameters
// laid out per command.
type mciCommand struct {
	command byte
	tuner   byte
	demod   byte
	output  byte
	params  [mciMessageSize - 4]byte
}

func (command *mciCommand) bytes() []byte {
	message := make([]byte, mciMessageSize)
	message[0] = command.command
	message[1] = command.tuner
	message[2] = command.demod
	message[3] = command.output
	copy(message[4:], command.params[:])
	return message
}

// putUint32 writes a little-endian word at an offset into the parameter area.
func (command *mciCommand) putUint32(offset int, value uint32) {
	copy(command.params[offset:], encodeUint32(value))
}

func (command *mciCommand) putUint16(offset int, value uint16) {
	command.params[offset] = byte(value)
	command.params[offset+1] = byte(value >> 8)
}

type mciResult struct {
	status byte
	mode   byte
	time   uint16
	// payload is everything after the four-byte header, laid out per command.
	payload []byte
}

func (result mciResult) err(what string) error {
	if result.status == mciStatusOK {
		return nil
	}
	return fmt.Errorf("%s: the card answered %s", what, mciStatusName(result.status))
}

// mciLocks serialises the one mailbox each card has: two tunes at once would
// interleave a command with another's reply. Keyed by the card rather than the
// handle, because each tuner opens its own.
var (
	mciMutex sync.Mutex
	mciLocks = map[string]*sync.Mutex{}
)

func mciLockFor(card string) *sync.Mutex {
	mciMutex.Lock()
	defer mciMutex.Unlock()
	lock, ok := mciLocks[card]
	if !ok {
		lock = &sync.Mutex{}
		mciLocks[card] = lock
	}
	return lock
}

// mciTimeout only has to cover the mailbox itself: even a search is answered
// immediately rather than held until the demodulator locks.
const (
	mciTimeout = 3 * time.Second
	mciPoll    = 2 * time.Millisecond
)

// waitIdle waits for the mailbox to come free and reports the control register
// as it was then. mciLocks only keeps this backend's own commands apart: the
// vendor's driver owns the same mailbox and uses it whenever a graph is built
// or a filter tuned, so finding it busy is ordinary rather than an error.
func (device *bridge) waitIdle() (uint32, error) {
	deadline := time.Now().Add(mciTimeout)
	for {
		control, err := device.readRegister(mciControl)
		if err != nil {
			return 0, err
		}
		if control&mciReady == 0 {
			return 0, fmt.Errorf("the card's firmware is not ready (control %#08x)", control)
		}
		if control&(mciStart|mciReset) == 0 {
			return control, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("the card's command interface stayed busy for %s (control %#08x)",
				mciTimeout, control)
		}
		time.Sleep(mciPoll)
	}
}

func (device *bridge) execute(card string, command *mciCommand) (mciResult, error) {
	lock := mciLockFor(card)
	lock.Lock()
	defer lock.Unlock()

	control, err := device.waitIdle()
	if err != nil {
		return mciResult{}, err
	}
	if err := device.writeBytes(mciCommandBuf, command.bytes()); err != nil {
		return mciResult{}, err
	}
	// Only the start bit: the done interrupt belongs to the vendor's driver.
	if err := device.writeRegister(mciControl, control&^mciDoneIRQ|mciStart); err != nil {
		return mciResult{}, err
	}
	deadline := time.Now().Add(mciTimeout)
	for {
		control, err = device.readRegister(mciControl)
		if err != nil {
			return mciResult{}, err
		}
		if control&mciStart == 0 {
			break
		}
		if time.Now().After(deadline) {
			return mciResult{}, fmt.Errorf("the card did not answer command %#02x within %s",
				command.command, mciTimeout)
		}
		time.Sleep(mciPoll)
	}
	reply, err := device.readBytes(mciResultBuf, mciMessageSize)
	if err != nil {
		return mciResult{}, err
	}
	return mciResult{
		status:  reply[0],
		mode:    reply[1],
		time:    uint16(reply[2]) | uint16(reply[3])<<8,
		payload: reply[4:],
	}, nil
}

// signalInfo is the part of a reply every standard shares. Offsets are into the
// payload, which begins with the frequency the card was last given.
type signalInfo struct {
	frequencyHz uint32
	symbolRate  uint32
	// powerDBm is the channel power; the firmware reports hundredths of a dBm.
	powerDBm float64
	// cnrDB is the signal to noise ratio in dB, signed: DVB-S2 locks below zero.
	cnrDB        float64
	packetErrors uint32
}

func parseSignalInfo(payload []byte) (signalInfo, bool) {
	if len(payload) < 28 {
		return signalInfo{}, false
	}
	power := int16(uint16(payload[12]) | uint16(payload[13])<<8)
	cnr := int16(uint16(payload[16]) | uint16(payload[17])<<8)
	return signalInfo{
		frequencyHz:  decodeUint32(payload[4:]),
		symbolRate:   decodeUint32(payload[8:]),
		powerDBm:     float64(power) / 100,
		cnrDB:        float64(cnr) / 100,
		packetErrors: decodeUint32(payload[24:]),
	}, true
}

// The card's input configuration. A MAX M4 has five aerial sockets: one per
// tuner for satellite, and a fifth feeding all four with terrestrial or cable.
// Nothing reaches the fifth until the card is told to route it -- value 3, a
// card-wide setting sent once before any tune.
//
// Only the M4 and M8 have the shared socket, which is why the value lives in
// the model table. On an M2 the same command picks which of two modes the
// demodulator pair runs in, already chosen by the vendor's driver. Those models
// say inputConfigNone.
const (
	demodAll          = 0xff
	inputConfigShared = 3
	// inputConfigNone marks a model this command is not for.
	inputConfigNone = -1
)

var (
	inputConfigMutex sync.Mutex
	inputConfigDone  = map[string]bool{}
)

// ensureInputConfig routes the shared aerial socket, once per card. MAX M4
// firmware 1.2 answers "unsupported" and receives from the shared socket anyway,
// so a refusal is remembered and dropped; only a failure to reach the card is
// passed on.
func (device *bridge) ensureInputConfig(card string, value int) error {
	if value == inputConfigNone {
		return nil
	}
	inputConfigMutex.Lock()
	done := inputConfigDone[card]
	inputConfigMutex.Unlock()
	if done {
		return nil
	}
	command := &mciCommand{command: cmdSetInputConfig, demod: demodAll}
	command.params[0] = byte(value)
	if _, err := device.execute(card, command); err != nil {
		return err
	}
	inputConfigMutex.Lock()
	inputConfigDone[card] = true
	inputConfigMutex.Unlock()
	return nil
}
