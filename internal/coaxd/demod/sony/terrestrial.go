// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package sony

import (
	"errors"
	"fmt"
)

// IFFrequencyRegisters converts an IF to the three bytes the chip's IF register
// takes: SONY_DEMOD_MAKE_IFFREQ_CONFIG, the frequency as a fraction of a fixed
// 48 MHz sampling clock in 24-bit fixed point, rounded to nearest. The
// CXD2878ER does not vary this with the crystal, and it rounds rather than
// truncating -- where it differs from the CXD2856ER.
func IFFrequencyRegisters(mhz float64) [3]byte {
	value := uint32(mhz/48.0*16777216.0 + 0.5)
	return [3]byte{byte(value >> 16), byte(value >> 8), byte(value)}
}

// ATSCIFFrequencyRegister is SONY_DEMOD_ATSC_MAKE_IFFREQ_CONFIG: the ATSC
// core's IF register is 32-bit against a 24 MHz reference instead.
func ATSCIFFrequencyRegister(mhz float64) uint32 {
	return uint32(mhz/24.0*4294967296.0 + 0.5)
}

// terrestrialBand holds the per-bandwidth filter settings the
// SLtoAT/SLtoAT2/SLtoAIT band-setting sequences write. Each system has its own
// table because the coefficients differ even where the bandwidth matches.
type terrestrialBand struct {
	nominalRate  []byte
	itbCoef      []byte
	ifMHz        float64
	channelWidth byte
	trl          []byte // register 0xD9, the timing recovery loop setting
}

var dvbtBands = map[uint32]terrestrialBand{
	8: {[]byte{0x15, 0, 0, 0, 0}, []byte{0x2F, 0xBA, 0x28, 0x9B, 0x28, 0x9D, 0x28, 0xA1, 0x29, 0xA5, 0x2A, 0xAC, 0x29, 0xB5}, 4.8, 0x00, []byte{0x15, 0x28}},
	7: {[]byte{0x18, 0, 0, 0, 0}, []byte{0x30, 0xB1, 0x29, 0x9A, 0x28, 0x9C, 0x28, 0xA0, 0x29, 0xA2, 0x2B, 0xA6, 0x2B, 0xAD}, 4.2, 0x02, []byte{0x1F, 0xF8}},
	6: {[]byte{0x1C, 0, 0, 0, 0}, []byte{0x31, 0xA8, 0x29, 0x9B, 0x27, 0x9C, 0x28, 0x9E, 0x29, 0xA4, 0x29, 0xA2, 0x29, 0xA8}, 3.6, 0x04, []byte{0x25, 0x4C}},
	5: {[]byte{0x21, 0x99, 0x99, 0x99, 0x99}, []byte{0x31, 0xA8, 0x29, 0x9B, 0x27, 0x9C, 0x28, 0x9E, 0x29, 0xA4, 0x29, 0xA2, 0x29, 0xA8}, 3.6, 0x06, []byte{0x2C, 0xC2}},
}

var dvbt2Bands = map[uint32]terrestrialBand{
	8: {[]byte{0x15, 0, 0, 0, 0}, []byte{0x2F, 0xBA, 0x28, 0x9B, 0x28, 0x9D, 0x28, 0xA1, 0x29, 0xA5, 0x2A, 0xAC, 0x29, 0xB5}, 4.8, 0x00, nil},
	7: {[]byte{0x18, 0, 0, 0, 0}, []byte{0x30, 0xB1, 0x29, 0x9A, 0x28, 0x9C, 0x28, 0xA0, 0x29, 0xA2, 0x2B, 0xA6, 0x2B, 0xAD}, 4.2, 0x02, nil},
	6: {[]byte{0x1C, 0, 0, 0, 0}, []byte{0x31, 0xA8, 0x29, 0x9B, 0x27, 0x9C, 0x28, 0x9E, 0x29, 0xA4, 0x29, 0xA2, 0x29, 0xA8}, 3.6, 0x04, nil},
	5: {[]byte{0x21, 0x99, 0x99, 0x99, 0x99}, []byte{0x31, 0xA8, 0x29, 0x9B, 0x27, 0x9C, 0x28, 0x9E, 0x29, 0xA4, 0x29, 0xA2, 0x29, 0xA8}, 3.6, 0x06, nil},
	// 1.7 MHz has no ITB coefficient table: register 0xA5 (auto-search
	// enable) is written 0 instead.
}

var dvbt2Bandwidth1_7 = terrestrialBand{nominalRate: []byte{0x1A, 0x03, 0xE8, 0x8C, 0xB3}, ifMHz: 3.5, channelWidth: 0x03}

// isdbtBands carries two settings that are easy to get wrong. Channel-width
// register 0xD7 is bandwidth-dependent -- 0x00 for 8 MHz, 0x02 for 7 MHz, 0x04
// for 6 MHz -- and a literal 0x00 everywhere would demodulate every 6 and 7 MHz
// channel as 8 MHz wide. The 8 MHz demodulator-latency value (register 0xD9) is
// 0x13,0xFC, not 7 MHz's 0x1A,0xFA.
var isdbtBands = map[uint32]terrestrialBand{
	8: {[]byte{0x11, 0xB8, 0, 0, 0}, []byte{0x2F, 0xBA, 0x28, 0x9B, 0x28, 0x9D, 0x28, 0xA1, 0x29, 0xA5, 0x2A, 0xAC, 0x29, 0xB5}, 4.75, 0x00, []byte{0x13, 0xFC}},
	7: {[]byte{0x14, 0x40, 0, 0, 0}, []byte{0x30, 0xB1, 0x29, 0x9A, 0x28, 0x9C, 0x28, 0xA0, 0x29, 0xA2, 0x2B, 0xA6, 0x2B, 0xAD}, 4.15, 0x02, []byte{0x1A, 0xFA}},
	6: {[]byte{0x17, 0xA0, 0, 0, 0}, []byte{0x31, 0xA8, 0x29, 0x9B, 0x27, 0x9C, 0x28, 0x9E, 0x29, 0xA4, 0x29, 0xA2, 0x29, 0xA8}, 3.55, 0x04, []byte{0x1F, 0x79}},
}

// isdbtSecondary is the bank-0x12/0x15 pair each ISDB-T bandwidth writes after
// the common table above.
var isdbtSecondary = map[uint32][2]byte{8: {3, 3}, 7: {3, 2}, 6: {7, 2}}

var dvbcITBCoef6 = []byte{0x31, 0xA8, 0x29, 0x9B, 0x27, 0x9C, 0x28, 0x9E, 0x29, 0xA4, 0x29, 0xA2, 0x29, 0xA8}

func (core *Core) TerrestrialPreamble(modeSelect byte) []Op {
	return []Op{
		{Where: SLVX, Register: 0x00, Value: 0x00},
		{Where: SLVX, Register: 0x17, Value: modeSelect},
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0xa9, Value: 0x00},
		{Where: SLVT, Register: 0x2c, Value: 0x01},
		{Where: SLVT, Register: 0x4b, Value: 0x74},
		{Where: SLVT, Register: 0x49, Value: 0x00},
		{Where: SLVX, Register: 0x18, Value: 0x00},
	}
}

// FinishWakeup is the tail every system's wake-up shares: release the
// demodulator's stand-by hold (bank 0x00 register 0x80) and un-hi-Z the
// parallel TS data pins.
func (core *Core) FinishWakeup() error {
	if err := core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0x80, Value: 0x08, Mask: 0x1f},
	}); err != nil {
		return err
	}
	return core.SetTSDataPinHiZ(false)
}

// WakeupDVBT brings the chip up in DVB-T mode (SLtoAT).
func (core *Core) WakeupDVBT(bandwidthMHz uint32) error {
	if err := core.Run(core.TerrestrialPreamble(0x01)); err != nil {
		return err
	}
	if err := core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x11},
		{Where: SLVT, Register: 0x6a, Value: 0x50},
		{Where: SLVT, Register: 0x00, Value: 0x10},
		{Where: SLVT, Register: 0xa5, Value: 0x01},
		{Where: SLVT, Register: 0x00, Value: 0x18},
		{Where: SLVT, Register: 0x31, Value: 0x01},
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0xce, Data: []byte{0x01, 0x01}},
		{Where: SLVT, Register: 0x00, Value: 0x11},
		{Where: SLVT, Register: 0x33, Data: []byte{0x00, 0x03, 0x3b}},
	}); err != nil {
		return err
	}
	if err := core.SetBandwidthDVBT(bandwidthMHz); err != nil {
		return err
	}
	return core.FinishWakeup()
}

func (core *Core) SetBandwidthDVBT(bandwidthMHz uint32) error {
	band, ok := dvbtBands[bandwidthMHz]
	if !ok {
		return fmt.Errorf("CXD2878ER does not support a %d MHz DVB-T channel", bandwidthMHz)
	}
	ifFreq := IFFrequencyRegisters(band.ifMHz)
	return core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x13},
		{Where: SLVT, Register: 0x9c, Data: []byte{0x01, 0x14}},
		{Where: SLVT, Register: 0x00, Value: 0x10},
		{Where: SLVT, Register: 0x9f, Data: band.nominalRate},
		{Where: SLVT, Register: 0xa6, Data: band.itbCoef},
		{Where: SLVT, Register: 0xb6, Data: ifFreq[:]},
		{Where: SLVT, Register: 0xd7, Value: band.channelWidth},
		{Where: SLVT, Register: 0xd9, Data: band.trl},
		{Where: SLVT, Register: 0x00, Value: 0x17},
		{Where: SLVT, Register: 0x38, Data: []byte{0x01, 0x02}},
	})
}

// WakeupDVBT2 brings the chip up in DVB-T2 mode (SLtoAT2). plpID selects one
// physical layer pipe manually; this chip has no automatic-PLP path.
func (core *Core) WakeupDVBT2(bandwidthMHz uint32, plpID byte) error {
	if err := core.Run(append(core.TerrestrialPreamble(0x02), []Op{
		{Where: SLVT, Register: 0x00, Value: 0x11},
		{Where: SLVT, Register: 0x6a, Value: 0x50},
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0xce, Data: []byte{0x01, 0x01}},
		{Where: SLVT, Register: 0x00, Value: 0x13},
		{Where: SLVT, Register: 0x83, Value: 0x10},
		{Where: SLVT, Register: 0x86, Value: 0x34},
		{Where: SLVT, Register: 0x9f, Value: 0xd8},
		{Where: SLVT, Register: 0x00, Value: 0x23},
		{Where: SLVT, Register: 0xdb, Value: 0x01},
		{Where: SLVT, Register: 0x00, Value: 0x11},
		{Where: SLVT, Register: 0x33, Data: []byte{0x00, 0x03, 0x3b}},
		{Where: SLVT, Register: 0x00, Value: 0x23},
		{Where: SLVT, Register: 0xaf, Value: plpID},
		{Where: SLVT, Register: 0xad, Value: 0x01},
	}...)); err != nil {
		return err
	}
	if err := core.SetBandwidthDVBT2(bandwidthMHz); err != nil {
		return err
	}
	return core.FinishWakeup()
}

func (core *Core) SetBandwidthDVBT2(bandwidthMHz uint32) error {
	if bandwidthMHz == 0 {
		return errors.New("CXD2878ER needs BANDWIDTH_HZ for DVB-T2")
	}
	if err := core.Bank(0x00); err != nil {
		return err
	}
	if bandwidthMHz == 1 { // 1.7 MHz has been rounded down to 1 by bandwidthMHz
		band := dvbt2Bandwidth1_7
		ifFreq := IFFrequencyRegisters(band.ifMHz)
		return core.Run([]Op{
			{Where: SLVT, Register: 0x00, Value: 0x20},
			{Where: SLVT, Register: 0x9f, Data: band.nominalRate},
			{Where: SLVT, Register: 0x00, Value: 0x27},
			{Where: SLVT, Register: 0x7a, Value: 0x03},
			{Where: SLVT, Register: 0x00, Value: 0x10},
			{Where: SLVT, Register: 0xa5, Value: 0x00},
			{Where: SLVT, Register: 0xb6, Data: ifFreq[:]},
			{Where: SLVT, Register: 0xd7, Value: band.channelWidth},
		})
	}
	band, ok := dvbt2Bands[bandwidthMHz]
	if !ok {
		return fmt.Errorf("CXD2878ER does not support a %d MHz DVB-T2 channel", bandwidthMHz)
	}
	ifFreq := IFFrequencyRegisters(band.ifMHz)
	return core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x20},
		{Where: SLVT, Register: 0x9f, Data: band.nominalRate},
		{Where: SLVT, Register: 0x00, Value: 0x27},
		{Where: SLVT, Register: 0x7a, Value: 0x00},
		{Where: SLVT, Register: 0x00, Value: 0x10},
		{Where: SLVT, Register: 0xa6, Data: band.itbCoef},
		{Where: SLVT, Register: 0xa5, Value: 0x01},
		{Where: SLVT, Register: 0xb6, Data: ifFreq[:]},
		{Where: SLVT, Register: 0xd7, Value: band.channelWidth},
	})
}

// PLPOrZero folds a caller's PLP choice into the byte the DVB-T2 wake-up
// writes, mapping "no preference" onto pipe 0. There is no SetPLP: the PLP is
// set only while waking DVB-T2 up, so a PLP change costs a retune.
func PLPOrZero(plpID int) byte {
	if plpID < 0 {
		return 0
	}
	return byte(plpID)
}

func (core *Core) WakeupISDBT(bandwidthMHz uint32) error {
	if err := core.Run(core.TerrestrialPreamble(0x06)); err != nil {
		return err
	}
	if err := core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x11},
		{Where: SLVT, Register: 0x6a, Value: 0x50},
		{Where: SLVT, Register: 0x00, Value: 0x10},
		{Where: SLVT, Register: 0xa5, Value: 0x01},
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0xce, Data: []byte{0x00, 0x00}},
		{Where: SLVT, Register: 0x00, Value: 0x10},
		{Where: SLVT, Register: 0x69, Value: 0x04},
		{Where: SLVT, Register: 0x6b, Value: 0x03},
		{Where: SLVT, Register: 0x9d, Value: 0x50},
		{Where: SLVT, Register: 0xd3, Value: 0x06},
		{Where: SLVT, Register: 0xed, Value: 0x00},
		{Where: SLVT, Register: 0xe2, Value: 0xce},
		{Where: SLVT, Register: 0xf2, Value: 0x13},
		{Where: SLVT, Register: 0xde, Value: 0x2e},
		{Where: SLVT, Register: 0x00, Value: 0x15},
		{Where: SLVT, Register: 0xde, Value: 0x02},
		{Where: SLVT, Register: 0x00, Value: 0x17},
		{Where: SLVT, Register: 0x38, Data: []byte{0x00, 0x03}},
		{Where: SLVT, Register: 0x00, Value: 0x1e},
		{Where: SLVT, Register: 0x73, Value: 0x68},
		{Where: SLVT, Register: 0x00, Value: 0x63},
		{Where: SLVT, Register: 0x81, Value: 0x00},
		{Where: SLVT, Register: 0x00, Value: 0x11},
		{Where: SLVT, Register: 0x33, Data: []byte{0x00, 0x03, 0x3b}},
		{Where: SLVT, Register: 0x00, Value: 0x60},
		{Where: SLVT, Register: 0xa8, Data: []byte{0xb7, 0x1b}},
	}); err != nil {
		return err
	}
	if err := core.SetBandwidthISDBT(bandwidthMHz); err != nil {
		return err
	}
	return core.FinishWakeup()
}

// RetuneISDBT restarts ISDB-T acquisition without the full wake-up: the
// "Active, same system" path, which retriggers the demodulator and reloads
// the channel filter and nothing else.
func (core *Core) RetuneISDBT(bandwidthMHz uint32) error {
	if err := core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0xc3, Value: 0x01},
	}); err != nil {
		return err
	}
	return core.SetBandwidthISDBT(bandwidthMHz)
}

func (core *Core) SetBandwidthISDBT(bandwidthMHz uint32) error {
	band, ok := isdbtBands[bandwidthMHz]
	if !ok {
		return fmt.Errorf("CXD2878ER does not support a %d MHz ISDB-T channel", bandwidthMHz)
	}
	secondary := isdbtSecondary[bandwidthMHz]
	ifFreq := IFFrequencyRegisters(band.ifMHz)
	return core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x10},
		{Where: SLVT, Register: 0x9f, Data: band.nominalRate},
		{Where: SLVT, Register: 0xa6, Data: band.itbCoef},
		{Where: SLVT, Register: 0xb6, Data: ifFreq[:]},
		// See isdbtBands: this is keyed by bandwidth, not a literal 0x00.
		{Where: SLVT, Register: 0xd7, Value: band.channelWidth},
		{Where: SLVT, Register: 0xd9, Data: band.trl},
		{Where: SLVT, Register: 0x00, Value: 0x12},
		{Where: SLVT, Register: 0x71, Value: secondary[0]},
		{Where: SLVT, Register: 0x00, Value: 0x15},
		{Where: SLVT, Register: 0xbe, Value: secondary[1]},
	})
}

// WakeupATSC brings the chip up in ATSC mode (SLtoAA). ATSC lives on its own
// I2C address pair rather than the terrestrial banks the other systems share,
// which is why its sequence does not go through TerrestrialPreamble.
func (core *Core) WakeupATSC() error {
	if err := core.Run([]Op{
		{Where: SLVX, Register: 0x00, Value: 0x00},
		{Where: SLVX, Register: 0x17, Value: 0x0f},
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0xa9, Value: 0x00},
		{Where: SLVT, Register: 0x2c, Value: 0x01},
		{Where: SLVT, Register: 0x4b, Value: 0x74},
		{Where: SLVT, Register: 0x49, Value: 0x00},
		{Where: SLVX, Register: 0x18, Value: 0x00},
		{Where: SLVR, Register: 0x00, Value: 0x01},
		{Where: SLVR, Register: 0x48, Value: 0x01},
	}); err != nil {
		return err
	}
	ifFreq := ATSCIFFrequencyRegister(3.7)
	ifData := []byte{byte(ifFreq), byte(ifFreq >> 8), byte(ifFreq >> 16), byte(ifFreq >> 24)}
	if err := core.Run([]Op{
		{Where: SLVR, Register: 0xa1, Data: ifData},
		{Where: SLVR, Register: 0x13, Value: 0x03, Mask: 0x03},
	}); err != nil {
		return err
	}
	if err := core.WriteMulti(SLVR, 0xa0, []byte{
		0x31, 0xa5, 0x2e, 0x9f, 0x2b, 0x99, 0x00, 0xcd, 0x00, 0xcd, 0x00, 0x00, 0x2b, 0x9d,
	}); err != nil {
		return err
	}
	// Calibration constants for the ATSC lock detector, board-independent.
	noSignalThreshold := uint32(0x7ffb61)
	signalThreshold := uint32(0x7c4926)
	if err := core.Run([]Op{
		{Where: SLVR, Register: 0x6f, Data: []byte{byte(noSignalThreshold), byte(noSignalThreshold >> 8), byte(noSignalThreshold >> 16)}},
		{Where: SLVM, Register: 0x80, Data: []byte{byte(noSignalThreshold), byte(noSignalThreshold >> 8), byte(noSignalThreshold >> 16)}},
		{Where: SLVR, Register: 0x73, Data: []byte{byte(signalThreshold), byte(signalThreshold >> 8), byte(signalThreshold >> 16)}},
		{Where: SLVM, Register: 0x83, Data: []byte{byte(signalThreshold), byte(signalThreshold >> 8), byte(signalThreshold >> 16)}},
		{Where: SLVR, Register: 0x71, Value: 0x05, Mask: 0x07},
		{Where: SLVR, Register: 0xa0, Value: 0x10, Mask: 0x10},
	}); err != nil {
		return err
	}
	return core.FinishWakeup()
}

// SetTSDataPinHiZ un-hi-Zs the TS data pins once a system is up, or re-Hi-Zs
// them before sleeping. A parallel board uses all eight pins; a serial one uses
// exactly one, chosen by the same bit as in ConfigureTS -- freeing the other
// seven would hand back pins the board may be using for something else.
func (core *Core) SetTSDataPinHiZ(enable bool) error {
	value := byte(0x00)
	if enable {
		value = 0xff
	}
	return core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0x81, Value: value, Mask: core.tsDataPinMask()},
	})
}

// tsDataPinMask is which of the eight data pins carry the stream.
func (core *Core) tsDataPinMask() byte {
	if !core.Config().TSSerial {
		return 0xff
	}
	if core.Config().TSSerialData {
		// OSEREXCHGB7: the stream leaves on TSDATA7 rather than TSDATA0.
		return 0x80
	}
	return 0x01
}

// SetStreamOutput enables or disables the TS output pins via register 0xC3,
// for a board fixed to TS (not TLV) output.
func (core *Core) SetStreamOutput(enable bool) error {
	if err := core.Bank(0x00); err != nil {
		return err
	}
	data, err := core.Read(SLVT, 0xa9, 1)
	if err != nil {
		return err
	}
	if data[0]&0x03 != 0x00 {
		return nil
	}
	value := byte(0x01)
	if enable {
		value = 0x00
	}
	if err := core.Bank(0x00); err != nil {
		return err
	}
	return core.Write(SLVT, 0xc3, value)
}

// TuneEnd finishes a cold (sleep-to-active) tune: soft reset, then enable the
// stream.
func (core *Core) TuneEnd() error {
	if err := core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0xfe, Value: 0x01},
	}); err != nil {
		return err
	}
	return core.SetStreamOutput(true)
}

// WakeupParams carries what a wake-up needs beyond the system itself.
type WakeupParams struct {
	// BandwidthMHz is the channel width, for every system but ATSC and J.83B.
	// DVB-T2's 1.7 MHz channel is passed as 1 here (bandwidthMHz already
	// rounds it down from Hz, and no other system uses that value).
	BandwidthMHz uint32
	// PLPID selects a DVB-T2 physical layer pipe; negative means no preference.
	// The CXD2856ER has a register for "let the demodulator choose"; the
	// CXD2878ER family has no such path and falls back to pipe 0.
	PLPID int
	// SymbolRateSPS selects the J.83B band, and programs the DVB-S2 symbol
	// rate on the parts that reach it. Zero leaves the chip to search.
	SymbolRateSPS uint32
	// StreamID names which stream to demodulate out of an ISDB-S or ISDB-S3
	// carrier, both of which multiplex several onto one frequency.
	StreamID uint32
}

// SleepPrologue stops the output and parks the transport data pins; every
// teardown starts with it.
func (core *Core) SleepPrologue() error {
	if err := core.SetStreamOutput(false); err != nil {
		return err
	}
	if err := core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0x80, Value: 0x1f, Mask: 0x1f},
	}); err != nil {
		return err
	}
	return core.SetTSDataPinHiZ(true)
}

// SleepEpilogue is the system-independent tail of the teardown, leaving the
// chip asleep and holding no system.
func (core *Core) SleepEpilogue() error {
	if err := core.Run([]Op{
		{Where: SLVX, Register: 0x00, Value: 0x00},
		{Where: SLVX, Register: 0x18, Value: 0x01},
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0x49, Value: 0x33},
		{Where: SLVT, Register: 0x4b, Value: 0x21},
		{Where: SLVT, Register: 0xfe, Value: 0x01},
		{Where: SLVT, Register: 0x2c, Value: 0x00},
		{Where: SLVT, Register: 0xa9, Value: 0x00},
		{Where: SLVX, Register: 0x17, Value: 0x01},
	}); err != nil {
		return err
	}
	core.State = StateSleep
	core.System = SystemUnspecified
	return nil
}

// Sleep parks the chip: shared prologue, this system's teardown, shared
// epilogue. A part whose cable or satellite half comes down differently -- the
// CXD2856ER -- declares its own Sleep and shadows this.
func (core *Core) Sleep() error {
	if core.State == StateActive {
		if err := core.SleepPrologue(); err != nil {
			return err
		}
		if ops, ok := TeardownFor(core.System); ok {
			if err := core.Run(ops); err != nil {
				return err
			}
		}
	}
	return core.SleepEpilogue()
}

// TeardownFor is the teardown sequence for the systems this package owns, and
// reports false for one it does not -- a chip driver's cue to supply its own,
// the cable and satellite halves differing across the family.
func TeardownFor(system System) ([]Op, bool) {
	switch system {
	case SystemDVBT:
		return []Op{
			{Where: SLVT, Register: 0x00, Value: 0x17},
			{Where: SLVT, Register: 0x38, Data: []byte{0x01, 0x02}},
			{Where: SLVT, Register: 0x00, Value: 0x18},
			{Where: SLVT, Register: 0x31, Value: 0x00},
		}, true
	case SystemDVBT2:
		return []Op{
			{Where: SLVT, Register: 0x00, Value: 0x10},
			{Where: SLVT, Register: 0xa5, Value: 0x01},
			{Where: SLVT, Register: 0x00, Value: 0x13},
			{Where: SLVT, Register: 0x83, Value: 0x40},
			{Where: SLVT, Register: 0x86, Value: 0x21},
			{Where: SLVT, Register: 0x9f, Value: 0xfb},
		}, true
	case SystemDVBC:
		return []Op{
			{Where: SLVT, Register: 0x00, Value: 0x11},
			{Where: SLVT, Register: 0xa3, Value: 0x00},
			{Where: SLVT, Register: 0x00, Value: 0x48},
			{Where: SLVT, Register: 0x2c, Value: 0x01},
		}, true
	case SystemISDBT:
		return []Op{
			{Where: SLVT, Register: 0x00, Value: 0x10},
			{Where: SLVT, Register: 0x69, Value: 0x05},
			{Where: SLVT, Register: 0x6b, Value: 0x07},
			{Where: SLVT, Register: 0x9d, Value: 0x14},
			{Where: SLVT, Register: 0xd3, Value: 0x00},
			{Where: SLVT, Register: 0xed, Value: 0x01},
			{Where: SLVT, Register: 0xe2, Value: 0x4e},
			{Where: SLVT, Register: 0xf2, Value: 0x03},
			{Where: SLVT, Register: 0xde, Value: 0x32},
			{Where: SLVT, Register: 0x00, Value: 0x15},
			{Where: SLVT, Register: 0xde, Value: 0x03},
			{Where: SLVT, Register: 0x00, Value: 0x17},
			{Where: SLVT, Register: 0x38, Data: []byte{0x01, 0x02}},
			{Where: SLVT, Register: 0x00, Value: 0x1e},
			{Where: SLVT, Register: 0x73, Value: 0x00},
			{Where: SLVT, Register: 0x00, Value: 0x63},
			{Where: SLVT, Register: 0x81, Value: 0x01},
		}, true
	case SystemJ83B:
		return []Op{
			{Where: SLVT, Register: 0x00, Value: 0x11},
			{Where: SLVT, Register: 0xa3, Value: 0x00},
			{Where: SLVT, Register: 0x00, Value: 0x40},
			{Where: SLVT, Register: 0x21, Value: 0x00},
			{Where: SLVT, Register: 0xc3, Value: 0x00},
			{Where: SLVT, Register: 0xb3, Value: 0x02},
			{Where: SLVT, Register: 0x1e, Value: 0x00},
			{Where: SLVT, Register: 0x8e, Value: 0x0e},
			{Where: SLVT, Register: 0x00, Value: 0x41},
			{Where: SLVT, Register: 0xcf, Value: 0x77},
			{Where: SLVT, Register: 0x00, Value: 0x40},
			{Where: SLVT, Register: 0x14, Value: 0x1f},
			{Where: SLVT, Register: 0x26, Data: []byte{0x09, 0x9a, 0x00, 0xee}},
		}, true
	case SystemATSC:
		return []Op{
			{Where: SLVR, Register: 0x00, Value: 0x01},
			{Where: SLVR, Register: 0x48, Value: 0x01},
			{Where: SLVT, Register: 0x00, Value: 0x00},
			{Where: SLVT, Register: 0xd3, Value: 0x00},
		}, true
	default:
		return nil, false
	}
}

// SetTSClockMode sets the TS clock mode and frequency, run before every cold
// wake-up. One bit is system-dependent: TS rate control must be off for ISDB-T
// and ATSC and on for everything else. Left on, the demodulator paces its
// output to a rate it computes from framing it cannot compute it from, which
// shows up downstream as dropped packets.
//
// ISDB-C wants it off too, but there is no ISDB-C here: this driver receives
// that system as DVB-C, which is what the chip's cable core is.
//
// A serially wired board takes two registers more, taken from the board's
// description.
func (core *Core) SetTSClockMode(system System) error {
	// Clock period and TS interface clock rate are the same in both wirings:
	// eight, and the middle of the six rates.
	const tsClkPeriod, clkSelTSIf = 0x08, 0x01

	rateControlOff := byte(0x00)
	if system == SystemISDBT || system == SystemATSC {
		rateControlOff = 0x01
	}

	operations := []Op{
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0xd3, Value: rateControlOff, Mask: 0x01},
		{Where: SLVT, Register: 0xde, Value: 0x00, Mask: 0x01},
		{Where: SLVT, Register: 0xda, Value: 0x00, Mask: 0x01},
	}
	if core.Config().TSSerial {
		// Continuous clock at full rate, the only one of the six ever selected:
		// OSERCKMODE and OSERDUTYMODE both one.
		const serialClkMode, serialDutyMode = 0x01, 0x01
		operations = append(operations,
			Op{Where: SLVT, Register: 0xc4, Value: serialClkMode, Mask: 0x03},
			Op{Where: SLVT, Register: 0xd1, Value: serialDutyMode, Mask: 0x03},
		)
	}
	operations = append(operations, []Op{
		{Where: SLVT, Register: 0xd9, Value: tsClkPeriod},
		// The TS interface clock is stopped while its rate is changed.
		{Where: SLVT, Register: 0x32, Value: 0x00, Mask: 0x01},
		{Where: SLVT, Register: 0x33, Value: clkSelTSIf, Mask: 0x03},
		{Where: SLVT, Register: 0x32, Value: 0x01, Mask: 0x01},
		// Parity period, enabled for DVB-T and for DVB-C (which also covers
		// ISDB-C and J.83B). Both are set regardless of the system in play.
		{Where: SLVT, Register: 0x00, Value: 0x10},
		{Where: SLVT, Register: 0x66, Value: 0x01, Mask: 0x01},
		{Where: SLVT, Register: 0x00, Value: 0x40},
		{Where: SLVT, Register: 0x66, Value: 0x01, Mask: 0x01},
	}...)
	return core.Run(operations)
}

// ConfigureTS sets up the transport stream output pins for the board's wiring.
// Bank 0x00 register 0xC4 bit 7 is set for serial, clear for parallel. Bank
// 0x02 register 0xE4 bit 0 belongs to a third mode no board here uses. A serial
// board takes two bits more in the same register -- written only when serial,
// since on a parallel board they mean something else.
func (core *Core) ConfigureTS() error {
	serial := core.Config().TSSerial

	mode := byte(0x00)
	if serial {
		mode = 0x80
	}
	operations := []Op{
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0xc4, Value: mode, Mask: 0x80},
		{Where: SLVT, Register: 0x00, Value: 0x02},
		{Where: SLVT, Register: 0xe4, Value: 0x00, Mask: 0x01},
		{Where: SLVT, Register: 0x00, Value: 0x00},
	}
	if serial {
		data := byte(0x00)
		if core.Config().TSSerialData {
			data = 0x08
		}
		operations = append(operations,
			Op{Where: SLVT, Register: 0xc4, Value: data, Mask: 0x08},
			Op{Where: SLVT, Register: 0xc4, Value: 0x00, Mask: 0x10},
		)
	}
	if err := core.Run(operations); err != nil {
		return err
	}
	if mask := core.Config().TSClockMask; mask != 0 {
		if err := core.Run([]Op{
			{Where: SLVT, Register: 0xc6, Value: mask, Mask: 0x1f},
			{Where: SLVT, Register: 0x00, Value: 0x60},
			{Where: SLVT, Register: 0x52, Value: mask, Mask: 0x1f},
			{Where: SLVT, Register: 0x00, Value: 0x00},
		}); err != nil {
			return err
		}
	}
	// Drive strength: 4 mA on the TS clock, sync, valid and data pins.
	return core.Run([]Op{
		{Where: SLVT, Register: 0x95, Value: 0x01, Mask: 0x03},
		{Where: SLVT, Register: 0x95, Value: 0x14, Mask: 0x3c},
		{Where: SLVT, Register: 0x96, Value: 0x54, Mask: 0xff},
		{Where: SLVT, Register: 0x97, Value: 0x54, Mask: 0xff},
	})
}
