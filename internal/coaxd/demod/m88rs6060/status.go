// Copyright (c) Montage; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package m88rs6060

import (
	"errors"
	"fmt"
)

// mesLog10 and mesLoge are m88rs6060_read_status's mes_log10/mes_loge: 1000x
// scaled 10*log10(n) and 10000x scaled ln(n), turning the raw signal/noise
// counts into a CNR without floating point.
var mesLog10 = [80]uint16{
	0, 3010, 4771, 6021, 6990, 7781, 8451, 9031, 9542, 10000,
	10414, 10792, 11139, 11461, 11761, 12041, 12304, 12553, 12788, 13010,
	13222, 13424, 13617, 13802, 13979, 14150, 14314, 14472, 14624, 14771,
	14914, 15052, 15185, 15315, 15441, 15563, 15682, 15798, 15911, 16021,
	16128, 16232, 16335, 16435, 16532, 16628, 16721, 16812, 16902, 16990,
	17076, 17160, 17243, 17324, 17404, 17482, 17559, 17634, 17709, 17782,
	17853, 17924, 17993, 18062, 18129, 18195, 18261, 18325, 18388, 18451,
	18513, 18573, 18633, 18692, 18751, 18808, 18865, 18921, 18976, 19031,
}

var mesLoge = [32]uint16{
	0, 6931, 10986, 13863, 16094, 17918, 19459, 20794, 21972, 23026,
	23979, 24849, 25649, 26391, 27081, 27726, 28332, 28904, 29444, 29957,
	30445, 30910, 31355, 31781, 32189, 32581, 32958, 33322, 33673, 34012,
	34340, 34657,
}

// Status is one status query's result.
type Status struct {
	Locked bool
	// CNRdB is the carrier-to-noise ratio in dB. HasCNR is false until the
	// Viterbi stage locks, which is when the chip starts measuring.
	CNRdB  float64
	HasCNR bool
}

// lockMask returns register 0x0D's mask and expected value per delivery
// system, from m88rs6060_read_status.
func lockMask(system string) (mask, want byte) {
	if system == "DVBS" {
		return 0xf7, 0xf7
	}
	return 0x8f, 0x8f
}

// Status reports the lock state and CNR. Signal strength is separate, as
// SignalStrength; BER is not exposed, since the error counters need
// call-to-call state and nothing here scans for BER.
func (demod *Demod) Status() (Status, error) {
	if !demod.warm {
		return Status{}, errors.New("M88RS6060 is not initialized")
	}
	if demod.system != "DVBS" && demod.system != "DVBS2" && demod.system != "DVBS2X" {
		return Status{}, fmt.Errorf("M88RS6060 has no status for delivery system %q", demod.system)
	}
	lock, err := demod.regRead(0x0d)
	if err != nil {
		return Status{}, err
	}
	mask, want := lockMask(demod.system)
	if lock&mask != want {
		return Status{}, nil
	}

	cnr, err := demod.cnr()
	if err != nil {
		return Status{Locked: true}, err
	}
	return Status{Locked: true, CNRdB: cnr, HasCNR: true}, nil
}

// cnr is the CNR portion of m88rs6060_read_status's FE_HAS_VITERBI branch.
func (demod *Demod) cnr() (float64, error) {
	if demod.system == "DVBS" {
		var total uint32
		for range 10 {
			value, err := demod.regRead(0xff)
			if err != nil {
				return 0, err
			}
			total += uint32(value)
		}
		temp := min(total/80, 32)
		if temp <= 1 {
			return 0, nil
		}
		// mesLoge is 10000*ln(temp); *10/23 converts to the millidecibels the
		// DVB-S2 branch gets straight from mesLog10 (23 ~ 10*ln(10) = 23.026).
		return float64(uint32(mesLoge[temp-1])*10/23) / 1000, nil
	}

	var noiseTotal, signalTotal uint32
	for range 10 {
		buf, err := demod.regReadMulti(0x8c, 3)
		if err != nil {
			return 0, err
		}
		noise := (uint32(buf[1])<<6 | uint32(buf[0])&0x3f) >> 2
		signal := (uint32(buf[2]) * uint32(buf[2])) >> 1
		noiseTotal += noise
		signalTotal += signal
	}
	noise := noiseTotal / 10
	signal := signalTotal / 10

	var cnr uint16
	switch {
	case signal == 0:
		cnr = 0
	case noise == 0:
		cnr = 19
	case signal > noise:
		ratio := min(signal/noise, 80)
		cnr = mesLog10[ratio-1]
	case signal < noise:
		ratio := min(noise/signal, 80)
		cnr = mesLog10[ratio-1]
	}
	// mesLog10 is already millidecibels: 1000 * 10*log10(ratio).
	return float64(cnr) / 1000, nil
}
