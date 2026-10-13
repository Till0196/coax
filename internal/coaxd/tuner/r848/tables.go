// Copyright (c) 2015 Luis Alves; Rafael Micro; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Register tables ported from TBS linux_media (tuners/r848_priv.h).

package r848

// registerCount is R848_REG_NUM: the shadow register file starts at 0x08.
const registerCount = 40

// initRegistersHybrid is R848_iniArray_hybrid: the terrestrial/cable power-on
// register image, written from 0x08.
var initRegistersHybrid = [registerCount]byte{
	0x00, 0x00, 0x40, 0x44, 0x17, 0x00, 0x06, 0xf0, 0x00, 0x41, // 0x08-0x11
	0x7b, 0x0b, 0x70, 0x06, 0x6e, 0x20, 0x70, 0x87, 0x96, 0x00, // 0x12-0x1b
	0x10, 0x00, 0x80, 0xa5, 0xb7, 0x00, 0x40, 0xcb, 0x95, 0xf0, // 0x1c-0x25
	0x24, 0x00, 0xfd, 0x8b, 0x17, 0x13, 0x01, 0x07, 0x01, 0x3f, // 0x26-0x2f
}

// initRegistersDVBS is R848_iniArray_dvbs: the satellite power-on register
// image, written from 0x08.
var initRegistersDVBS = [registerCount]byte{
	0x80, 0x05, 0x40, 0x40, 0x1f, 0x1f, 0x07, 0xff, 0x00, 0x40, // 0x08-0x11
	0xf0, 0x0f, 0x4d, 0x06, 0x6f, 0x20, 0x28, 0x83, 0x96, 0x00, // 0x12-0x1b
	0x1c, 0x99, 0xc1, 0x83, 0xb7, 0x00, 0x4f, 0xcb, 0x95, 0xfd, // 0x1c-0x25
	0xa4, 0x01, 0x24, 0x0b, 0x4f, 0x05, 0x01, 0x47, 0x3f, 0x3f, // 0x26-0x2f
}

// lnaHighMid is R848_LNA_HIGH_MID, indexed by tracking-filter type.
var lnaHighMid = [tfTypeCount]uint32{644000, 644000, 644000, 644000, 644000, 500000, 500000, 500000, 500000}

// lnaMidLow is R848_LNA_MID_LOW, indexed by tracking-filter type.
var lnaMidLow = [tfTypeCount]uint32{388000, 388000, 348000, 348000, 348000, 300000, 300000, 300000, 300000}

// lnaLowLowest is R848_LNA_LOW_LOWEST, indexed by tracking-filter type.
var lnaLowLowest = [tfTypeCount]uint32{164000, 164000, 148000, 124000, 124000, 156000, 156000, 108000, 108000}

// tfFreqHigh is R848_TF_Freq_High, indexed by tracking-filter type then band
// step.
var tfFreqHigh = [tfTypeCount][tfBandSteps]uint32{
	{784000, 784000, 776000, 744000, 712000, 680000, 648000, 647000},
	{784000, 784000, 776000, 744000, 712000, 680000, 648000, 647000},
	{784000, 784000, 776000, 744000, 712000, 680000, 648000, 647000},
	{784000, 784000, 776000, 744000, 712000, 680000, 648000, 647000},
	{784000, 784000, 776000, 744000, 712000, 680000, 648000, 647000},
	{784000, 784000, 776000, 680000, 608000, 584000, 536000, 504000},
	{784000, 784000, 776000, 680000, 608000, 584000, 536000, 504000},
	{784000, 776000, 712000, 616000, 584000, 560000, 520000, 504000},
	{784000, 776000, 712000, 616000, 584000, 560000, 520000, 504000},
}

// tfFreqMid is R848_TF_Freq_Mid, indexed by tracking-filter type then band step.
var tfFreqMid = [tfTypeCount][tfBandSteps]uint32{
	{608000, 584000, 560000, 536000, 488000, 440000, 416000, 392000},
	{608000, 584000, 560000, 536000, 488000, 440000, 416000, 392000},
	{608000, 560000, 536000, 488000, 440000, 392000, 376000, 352000},
	{608000, 560000, 536000, 488000, 440000, 392000, 376000, 352000},
	{608000, 560000, 536000, 488000, 440000, 392000, 376000, 352000},
	{488000, 464000, 440000, 416000, 392000, 352000, 320000, 304000},
	{488000, 464000, 440000, 416000, 392000, 352000, 320000, 304000},
	{480000, 464000, 440000, 416000, 392000, 352000, 320000, 304000},
	{480000, 464000, 440000, 416000, 392000, 352000, 320000, 304000},
}

// tfFreqLow is R848_TF_Freq_Low, indexed by tracking-filter type then band step.
var tfFreqLow = [tfTypeCount][tfBandSteps]uint32{
	{320000, 304000, 272000, 240000, 232000, 216000, 192000, 168000},
	{320000, 304000, 272000, 240000, 232000, 216000, 192000, 168000},
	{256000, 240000, 232000, 224000, 216000, 192000, 168000, 160000},
	{256000, 240000, 232000, 192000, 160000, 152000, 144000, 128000},
	{264000, 240000, 192000, 184000, 176000, 152000, 144000, 128000},
	{280000, 248000, 232000, 216000, 192000, 176000, 168000, 160000},
	{280000, 248000, 232000, 216000, 192000, 176000, 168000, 160000},
	{296000, 280000, 256000, 216000, 184000, 168000, 136000, 112000},
	{296000, 280000, 256000, 216000, 184000, 168000, 136000, 112000},
}

// tfFreqLowest is R848_TF_Freq_Lowest, indexed by tracking-filter type then
// band step.
var tfFreqLowest = [tfTypeCount][tfBandSteps]uint32{
	{160000, 120000, 104000, 88000, 80000, 72000, 56000, 48000},
	{160000, 120000, 104000, 88000, 80000, 72000, 56000, 48000},
	{144000, 120000, 104000, 88000, 80000, 72000, 56000, 48000},
	{120000, 96000, 88000, 80000, 72000, 64000, 56000, 48000},
	{104000, 96000, 88000, 80000, 72000, 64000, 56000, 48000},
	{136000, 120000, 104000, 88000, 72000, 64000, 56000, 48000},
	{128000, 120000, 104000, 96000, 80000, 72000, 64000, 56000},
	{104000, 96000, 88000, 80000, 72000, 64000, 56000, 48000},
	{104000, 96000, 88000, 80000, 72000, 64000, 56000, 48000},
}

// tfResultHigh is R848_TF_Result_High, the register value paired with each
// tfFreqHigh step.
var tfResultHigh = [tfTypeCount][tfBandSteps]byte{
	{0x00, 0x00, 0x01, 0x03, 0x04, 0x05, 0x07, 0x07},
	{0x00, 0x00, 0x01, 0x03, 0x04, 0x05, 0x07, 0x07},
	{0x00, 0x00, 0x01, 0x03, 0x04, 0x05, 0x07, 0x07},
	{0x00, 0x00, 0x01, 0x03, 0x04, 0x05, 0x07, 0x07},
	{0x00, 0x00, 0x01, 0x03, 0x04, 0x05, 0x07, 0x07},
	{0x00, 0x00, 0x01, 0x05, 0x0a, 0x0c, 0x13, 0x19},
	{0x00, 0x00, 0x01, 0x05, 0x0a, 0x0c, 0x13, 0x19},
	{0x00, 0x03, 0x07, 0x0c, 0x0e, 0x0f, 0x1a, 0x1a},
	{0x00, 0x03, 0x07, 0x0c, 0x0e, 0x0f, 0x1a, 0x1a},
}

// tfResultMid is R848_TF_Result_Mid, the register value paired with each
// tfFreqMid step.
var tfResultMid = [tfTypeCount][tfBandSteps]byte{
	{0x00, 0x01, 0x03, 0x03, 0x06, 0x0b, 0x0e, 0x11},
	{0x00, 0x01, 0x03, 0x03, 0x06, 0x0b, 0x0e, 0x11},
	{0x00, 0x03, 0x03, 0x06, 0x0b, 0x11, 0x12, 0x19},
	{0x00, 0x03, 0x03, 0x06, 0x0b, 0x11, 0x12, 0x19},
	{0x00, 0x03, 0x03, 0x06, 0x0b, 0x11, 0x12, 0x19},
	{0x06, 0x08, 0x0b, 0x0e, 0x13, 0x17, 0x1e, 0x1f},
	{0x06, 0x08, 0x0b, 0x0e, 0x13, 0x17, 0x1e, 0x1f},
	{0x09, 0x0d, 0x10, 0x12, 0x16, 0x1b, 0x1e, 0x1f},
	{0x09, 0x0d, 0x10, 0x12, 0x16, 0x1b, 0x1e, 0x1f},
}

// tfResultLow is R848_TF_Result_Low, the register value paired with each
// tfFreqLow step.
var tfResultLow = [tfTypeCount][tfBandSteps]byte{
	{0x00, 0x02, 0x04, 0x07, 0x0a, 0x0b, 0x0f, 0x16},
	{0x00, 0x02, 0x04, 0x07, 0x0a, 0x0b, 0x0f, 0x16},
	{0x05, 0x07, 0x0a, 0x0b, 0x0b, 0x0f, 0x16, 0x1a},
	{0x05, 0x07, 0x0a, 0x0f, 0x1a, 0x1a, 0x23, 0x2a},
	{0x05, 0x08, 0x10, 0x13, 0x1a, 0x1a, 0x23, 0x2a},
	{0x05, 0x08, 0x0c, 0x0e, 0x10, 0x14, 0x18, 0x1a},
	{0x05, 0x08, 0x0c, 0x0e, 0x10, 0x14, 0x18, 0x1a},
	{0x00, 0x01, 0x03, 0x07, 0x0d, 0x11, 0x1e, 0x2f},
	{0x00, 0x01, 0x03, 0x07, 0x0d, 0x11, 0x1e, 0x2f},
}

// tfResultLowest is R848_TF_Result_Lowest, the register value paired with each
// tfFreqLowest step.
var tfResultLowest = [tfTypeCount][tfBandSteps]byte{
	{0x00, 0x06, 0x0c, 0x15, 0x1c, 0x1f, 0x3c, 0x3f},
	{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x03, 0x08},
	{0x02, 0x06, 0x0c, 0x15, 0x1c, 0x1f, 0x3c, 0x3f},
	{0x06, 0x11, 0x15, 0x1c, 0x1f, 0x2f, 0x3c, 0x3f},
	{0x04, 0x08, 0x08, 0x08, 0x10, 0x12, 0x13, 0x13},
	{0x06, 0x09, 0x0e, 0x18, 0x25, 0x2f, 0x3c, 0x3f},
	{0x00, 0x04, 0x04, 0x08, 0x08, 0x10, 0x12, 0x13},
	{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x03, 0x08},
	{0x0e, 0x14, 0x18, 0x1e, 0x25, 0x2f, 0x3c, 0x3f},
}

// sysInfoTable is R848_Sys_Sel's per-standard switch, flattened. The fields it
// does not set are applied by sysInfoFor, which reproduces the function's tail.
var sysInfoTable = map[standard]sysInfo{
	stdDvbT6m:         {ifKHz: 4570, filtCalIF: 7450, bw: 0x40, hpfCor: 0x05, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDvbT26m:        {ifKHz: 4570, filtCalIF: 7450, bw: 0x40, hpfCor: 0x05, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDvbT7m:         {ifKHz: 4570, filtCalIF: 7750, bw: 0x40, hpfCor: 0x08, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDvbT27m:        {ifKHz: 4570, filtCalIF: 7750, bw: 0x40, hpfCor: 0x08, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDvbT8m:         {ifKHz: 4570, filtCalIF: 8130, bw: 0x00, hpfCor: 0x09, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDvbT28m:        {ifKHz: 4570, filtCalIF: 8130, bw: 0x00, hpfCor: 0x09, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDvbT217m:       {ifKHz: 1900, filtCalIF: 7800, bw: 0x40, hpfCor: 0x08, filtExtEna: 0x00, filtExtWidest: 0x00, filtExtPoint: 0x00},
	stdDvbT210m:       {ifKHz: 5600, filtCalIF: 10800, bw: 0x00, hpfCor: 0x0C, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x00},
	stdDvbC8m:         {ifKHz: 5070, filtCalIF: 9000, bw: 0x00, hpfCor: 0x0A, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x02},
	stdDvbC6m:         {ifKHz: 5070, filtCalIF: 8025, bw: 0x40, hpfCor: 0x03, filtExtEna: 0x00, filtExtWidest: 0x00, filtExtPoint: 0x00},
	stdJ83b:           {ifKHz: 5070, filtCalIF: 8025, bw: 0x40, hpfCor: 0x03, filtExtEna: 0x00, filtExtWidest: 0x00, filtExtPoint: 0x00},
	stdIsdbT:          {ifKHz: 4063, filtCalIF: 7000, bw: 0x40, hpfCor: 0x08, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdIsdbT4570:      {ifKHz: 4570, filtCalIF: 7250, bw: 0x40, hpfCor: 0x05, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDtmb4570:       {ifKHz: 4570, filtCalIF: 8330, bw: 0x00, hpfCor: 0x0B, filtExtEna: 0x00, filtExtWidest: 0x00, filtExtPoint: 0x00},
	stdDtmb6000:       {ifKHz: 6000, filtCalIF: 9550, bw: 0x00, hpfCor: 0x03, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x02},
	stdDtmb6mBwIf5m:   {ifKHz: 5000, filtCalIF: 7700, bw: 0x40, hpfCor: 0x04, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x00},
	stdDtmb6mBwIf4500: {ifKHz: 4500, filtCalIF: 7000, bw: 0x40, hpfCor: 0x05, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x02},
	stdAtsc:           {ifKHz: 5070, filtCalIF: 7900, bw: 0x40, hpfCor: 0x04, filtExtEna: 0x00, filtExtWidest: 0x00, filtExtPoint: 0x00},
	stdDvbT6mIf5m:     {ifKHz: 5000, filtCalIF: 7800, bw: 0x40, hpfCor: 0x04, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDvbT26mIf5m:    {ifKHz: 5000, filtCalIF: 7800, bw: 0x40, hpfCor: 0x04, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDvbT7mIf5m:     {ifKHz: 5000, filtCalIF: 8300, bw: 0x00, hpfCor: 0x07, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDvbT27mIf5m:    {ifKHz: 5000, filtCalIF: 8300, bw: 0x00, hpfCor: 0x07, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDvbT8mIf5m:     {ifKHz: 5000, filtCalIF: 8500, bw: 0x00, hpfCor: 0x08, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDvbT28mIf5m:    {ifKHz: 5000, filtCalIF: 8500, bw: 0x00, hpfCor: 0x08, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDvbT217mIf5m:   {ifKHz: 5000, filtCalIF: 5900, bw: 0x60, hpfCor: 0x01, filtExtEna: 0x00, filtExtWidest: 0x00, filtExtPoint: 0x00},
	stdDvbC8mIf5m:     {ifKHz: 5000, filtCalIF: 9000, bw: 0x00, hpfCor: 0x0A, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x02},
	stdDvbC6mIf5m:     {ifKHz: 5000, filtCalIF: 8100, bw: 0x40, hpfCor: 0x04, filtExtEna: 0x00, filtExtWidest: 0x00, filtExtPoint: 0x00},
	stdJ83bIf5m:       {ifKHz: 5000, filtCalIF: 8025, bw: 0x40, hpfCor: 0x03, filtExtEna: 0x00, filtExtWidest: 0x00, filtExtPoint: 0x00},
	stdIsdbTIf5m:      {ifKHz: 5000, filtCalIF: 7940, bw: 0x40, hpfCor: 0x04, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x03},
	stdDtmbIf5m:       {ifKHz: 5000, filtCalIF: 8650, bw: 0x00, hpfCor: 0x09, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x02},
	stdAtscIf5m:       {ifKHz: 5000, filtCalIF: 7900, bw: 0x40, hpfCor: 0x04, filtExtEna: 0x00, filtExtWidest: 0x00, filtExtPoint: 0x00},
	stdFm:             {ifKHz: 4570, filtCalIF: 8330, bw: 0x00, hpfCor: 0x0B, filtExtEna: 0x10, filtExtWidest: 0x00, filtExtPoint: 0x00},
}
