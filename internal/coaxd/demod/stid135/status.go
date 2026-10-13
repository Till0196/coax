// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stid135

import "time"

// Status is one status query's result.
type Status struct {
	Locked bool
	// CNRdB is the carrier-to-noise ratio in dB. HasCNR is false until the
	// chip locks, which is when the reading means anything.
	CNRdB  float64
	HasCNR bool
}

// calibration is one point of fe_lla_lookup_t: C/N in tenths of a dB against
// the noise register reading for it.
type calibration struct {
	realValue int32
	register  int32
}

// cnrTableDVBS2 maps the physical-layer-header normalised noise indicator to
// carrier-to-noise ratio. The register falls as the ratio rises, which is why
// the search below works downwards.
var cnrTableDVBS2 = []calibration{
	{-32, 13393}, {-22, 12561}, {-17, 12200},
	{-12, 11767}, {-2, 11030}, {8, 10208},
	{18, 9441}, {28, 8695}, {38, 7944},
	{48, 7275}, {58, 6607}, {68, 5992},
	{78, 5384}, {88, 4851}, {98, 4357},
	{108, 3936}, {118, 3488}, {128, 3134},
	{138, 2795}, {148, 2484}, {158, 2214},
	{168, 1997}, {178, 1767}, {188, 1585},
	{198, 1401}, {208, 1264}, {218, 1118},
	{228, 996}, {238, 896}, {248, 798},
	{258, 713}, {268, 645}, {278, 596},
	{288, 591}, {298, 558}, {308, 493},
	{318, 459}, {328, 430}, {338, 415},
	{348, 400}, {358, 385}, {368, 382},
	{378, 380}, {388, 371}, {398, 339},
	{498, 280}, {598, 230}, {698, 205},
	{798, 185}, {898, 170}, {998, 160},
}

// cnrTableDVBS1 is read against the symbol normalised noise indicator.
var cnrTableDVBS1 = []calibration{
	{-2, 9200}, {8, 8800}, {18, 8350}, {28, 7920},
	{38, 7492}, {48, 7010}, {58, 6475}, {68, 5920},
	{78, 5378}, {88, 4848}, {98, 4370}, {108, 3909},
	{118, 3500}, {128, 3125}, {138, 2788}, {148, 2485},
	{158, 2226}, {168, 1975}, {178, 1759}, {188, 1573},
	{198, 1400}, {208, 1247}, {218, 1112}, {228, 1000},
	{238, 890}, {248, 800}, {258, 710}, {268, 636},
	{278, 581}, {288, 524}, {298, 478}, {308, 440},
	{318, 394}, {328, 355},
}

// cnrUnavailable and cnrSaturated are reported outside the table: above the
// noisiest entry there is no answer, below the quietest it is a hundred dB.
const (
	cnrUnavailable = -100
	cnrSaturated   = 1000
)

// lookupCNR binary-searches the table and interpolates linearly.
func lookupCNR(table []calibration, reading int32) int32 {
	low, high := 0, len(table)-1
	if reading < table[high].register || reading > table[low].register {
		if reading < table[low].register {
			return cnrSaturated
		}
		return cnrUnavailable
	}
	for high-low > 1 {
		middle := (high + low) >> 1
		if reading <= table[low].register && reading >= table[middle].register {
			high = middle
		} else {
			low = middle
		}
	}
	span := table[high].register - table[low].register
	return (reading-table[low].register)*(table[high].realValue-table[low].realValue)/span +
		table[low].realValue
}

// Status reports the lock state and the carrier-to-noise ratio. The noise
// indicator is averaged over eight readings a millisecond apart: a single
// reading moves by more than a dB from frame to frame.
func (demod *Demod) Status() (Status, error) {
	if !demod.warm {
		return Status{}, ErrNotInitialized
	}
	lock, err := demod.fieldRead(demod.demodReg(regDSTATUS), fldLOCKDEFINITIF)
	if err != nil {
		return Status{}, err
	}
	status := Status{Locked: lock != 0}
	if !status.Locked {
		return status, nil
	}
	standard, err := demod.getStandard()
	if err != nil {
		return status, err
	}
	table, noiseRegister := cnrTableDVBS1, demod.demodReg(regNNOSDATAT1)
	if standard == standardDVBS2 {
		table, noiseRegister = cnrTableDVBS2, demod.demodReg(regNNOSPLHT1)
	}
	total := int32(0)
	for reading := 0; reading < 8; reading++ {
		raw, err := demod.regReadMulti(noiseRegister, 2)
		if err != nil {
			return status, err
		}
		total += int32(raw[0])<<8 | int32(raw[1])
		time.Sleep(time.Millisecond)
	}
	tenths := lookupCNR(table, total/8)
	if tenths == cnrUnavailable {
		return status, nil
	}
	status.CNRdB = float64(tenths) / 10
	status.HasCNR = true
	return status, nil
}

// GainIntegrator is the RF gain control's integrator for the input this
// demodulator is currently on: the sixteen-bit value the search consults to
// decide whether there is anything on the lead. It rises with input level and
// belongs to the RF input rather than the demodulator, so two demodulators
// sharing an input share a reading.
//
// There is deliberately no SignalStrength here: a dBm figure would need the
// front end's gain curves, which depend on a calibration this package does not
// perform.
func (demod *Demod) GainIntegrator() (uint16, error) {
	if !demod.warm {
		return 0, ErrNotInitialized
	}
	return demod.readGainIntegrator()
}
