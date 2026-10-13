// Copyright (c) 2007, 2008 Michael Krufky <mkrufky@linuxtv.org>; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// The frequency maps below are those of the Linux media subsystem's TDA18271
// driver, whose tda18271-maps.c carries the same tables for the same silicon.
// The TDA18211 is the ISDB-T sibling of the TDA18271 and takes the C2 set.

package tda18211

// rfBandTable is tda18271_rf_band, for the top three bits of register 0x04.
var rfBandTable = []tableEntry{
	{152600, 2}, {164700, 3}, {203500, 4}, {457800, 5},
	{865000, 6},
}

// gainTaperTable is tda18271_gain_taper, for the bottom five bits of 0x04.
var gainTaperTable = []tableEntry{
	{100300, 0x08}, {106900, 0x07}, {113400, 0x06}, {119900, 0x05},
	{126500, 0x04}, {133000, 0x03}, {139500, 0x02}, {146100, 0x01},
	{152600, 0x00}, {154300, 0x1f}, {156100, 0x1e}, {157800, 0x1d},
	{159500, 0x1c}, {161200, 0x1b}, {163000, 0x1a}, {164700, 0x19},
	{170200, 0x17}, {175800, 0x16}, {181300, 0x15}, {186900, 0x14},
	{192400, 0x13}, {198000, 0x12}, {203500, 0x11}, {216200, 0x14},
	{228900, 0x13}, {241600, 0x12}, {254400, 0x11}, {267100, 0x10},
	{279800, 0x0f}, {292500, 0x0e}, {305200, 0x0d}, {317900, 0x0c},
	{330700, 0x0b}, {343400, 0x0a}, {356100, 0x09}, {368800, 0x08},
	{381500, 0x07}, {394200, 0x06}, {406900, 0x05}, {419700, 0x04},
	{432400, 0x03}, {445100, 0x02}, {457800, 0x01}, {476300, 0x19},
	{494800, 0x18}, {513300, 0x17}, {531800, 0x16}, {550300, 0x15},
	{568900, 0x14}, {587400, 0x13}, {605900, 0x12}, {624400, 0x11},
	{642900, 0x10}, {661400, 0x0f}, {679900, 0x0e}, {698400, 0x0d},
	{716900, 0x0c}, {735400, 0x0b}, {753900, 0x0a}, {772500, 0x09},
}

// bandpassTable is tda18271_bp_filter, for the bottom three bits of 0x03.
var bandpassTable = []tableEntry{
	{100000, 2}, {140000, 3}, {170000, 4}, {180000, 5},
	{865000, 6},
}

// kmTable is tda18271c2_km, for the K and M bits of register 0x1c.
var kmTable = []tableEntry{
	{350000, 0x30}, {720000, 0x24}, {865000, 0x3c},
}

// irMeasureTable is tda18271_ir_measure, for the bottom three bits of 0x07.
var irMeasureTable = []tableEntry{
	{200000, 5}, {600000, 6}, {865000, 7},
}

// calPLLTable is tda18271c2_cal_pll. The high byte is CAL_POST_DIV_BYTE, the
// low byte the divider the CAL_DIV bytes are computed from.
var calPLLTable = []tableEntry{
	{73250, 0xcc60}, {79875, 0xcb58}, {87875, 0xca50}, {97625, 0xc948},
	{109875, 0xc840}, {125625, 0xc338}, {135250, 0xbd34}, {146500, 0xbc30},
	{159750, 0xbb2c}, {175750, 0xba28}, {195250, 0xb924}, {219750, 0xb820},
	{251250, 0xb31c}, {270500, 0xad1a}, {293000, 0xac18}, {319500, 0xab16},
	{351500, 0xaa14}, {390500, 0xa912}, {439500, 0xa810}, {502500, 0xa30e},
	{541000, 0x9d0d}, {586000, 0x9c0c}, {639000, 0x9b0b}, {703000, 0x9a0a},
	{781000, 0x9909}, {879000, 0x9808},
}

// mainPLLTable is tda18271c2_main_pll, laid out like calPLLTable. The unused C1
// post dividers spell the same divider with bit 3 set.
var mainPLLTable = []tableEntry{
	{90250, 0x4358}, {99375, 0x4250}, {110375, 0x4148}, {124250, 0x4040},
	{132500, 0x373c}, {142000, 0x3638}, {152750, 0x3534}, {165500, 0x3430},
	{180500, 0x332c}, {198750, 0x3228}, {220750, 0x3124}, {248500, 0x3020},
	{265000, 0x271e}, {284000, 0x261c}, {305500, 0x251a}, {331000, 0x2418},
	{361000, 0x2316}, {397500, 0x2214}, {441500, 0x2112}, {497000, 0x2010},
	{530000, 0x170f}, {568000, 0x160e}, {611000, 0x150d}, {662000, 0x140c},
	{722000, 0x130b}, {795000, 0x120a}, {883000, 0x1109},
}
