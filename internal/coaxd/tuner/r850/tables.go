// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Register tables ported from px4_drv (r850.c) by nns779.

package r850

// Register images. Entries 0x00 to 0x07 are read-only status and stay zero.

// initRegisters is the tuner's working register image.
var initRegisters = [registerCount]byte{
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0xca, 0xc0, 0x72, 0x50, 0x00, 0xe0, 0x00, 0x30,
	0x86, 0xbb, 0xf8, 0xb0, 0xd2, 0x81, 0xcd, 0x46,
	0x37, 0x40, 0x89, 0x8c, 0x55, 0x95, 0x07, 0x23,
	0x21, 0xf1, 0x4c, 0x5f, 0xc4, 0x20, 0xa9, 0x6c,
	0x53, 0xab, 0x5b, 0x46, 0xb3, 0x93, 0x6e, 0x41,
}

// sleepRegisters powers the tuner down.
var sleepRegisters = [registerCount]byte{
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x33, 0xee, 0xb9, 0xfe, 0x0f, 0xe1, 0x04, 0x30,
	0x86, 0xfb, 0xf8, 0xb0, 0xd2, 0x81, 0xcd, 0x46,
	0x37, 0x44, 0x89, 0x8c, 0x55, 0x95, 0x07, 0x23,
	0x21, 0xf1, 0x4c, 0x5f, 0xc4, 0x20, 0xa9, 0xfc,
	0x53, 0xab, 0x0b, 0x46, 0xb3, 0x93, 0x6e, 0x41,
}

// wakeupRegisters brings the tuner back from sleep.
var wakeupRegisters = [registerCount]byte{
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0xca, 0xe0, 0xf2, 0x7c, 0xc0, 0xe0, 0x00, 0x30,
	0x86, 0xbb, 0xf8, 0xb0, 0xd2, 0x81, 0xcd, 0x46,
	0x37, 0x40, 0x89, 0x8c, 0x55, 0x95, 0x07, 0x23,
	0x21, 0xf1, 0x4c, 0x5f, 0xc4, 0x20, 0xa9, 0x6c,
	0x53, 0xab, 0x5b, 0x46, 0xb3, 0x93, 0x6e, 0x41,
}

// imrCalibrationRegisters set the chip up for image-rejection calibration.
var imrCalibrationRegisters = [registerCount]byte{
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0xc0, 0x49, 0x3a, 0x90, 0x03, 0xc1, 0x61, 0x71,
	0x17, 0xf1, 0x18, 0x55, 0x30, 0x20, 0xf3, 0xed,
	0x1f, 0x1c, 0x81, 0x13, 0x00, 0x80, 0x0a, 0x07,
	0x21, 0x71, 0x54, 0xf1, 0xf2, 0xa9, 0xbb, 0x0b,
	0xa3, 0xf6, 0x0b, 0x44, 0x92, 0x17, 0xe6, 0x80,
}

// lpfCalibrationRegisters set the chip up for low-pass filter calibration.
var lpfCalibrationRegisters = [registerCount]byte{
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0xc0, 0x49, 0x3f, 0x90, 0x13, 0xe1, 0x89, 0x7a,
	0x07, 0xf1, 0x9a, 0x50, 0x30, 0x20, 0xe1, 0x00,
	0x00, 0x04, 0x81, 0x11, 0xef, 0xee, 0x17, 0x07,
	0x31, 0x71, 0x54, 0xb2, 0xee, 0xa9, 0xbb, 0x0b,
	0xa3, 0x00, 0x0b, 0x44, 0x92, 0x1f, 0xe6, 0x80,
}

// System parameter tables, indexed by chip revision then by entry. Each entry
// matches on bandwidth and IF frequency.

var dvbTParams = [2][]systemParams{
	{
		{bandwidth: 0, ifFrequency: 4570, filterCalibrationIF: 7550, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x08, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x01, bandwidth: 3, lsb: 1}},
		{bandwidth: 1, ifFrequency: 4570, filterCalibrationIF: 7920, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0b, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x04, bandwidth: 2, lsb: 0}},
		{bandwidth: 2, ifFrequency: 4570, filterCalibrationIF: 8450, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0c, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x01, bandwidth: 2, lsb: 0}},
		{bandwidth: 0, ifFrequency: 5000, filterCalibrationIF: 7920, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x06, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x06, bandwidth: 2, lsb: 1}},
		{bandwidth: 1, ifFrequency: 5000, filterCalibrationIF: 8450, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x09, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x00, bandwidth: 2, lsb: 1}},
		{bandwidth: 2, ifFrequency: 5000, filterCalibrationIF: 8700, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0a, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x06, bandwidth: 0, lsb: 1}},
	},
	{
		{bandwidth: 0, ifFrequency: 4570, filterCalibrationIF: 7550, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x08, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x01, bandwidth: 3, lsb: 1}},
		{bandwidth: 1, ifFrequency: 4570, filterCalibrationIF: 7920, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0b, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x04, bandwidth: 2, lsb: 0}},
		{bandwidth: 2, ifFrequency: 4570, filterCalibrationIF: 8450, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0c, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x01, bandwidth: 2, lsb: 0}},
		{bandwidth: 0, ifFrequency: 5000, filterCalibrationIF: 7920, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x06, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x06, bandwidth: 2, lsb: 1}},
		{bandwidth: 1, ifFrequency: 5000, filterCalibrationIF: 8450, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x09, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x00, bandwidth: 2, lsb: 1}},
		{bandwidth: 2, ifFrequency: 5000, filterCalibrationIF: 8700, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0a, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x06, bandwidth: 0, lsb: 1}},
	},
}

var dvbT21Params = [2][]systemParams{
	{
		{bandwidth: 1, ifFrequency: 1900, filterCalibrationIF: 7920, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x08, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x04, bandwidth: 2, lsb: 0}},
		{bandwidth: 1, ifFrequency: 5000, filterCalibrationIF: 6000, bw: 2, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x01, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x0b, bandwidth: 3, lsb: 1}},
	},
	{
		{bandwidth: 1, ifFrequency: 1900, filterCalibrationIF: 7920, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x08, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x04, bandwidth: 2, lsb: 0}},
		{bandwidth: 1, ifFrequency: 5000, filterCalibrationIF: 6000, bw: 2, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x01, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x0b, bandwidth: 3, lsb: 1}},
	},
}

var dvbCParams = [2][]systemParams{
	{
		{bandwidth: 0, ifFrequency: 5070, filterCalibrationIF: 8100, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x05, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x02, bandwidth: 2, lsb: 0}},
		{bandwidth: 2, ifFrequency: 5070, filterCalibrationIF: 9550, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0b, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x04, bandwidth: 0, lsb: 0}},
		{bandwidth: 0, ifFrequency: 5000, filterCalibrationIF: 7780, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x06, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x01, bandwidth: 2, lsb: 1}},
		{bandwidth: 2, ifFrequency: 5000, filterCalibrationIF: 9250, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0b, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x05, bandwidth: 0, lsb: 1}},
	},
	{
		{bandwidth: 0, ifFrequency: 5070, filterCalibrationIF: 8100, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x05, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x02, bandwidth: 2, lsb: 0}},
		{bandwidth: 2, ifFrequency: 5070, filterCalibrationIF: 9550, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0b, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x04, bandwidth: 0, lsb: 0}},
		{bandwidth: 0, ifFrequency: 5000, filterCalibrationIF: 7780, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x06, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x01, bandwidth: 2, lsb: 1}},
		{bandwidth: 2, ifFrequency: 5000, filterCalibrationIF: 9250, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0b, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x05, bandwidth: 0, lsb: 1}},
	},
}

var j83bParams = [2][]systemParams{
	{
		{bandwidth: 0, ifFrequency: 5070, filterCalibrationIF: 8100, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x05, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x03, bandwidth: 2, lsb: 1}},
		{bandwidth: 0, ifFrequency: 5000, filterCalibrationIF: 7550, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x05, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x05, bandwidth: 2, lsb: 1}},
	},
	{
		{bandwidth: 0, ifFrequency: 5070, filterCalibrationIF: 8100, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x05, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x03, bandwidth: 2, lsb: 1}},
		{bandwidth: 0, ifFrequency: 5000, filterCalibrationIF: 7550, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x05, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x05, bandwidth: 2, lsb: 1}},
	},
}

var isdbTParams = [2][]systemParams{
	{
		{bandwidth: 0, ifFrequency: 4063, filterCalibrationIF: 7070, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x08, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x02, bandwidth: 3, lsb: 1}},
		{bandwidth: 0, ifFrequency: 4570, filterCalibrationIF: 7400, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x05, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x08, bandwidth: 2, lsb: 0}},
		{bandwidth: 0, ifFrequency: 5000, filterCalibrationIF: 7780, bw: 1, filterExtend: 1, hpfNotch: 0, hpfCorner: 0x03, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x05, bandwidth: 2, lsb: 0}},
	},
	{
		{bandwidth: 0, ifFrequency: 4063, filterCalibrationIF: 7070, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0a, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x02, bandwidth: 3, lsb: 1}},
		{bandwidth: 0, ifFrequency: 4570, filterCalibrationIF: 7400, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x08, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x08, bandwidth: 2, lsb: 0}},
		{bandwidth: 0, ifFrequency: 5000, filterCalibrationIF: 7780, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x03, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x05, bandwidth: 2, lsb: 0}},
	},
}

var dtmbParams = [2][]systemParams{
	{
		{bandwidth: 0, ifFrequency: 4500, filterCalibrationIF: 7200, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x08, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x02, bandwidth: 3, lsb: 1}},
		{bandwidth: 2, ifFrequency: 4570, filterCalibrationIF: 8450, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0c, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x00, bandwidth: 2, lsb: 1}},
		{bandwidth: 0, ifFrequency: 5000, filterCalibrationIF: 8100, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x06, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x04, bandwidth: 2, lsb: 1}},
		{bandwidth: 2, ifFrequency: 5000, filterCalibrationIF: 8800, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0b, filterCompensation: 2, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x05, bandwidth: 0, lsb: 1}},
	},
	{
		{bandwidth: 0, ifFrequency: 4500, filterCalibrationIF: 7200, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x08, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x02, bandwidth: 3, lsb: 1}},
		{bandwidth: 2, ifFrequency: 4570, filterCalibrationIF: 8450, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0c, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x00, bandwidth: 2, lsb: 1}},
		{bandwidth: 0, ifFrequency: 5000, filterCalibrationIF: 8100, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x06, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x04, bandwidth: 2, lsb: 1}},
		{bandwidth: 2, ifFrequency: 5000, filterCalibrationIF: 8800, bw: 0, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x0b, filterCompensation: 2, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x05, bandwidth: 0, lsb: 1}},
	},
}

var atscParams = [2][]systemParams{
	{
		{bandwidth: 0, ifFrequency: 5070, filterCalibrationIF: 8050, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x05, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x03, bandwidth: 2, lsb: 0}},
		{bandwidth: 0, ifFrequency: 5000, filterCalibrationIF: 7920, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x05, filterCompensation: 1, imageGain: 0, agcClock: 0, lpf: lpfParams{code: 0x04, bandwidth: 2, lsb: 0}},
	},
	{
		{bandwidth: 0, ifFrequency: 5070, filterCalibrationIF: 8050, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x05, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x03, bandwidth: 2, lsb: 0}},
		{bandwidth: 0, ifFrequency: 5000, filterCalibrationIF: 7920, bw: 1, filterExtend: 0, hpfNotch: 0, hpfCorner: 0x05, filterCompensation: 1, imageGain: 3, agcClock: 1, lpf: lpfParams{code: 0x04, bandwidth: 2, lsb: 0}},
	},
}

// Per-frequency gain and filter settings. The first entry whose IF frequency and
// RF range match the request wins; a zero bound means "no bound".

var dvbTFrequencyParams = []frequencyParams{
	{
		ifFrequency: 0, rfMin: 0, rfMax: 340000, lnaTop: 5, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 4, rfVTLH: 0x5a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 5,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 9, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 4, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 2,
		filterTop: 4, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 3,
		basebandDisableCurrent: 0, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 0,
		imageNRBAdder: 2, hpfCompensation: 1, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 0, rfMin: 662001, rfMax: 670000, lnaTop: 4, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 4, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 4, rfVTLH: 0x5a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 4,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 9, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 4, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 2,
		filterTop: 4, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 3,
		basebandDisableCurrent: 0, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 0,
		imageNRBAdder: 2, hpfCompensation: 1, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 0, rfMin: 782001, rfMax: 790000, lnaTop: 5, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 2, lnaRFChargeCurrent: 0, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 4, rfVTLH: 0x5a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 4,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 9, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 4, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 2,
		filterTop: 4, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 3,
		basebandDisableCurrent: 0, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 0,
		imageNRBAdder: 2, hpfCompensation: 1, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 0, rfMin: 0, rfMax: 0, lnaTop: 4, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 4, rfVTLH: 0x5a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 4,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 9, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 4, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 2,
		filterTop: 4, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 3,
		basebandDisableCurrent: 0, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 0,
		imageNRBAdder: 2, hpfCompensation: 1, feedbackResistor1st: 1,
	},
}

var dvbCFrequencyParams = []frequencyParams{
	{
		ifFrequency: 0, rfMin: 0, rfMax: 660000, lnaTop: 4, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 4, rfVTLH: 0x4a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 0, nrbTop: 5,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 12, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 4, mixerGainLimit: 2, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 0,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 1,
		imageNRBAdder: 2, hpfCompensation: 1, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 0, rfMin: 0, rfMax: 0, lnaTop: 4, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 3, rfVTLH: 0x4a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 0, nrbTop: 5,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 12, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 4, mixerGainLimit: 2, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 0,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 1,
		imageNRBAdder: 1, hpfCompensation: 1, feedbackResistor1st: 1,
	},
}

var j83bFrequencyParams = []frequencyParams{
	{
		ifFrequency: 0, rfMin: 0, rfMax: 335000, lnaTop: 5, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 4, rfVTLH: 0x4a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 0, nrbTop: 5,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 0, mixerTop: 12, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 7, mixerGainLimit: 2, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 0,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 1,
		imageNRBAdder: 2, hpfCompensation: 1, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 0, rfMin: 340001, rfMax: 660000, lnaTop: 5, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 4, rfVTLH: 0x4a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 0, nrbTop: 5,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 0, mixerTop: 12, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 7, mixerGainLimit: 2, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 0,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 1,
		imageNRBAdder: 2, hpfCompensation: 1, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 0, rfMin: 0, rfMax: 0, lnaTop: 4, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 3, rfVTLH: 0x4a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 0, nrbTop: 5,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 0, mixerTop: 12, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 7, mixerGainLimit: 2, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 0,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 1,
		imageNRBAdder: 1, hpfCompensation: 1, feedbackResistor1st: 1,
	},
}

var isdbTFrequencyParams = []frequencyParams{
	{
		ifFrequency: 4063, rfMin: 0, rfMax: 340000, lnaTop: 5, lnaVTLH: 0x6b, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 5, rfVTLH: 0x4a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 12,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 15, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 7, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 0,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 0,
		imageNRBAdder: 2, hpfCompensation: 2, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 4063, rfMin: 470000, rfMax: 487999, lnaTop: 6, lnaVTLH: 0x8c, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 5, rfVTLH: 0x6b, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 3,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 14, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 7, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 3,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 1,
		imageNRBAdder: 3, hpfCompensation: 2, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 4063, rfMin: 680000, rfMax: 691999, lnaTop: 5, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 2, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x07,
		rfTop: 6, rfVTLH: 0x6b, rfGainLimit: 0, rfDisableSlowFast: 0x04, rfLTEPSG: 1, nrbTop: 3,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 14, mixerVTH: 0x09, mixerVTL: 0x05,
		mixerAmpLPF: 7, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 3,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 0, enablePolyGain: 1,
		imageNRBAdder: 3, hpfCompensation: 2, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 4063, rfMin: 692000, rfMax: 697999, lnaTop: 5, lnaVTLH: 0x5b, lnaNRBDetect: 0,
		lnaRFDisableMode: 2, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x07,
		rfTop: 6, rfVTLH: 0x6b, rfGainLimit: 0, rfDisableSlowFast: 0x04, rfLTEPSG: 1, nrbTop: 10,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 3, mixerTop: 12, mixerVTH: 0x09, mixerVTL: 0x05,
		mixerAmpLPF: 7, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 3,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 0, enablePolyGain: 1,
		imageNRBAdder: 2, hpfCompensation: 2, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 4063, rfMin: 0, rfMax: 0, lnaTop: 5, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 6, rfVTLH: 0x6b, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 3,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 14, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 7, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 3,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 1,
		imageNRBAdder: 3, hpfCompensation: 2, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 0, rfMin: 0, rfMax: 340000, lnaTop: 5, lnaVTLH: 0x6b, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 5, rfVTLH: 0x4a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 12,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 15, mixerVTH: 0x0b, mixerVTL: 0x06,
		mixerAmpLPF: 7, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 0,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 0,
		imageNRBAdder: 2, hpfCompensation: 2, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 0, rfMin: 470000, rfMax: 487999, lnaTop: 5, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 2, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x07,
		rfTop: 6, rfVTLH: 0x6b, rfGainLimit: 0, rfDisableSlowFast: 0x04, rfLTEPSG: 1, nrbTop: 3,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 14, mixerVTH: 0x09, mixerVTL: 0x05,
		mixerAmpLPF: 7, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 3,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 0, enablePolyGain: 1,
		imageNRBAdder: 3, hpfCompensation: 2, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 0, rfMin: 680000, rfMax: 691999, lnaTop: 5, lnaVTLH: 0x5b, lnaNRBDetect: 0,
		lnaRFDisableMode: 2, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x07,
		rfTop: 6, rfVTLH: 0x6b, rfGainLimit: 0, rfDisableSlowFast: 0x04, rfLTEPSG: 1, nrbTop: 10,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 3, mixerTop: 12, mixerVTH: 0x09, mixerVTL: 0x05,
		mixerAmpLPF: 7, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 3,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 0, enablePolyGain: 1,
		imageNRBAdder: 2, hpfCompensation: 2, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 0, rfMin: 692000, rfMax: 697999, lnaTop: 5, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 6, rfVTLH: 0x6b, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 3,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 14, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 7, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 3,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 1,
		imageNRBAdder: 3, hpfCompensation: 2, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 0, rfMin: 0, rfMax: 0, lnaTop: 5, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 6, rfVTLH: 0x6b, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 3,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 14, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 7, mixerGainLimit: 3, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 0,
		filterTop: 12, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 3,
		basebandDisableCurrent: 1, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 1,
		imageNRBAdder: 3, hpfCompensation: 2, feedbackResistor1st: 1,
	},
}

var dtmbFrequencyParams = []frequencyParams{
	{
		ifFrequency: 0, rfMin: 0, rfMax: 100000, lnaTop: 4, lnaVTLH: 0x6b, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 4, rfVTLH: 0x4a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 10,
		nrbBandwidthHPF: 3, nrbBandwidthLPF: 3, mixerTop: 9, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 4, mixerGainLimit: 1, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 2,
		filterTop: 4, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 0, filter3rdLPFGain: 0,
		basebandDisableCurrent: 0, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 0,
		imageNRBAdder: 1, hpfCompensation: 0, feedbackResistor1st: 0,
	},
	{
		ifFrequency: 0, rfMin: 0, rfMax: 340000, lnaTop: 4, lnaVTLH: 0x6b, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 4, rfVTLH: 0x4a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 10,
		nrbBandwidthHPF: 0, nrbBandwidthLPF: 2, mixerTop: 9, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 4, mixerGainLimit: 1, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 2,
		filterTop: 4, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 0, filter3rdLPFGain: 0,
		basebandDisableCurrent: 0, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 0,
		imageNRBAdder: 1, hpfCompensation: 0, feedbackResistor1st: 0,
	},
	{
		ifFrequency: 0, rfMin: 0, rfMax: 0, lnaTop: 4, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 4, rfVTLH: 0x4a, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 6,
		nrbBandwidthHPF: 3, nrbBandwidthLPF: 2, mixerTop: 9, mixerVTH: 0x09, mixerVTL: 0x04,
		mixerAmpLPF: 4, mixerGainLimit: 1, mixerDetectBandwidthLPF: 0, mixerFilterDisable: 2,
		filterTop: 4, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 0, filter3rdLPFGain: 3,
		basebandDisableCurrent: 0, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 0,
		imageNRBAdder: 0, hpfCompensation: 0, feedbackResistor1st: 0,
	},
}

var atscFrequencyParams = []frequencyParams{
	{
		ifFrequency: 0, rfMin: 0, rfMax: 340000, lnaTop: 6, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 5, rfVTLH: 0x6b, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 12,
		nrbBandwidthHPF: 2, nrbBandwidthLPF: 2, mixerTop: 12, mixerVTH: 0x0b, mixerVTL: 0x04,
		mixerAmpLPF: 7, mixerGainLimit: 2, mixerDetectBandwidthLPF: 1, mixerFilterDisable: 2,
		filterTop: 6, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 0,
		basebandDisableCurrent: 0, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 0,
		imageNRBAdder: 1, hpfCompensation: 2, feedbackResistor1st: 1,
	},
	{
		ifFrequency: 0, rfMin: 0, rfMax: 0, lnaTop: 6, lnaVTLH: 0x5a, lnaNRBDetect: 0,
		lnaRFDisableMode: 1, lnaRFChargeCurrent: 1, lnaRFDisableCurrent: 1, lnaDisableSlowFast: 0x05,
		rfTop: 5, rfVTLH: 0x6b, rfGainLimit: 0, rfDisableSlowFast: 0x05, rfLTEPSG: 1, nrbTop: 12,
		nrbBandwidthHPF: 2, nrbBandwidthLPF: 2, mixerTop: 12, mixerVTH: 0x0b, mixerVTL: 0x04,
		mixerAmpLPF: 7, mixerGainLimit: 2, mixerDetectBandwidthLPF: 1, mixerFilterDisable: 2,
		filterTop: 6, filterVTH: 0x09, filterVTL: 0x04, filter3rdLPFCurrent: 1, filter3rdLPFGain: 3,
		basebandDisableCurrent: 0, basebandDetectMode: 0, naPowerDetect: 1, enablePolyGain: 0,
		imageNRBAdder: 1, hpfCompensation: 2, feedbackResistor1st: 1,
	},
}

// Accumulated gain tables, in tenths of a dB. Nothing reads them yet: they are
// kept for a future signal strength implementation.

var lnaAccumulatedGain = [32]uint16{
	0, 15, 26, 34, 50, 61, 75, 87,
	101, 117, 130, 144, 154, 164, 176, 188,
	199, 209, 220, 226, 233, 232, 232, 232,
	232, 247, 262, 280, 296, 311, 296, 308,
}

var rfAccumulatedGain = [16]uint16{
	0, 15, 26, 34, 50, 61, 75, 87,
	101, 117, 130, 144, 154, 164, 176, 188,
}

var mixerAccumulatedGain = [16]uint16{
	0, 0, 0, 0, 9, 22, 32, 44,
	56, 68, 80, 90, 100, 100, 100, 100,
}

// systemTables and frequencyTables index the per-system tables by System.
var systemTables = [systemCount][2][]systemParams{
	SystemDVBT:   dvbTParams,
	SystemDVBT2:  dvbTParams,
	SystemDVBT21: dvbT21Params,
	SystemDVBC:   dvbCParams,
	SystemJ83B:   j83bParams,
	SystemISDBT:  isdbTParams,
	SystemDTMB:   dtmbParams,
	SystemATSC:   atscParams,
}

var frequencyTables = [systemCount][]frequencyParams{
	SystemDVBT:   dvbTFrequencyParams,
	SystemDVBT2:  dvbTFrequencyParams,
	SystemDVBT21: dvbTFrequencyParams,
	SystemDVBC:   dvbCFrequencyParams,
	SystemJ83B:   j83bFrequencyParams,
	SystemISDBT:  isdbTFrequencyParams,
	SystemDTMB:   dtmbFrequencyParams,
	SystemATSC:   atscFrequencyParams,
}
