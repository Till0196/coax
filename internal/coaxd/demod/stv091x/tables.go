// Copyright (c) Ralph Metzler; Marcus Metzler; Digital Devices GmbH; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stv091x

// calibration is one measured pair: a raw register reading and what it means.
// Every table below descends in raw as the value rises -- they are noise or
// power estimates, which fall as the signal improves.
type calibration struct {
	value int32
	raw   uint32
}

// snrDVBS is S1_SN_Lookup: carrier-to-noise in tenths of a dB against the
// DVB-S noise estimator (NNOSDATAT), 0 dB to 51 dB.
var snrDVBS = []calibration{
	{0, 9242}, {5, 9105}, {10, 8950}, {15, 8780}, {20, 8566},
	{25, 8366}, {30, 8146}, {35, 7908}, {40, 7666}, {45, 7405},
	{50, 7136}, {55, 6861}, {60, 6576}, {65, 6330}, {70, 6048},
	{75, 5768}, {80, 5492}, {85, 5224}, {90, 4959}, {95, 4709},
	{100, 4467}, {105, 4236}, {110, 4013}, {115, 3800}, {120, 3598},
	{125, 3406}, {130, 3225}, {135, 3052}, {140, 2889}, {145, 2733},
	{150, 2587}, {160, 2318}, {170, 2077}, {180, 1862}, {190, 1670},
	{200, 1499}, {210, 1347}, {220, 1213}, {230, 1095}, {240, 992},
	{250, 900}, {260, 826}, {270, 758}, {280, 702}, {290, 653},
	{300, 613}, {310, 579}, {320, 550}, {330, 526}, {350, 490},
	{400, 445}, {450, 430}, {500, 426}, {510, 425},
}

// snrDVBS2 is S2_SN_Lookup: the same for the DVB-S2 estimator (NNOSPLHT). It
// starts at -0.3 dB because the standard locks below the noise floor.
var snrDVBS2 = []calibration{
	{-30, 13950}, {-25, 13580}, {-20, 13150}, {-15, 12760}, {-10, 12345},
	{-5, 11900}, {0, 11520}, {5, 11080}, {10, 10630}, {15, 10210},
	{20, 9790}, {25, 9390}, {30, 8970}, {35, 8575}, {40, 8180},
	{45, 7800}, {50, 7430}, {55, 7080}, {60, 6720}, {65, 6320},
	{70, 6060}, {75, 5760}, {80, 5480}, {85, 5200}, {90, 4930},
	{95, 4680}, {100, 4425}, {105, 4210}, {110, 3980}, {115, 3765},
	{120, 3570}, {125, 3315}, {130, 3140}, {135, 2980}, {140, 2820},
	{145, 2670}, {150, 2535}, {160, 2270}, {170, 2035}, {180, 1825},
	{190, 1650}, {200, 1485}, {210, 1340}, {220, 1212}, {230, 1100},
	{240, 1000}, {250, 910}, {260, 836}, {270, 772}, {280, 718},
	{290, 671}, {300, 635}, {310, 602}, {320, 575}, {330, 550},
	{350, 517}, {400, 480}, {450, 466}, {500, 464}, {510, 463},
}

// padcLookup is PADC_Lookup: power at the demodulator's own input, in
// hundredths of a dBm, against the sum of squares of its I and Q samples.
//
// Two rows are out of order -- -15 dBm sits between -11 and -13 -- and are left
// that way: lookupDescending assumes monotonically falling readings, so the
// entries either side of the swap interpolate against the wrong neighbour, but
// correcting it would move every strength away from what the cards in the field
// report.
var padcLookup = []calibration{
	{0, 118000},
	{-100, 93600},
	{-200, 74500},
	{-300, 59100},
	{-400, 47000},
	{-500, 37300},
	{-600, 29650},
	{-700, 23520},
	{-900, 14850},
	{-1100, 9380},
	{-1500, 3730},
	{-1300, 5910},
	{-1700, 2354},
	{-1900, 1485},
	{-2000, 1179},
	{-2100, 1000},
}

// lookupDescending is TableLookup for a table whose readings fall as its values
// rise: linear interpolation between the bracketing points, clamped outside.
func lookupDescending(table []calibration, reading uint32) int32 {
	last := len(table) - 1
	if reading >= table[0].raw {
		return table[0].value
	}
	if reading <= table[last].raw {
		return table[last].value
	}
	lower, upper := 0, last
	for upper-lower > 1 {
		middle := (upper + lower) / 2
		if table[lower].raw >= reading && reading >= table[middle].raw {
			upper = middle
		} else {
			lower = middle
		}
	}
	span := int32(table[upper].raw) - int32(table[lower].raw)
	value := table[lower].value
	if span != 0 {
		value += (int32(reading) - int32(table[lower].raw)) *
			(table[upper].value - table[lower].value) / span
	}
	return value
}

// carrierLoopColumns: five symbol rate bands, each with a pilots-on and a
// pilots-off entry.
const carrierLoopColumns = 10

// carrierLoop is S2CarLoop: the carrier tracking loop bandwidth for every DVB-S2
// modcod. Rows run QPSK 1/4 to 32APSK 9/10 in the chip's modcod order; each
// row's ten entries are 2 MSym/s pilots on and off, then 5, 10, 20 and 30 MSym/s
// likewise.
var carrierLoop = [28 * carrierLoopColumns]byte{
	// QPSK 1/4
	0x0c, 0x3c, 0x0b, 0x3c, 0x2a, 0x2c, 0x2a, 0x1c, 0x3a, 0x3b,
	// QPSK 1/3
	0x0c, 0x3c, 0x0b, 0x3c, 0x2a, 0x2c, 0x3a, 0x0c, 0x3a, 0x2b,
	// QPSK 2/5
	0x1c, 0x3c, 0x1b, 0x3c, 0x3a, 0x1c, 0x3a, 0x3b, 0x3a, 0x2b,
	// QPSK 1/2
	0x0c, 0x1c, 0x2b, 0x1c, 0x0b, 0x2c, 0x0b, 0x0c, 0x2a, 0x2b,
	// QPSK 3/5
	0x1c, 0x1c, 0x2b, 0x1c, 0x0b, 0x2c, 0x0b, 0x0c, 0x2a, 0x2b,
	// QPSK 2/3
	0x2c, 0x2c, 0x2b, 0x1c, 0x0b, 0x2c, 0x0b, 0x0c, 0x2a, 0x2b,
	// QPSK 3/4
	0x3c, 0x2c, 0x3b, 0x2c, 0x1b, 0x1c, 0x1b, 0x3b, 0x3a, 0x1b,
	// QPSK 4/5
	0x0d, 0x3c, 0x3b, 0x2c, 0x1b, 0x1c, 0x1b, 0x3b, 0x3a, 0x1b,
	// QPSK 5/6
	0x1d, 0x3c, 0x0c, 0x2c, 0x2b, 0x1c, 0x1b, 0x3b, 0x0b, 0x1b,
	// QPSK 8/9
	0x3d, 0x0d, 0x0c, 0x2c, 0x2b, 0x0c, 0x2b, 0x2b, 0x0b, 0x0b,
	// QPSK 9/10
	0x1e, 0x0d, 0x1c, 0x2c, 0x3b, 0x0c, 0x2b, 0x2b, 0x1b, 0x0b,
	// 8PSK 3/5
	0x28, 0x09, 0x28, 0x09, 0x28, 0x09, 0x28, 0x08, 0x28, 0x27,
	// 8PSK 2/3
	0x19, 0x29, 0x19, 0x29, 0x19, 0x29, 0x38, 0x19, 0x28, 0x09,
	// 8PSK 3/4
	0x1a, 0x0b, 0x1a, 0x3a, 0x0a, 0x2a, 0x39, 0x2a, 0x39, 0x1a,
	// 8PSK 5/6
	0x2b, 0x2b, 0x1b, 0x1b, 0x0b, 0x1b, 0x1a, 0x0b, 0x1a, 0x1a,
	// 8PSK 8/9
	0x0c, 0x0c, 0x3b, 0x3b, 0x1b, 0x1b, 0x2a, 0x0b, 0x2a, 0x2a,
	// 8PSK 9/10
	0x0c, 0x1c, 0x0c, 0x3b, 0x2b, 0x1b, 0x3a, 0x0b, 0x2a, 0x2a,
	// 16APSK 2/3
	0x0a, 0x0a, 0x0a, 0x0a, 0x1a, 0x0a, 0x39, 0x0a, 0x29, 0x0a,
	// 16APSK 3/4
	0x0a, 0x0a, 0x0a, 0x0a, 0x0b, 0x0a, 0x2a, 0x0a, 0x1a, 0x0a,
	// 16APSK 4/5
	0x0a, 0x0a, 0x0a, 0x0a, 0x1b, 0x0a, 0x3a, 0x0a, 0x2a, 0x0a,
	// 16APSK 5/6
	0x0a, 0x0a, 0x0a, 0x0a, 0x1b, 0x0a, 0x3a, 0x0a, 0x2a, 0x0a,
	// 16APSK 8/9
	0x0a, 0x0a, 0x0a, 0x0a, 0x2b, 0x0a, 0x0b, 0x0a, 0x3a, 0x0a,
	// 16APSK 9/10
	0x0a, 0x0a, 0x0a, 0x0a, 0x2b, 0x0a, 0x0b, 0x0a, 0x3a, 0x0a,
	// 32APSK 3/4
	0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09,
	// 32APSK 4/5
	0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09,
	// 32APSK 5/6
	0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09,
	// 32APSK 8/9
	0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09,
	// 32APSK 9/10
	0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09, 0x09,
}

// The chip's modcod numbering; only the values compared against here and in the
// tracking optimization are named. The four 9/10 rates are declared in tune.go,
// beside the modcod list they are exceptions in.
const (
	modcodQPSK14 = 1
)

// optimalCarrierLoop is get_optim_cloop: the row for a modcod, the column for
// the symbol rate, and one place further along when there are no pilots.
func optimalCarrierLoop(modCod byte, pilots bool, symbolRate uint32) byte {
	index := 0
	switch {
	case modCod >= modcod32APSK910:
		index = (modcod32APSK910 - modcodQPSK14) * carrierLoopColumns
	case modCod >= modcodQPSK14:
		index = (int(modCod) - modcodQPSK14) * carrierLoopColumns
	}
	switch {
	case symbolRate <= 3_000_000:
		index += 0
	case symbolRate <= 7_000_000:
		index += 2
	case symbolRate <= 15_000_000:
		index += 4
	case symbolRate <= 25_000_000:
		index += 6
	default:
		index += 8
	}
	if !pilots {
		index++
	}
	return carrierLoop[index]
}
