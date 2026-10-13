// Copyright (c) Montage; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package m88rs6060

// bbListDBm is m88rs6060_get_gain's bb_list_dBm: a 16x16 lookup of baseband
// power in millidecibel-milliwatts, indexed by the high and low nibbles of
// tuner register 0x96.
var bbListDBm = [16][16]int32{
	{-5000, -4999, -4397, -4044, -3795, -3601, -3442, -3309, -3193, -3090, -2999, -2916, -2840, -2771, -2706, -2647},
	{-2590, -2538, -2488, -2441, -2397, -2354, -2314, -2275, -2238, -2203, -2169, -2136, -2104, -2074, -2044, -2016},
	{-1988, -1962, -1936, -1911, -1886, -1862, -1839, -1817, -1795, -1773, -1752, -1732, -1712, -1692, -1673, -1655},
	{-1636, -1618, -1601, -1584, -1567, -1550, -1534, -1518, -1502, -1487, -1472, -1457, -1442, -1428, -1414, -1400},
	{-1386, -1373, -1360, -1347, -1334, -1321, -1309, -1296, -1284, -1272, -1260, -1249, -1237, -1226, -1215, -1203},
	{-1193, -1182, -1171, -1161, -1150, -1140, -1130, -1120, -1110, -1100, -1090, -1081, -1071, -1062, -1052, -1043},
	{-1034, -1025, -1016, -1007, -999, -990, -982, -973, -965, -956, -948, -940, -932, -924, -916, -908},
	{-900, -893, -885, -877, -870, -862, -855, -848, -840, -833, -826, -819, -812, -805, -798, -791},
	{-784, -778, -771, -764, -758, -751, -745, -738, -732, -725, -719, -713, -706, -700, -694, -688},
	{-682, -676, -670, -664, -658, -652, -647, -641, -635, -629, -624, -618, -612, -607, -601, -596},
	{-590, -585, -580, -574, -569, -564, -558, -553, -548, -543, -538, -533, -528, -523, -518, -513},
	{-508, -503, -498, -493, -488, -483, -479, -474, -469, -464, -460, -455, -450, -446, -441, -437},
	{-432, -428, -423, -419, -414, -410, -405, -401, -397, -392, -388, -384, -379, -375, -371, -367},
	{-363, -358, -354, -350, -346, -342, -338, -334, -330, -326, -322, -318, -314, -310, -306, -302},
	{-298, -294, -290, -287, -283, -279, -275, -271, -268, -264, -260, -257, -253, -249, -246, -242},
	{-238, -235, -231, -227, -224, -220, -217, -213, -210, -206, -203, -199, -196, -192, -189, -186},
}

// SignalStrength reports the input level in dBm. frequencyKHz is the tuned
// RF frequency: the RF and IF gain-stage tables and the calibration offset
// all depend on which of three frequency bands the tuner runs in.
func (demod *Demod) SignalStrength(frequencyKHz uint32) (float64, error) {
	frequencyMHz := frequencyKHz / 1000

	reg5a, err := demod.tunerRead(0x5a)
	if err != nil {
		return 0, err
	}
	reg5f, err := demod.tunerRead(0x5f)
	if err != nil {
		return 0, err
	}
	reg3f, err := demod.tunerRead(0x3f)
	if err != nil {
		return 0, err
	}
	reg77, err := demod.tunerRead(0x77)
	if err != nil {
		return 0, err
	}
	reg76, err := demod.tunerRead(0x76)
	if err != nil {
		return 0, err
	}
	reg96, err := demod.tunerRead(0x96)
	if err != nil {
		return 0, err
	}

	rfGC := uint32(reg5a & 0x0f)
	ifGC := uint32(reg5f & 0x0f)
	tiaGC := uint32((reg3f >> 4) & 0x07)
	bbGC := uint32((reg77 >> 4) & 0x0f)
	pga2GC := uint32(reg76 & 0x3f)
	pga2Cri := pga2GC >> 2
	pga2Crf := pga2GC & 0x03

	rfGS := [13]uint32{0, 276, 278, 283, 272, 294, 296, 292, 292, 299, 305, 292, 300}
	ifGS := [12]uint32{0, 0, 232, 268, 266, 289, 295, 290, 291, 298, 304, 304}
	bbGS := [13]uint32{0, 296, 297, 295, 298, 302, 293, 292, 286, 294, 278, 298, 267}

	var delta uint32
	switch {
	case frequencyMHz >= 1750:
		rfGS[1], rfGS[2] = 240, 260
		ifGS[2], ifGS[3], ifGS[4] = 200, 245, 255
		delta = 800
	case frequencyMHz >= 1350:
		rfGS[12] = 285
		delta = 900
	default:
		rfGS[1], rfGS[2] = 310, 293
		ifGS[2], ifGS[3], ifGS[4] = 270, 290, 280
		ifGS[11] = 320
		delta = 1000
	}

	var rfG, ifG, bbG uint32
	for i := uint32(0); i <= rfGC; i++ {
		rfG += rfGS[i]
	}
	for i := uint32(1); i <= ifGC; i++ {
		ifG += ifGS[i]
	}
	for i := uint32(0); i <= bbGC; i++ {
		bbG += bbGS[i]
	}
	const pga2CriGS, pga2CrfGS, tiaGS = 46, 290, 290
	tiaG := tiaGC * tiaGS
	pga2G := pga2Cri*pga2CriGS + pga2Crf*pga2CrfGS

	totalGain := int64(rfG) + int64(ifG) - int64(tiaG) + int64(bbG) + int64(pga2G)
	bbPower := bbListDBm[(reg96>>4)&0x0f][reg96&0x0f]
	gain := totalGain - int64(delta) - int64(bbPower)

	// m88rs6060_read_status's conversion from get_gain's raw units to
	// millidecibels.
	return float64(-gain*10+2790) / 1000, nil
}
