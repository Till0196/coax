// Copyright (c) 2015 Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package avl6882

import (
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// SetDeliverySystem uploads a delivery system's image and configures the chip
// (avl6882_set_dvbmode). A no-op when the chip is already in that system, so
// DVB-T<->T2 and DVB-S<->S2 are free. A full upload is expensive: hence Tune.
func (demod *Demod) SetDeliverySystem(delivery string) error {
	system, err := systemFor(delivery)
	if err != nil {
		return err
	}
	demod.delivery = tuning.Normalize(delivery)
	if demod.system == system {
		return nil
	}
	demod.system = system

	if err := demod.uploadFirmware(system); err != nil {
		return err
	}
	for _, command := range []uint32{fwCmdLoadDefault, fwCmdInitSDRAM, fwCmdInitADC} {
		if err := demod.execute(command); err != nil {
			return fmt.Errorf("AVL6882 firmware command %d: %w", command, err)
		}
	}

	switch system {
	case SystemDVBC:
		err = demod.initDVBC()
	case SystemDVBS:
		err = demod.initDVBS()
	default:
		err = demod.initDVBT()
	}
	if err != nil {
		return err
	}

	// Park the LNB supply off at 13 V before anything else touches it.
	if err := demod.gpioSet(gpioLNBPower, gpioLevel1); err != nil {
		return err
	}
	if err := demod.gpioSet(gpioLNBVoltage, gpioLevel0); err != nil {
		return err
	}

	if err := demod.configureTransportStream(); err != nil {
		return err
	}
	if err := demod.configureTunerRepeater(); err != nil {
		return err
	}
	return demod.initErrorStatistics()
}

func (demod *Demod) configureTransportStream() error {
	for _, step := range [][2]uint32{
		{rcTSSerial, avlTSParallel},
		{rcTSClockEdge, avlMPCMRising},
		{rcTSContinuous, avlTSContinuousEnable},
		{rcTSSerialOutPin, avlMPSPData0},
		{rcTSSerialMSB, avlMPBOMSB},
		{rcTSSyncPulse, avlTSSerialSync1Pulse},
		{rcTSErrorPolarity, avlMPEPNormal},
		{rcTSValidPolarity, avlMPVPNormal},
		{rcTSPacketLen, avlTS188},
		{rcTSClockPhase, avlTSParallelPhase0},
	} {
		if err := demod.write8(0x200+step[0], step[1]); err != nil {
			return err
		}
	}
	// rc_ts_packet_order shares an offset with rc_ts_serial_msb, so this
	// overwrites the MSB-first byte above. Order matches the vendor driver.
	if err := demod.write8(0x200+rcTSSerialMSB, avlTSParallelOrderNormal); err != nil {
		return err
	}
	return demod.write32(regTSOutput, tsOutputEnable)
}

func (demod *Demod) configureTunerRepeater() error {
	if err := demod.write32(0x118000+tunerI2CSoftReset, 1); err != nil {
		return err
	}
	// Force past the cached state: the reset above invalidated it.
	demod.gateOpen = true
	if err := demod.OpenTunerGate(false); err != nil {
		return err
	}
	control, err := demod.read32(0x118000 + tunerI2CControl)
	if err != nil {
		return err
	}
	if err := demod.write32(0x118000+tunerI2CControl, control&^1); err != nil {
		return err
	}
	if err := demod.write32(0x118000+tunerI2CBitRptClkDiv, i2cRepeaterClockDivider); err != nil {
		return err
	}
	return demod.write32(0x118000+tunerI2CSoftReset, 0)
}

// initErrorStatistics is InitErrorStat_Demod: arm the automatic error counter
// over a 3-second window.
func (demod *Demod) initErrorStatistics() error {
	const thresholdMs = 3000
	// The tick counter runs at the chip's 270 kHz statistics clock.
	timeTicks := uint64(270000) * thresholdMs

	for _, step := range [][2]uint32{
		{esmMode, avlErrorStatAuto},
		{esmTickType, avlErrorStatTime},
		{esmTimeTickLow, uint32(timeTicks)},
		{esmTimeTickHigh, uint32(timeTicks >> 32)},
		{esmByteTickLow, thresholdMs},
		{esmByteTickHigh, 0},
		// Auto mode: pulse the tick-clear line.
		{esmTickClear, 0}, {esmTickClear, 1}, {esmTickClear, 0},
	} {
		if err := demod.write32(0x132050+step[0], step[1]); err != nil {
			return err
		}
	}
	return demod.resetPacketErrorRate()
}

// resetPacketErrorRate is ResetPER_Demod, restarting the counters.
func (demod *Demod) resetPacketErrorRate() error {
	control, err := demod.read32(0x132050 + esmControl)
	if err != nil {
		return err
	}
	control |= 0x01
	if err := demod.write32(0x132050+esmControl, control); err != nil {
		return err
	}
	control, err = demod.read32(0x132050 + esmControl)
	if err != nil {
		return err
	}
	for _, value := range []uint32{control | 0x08, control | 0x08 | 0x01, (control | 0x08 | 0x01) &^ 0x01} {
		if err := demod.write32(0x132050+esmControl, value); err != nil {
			return err
		}
	}
	return nil
}

func (demod *Demod) initDVBS() error {
	for _, step := range [][2]uint32{
		{rcDVBSxIntMpegClkMHz, 27000},
		{rcDVBSxIntFecClkMHz, 25000},
		{rcDVBSxIntADCClkMHz, 12500},
		// The demodulator clock is 166.666667 MHz, expressed in 10 kHz units.
		{rcDVBSxIntDmdClkMHz, 166666667 / 10000},
	} {
		if err := demod.write16(0xe00+step[0], step[1]); err != nil {
			return err
		}
	}
	for _, step := range [][2]uint32{
		{rcDVBSxRFAGCPol, avlAGCInverted},
		{rcDVBSxFormat, avlOffBinary},
		{rcDVBSxInput, avlADCIn},
	} {
		if err := demod.write32(0xe00+step[0], step[1]); err != nil {
			return err
		}
	}
	if err := demod.write16(0xe00+rcDVBSxIFOffset10kHz, 0); err != nil {
		return err
	}
	if err := demod.gpioSet(gpioAGCDVBS, gpioAGCOn); err != nil {
		return err
	}
	return demod.initDiseqc()
}

// initDiseqc is avl6882_init_diseqc with its only caller's parameters folded
// in: 22 kHz tone, normal waveform both ways, 15 ms TX gap, 150 ms RX timeout.
func (demod *Demod) initDiseqc() error {
	const (
		ddcFrequencyHz = 166666667
		toneFreqKHz    = 22
		txGap15ms      = 0
		waveformNormal = 0
		rxTimeout150ms = 0
	)
	if err := demod.write32(0x16c000+diseqcSoftReset, 1); err != nil {
		return err
	}
	for _, step := range [][2]uint32{
		{diseqcSampFracN, 2000000}, // 2 MHz, as 200 * 10 kHz
		{diseqcSampFracD, ddcFrequencyHz},
		{diseqcToneFracN, toneFreqKHz << 1},
		{diseqcToneFracD, ddcFrequencyHz / 1000},
	} {
		if err := demod.write32(0x16c000+step[0], step[1]); err != nil {
			return err
		}
	}

	txControl, err := demod.read32(0x16c000 + diseqcTxControl)
	if err != nil {
		return err
	}
	txControl &= 0x00000300
	txControl |= 0x20 // reset the TX FIFO
	txControl |= txGap15ms << 6
	txControl |= waveformNormal << 4
	txControl |= 1 << 3 // enable the TX gap
	if err := demod.write32(0x16c000+diseqcTxControl, txControl); err != nil {
		return err
	}
	txControl &^= 0x20 // release the TX FIFO reset
	if err := demod.write32(0x16c000+diseqcTxControl, txControl); err != nil {
		return err
	}

	rxControl := uint32(waveformNormal << 2)
	rxControl |= 1 << 1 // activate the receiver
	rxControl |= 1 << 3 // envelope high when a tone is present
	if err := demod.write32(0x16c000+diseqcRxControl, rxControl); err != nil {
		return err
	}
	if err := demod.write32(0x16c000+diseqcRxMsgTimer, rxTimeout150ms); err != nil {
		return err
	}
	return demod.write32(0x16c000+diseqcSoftReset, 0)
}

func (demod *Demod) initDVBC() error {
	for _, step := range [][2]uint32{
		{rcDVBCDmdClkHz, 250000000},
		{rcDVBCFecClkHz, 250000000},
		{rcDVBCIFFreqHz, 5000000},
		{rcDVBCSymbolRateHz, 6875000},
		{rcDVBCSampleRateHz, 30000000},
	} {
		if err := demod.write32(0x600+step[0], step[1]); err != nil {
			return err
		}
	}
	for _, step := range [][2]uint32{
		{rcDVBCRFAGCPol, avlAGCNormal},
		{rcDVBCADCSel, avlIFQ},
		{rcDVBCJ83BMode, avlDVBCJ83A},
		{rcDVBCInputFormat, avlADCIn},
		{rcDVBCInputSelect, avlOffBinary},
		{rcDVBCTunerType, avlDVBCIF},
		{rcDVBCADCUsePLLClk, 0},
	} {
		if err := demod.write8(0x600+step[0], step[1]); err != nil {
			return err
		}
	}
	return demod.gpioSet(gpioAGCDVBTC, gpioAGCOn)
}

func (demod *Demod) initDVBT() error {
	for _, step := range [][2]uint32{
		{rcDVBTxSampleRateHz, 30000000},
		{rcDVBTxMpegClkRateHz, 270000000},
		{rcDVBTxNomCarrierFreq, 5000000},
	} {
		if err := demod.write32(0xa00+step[0], step[1]); err != nil {
			return err
		}
	}
	for _, step := range [][2]uint32{
		{rcDVBTxInputFormat, avlOffBinary},
		{rcDVBTxInputSelect, avlADCIn},
		{rcDVBTxTunerType, avlDVBTxRealIF},
		{rcDVBTxRFAGCPol, avlAGCNormal},
		{rcDVBTxADCSel, avlIFQ},
		{rcDVBTxADCUsePLLClk, 0},
	} {
		if err := demod.write8(0xa00+step[0], step[1]); err != nil {
			return err
		}
	}
	return demod.gpioSet(gpioAGCDVBTC, gpioAGCOn)
}

// Params is a channel to acquire; the RF tuner must already be on frequency.
type Params struct {
	// DeliverySystem must be the family SetDeliverySystem last got; the T-vs-T2
	// and S-vs-S2 distinction is made here, not at upload.
	DeliverySystem string
	BandwidthHz    uint32
	SymbolRate     uint32
	// StreamID selects a DVB-T2 PLP; negative means pipe 0.
	StreamID int
}

// Tune starts acquisition on the channel the tuner is pointed at.
func (demod *Demod) Tune(params Params) error {
	system, err := systemFor(params.DeliverySystem)
	if err != nil {
		return err
	}
	// Refuse rather than silently acquire in the wrong mode.
	mode, err := demod.read32(0x200 + rsCurrentActiveMode)
	if err != nil {
		return err
	}
	if mode != system.activeMode() {
		return fmt.Errorf("AVL6882 is in mode %d, not the %s mode %d SetDeliverySystem should have entered",
			mode, system, system.activeMode())
	}
	demod.delivery = tuning.Normalize(params.DeliverySystem)

	switch system {
	case SystemDVBC:
		return demod.tuneDVBC(params)
	case SystemDVBS:
		return demod.tuneDVBS(params)
	default:
		return demod.tuneDVBT(params)
	}
}

func (demod *Demod) tuneDVBS(params Params) error {
	if err := demod.write16(0xc00+rsDVBSxFecLock, 0); err != nil {
		return err
	}
	// The repeated decode-mode and FEC-bypass writes match the vendor sequence.
	for _, step := range [][2]uint32{
		{rcDVBSxDecodeMode, 0x14},
		{rcDVBSxFecBypassRate, 0}, // let DVB-S auto-lock the code rate
		{rcDVBSxIQMode, 1},        // auto spectrum detection
		{rcDVBSxDecodeMode, 0x14},
		{rcDVBSxFecBypassRate, 0},
	} {
		if err := demod.write16(0xe00+step[0], step[1]); err != nil {
			return err
		}
	}
	if err := demod.write32(0xe00+rcDVBSxIntSymRateMHz, params.SymbolRate); err != nil {
		return err
	}
	return demod.execute(fwCmdAcquire)
}

func (demod *Demod) tuneDVBC(params Params) error {
	if err := demod.write32(0x600+rcDVBCQAMModeScan, 0x0101); err != nil {
		return err
	}
	if err := demod.write32(0x600+rcDVBCSymbolRateHz, params.SymbolRate); err != nil {
		return err
	}
	return demod.execute(fwCmdAcquire)
}

func (demod *Demod) tuneDVBT(params Params) error {
	if err := demod.write32(0xa00+rcDVBTxFundRateHz, fftBandwidth(params.BandwidthHz)); err != nil {
		return err
	}
	if err := demod.write8(0xa00+rcDVBTxL1ProcOnly, 0); err != nil {
		return err
	}
	if err := demod.write8(0xa00+rcDVBTxSpectrumInvert, avlSpectrumAuto); err != nil {
		return err
	}

	var steps [][2]uint32
	if tuning.Normalize(params.DeliverySystem) == "DVBT" {
		steps = [][2]uint32{
			{rcDVBTxAcquireMode, avlLockModeTOnly},
			{rcDVBTxLayerSelect, 0},
			{rcDVBTxDataPLPID, 0},
			{rcDVBTxCommonPLPID, 0},
			{rcDVBTxCommonPLPPresnt, 0},
		}
	} else {
		plp := uint32(0)
		if params.StreamID >= 0 {
			plp = uint32(params.StreamID) & 0xff
		}
		steps = [][2]uint32{
			{rcDVBTxAcquireMode, avlLockModeAll},
			{rcDVBTxDataPLPID, plp},
			{rcDVBTxCommonPLPID, 0},
			{rcDVBTxCommonPLPPresnt, 2},
		}
	}
	for _, step := range steps {
		if err := demod.write8(0xa00+step[0], step[1]); err != nil {
			return err
		}
	}
	return demod.execute(fwCmdAcquire)
}

// fftBandwidth is avl6882_set_dvbt's bw_fft ladder: channel width to the
// chip's fundamental-rate value.
func fftBandwidth(bandwidthHz uint32) uint32 {
	switch {
	case bandwidthHz <= 1_700_000:
		return 1845070
	case bandwidthHz <= 5_000_000:
		return 5714285
	case bandwidthHz <= 6_000_000:
		return 6857143
	case bandwidthHz <= 7_000_000:
		return 8000000
	default:
		return 9142857
	}
}
