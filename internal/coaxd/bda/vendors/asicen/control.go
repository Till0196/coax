// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package asicen implements the PLEX BDA driver's private transport interface.
// Protocol reference: radi-sh/BDASpecial-PlexPX (AsicenPropset.h, PlexPX.cpp).
package asicen

import (
	"bytes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/bda/tuning"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
	"github.com/Till0196/coax/pkg/win32"
)

var Set = win32.GUID{Data1: 0x9e1781e1, Data2: 0x9cb1, Data3: 0x4407, Data4: [8]byte{0xbb, 0xce, 0x54, 0x26, 0xc8, 0xd0, 0x0a, 0x4b}}

const (
	propInit   = 9
	propSeed   = 51
	propFlags  = 12
	propRandom = 13
	propTSID   = 47
	readSize   = 188 * 1024
)

// Profile is the DVB-T-shaped request the driver uses even for satellite IF.
func Profile(p tuning.TuneProfile) tuning.TuneProfile {
	if p.Delivery == tuning.DeliverySatellite && !p.LNB.Known() && p.FrequencyKHz < 10_000_000 {
		// The driver wants BS/CS downlink kHz even though it publishes DVB-T.
		p.FrequencyKHz += 10_678_000
	}
	p.System = "DVBT"
	p.Delivery = tuning.DeliveryTerrestrial
	p.BandwidthMHz = 6
	p.Modulation = ""
	p.StreamID = -1
	return p
}

// Control belongs to one tuner filter and must not outlive that filter.
type Control struct {
	mutex            sync.Mutex
	closed           bool
	properties       vendors.Properties
	cardCommandLimit int
	// card is the card channel's cipher, made on first use (card.go).
	card cipher.Block
}

func Open(p vendors.Properties) (*Control, error) {
	c := &Control{properties: p}
	var b [4]byte
	n, e := p.Get(Set, propFlags, b[:])
	if e != nil {
		return nil, e
	}
	if n != 4 {
		return nil, fmt.Errorf("ASICEN flags: got %d bytes", n)
	}
	flags := binary.LittleEndian.Uint32(b[:])
	c.cardCommandLimit = cardLimit
	if flags&4 == 0 {
		c.cardCommandLimit = 0x3c
	}
	var key [32]byte
	copy(key[:16], []byte{0x61, 0xd8, 0x56, 0x3d, 0xc1, 0x15, 0x46, 0x68, 0xb2, 0xec, 0x6f, 0xa9, 0xed, 0x45, 0x33, 0x81})
	copy(key[16:], streamSetup[:])
	if flags&8 != 0 {
		return nil, fmt.Errorf("ASICEN transport requires an additional transformation (flags %#x)", flags)
	}
	if flags&0x80 != 0 {
		return nil, fmt.Errorf("ASICEN initialization variant %#x is unknown", flags)
	}
	if flags&1 != 0 {
		var r [16]byte
		n, e = p.Get(Set, propRandom, r[:])
		if e != nil {
			return nil, e
		}
		if n != 16 {
			return nil, fmt.Errorf("ASICEN random: got %d bytes", n)
		}
		var k [16]byte
		for i := range k {
			k[i] = key[i] ^ r[i]
		}
		last := lastRound(k)
		copy(key[:16], last[:])
	}
	if e = p.Set(Set, propInit, key[:]); e != nil {
		return nil, fmt.Errorf("initialize ASICEN transport: %w", e)
	}
	var seed [20]byte
	n, e = p.Get(Set, propSeed, seed[:])
	if e != nil {
		return nil, fmt.Errorf("ASICEN stream setup: %w", e)
	}
	if n != 20 || !bytes.Equal(seed[:16], transportSeed[:]) {
		return nil, fmt.Errorf("ASICEN stream setup: got %x (%d bytes)", seed[:16], n)
	}
	return c, nil
}
func (c *Control) Select(tsid int32) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return io.ErrClosedPipe
	}
	if tsid < 0 {
		return nil
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(tsid))
	return c.properties.Set(Set, propTSID, b[:])
}

// RestoreSync clears the driver's marker in each packet's sync byte. The IC
// emits the payload unchanged because initialization selected transportSeed.
func (c *Control) RestoreSync(data []byte) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return io.ErrClosedPipe
	}
	if len(data)%188 != 0 {
		return fmt.Errorf("ASICEN transport is not packet aligned: %d", len(data))
	}
	for i := 0; i < len(data); i += 188 {
		if data[i]&0x7f != 0x47 {
			return fmt.Errorf("ASICEN transport sync at %d: %#02x", i, data[i])
		}
		data[i] &= 0x7f
	}
	return nil
}

// Close waits for property exchanges before the graph releases their COM owner.
func (c *Control) Close() { c.mutex.Lock(); defer c.mutex.Unlock(); c.closed = true }

// propSignalLevel is the C/N of the tuned carrier in hundredths of a dB.
const propSignalLevel = 58

// SignalLevel reads the C/N of the tuned carrier in dB, and reports whether
// the driver has a measurement.
func (c *Control) SignalLevel() (float64, bool, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return 0, false, io.ErrClosedPipe
	}
	var b [4]byte
	n, err := c.properties.Get(Set, propSignalLevel, b[:])
	if err != nil {
		return 0, false, fmt.Errorf("ASICEN signal level: %w", err)
	}
	if n != uint32(len(b)) {
		return 0, false, fmt.Errorf("ASICEN signal level: got %d bytes", n)
	}
	level := binary.LittleEndian.Uint32(b[:])
	return float64(level) / 100, level != 0, nil
}
