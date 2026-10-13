// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package it930x

import (
	"fmt"
	"time"
)

// The card slot hangs off the bridge's UART: four registers and four commands
// drive it. Framing, block numbers and the answer to reset are the card/t1
// package's, because the bridge only moves bytes.
const (
	// registerUARTCommit is written just before the last chunk of a write, to
	// tell the firmware the message is complete.
	registerUARTCommit = 0x4965
	// registerUARTReady is non-zero once received bytes are waiting. It is the
	// only sound test for that: registerUARTLength climbs while a message is
	// still arriving, so a non-zero length does not mean a complete one.
	registerUARTReady = 0x496a
	// registerUARTLength counts the bytes waiting.
	registerUARTLength = 0x496b
	// registerUARTReset clears the receiver's state. Writing 2 is what the
	// reset sequence sends between dropping the card's reset line and letting
	// it go again.
	registerUARTReset = 0x7904
)

const (
	commandUARTRead        = 0x0033
	commandUARTWrite       = 0x0034
	commandUARTSetBaudrate = 0x0035
	commandUARTSetMode     = 0x0037
)

// The GPIO pins the slot uses. Detection is low-active: the pin is pulled down
// when a card is seated.
const (
	gpioCardDetect = 6
	gpioCardReset  = 14
)

// Transfer limits the firmware imposes on the UART commands.
const (
	uartReadChunk  = 32
	uartWriteChunk = 48
)

// cardResetSettle is how long the card is held in reset. It is the only delay
// in the whole card path.
const cardResetSettle = 5 * time.Millisecond

// CardLine is the bridge's UART as the byte pipe the T=1 layer drives.
type CardLine struct {
	bridge *Bridge
}

// OpenCardLine switches the UART into card mode and prepares the detect pin here
// rather than on first use, presence being asked for far more often than
// anything else.
func (bridge *Bridge) OpenCardLine() (*CardLine, error) {
	if err := bridge.uartCommand(commandUARTSetMode, []byte{0x01}); err != nil {
		return nil, fmt.Errorf("switch the IT930x UART into card mode: %w", err)
	}
	if err := bridge.SetGPIOMode(gpioCardDetect, GPIOIn, true); err != nil {
		return nil, fmt.Errorf("prepare the card detect pin: %w", err)
	}
	return &CardLine{bridge: bridge}, nil
}

func (bridge *Bridge) uartCommand(code uint16, payload []byte) error {
	_, err := bridge.command(code, payload, 0)
	return err
}

// Present reports whether a card is seated. The pin reads low when it is.
func (line *CardLine) Present() (bool, error) {
	high, err := line.bridge.ReadGPIO(gpioCardDetect)
	if err != nil {
		return false, err
	}
	return !high, nil
}

// Reset power-cycles the card, leaving it about to send its answer to reset.
//
// The order matters: the card is held in reset, the receiver's leftovers are
// cleared, the line is put back to the speed every card starts at, and only then
// is the card let go, so the answer arrives on a clean line at a known speed.
func (line *CardLine) Reset() error {
	if err := line.bridge.SetGPIOMode(gpioCardReset, GPIOOut, true); err != nil {
		return fmt.Errorf("prepare the card reset pin: %w", err)
	}
	if err := line.bridge.WriteGPIO(gpioCardReset, false); err != nil {
		return err
	}
	if err := line.bridge.WriteRegister(registerUARTReset, 2); err != nil {
		return err
	}
	if err := line.SetBaudrate(9600); err != nil {
		return err
	}
	time.Sleep(cardResetSettle)
	return line.bridge.WriteGPIO(gpioCardReset, true)
}

// SetBaudrate switches the line speed.
//
// The firmware numbers the first two speeds 0 and 1. Its third step is 38400 on
// the generic UART but not in card mode, where asking for it quietly drops the
// line to about 9600; 38400 is reached there by writing the reload value the
// generic UART would have used instead. That last part is not something the
// numbering shows and was found by trying it.
func (line *CardLine) SetBaudrate(rate int) error {
	var value byte
	switch rate {
	case 9600:
		value = 0x00
	case 19200:
		value = 0x01
	case 38400:
		value = 0xef
	default:
		return fmt.Errorf("IT930x: %d is not a card line speed", rate)
	}
	if err := line.bridge.uartCommand(commandUARTSetBaudrate, []byte{value}); err != nil {
		return fmt.Errorf("set the card line to %d baud: %w", rate, err)
	}
	return nil
}

// Ready reports whether any received bytes are waiting.
func (line *CardLine) Ready() (bool, error) {
	value, err := line.bridge.ReadRegister(registerUARTReady)
	if err != nil {
		return false, err
	}
	return value != 0, nil
}

// Read takes up to len(buffer) bytes that have already arrived, and does not
// wait for more. Returning fewer than asked for is normal.
func (line *CardLine) Read(buffer []byte) (int, error) {
	total := 0
	for total < len(buffer) {
		available, err := line.bridge.ReadRegister(registerUARTLength)
		if err != nil {
			return total, err
		}
		if available == 0 {
			break
		}
		want := min(int(available), uartReadChunk, len(buffer)-total)
		data, err := line.bridge.command(commandUARTRead, []byte{byte(want)}, want)
		if err != nil {
			return total, fmt.Errorf("read from the card line: %w", err)
		}
		if len(data) != want {
			return total, fmt.Errorf("read from the card line: %d bytes arrived, want %d", len(data), want)
		}
		total += copy(buffer[total:], data)
	}
	return total, nil
}

// Write sends bytes to the card. The firmware is told the message is complete
// just before the last chunk goes out, so a message split across transfers is
// not acted on until all of it has arrived.
func (line *CardLine) Write(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if len(data) > 255 {
		return fmt.Errorf("a %d-byte message is over the 255 the card line carries", len(data))
	}
	for offset := 0; offset < len(data); offset += uartWriteChunk {
		end := min(offset+uartWriteChunk, len(data))
		if end == len(data) {
			if err := line.bridge.WriteRegister(registerUARTCommit, 1); err != nil {
				return err
			}
		}
		payload := append([]byte{byte(end - offset)}, data[offset:end]...)
		if err := line.bridge.uartCommand(commandUARTWrite, payload); err != nil {
			return fmt.Errorf("write to the card line: %w", err)
		}
	}
	return nil
}
