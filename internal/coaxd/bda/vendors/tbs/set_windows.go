// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import "github.com/Till0196/coax/internal/coaxd/win32"

// TBS's private interface on a BDA tuner.
//
// One property set carries everything: the demodulator mode the card is
// switched with, and the raw I2C transfer used to reach the demodulator and
// tuner chips. It is an IKsPropertySet on the tuner filter's input pin rather
// than on the filter, which is why reaching it costs a graph.

// Set is that set.
var Set = win32.GUID{
	Data1: 0xfaa8f3e5, Data2: 0x31d4, Data3: 0x4e41,
	Data4: [8]byte{0x88, 0xef, 0xd9, 0xeb, 0x71, 0x6f, 0x6e, 0xc9},
}

const (
	// PropDemodMode is the demodulator mode; its width depends on the card, which
	// is what Model carries.
	PropDemodMode = 0x3c

	// PropI2C carries one transfer; request and reply share a layout:
	//
	//	[0]           slave address, shifted
	//	[1..0x80]     bytes to write
	//	[0x81]        write length
	//	[0x82..0x101] bytes read back
	//	[0x102]       read length
	PropI2C = 0x19

	I2CStructBytes = 0x103
	I2CWriteAt     = 0x01
	I2CWriteLenAt  = 0x81
	I2CReadAt      = 0x82
	I2CReadLenAt   = 0x102

	// I2CMaxWrite and I2CMaxRead are what the structure has room for.
	I2CMaxWrite = I2CWriteLenAt - I2CWriteAt
	I2CMaxRead  = I2CReadLenAt - I2CReadAt

	// I2CMaxRegisterBytes is how much of a write the read path will send
	// before turning the bus around: the driver's engine encodes one or two
	// register bytes into its command word and has no room for more.
	I2CMaxRegisterBytes = 2

	// RepeaterRegister is SLVX 0x08, the demodulator's I2C repeater enable.
	RepeaterRegister = 0x08

	// PropLNB carries the supply on the aerial lead and DiSEqC, in a structure whose first
	// word says which of the two is being asked for. Setting a value is a read-modify-write
	// of the bridge's first GPIO register: bit 2 is the supply and is active low, bit 1 is
	// the voltage and is active high.
	//
	//	[0x00]        what this call is: LNBCommand or DiSEqCCommand
	//	[0x0c]        the supply setting, for LNBCommand
	//	[0x10..0x8f]  the message, for DiSEqCCommand
	//	[0x90]        its length
	PropLNB = 0x15

	LNBStructBytes = 0x94
	LNBCommandAt   = 0x00
	LNBValueAt     = 0x0c
	DiSEqCAt       = 0x10
	DiSEqCLengthAt = 0x90
)

// What the first word of the structure may say.
const (
	LNBCommand    = 0
	DiSEqCCommand = 1
)

// The supply settings the driver understands. The two voltages are the other way
// round from the order they are usually written in: 1 is the higher of them.
// That is the numbering the property uses, not a slip here.
const (
	LNBOff = 0
	LNB18V = 1
	LNB13V = 2
	LNBOn  = 3
)
