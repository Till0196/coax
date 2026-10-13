// Copyright (c) 2015 Luis Alves; Rafael Micro; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package r848

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// standardFor maps a channel file's delivery system and bandwidth onto the
// tuner's standard. The TBS5520 wires the R848's IF to the AVL6882's fixed 5 MHz
// input, so only the IF_5M variants are reachable.
func standardFor(delivery string, bandwidthHz uint32) (standard, error) {
	switch tuning.DemodDeliverySystem(delivery) {
	case "DVBC", "DVBC/ANNEXA":
		if bandwidthHz <= 6_000_000 {
			return stdDvbC6mIf5m, nil
		}
		return stdDvbC8mIf5m, nil
	case "DVBS", "DVBS2", "DVBS2X":
		return stdDvbS, nil
	case "DVBT", "DVBT2":
		switch {
		case bandwidthHz <= 1_700_000:
			return stdDvbT217mIf5m, nil
		case bandwidthHz <= 6_000_000:
			return stdDvbT26mIf5m, nil
		case bandwidthHz <= 7_000_000:
			return stdDvbT27mIf5m, nil
		default:
			return stdDvbT28mIf5m, nil
		}
	default:
		return stdSize, fmt.Errorf("R848 does not support delivery system %q", delivery)
	}
}

// setStandard is r848_set_standard: it loads the standard's register image,
// measures the channel filter code if not cached, and applies the filter,
// current and clock settings.
func (tuner *Tuner) setStandard(system standard) error {
	if system != tuner.standard {
		if err := tuner.initRegisters(system); err != nil {
			return err
		}
	}
	tuner.sys = tuner.sysInfoFor(system)

	// The calibration is slow, so it is cached per standard; it also scribbles
	// over the register image, hence the reload afterwards.
	cal := &tuner.filter[system]
	if !cal.measured {
		const filterCalGap = 8
		code, err := tuner.filterCalibrate(tuner.sys.filtCalIF, tuner.sys.bw, filterCalGap)
		if err != nil {
			return fmt.Errorf("R848 channel filter calibration: %w", err)
		}
		cal.code, cal.bw, cal.measured = code, 0, true
		if err := tuner.initRegisters(system); err != nil {
			return err
		}
	}

	// Filter auto-extension has to be set before the corner it constrains.
	if err := tuner.setBits(0x13, 0xef, tuner.sys.filtExtEna); err != nil {
		return err
	}
	code := cal.code
	if tuner.sys.filtExtEna == 0x10 {
		if code < 2 {
			code = 2
		}
		if tuner.sys.filtExtPoint&0x02 == 0x02 { // HPF+3
			if tuner.sys.hpfCor > 12 {
				tuner.sys.hpfCor = 12
			}
		} else if tuner.sys.hpfCor > 14 { // HPF+1
			tuner.sys.hpfCor = 15
		}
	}

	if err := tuner.setBits(0x12, 0xf0, code); err != nil { // LPF fine code
		return err
	}
	// The coarse-bandwidth field is only shadowed here, not written on its own:
	// the HPF write below carries it out.
	*tuner.shadow(0x13) = *tuner.shadow(0x13)&0x9f | cal.bw
	if err := tuner.setBits(0x13, 0x70, tuner.sys.hpfCor|tuner.sys.v17M); err != nil {
		return err
	}

	for _, step := range []struct{ register, keep, value byte }{
		{0x0b, 0xbf, tuner.sys.tfCur},    // tracking filter current
		{0x12, 0x9f, tuner.sys.filtCur},  // filter current
		{0x0c, 0xfb, tuner.sys.swbufCur}, // switch buffer current
	} {
		if err := tuner.setBits(step.register, step.keep, step.value); err != nil {
			return err
		}
	}

	// Filter compensation, 3 dB step and extension point share register 0x26.
	*tuner.shadow(0x26) = *tuner.shadow(0x26)&0x9f | tuner.sys.filtComp
	*tuner.shadow(0x26) = *tuner.shadow(0x26)&0xf7 | tuner.sys.filt3dB
	if err := tuner.setBits(0x26, 0xf8, 0x04|tuner.sys.filtExtPoint); err != nil {
		return err
	}
	if err := tuner.setBits(0x1a, 0xfc, tuner.sys.swcapClk); err != nil {
		return err
	}
	if err := tuner.setBits(0x26, 0x7f, tuner.sys.naPwrDet); err != nil {
		return err
	}
	if err := tuner.setBits(0x17, 0xfe, 0x01); err != nil { // GPO high
		return err
	}

	tuner.standard = system
	return nil
}

// setFrequency is r848_set_frequency: it points the mixer, PLL and tracking
// filter at a channel and applies the frequency-dependent AGC settings.
func (tuner *Tuner) setFrequency() error {
	if tuner.frequency < 40000 {
		return fmt.Errorf("R848: %d kHz is below the tuner's 40 MHz floor", tuner.frequency)
	}
	if tuner.standard != stdDvbS && tuner.frequency > 1_002_000 {
		return fmt.Errorf("R848: %d kHz is above the terrestrial tuner's 1002 MHz ceiling", tuner.frequency)
	}
	if tuner.standard == stdDvbS && tuner.frequency > 2_400_000 {
		return fmt.Errorf("R848: %d kHz is above the satellite tuner's 2400 MHz ceiling", tuner.frequency)
	}

	loKHz := tuner.frequency + tuner.sys.ifKHz
	if err := tuner.setMux(loKHz, tuner.frequency, tuner.standard); err != nil {
		return err
	}
	if err := tuner.setPLL(loKHz, tuner.standard); err != nil {
		return err
	}
	if err := tuner.setTrackingFilter(tuner.frequency, tuner.config.SetTfType); err != nil {
		return err
	}

	// Q control below 472 MHz, plus the extra 1.5 k step in 300-472 MHz.
	qControl := byte(0x00)
	if tuner.frequency <= 472000 {
		qControl = 0x10
	}
	medQControl := byte(0x00)
	if tuner.frequency >= 300000 && tuner.frequency <= 472000 {
		medQControl = 0x01
	}
	if err := tuner.setBits(0x0e, 0xee, qControl|medQControl); err != nil {
		return err
	}

	// The 3-6 shrink is enabled (bit clear) across 300-550 MHz.
	shrink := byte(0x04)
	if tuner.frequency >= 300000 && tuner.frequency <= 550000 {
		shrink = 0x00
	}
	if err := tuner.setBits(0x0b, 0xfb, shrink); err != nil {
		return err
	}
	if err := tuner.setBits(0x18, 0xfd, 0x00); err != nil { // crystal AAC off
		return err
	}

	info := tuner.sysFreqInfoFor(tuner.standard, tuner.frequency)
	for _, step := range []struct{ register, keep, value byte }{
		{0x23, 0xf8, info.lnaTop},
		{0x1f, 0x00, info.lnaVthL},
		{0x24, 0xf0, info.mixerTop},
		{0x20, 0x00, info.mixerVthL},
		{0x22, 0x1f, info.rfTop},
		{0x24, 0x0f, info.nrbTop},
		{0x23, 0x3f, info.nrbBW},
		{0x0c, 0xbf, info.bypLPF},
		{0x2e, 0xf1, info.rfFastDischarge},
		{0x16, 0x1f, info.rfSlowDischarge},
		{0x26, 0xef, info.rfpdPulseEnable},
		{0x2b, 0xe0, info.lnaFastDischarge},
		{0x16, 0xe3, info.lnaSlowDischarge},
		{0x11, 0x7f, info.lnapdPulseEnable},
		{0x1a, 0xe3, info.agcClk},
		{0x19, 0xff, 0x80}, // no clock out
	} {
		if err := tuner.setBits(step.register, step.keep, step.value); err != nil {
			return err
		}
	}

	// DVB-T2 needs the AGC to settle and the tracking filter forced plain.
	switch tuner.standard {
	case stdDvbT26m, stdDvbT27m, stdDvbT28m, stdDvbT217m, stdDvbT210m,
		stdDvbT26mIf5m, stdDvbT27mIf5m, stdDvbT28mIf5m, stdDvbT217mIf5m:
		time.Sleep(100 * time.Millisecond)
		if err := tuner.setBits(0x0b, 0xff, 0x80); err != nil {
			return err
		}
		if err := tuner.setBits(0x0a, 0xdf, 0x00); err != nil {
			return err
		}
	}
	return nil
}

// Output modes for the satellite path. Only differential is wired on the boards
// here; outputSingleEnded is kept so both values are named.
const (
	outputDifferential = false
	outputSingleEnded  = true
)

// setDVBS is R848_DVBS_Setting: the satellite path tunes directly rather than
// through an IF, so it skips the mux and tracking filter and programs a baseband
// filter width from the symbol rate instead.
func (tuner *Tuner) setDVBS(singleEnded, agcPositive bool) error {
	tuner.standard = stdDvbS
	if err := tuner.initRegisters(stdDvbS); err != nil {
		return err
	}
	if err := tuner.setPLL(tuner.frequency, stdDvbS); err != nil {
		return err
	}

	// LNA detector thresholds, raised across the middle of the band.
	vth := byte(0x83) // 1.14/0.64
	if tuner.frequency >= 1_200_000 && tuner.frequency <= 1_750_000 {
		vth = 0x93 // 1.24/0.64
	}
	if err := tuner.setBits(0x1f, 0x00, vth); err != nil {
		return err
	}
	if tuner.frequency >= 2_000_000 {
		if err := tuner.setBits(0x2e, 0xcf, 0x20); err != nil {
			return err
		}
	}
	// LNA attenuator, where the LNB output is strongest.
	attenuator := byte(0x00)
	if tuner.frequency >= 1_600_000 && tuner.frequency <= 1_950_000 {
		attenuator = 0x20
	}
	if err := tuner.setBits(0x2b, 0xdf, attenuator); err != nil {
		return err
	}

	single := byte(0x00)
	if singleEnded {
		single = 0x80
	}
	if err := tuner.setBits(0x2b, 0x7f, single); err != nil {
		return err
	}
	positive := byte(0x00)
	if agcPositive {
		positive = 0x08
	}
	if err := tuner.setBits(0x2d, 0xf7, positive); err != nil {
		return err
	}

	coarse, fine := basebandFilter(tuner.bandwidth)
	if err := tuner.setBits(0x2f, 0x00, fine<<6|coarse); err != nil {
		return err
	}
	return tuner.setBits(0x17, 0xfe, 0x00) // GPO low
}

// basebandFilter is R848_DVBS_Setting's bandwidth ladder: the transponder's
// occupied bandwidth in kHz to the filter's coarse step and fine-tune bit.
func basebandFilter(bandwidthKHz uint32) (coarse, fine byte) {
	switch {
	case bandwidthKHz > 67400:
		steps := (bandwidthKHz-67400)/1600 + 31
		if (bandwidthKHz-67400)%1600 > 0 {
			steps++
		}
		return byte(steps), 1
	case bandwidthKHz > 62360:
		return 31, 1
	case bandwidthKHz > 38000:
		steps := (bandwidthKHz-38000)/1740 + 16
		if (bandwidthKHz-38000)%1740 > 0 {
			steps++
		}
		return byte(steps), 1
	case bandwidthKHz <= 5000:
		return 0, 0
	case bandwidthKHz <= 8000:
		return 0, 1
	case bandwidthKHz <= 10000:
		return 1, 1
	case bandwidthKHz <= 12000:
		return 2, 1
	case bandwidthKHz <= 14200:
		return 3, 1
	case bandwidthKHz <= 16000:
		return 4, 1
	case bandwidthKHz <= 17800:
		return 5, 0
	case bandwidthKHz <= 18600:
		return 5, 1
	case bandwidthKHz <= 20200:
		return 6, 1
	case bandwidthKHz <= 22400:
		return 7, 1
	case bandwidthKHz <= 24600:
		return 9, 1
	case bandwidthKHz <= 25400:
		return 10, 0
	case bandwidthKHz <= 26000:
		return 10, 1
	case bandwidthKHz <= 27200:
		return 11, 0
	case bandwidthKHz <= 27800:
		return 11, 1
	case bandwidthKHz <= 30200:
		return 12, 1
	case bandwidthKHz <= 32600:
		return 13, 1
	case bandwidthKHz <= 33800:
		return 14, 1
	case bandwidthKHz <= 36800:
		return 15, 1
	default: // up to 38000
		return 16, 1
	}
}

// Params is a channel to tune to.
type Params struct {
	DeliverySystem string
	// FrequencyHz is the RF frequency for terrestrial and cable. For satellite
	// the channel file's FREQUENCY is already in kHz, so pass it multiplied by
	// 1000 and this converts back -- the tuner works in kHz throughout.
	FrequencyHz uint32
	BandwidthHz uint32
	// SymbolRate is the satellite transponder's symbol rate, ignored for the
	// terrestrial and cable paths.
	SymbolRate uint32
}

// Tune points the tuner at a channel and waits briefly for its PLL to lock.
func (tuner *Tuner) Tune(params Params) error {
	system, err := standardFor(params.DeliverySystem, params.BandwidthHz)
	if err != nil {
		return err
	}

	if system == stdDvbS {
		tuner.frequency = params.FrequencyHz / 1000
		// The occupied bandwidth from the symbol rate, doubled because the filter
		// setting is per side of the carrier.
		tuner.bandwidth = (params.SymbolRate/200*135 + 2_000_000) / 1000 * 2
		if err := tuner.setDVBS(outputDifferential, false); err != nil {
			return err
		}
	} else {
		tuner.frequency = params.FrequencyHz / 1000
		if err := tuner.setStandard(system); err != nil {
			return err
		}
		if err := tuner.setFrequency(); err != nil {
			return err
		}
	}

	for range 5 {
		locked, err := tuner.PLLLocked()
		if err != nil {
			return err
		}
		if locked {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	// An unlocked tuner is not a failure: the demodulator's own lock decides
	// whether a channel is there.
	return nil
}
