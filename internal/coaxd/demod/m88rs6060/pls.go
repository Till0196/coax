// Copyright (c) Montage; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package m88rs6060

// plsListEntry is one checkpoint of the Gold code table: a sequence index and
// the 18-bit LFSR state (3 bytes) reached there. Only every 5000th state is
// stored, rather than all 2^18; the walk resumes from the nearest one below.
type plsListEntry struct {
	index uint32
	code  [3]byte
}

var plsList = []plsListEntry{
	{0, [3]byte{0x01, 0x00, 0x00}},
	{5000, [3]byte{0x0d, 0xe0, 0x00}},
	{10000, [3]byte{0x51, 0x15, 0x00}},
	{15000, [3]byte{0xcf, 0xc9, 0x00}},
	{20000, [3]byte{0x67, 0x33, 0x03}},
	{25000, [3]byte{0x02, 0xc9, 0x02}},
	{30000, [3]byte{0xe5, 0xc6, 0x01}},
	{35000, [3]byte{0xdb, 0xc0, 0x03}},
	{40000, [3]byte{0x7c, 0x5f, 0x02}},
	{45000, [3]byte{0x8d, 0x65, 0x00}},
	{50000, [3]byte{0x14, 0x96, 0x00}},
	{55000, [3]byte{0xf7, 0x61, 0x03}},
	{60000, [3]byte{0xbc, 0x28, 0x00}},
	{65000, [3]byte{0x77, 0xa9, 0x01}},
	{70000, [3]byte{0xe7, 0x05, 0x01}},
	{75000, [3]byte{0x88, 0x85, 0x01}},
	{80000, [3]byte{0x2f, 0xbb, 0x02}},
	{85000, [3]byte{0xe1, 0x07, 0x00}},
	{90000, [3]byte{0xd5, 0x67, 0x01}},
	{95000, [3]byte{0x94, 0x37, 0x03}},
	{100000, [3]byte{0x57, 0x39, 0x02}},
	{105000, [3]byte{0xc7, 0x03, 0x00}},
	{110000, [3]byte{0xbf, 0x12, 0x00}},
	{115000, [3]byte{0x50, 0x0e, 0x00}},
	{120000, [3]byte{0xca, 0xc4, 0x00}},
	{125000, [3]byte{0x46, 0xc3, 0x00}},
	{130000, [3]byte{0x2f, 0xc6, 0x01}},
	{135000, [3]byte{0x7c, 0xe5, 0x01}},
	{140000, [3]byte{0xb9, 0x36, 0x01}},
	{145000, [3]byte{0x9d, 0xe5, 0x01}},
	{150000, [3]byte{0xc4, 0x32, 0x01}},
	{155000, [3]byte{0x13, 0xb3, 0x00}},
	{160000, [3]byte{0x0c, 0x9f, 0x02}},
	{165000, [3]byte{0xb2, 0xb5, 0x03}},
	{170000, [3]byte{0xac, 0x7e, 0x01}},
	{175000, [3]byte{0xb6, 0xa2, 0x01}},
	{180000, [3]byte{0xb6, 0x3e, 0x01}},
	{185000, [3]byte{0x17, 0x2c, 0x02}},
	{190000, [3]byte{0xd7, 0x2a, 0x02}},
	{195000, [3]byte{0x93, 0x61, 0x02}},
	{200000, [3]byte{0x67, 0x92, 0x02}},
	{205000, [3]byte{0x38, 0x07, 0x01}},
	{210000, [3]byte{0xb4, 0x5a, 0x01}},
	{215000, [3]byte{0xed, 0x31, 0x02}},
	{220000, [3]byte{0x9e, 0x4d, 0x02}},
	{225000, [3]byte{0x17, 0x08, 0x02}},
	{230000, [3]byte{0x37, 0xb9, 0x00}},
	{235000, [3]byte{0x2c, 0xed, 0x00}},
	{240000, [3]byte{0xe0, 0x64, 0x00}},
	{245000, [3]byte{0x90, 0x39, 0x01}},
	{250000, [3]byte{0x35, 0x0e, 0x01}},
	{255000, [3]byte{0x1c, 0x9e, 0x02}},
	{260000, [3]byte{0x58, 0x78, 0x00}},
}

// calcPLSGoldCode is m88rs6060_calc_PLS_gold_code: the DVB-S2 physical layer
// scrambling code for an 18-bit Gold code index, from an 18-stage LFSR
// (x0..x17 packed across 3 bytes) stepped once per code from a checkpoint.
func calcPLSGoldCode(goldCode uint32) [3]byte {
	entryIndex := min(int(goldCode/5000), len(plsList)-1)
	entry := plsList[entryIndex]

	var x [18]byte
	for i := range 8 {
		x[i] = (entry.code[0] >> i) & 0x01
	}
	for i := range 8 {
		x[8+i] = (entry.code[1] >> i) & 0x01
	}
	x[16] = entry.code[2] & 0x01
	x[17] = (entry.code[2] >> 1) & 0x01

	var code [3]byte
	for i := entry.index; i <= goldCode; i++ {
		code[0] = x[7]<<7 | x[6]<<6 | x[5]<<5 | x[4]<<4 | x[3]<<3 | x[2]<<2 | x[1]<<1 | x[0]
		code[1] = x[15]<<7 | x[14]<<6 | x[13]<<5 | x[12]<<4 | x[11]<<3 | x[10]<<2 | x[9]<<1 | x[8]
		code[2] = x[17]<<1 | x[16]

		feedback := (x[0] ^ x[7]) & 0x01
		for j := range 17 {
			x[j] = x[j+1]
		}
		x[17] = feedback
	}
	return code
}
