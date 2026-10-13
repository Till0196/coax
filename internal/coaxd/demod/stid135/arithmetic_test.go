// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stid135

import "testing"

// testMasterClock is the master clock of a chip with the design's 6200 MHz
// oscillator: the 1550 MHz local oscillator divided by twelve.
const testMasterClock = 1_550_000_000 / 12

func TestRoundU32DividerX10(t *testing.T) {
	// Six or more rounds up, so five rounds down where round-to-nearest
	// would round up.
	cases := []struct {
		in   uint32
		want uint32
	}{
		{0, 0}, {4, 0}, {5, 0}, {6, 1}, {9, 1}, {10, 1},
		{15, 1}, {16, 2}, {62000, 6200}, {61995, 6199},
	}
	for _, test := range cases {
		if got := roundU32DividerX10(test.in); got != test.want {
			t.Errorf("roundU32DividerX10(%d) = %d, want %d", test.in, got, test.want)
		}
	}
}

func TestFvcoMHz(t *testing.T) {
	// A 27 MHz crystal, integer part 114, fraction 26700 with the sigma-delta
	// modulator on and no reference division give the design's 6200 MHz. The
	// reference divider at two halves it.
	if got := fvcoMHz(27, 114, 26700, 1, 1); got != 6200 {
		t.Errorf("fvcoMHz with the design ratios = %d, want 6200", got)
	}
	if got := fvcoMHz(27, 114, 26700, 1, 2); got != 3100 {
		t.Errorf("fvcoMHz with the reference halved = %d, want 3100", got)
	}
	// With the modulator off the fraction drops out entirely: 20*27*114/10.
	if got := fvcoMHz(27, 114, 26700, 0, 1); got != 6156 {
		t.Errorf("fvcoMHz with the modulator off = %d, want 6156", got)
	}
}

func TestSetSymbolRateRegister(t *testing.T) {
	// (2^27 / (mclk/10000)) * (sr/10000) / 96, in that fixed order, for the
	// master clock above.
	cases := []struct {
		symbolRate uint32
		want       uint32
	}{
		{22_000_000, 238127},
		{27_500_000, 297658},
		{1_000_000, 10823},
	}
	for _, test := range cases {
		if got := setSymbolRateRegister(testMasterClock, test.symbolRate); got != test.want {
			t.Errorf("setSymbolRateRegister(%d) = %d, want %d", test.symbolRate, got, test.want)
		}
	}
}

func TestSymbolRateFromRegister(t *testing.T) {
	// The inverse scaling; 60965000 is what a locked 22 MSym/s carrier reads.
	cases := []struct {
		register uint32
		want     uint32
	}{
		{60_965_000, 22_001_505},
		{0x1000000, 6_054_687},
		{1 << 20, 378_417},
	}
	for _, test := range cases {
		if got := symbolRateFromRegister(testMasterClock, test.register); got != test.want {
			t.Errorf("symbolRateFromRegister(%d) = %d, want %d", test.register, got, test.want)
		}
	}
}

func TestCarrierBoundaryRegister(t *testing.T) {
	// CFR_UP and CFR_LOW for 1245 MHz against a 1550 MHz oscillator -- a
	// 305 MHz negative offset -- with the fixed 10 MHz search range.
	const tunerFrequency = 1_245_000_000 - 1_550_000_000
	if got := carrierBoundaryRegister(testMasterClock, tunerFrequency+searchRangeHz/2); got != -3_246_080 {
		t.Errorf("upper boundary = %d, want -3246080", got)
	}
	if got := carrierBoundaryRegister(testMasterClock, tunerFrequency-searchRangeHz/2); got != -3_354_624 {
		t.Errorf("lower boundary = %d, want -3354624", got)
	}
	// A carrier sitting on the oscillator itself gives a symmetric pair.
	if got := carrierBoundaryRegister(testMasterClock, searchRangeHz/2); got != 53_248 {
		t.Errorf("upper boundary at zero offset = %d, want 53248", got)
	}
	if got := carrierBoundaryRegister(testMasterClock, -searchRangeHz/2); got != -53_248 {
		t.Errorf("lower boundary at zero offset = %d, want -53248", got)
	}
}

func TestCarrierInitRegister(t *testing.T) {
	// ((offset / 6200) * 6711) / 100, at offsets spanning the front end's
	// range and at the oscillator itself.
	cases := []struct {
		offset int32
		want   int32
	}{
		{1_245_000_000 - 1_550_000_000, -3_301_342},
		{-550_000_000, -5_953_260},
		{0, 0},
		{650_000_000, 7_035_678},
	}
	for _, test := range cases {
		if got := carrierInitRegister(test.offset); got != test.want {
			t.Errorf("carrierInitRegister(%d) = %d, want %d", test.offset, got, test.want)
		}
	}
}

func TestCarrierFrequencyFromRegister(t *testing.T) {
	// The inverse: 12*mclk/2^24 per step, about 92.4 Hz at this clock.
	if got := carrierFrequencyFromRegister(testMasterClock, 1000); got != 92_386 {
		t.Errorf("carrierFrequencyFromRegister(1000) = %d, want 92386", got)
	}
	// The magnitude is taken first, so the negative case mirrors exactly.
	if got := carrierFrequencyFromRegister(testMasterClock, -1000); got != -92_386 {
		t.Errorf("carrierFrequencyFromRegister(-1000) = %d, want -92386", got)
	}
	if got := carrierFrequencyFromRegister(testMasterClock, 0x100000); got != 96_874_999 {
		t.Errorf("carrierFrequencyFromRegister(0x100000) = %d, want 96874999", got)
	}
}

func TestTwosComplement(t *testing.T) {
	cases := []struct {
		raw  int32
		want int32
	}{
		{0, 0}, {1, 1}, {0x7fffff, 0x7fffff}, {0x800000, -0x800000}, {0xffffff, -1},
	}
	for _, test := range cases {
		if got := twosComplement(test.raw, 24); got != test.want {
			t.Errorf("twosComplement(%#x, 24) = %d, want %d", test.raw, got, test.want)
		}
	}
}

func TestTimingOffsetFromRegister(t *testing.T) {
	// That reciprocal order, for a 22 MSym/s carrier.
	cases := []struct {
		register int32
		want     int32
	}{
		{1000, 40},
		{-1000, -40},
		// Zero is replaced by one, so the offset vanishes rather than
		// dividing by zero.
		{0, 0},
	}
	for _, test := range cases {
		if got := timingOffsetFromRegister(22_000_000, test.register); got != test.want {
			t.Errorf("timingOffsetFromRegister(22e6, %d) = %d, want %d", test.register, got, test.want)
		}
	}
}

func TestCarrierWidth(t *testing.T) {
	// The roll-off percentage each enum value stands for. The cases are in
	// ascending percentage order, which is not the enum's own ordering.
	cases := []struct {
		shape rolloff
		want  uint32
	}{
		{rolloff05, 21_000_000},
		{rolloff10, 22_000_000},
		{rolloff15, 23_000_000},
		{rolloff20, 24_000_000},
		{rolloff25, 25_000_000},
		{rolloff35, 27_000_000},
	}
	for _, test := range cases {
		if got := carrierWidth(20_000_000, test.shape); got != test.want {
			t.Errorf("carrierWidth(20e6, %d) = %d, want %d", test.shape, got, test.want)
		}
	}
}

func TestLookupCNR(t *testing.T) {
	// Endpoints and one interpolated point of each table.
	if got := lookupCNR(cnrTableDVBS2, 13393); got != -32 {
		t.Errorf("DVB-S2 at the noisy end = %d, want -32", got)
	}
	if got := lookupCNR(cnrTableDVBS2, 160); got != 998 {
		t.Errorf("DVB-S2 at the quiet end = %d, want 998", got)
	}
	if got := lookupCNR(cnrTableDVBS2, 20000); got != cnrUnavailable {
		t.Errorf("DVB-S2 beyond the noisy end = %d, want %d", got, cnrUnavailable)
	}
	if got := lookupCNR(cnrTableDVBS2, 100); got != cnrSaturated {
		t.Errorf("DVB-S2 beyond the quiet end = %d, want %d", got, cnrSaturated)
	}
	// 2484 and 2214 are 14.8 and 15.8 dB, so halfway is 15.3 dB.
	if got := lookupCNR(cnrTableDVBS2, (2484+2214)/2); got != 153 {
		t.Errorf("DVB-S2 midway between two entries = %d, want 153", got)
	}
	if got := lookupCNR(cnrTableDVBS1, 9200); got != -2 {
		t.Errorf("DVB-S at the noisy end = %d, want -2", got)
	}
	if got := lookupCNR(cnrTableDVBS1, 355); got != 328 {
		t.Errorf("DVB-S at the quiet end = %d, want 328", got)
	}
}

func TestCarrierLoopTable(t *testing.T) {
	// One row per constellation group, and the fallback for an unnamed
	// modcode.
	cases := []struct {
		code modcode
		want byte
	}{
		{0x01, 0x07},        // FE_SAT_QPSK_14
		{0x04, 0x27},        // FE_SAT_QPSK_12
		{0x07, 0x08},        // FE_SAT_QPSK_34
		{0x0c, 0x07},        // FE_SAT_8PSK_35
		{0x72, 0x26},        // FE_SATX_8PSK_7_15
		{0x12, 0x28},        // FE_SAT_16APSK_23
		{0x18, 0x28},        // FE_SAT_32APSK_34
		{0x19, 0x09},        // FE_SAT_32APSK_45
		{0x5d, 0x09},        // FE_SATX_64APSK_11_15
		{0x64, 0x09},        // FE_SATX_128APSK_3_4
		{0x6b, 0x09},        // FE_SATX_256APSK_3_4
		{0x00, defaultACLC}, // FE_SAT_DUMMY_PLF, which the table omits
		{0x82, defaultACLC}, // FE_SATX_1024APSK, likewise
	}
	for _, test := range cases {
		if got := optimCarrierLoop(test.code); got != test.want {
			t.Errorf("optimCarrierLoop(%#02x) = %#02x, want %#02x", test.code, got, test.want)
		}
	}
	if len(carrierLoopACLC) != 83 {
		t.Errorf("the carrier loop table has %d rows, want the reference driver's 83",
			len(carrierLoopACLC))
	}
}

func TestClassify(t *testing.T) {
	// The range tests, sampled at the ends of each range they name.
	cases := []struct {
		code modcode
		want modulation
	}{
		{0x01, modulationQPSK}, {0x0b, modulationQPSK},
		{0x20, modulationQPSK}, {0x25, modulationQPSK},
		{0x42, modulationQPSK}, {0x71, modulationQPSK},
		{0x0c, modulation8PSK}, {0x11, modulation8PSK},
		{0x7d, modulation8PSK}, {0x45, modulation8PSK},
		{0x12, modulation16APSK}, {0x80, modulation16APSK},
		{0x4a, modulation16APSK}, {0x52, modulation16APSK},
		{0x18, modulation32APSK}, {0x7e, modulation32APSK},
		{0x57, modulation32APSK},
		{0x40, modulationVLSNR}, {0x41, modulationVLSNR},
		{0x5d, modulation64APSK}, {0x81, modulation64APSK},
		{0x64, modulation128APSK}, {0x65, modulation128APSK},
		{0x69, modulation256APSK}, {0x7f, modulation256APSK},
		{0x6a, modulation256APSK},
		{0x82, modulation1024APSK},
		{0x00, modulationUnknown}, {0x1d, modulationUnknown},
	}
	for _, test := range cases {
		if got := classify(test.code); got != test.want {
			t.Errorf("classify(%#02x) = %d, want %d", test.code, got, test.want)
		}
	}
}

func TestLockTimeouts(t *testing.T) {
	// The ladder with the fourfold multipliers applied to both results.
	cases := []struct {
		symbolRate uint32
		demodMs    uint32
		fecMs      uint32
	}{
		{1_000_000, 12000, 8000},
		{2_000_000, 10000, 5200},
		{5_000_000, 4000, 2600},
		{10_000_000, 2800, 1400},
		{20_000_000, 1600, 800},
		{30_000_000, 1200, 800},
	}
	for _, test := range cases {
		demodMs, fecMs := lockTimeouts(test.symbolRate)
		if demodMs != test.demodMs || fecMs != test.fecMs {
			t.Errorf("lockTimeouts(%d) = %d, %d, want %d, %d",
				test.symbolRate, demodMs, fecMs, test.demodMs, test.fecMs)
		}
	}
}
