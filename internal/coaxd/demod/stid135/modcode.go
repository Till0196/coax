// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stid135

// modcode is enum fe_sat_modcode: the modulation and code rate found. The
// numbering is the chip's own -- 0x00 to 0x1f are the DVB-S2 modcodes as they
// appear in a physical layer header, 0x20 upwards DVB-S code rates, 0x40
// upwards the DVB-S2X additions.
type modcode byte

// Boundaries of enum fe_sat_mod_code_ident, deciding what a raw DMDMODCOD
// reading means.
const (
	maxLegacyModcode modcode = 0x1c // FE_SAT_32APSK_910
	minDVBS1Modcode  modcode = 0x20 // FE_SAT_DVBS1_QPSK_12
	maxDVBS1Modcode  modcode = 0x25 // FE_SAT_DVBS1_QPSK_78
	minDVBS2XModcode modcode = 0x40 // FE_SATX_VLSNR1
	maxDVBS2XModcode modcode = 0x7f // FE_SATX_256APSK
	minShortModcode  modcode = 0x6c // FE_SATX_QPSK_11_45
	maxShortModcode  modcode = 0x7c // FE_SATX_32APSK_32_45_S
	minPilotOffMode  modcode = 0x7d // FE_SATX_8PSK
	maxPilotOffMode  modcode = 0x7f // FE_SATX_256APSK
	minPilotOnMode   modcode = 0x80 // FE_SATX_16APSK
)

// modulation is the constellation family a modcode belongs to, which picks
// the carrier loop register to write.
type modulation int

const (
	modulationUnknown modulation = iota
	modulationQPSK
	modulation8PSK
	modulation16APSK
	modulation32APSK
	modulation64APSK
	modulation128APSK
	modulation256APSK
	modulationVLSNR
	modulation1024APSK
)

// classify sorts a modcode into its family. The linear variants -- suffixed
// _L -- share a carrier loop register with their circular namesakes, so they
// fold into the same family here.
func classify(code modcode) modulation {
	switch {
	case between(code, 0x01, 0x0b), // FE_SAT_QPSK_14    .. FE_SAT_QPSK_910
		between(code, 0x20, 0x25), // FE_SAT_DVBS1_QPSK_12 .. FE_SAT_DVBS1_QPSK_78
		between(code, 0x42, 0x44), // FE_SATX_QPSK_13_45 .. FE_SATX_QPSK_11_20
		between(code, 0x6c, 0x71): // FE_SATX_QPSK_11_45 .. FE_SATX_QPSK_32_45
		return modulationQPSK
	case between(code, 0x0c, 0x11), // FE_SAT_8PSK_35     .. FE_SAT_8PSK_910
		between(code, 0x47, 0x49),  // FE_SATX_8PSK_23_36 .. FE_SATX_8PSK_13_18
		between(code, 0x72, 0x75),  // FE_SATX_8PSK_7_15  .. FE_SATX_8PSK_32_45
		code == 0x7d,               // FE_SATX_8PSK
		code == 0x45, code == 0x46: // FE_SATX_8APSK_5_9_L, FE_SATX_8APSK_26_45_L
		return modulation8PSK
	case between(code, 0x12, 0x17), // FE_SAT_16APSK_23     .. FE_SAT_16APSK_910
		between(code, 0x4d, 0x51),  // FE_SATX_16APSK_26_45 .. FE_SATX_16APSK_23_36
		between(code, 0x53, 0x56),  // FE_SATX_16APSK_25_36 .. FE_SATX_16APSK_77_90
		between(code, 0x76, 0x7a),  // FE_SATX_16APSK_7_15  .. FE_SATX_16APSK_32_45
		code == 0x80,               // FE_SATX_16APSK
		between(code, 0x4a, 0x4c),  // FE_SATX_16APSK_1_2_L .. FE_SATX_16APSK_5_9_L
		code == 0x4f, code == 0x52: // FE_SATX_16APSK_3_5_L, FE_SATX_16APSK_2_3_L
		return modulation16APSK
	case between(code, 0x18, 0x1c), // FE_SAT_32APSK_34     .. FE_SAT_32APSK_910
		between(code, 0x58, 0x5b), // FE_SATX_32APSK_R_58  .. FE_SATX_32APSK_7_9
		between(code, 0x7b, 0x7c), // FE_SATX_32APSK_2_3   .. FE_SATX_32APSK_32_45_S
		code == 0x7e,              // FE_SATX_32APSK
		code == 0x57:              // FE_SATX_32APSK_2_3_L
		return modulation32APSK
	case code == 0x40, code == 0x41: // FE_SATX_VLSNR1, FE_SATX_VLSNR2
		return modulationVLSNR
	case between(code, 0x5d, 0x63), // FE_SATX_64APSK_11_15 .. FE_SATX_64APSK_5_6
		code == 0x81, // FE_SATX_64APSK
		code == 0x5c: // FE_SATX_64APSK_32_45_L
		return modulation64APSK
	case code == 0x64, code == 0x65: // FE_SATX_128APSK_3_4, FE_SATX_128APSK_7_9
		return modulation128APSK
	case code == 0x69, code == 0x6b, // FE_SATX_256APSK_32_45, FE_SATX_256APSK_3_4
		code == 0x7f,              // FE_SATX_256APSK
		between(code, 0x66, 0x68), // FE_SATX_256APSK_29_45_L .. FE_SATX_256APSK_31_45_L
		code == 0x6a:              // FE_SATX_256APSK_11_15_L
		return modulation256APSK
	case code == 0x82: // FE_SATX_1024APSK
		return modulation1024APSK
	}
	return modulationUnknown
}

func between(value, low, high modcode) bool { return value >= low && value <= high }

// defaultACLC is used for a modcode the table below does not name.
const defaultACLC = 0x29

// carrierLoopACLC is the DVB-S2 carrier loop table. The reference driver's is
// eighty-three rows of twenty columns -- ten symbol rate bands, each with a
// pilots-on and a pilots-off value -- but every row holds the same value in all
// twenty, so neither symbol rate nor pilots change the answer.
var carrierLoopACLC = map[modcode]byte{
	0x01: 0x07, // FE_SAT_QPSK_14
	0x02: 0x07, // FE_SAT_QPSK_13
	0x03: 0x07, // FE_SAT_QPSK_25
	0x04: 0x27, // FE_SAT_QPSK_12
	0x05: 0x27, // FE_SAT_QPSK_35
	0x06: 0x27, // FE_SAT_QPSK_23
	0x07: 0x08, // FE_SAT_QPSK_34
	0x08: 0x08, // FE_SAT_QPSK_45
	0x09: 0x08, // FE_SAT_QPSK_56
	0x0A: 0x08, // FE_SAT_QPSK_89
	0x0B: 0x08, // FE_SAT_QPSK_910
	0x42: 0x07, // FE_SATX_QPSK_13_45
	0x43: 0x27, // FE_SATX_QPSK_9_20
	0x44: 0x27, // FE_SATX_QPSK_11_20
	0x6C: 0x07, // FE_SATX_QPSK_11_45
	0x6D: 0x07, // FE_SATX_QPSK_4_15
	0x6E: 0x07, // FE_SATX_QPSK_14_45
	0x6F: 0x27, // FE_SATX_QPSK_7_15
	0x70: 0x27, // FE_SATX_QPSK_8_15
	0x71: 0x08, // FE_SATX_QPSK_32_45
	0x0C: 0x07, // FE_SAT_8PSK_35
	0x0D: 0x08, // FE_SAT_8PSK_23
	0x0E: 0x08, // FE_SAT_8PSK_34
	0x0F: 0x08, // FE_SAT_8PSK_56
	0x10: 0x08, // FE_SAT_8PSK_89
	0x11: 0x08, // FE_SAT_8PSK_910
	0x47: 0x27, // FE_SATX_8PSK_23_36
	0x48: 0x08, // FE_SATX_8PSK_25_36
	0x49: 0x08, // FE_SATX_8PSK_13_18
	0x72: 0x26, // FE_SATX_8PSK_7_15
	0x73: 0x26, // FE_SATX_8PSK_8_15
	0x74: 0x27, // FE_SATX_8PSK_26_45
	0x75: 0x08, // FE_SATX_8PSK_32_45
	0x45: 0x26, // FE_SATX_8APSK_5_9_L
	0x46: 0x07, // FE_SATX_8APSK_26_45_L
	0x12: 0x28, // FE_SAT_16APSK_23
	0x13: 0x28, // FE_SAT_16APSK_34
	0x14: 0x28, // FE_SAT_16APSK_45
	0x15: 0x28, // FE_SAT_16APSK_56
	0x16: 0x28, // FE_SAT_16APSK_89
	0x17: 0x28, // FE_SAT_16APSK_910
	0x4D: 0x28, // FE_SATX_16APSK_26_45
	0x4E: 0x28, // FE_SATX_16APSK_3_5
	0x50: 0x28, // FE_SATX_16APSK_28_45
	0x51: 0x28, // FE_SATX_16APSK_23_36
	0x53: 0x28, // FE_SATX_16APSK_25_36
	0x54: 0x28, // FE_SATX_16APSK_13_18
	0x55: 0x28, // FE_SATX_16APSK_7_9
	0x56: 0x28, // FE_SATX_16APSK_77_90
	0x76: 0x28, // FE_SATX_16APSK_7_15
	0x77: 0x28, // FE_SATX_16APSK_8_15
	0x78: 0x28, // FE_SATX_16APSK_26_45_S
	0x79: 0x28, // FE_SATX_16APSK_3_5_S
	0x7A: 0x28, // FE_SATX_16APSK_32_45
	0x4A: 0x28, // FE_SATX_16APSK_1_2_L
	0x4B: 0x28, // FE_SATX_16APSK_8_15_L
	0x4C: 0x28, // FE_SATX_16APSK_5_9_L
	0x4F: 0x28, // FE_SATX_16APSK_3_5_L
	0x52: 0x28, // FE_SATX_16APSK_2_3_L
	0x18: 0x28, // FE_SAT_32APSK_34
	0x19: 0x09, // FE_SAT_32APSK_45
	0x1A: 0x09, // FE_SAT_32APSK_56
	0x1B: 0x09, // FE_SAT_32APSK_89
	0x1C: 0x09, // FE_SAT_32APSK_910
	0x59: 0x08, // FE_SATX_32APSK_32_45
	0x5A: 0x08, // FE_SATX_32APSK_11_15
	0x5B: 0x28, // FE_SATX_32APSK_7_9
	0x7B: 0x08, // FE_SATX_32APSK_2_3
	0x7C: 0x28, // FE_SATX_32APSK_32_45_S
	0x57: 0x08, // FE_SATX_32APSK_2_3_L
	0x5D: 0x09, // FE_SATX_64APSK_11_15
	0x5F: 0x09, // FE_SATX_64APSK_7_9
	0x61: 0x09, // FE_SATX_64APSK_4_5
	0x63: 0x09, // FE_SATX_64APSK_5_6
	0x5C: 0x09, // FE_SATX_64APSK_32_45_L
	0x64: 0x09, // FE_SATX_128APSK_3_4
	0x65: 0x09, // FE_SATX_128APSK_7_9
	0x69: 0x09, // FE_SATX_256APSK_32_45
	0x6B: 0x09, // FE_SATX_256APSK_3_4
	0x66: 0x09, // FE_SATX_256APSK_29_45_L
	0x67: 0x09, // FE_SATX_256APSK_2_3_L
	0x68: 0x09, // FE_SATX_256APSK_31_45_L
	0x6A: 0x09, // FE_SATX_256APSK_11_15_L
}

// optimCarrierLoop is the carrier loop coefficient for a modcode.
func optimCarrierLoop(code modcode) byte {
	if value, ok := carrierLoopACLC[code]; ok {
		return value
	}
	return defaultACLC
}
