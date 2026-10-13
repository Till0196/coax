// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from cxd2878.c's freia_init/freia_tune/freia_read_rssi, the newer of
// the two tuner cores this family is found with (every recognised chip ID but
// CXD6802 — see sony.Chip.usesASCOT3).

package cxd2878

import (
	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"time"
)

// freiaParams is g_terr_param_table_freia. The columns, the system enumeration
// keying it and the mapping onto it live in the sony package; only these
// numbers are this chip's.
var freiaParams = map[sony.FreiaSystem]sony.FreiaParam{
	sony.Freia8VSB:      sony.FreiaRow(0xff, 0x06, [3]byte{0x0e, 0x0e, 0x0e}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x1a, 0x1d),
	sony.FreiaISDBT6:    sony.FreiaRow(0xff, 0x06, [3]byte{0x0e, 0x0e, 0x0e}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x17, 0x1b),
	sony.FreiaISDBT7:    sony.FreiaRow(0xff, 0x06, [3]byte{0x0e, 0x0e, 0x0e}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x01, 0x19, 0x1a),
	sony.FreiaISDBT8:    sony.FreiaRow(0xff, 0x06, [3]byte{0x0e, 0x0e, 0x0e}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x02, 0x1b, 0x19),
	sony.FreiaDVBT5:     sony.FreiaRow(0xff, 0x06, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x18, 0x1d),
	sony.FreiaDVBT6:     sony.FreiaRow(0xff, 0x06, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x18, 0x1d),
	sony.FreiaDVBT7:     sony.FreiaRow(0xff, 0x06, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x01, 0x1a, 0x1b),
	sony.FreiaDVBT8:     sony.FreiaRow(0xff, 0x06, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x02, 0x1c, 0x1a),
	sony.FreiaDVBT2_1_7: sony.FreiaRow(0xff, 0x06, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x03, 0x16, 0x16),
	sony.FreiaDVBT2_5:   sony.FreiaRow(0xff, 0x06, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x18, 0x1d),
	sony.FreiaDVBT2_6:   sony.FreiaRow(0xff, 0x06, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x18, 0x1d),
	sony.FreiaDVBT2_7:   sony.FreiaRow(0xff, 0x06, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x01, 0x1a, 0x1b),
	sony.FreiaDVBT2_8:   sony.FreiaRow(0xff, 0x06, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x02, 0x1c, 0x1a),
	sony.FreiaCable6:    sony.FreiaRow(0xff, 0x03, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x1a, 0x1c),
	sony.FreiaCable8:    sony.FreiaRow(0xff, 0x03, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x02, 0x1e, 0x1d),
	sony.FreiaSKPOpt:    sony.FreiaRow(0xff, 0x03, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x1b, 0x02),
}

// freiaInit brings the Freia tuner core up: the fixed power-up register
// table, then a CPU-boot check and an SRAM status probe.
func (demod *Demod) freiaInit() error {
	tuner := demod.Tuner()

	if err := tuner.Write(0x01, 0x00); err != nil {
		return err
	}
	if err := tuner.Write(0x67, 0x00); err != nil {
		return err
	}
	if err := tuner.Write(0x43, 0xc0); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x5e, []byte{0x15, 0x00, 0x00}); err != nil {
		return err
	}
	if err := tuner.Write(0x0c, 0x14); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x79, []byte{0x9e, 0x00, 0x00}); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x99, []byte{0xa9, 0x01, 0x00}); err != nil {
		return err
	}
	// The crystal code heading the power-up block describes the oscillator
	// input, not the chip, so it comes from the family's table rather than a
	// constant: 24 MHz is not the only crystal with a code. On the 24 MHz
	// every board here fits, it is 0x18.
	crystal, err := sony.FreiaXtalCode(demod.Config().TunerXtal)
	if err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x81, []byte{
		crystal, 0x84, 0xb5, 0x82, 0x00, 0x00, 0x84, 0x40, 0x10,
		0x00, 0x45, 0x75, 0x01, 0x00, 0x00, 0x00, 0x0a, 0x0c, 0x3f, 0x00,
	}); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x22, []byte{0x00, 0x08}); err != nil {
		return err
	}
	if err := tuner.Write(0x46, 0x00); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)

	cpuStatus, err := tuner.Read(0x1a, 1)
	if err != nil {
		return err
	}
	if cpuStatus[0] != 0x00 {
		return sony.ErrFreiaNotReady
	}

	if err := tuner.WriteMulti(0x17, []byte{0x7f, 0x06}); err != nil {
		return err
	}
	time.Sleep(time.Millisecond)
	sram, err := tuner.Read(0x19, 1)
	if err != nil {
		return err
	}
	sramValue := byte(0x00)
	if sram[0] != 0x00 {
		sramValue = 0x9e
	}
	if err := tuner.Write(0x79, sramValue); err != nil {
		return err
	}
	if err := tuner.Write(0x74, 0x02); err != nil {
		return err
	}
	if err := tuner.Write(0xa0, 0x0b); err != nil {
		return err
	}
	if err := tuner.Write(0x88, 0x00); err != nil {
		return err
	}
	if err := tuner.Write(0x87, 0x80); err != nil {
		return err
	}
	if err := tuner.Write(0x80, 0x01); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x41, []byte{0x00, 0x00}); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x45, []byte{0x0a, 0x00, 0x00, 0x11, 0x00, 0x03}); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0xa6, []byte{0x66, 0x08}); err != nil {
		return err
	}
	return tuner.Write(0x1e, 0xa0)
}

func (demod *Demod) freiaTune(frequencyKHz uint32) error {
	system, err := demod.FreiaSystem()
	if err != nil {
		return err
	}
	param := freiaParams[system]
	tuner := demod.Tuner()

	if err := tuner.Write(0x01, 0x00); err != nil {
		return err
	}
	if err := tuner.Write(0x74, 0x02); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x87, []byte{0x84, 0x40}); err != nil {
		return err
	}
	var mixer []byte
	if system.IsCable() {
		mixer = []byte{0xfc, 0x87, 0xcf, 0x78, 0x05}
	} else {
		mixer = []byte{0xfc, 0x9c, 0x8f, 0x00, 0x75}
	}
	if err := tuner.WriteMulti(0x3c, mixer[:3]); err != nil {
		return err
	}
	if err := tuner.Write(0x52, mixer[3]); err != nil {
		return err
	}
	if err := tuner.Write(0x8b, mixer[4]); err != nil {
		return err
	}
	if err := tuner.Write(0x8d, 0x01); err != nil {
		return err
	}
	analogue := []byte{0x0a, 0x0c}
	if system.IsDVBTT2() {
		analogue = []byte{0x00, 0x02}
	}
	if err := tuner.WriteMulti(0x91, analogue); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x9c, []byte{0x8c, 0x01}); err != nil {
		return err
	}
	if err := tuner.Write(0x7c, 0x01); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x5e, []byte{0x6e, 0x02, 0x9e}); err != nil {
		return err
	}
	if err := tuner.Write(0x7c, 0x00); err != nil {
		return err
	}
	if err := tuner.Write(0x1e, 0xa4); err != nil {
		return err
	}
	if err := tuner.Write(0x5e, 0xee); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x61, []byte{0x01, 0x38, 0x1e, 0x02, 0x24}); err != nil {
		return err
	}
	if err := tuner.WriteMasked(0x67, 0x00, 0x02); err != nil {
		return err
	}

	// The 0x30 bits of the overload byte are left clear for DVB-T and DVB-T2
	// and set for every other system.
	ifOverload := sony.FreiaIFOverload(param, frequencyKHz)
	if !system.IsDVBTT2() {
		ifOverload |= 0x30
	}
	body := append(sony.FreiaTuneBody(param, frequencyKHz, ifOverload),
		0xff, 0x00,
		0xf1, 0x0f, 0x06, 0x03,
	)
	if err := tuner.WriteMulti(0x68, body); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	if err := tuner.Write(0x88, 0x00); err != nil {
		return err
	}
	return tuner.Write(0x87, 0x80)
}

// freiaReadRSSI reads the tuner's IF and RF AGC registers as ascot3ReadRSSI
// does for the older core, but with Freia's own gain curves and a terrestrial
// compensation register the older core does not have.
func (demod *Demod) freiaReadRSSI(frequencyKHz uint32) (int32, error) {
	tuner := demod.Tuner()
	if err := tuner.WriteMulti(0x87, []byte{0x84, 0x41}); err != nil {
		return 0, err
	}
	if err := tuner.WriteMulti(0x17, []byte{0x9e, 0x06}); err != nil {
		return 0, err
	}
	time.Sleep(2 * time.Millisecond)
	status, err := tuner.Read(0x1a, 1)
	if err != nil {
		return 0, err
	}
	if status[0] != 0x00 {
		return 0, sony.ErrFreiaNotReady
	}
	compensationData, err := tuner.Read(0x19, 1)
	if err != nil {
		return 0, err
	}
	compensation := sony.TwosComplement(compensationData[0]>>4&0x0f, 4)

	if err := tuner.WriteMulti(0x59, []byte{0x05, 0x01}); err != nil {
		return 0, err
	}
	ifagc, err := tuner.Read(0x5b, 1)
	if err != nil {
		return 0, err
	}
	if err := tuner.WriteMulti(0x59, []byte{0x03, 0x01}); err != nil {
		return 0, err
	}
	rfagc, err := tuner.Read(0x5b, 1)
	if err != nil {
		return 0, err
	}
	if err := tuner.Write(0x59, 0x04); err != nil {
		return 0, err
	}
	if err := tuner.Write(0x88, 0x00); err != nil {
		return 0, err
	}
	if err := tuner.Write(0x87, 0x80); err != nil {
		return 0, err
	}

	agc150 := int32(ifagc[0]) * 150
	bpf, err := tuner.Read(0x69, 1)
	if err != nil {
		return 0, err
	}
	bpfX100 := freiaIFBPFGainTable[bpf[0]&0x0f] * 100

	var ifGain int32
	switch {
	case agc150 < 8925:
		ifGain = 3528 + bpfX100 - (7654*agc150+12750)/25500
	case agc150 < 10200:
		ifGain = 3069 + bpfX100 - (6340*agc150+12750)/25500
	default:
		ifGain = 533 + bpfX100
	}

	maxAGC150 := agc150
	if rfagc[0] > ifagc[0] {
		maxAGC150 = int32(rfagc[0]) * 150
	}
	rfGainMax := freiaRFGainMax(frequencyKHz)
	var rfGain int32
	if demod.System == sony.SystemDVBT || demod.System == sony.SystemDVBT2 {
		rfGain = freiaRFGainTerrT(maxAGC150, rfGainMax)
	} else {
		rfGain = freiaRFGainTerrOther(maxAGC150, rfGainMax)
	}

	return -ifGain - rfGain - compensation*100, nil
}

var freiaIFBPFGainTable = [16]int32{-3, -1, 1, 3, 5, 7, 9, 11, 13, 15, 17, 19, 21, 23, 23, 23}

func freiaRFGainMax(frequencyKHz uint32) int32 {
	switch {
	case frequencyKHz < 42000:
		return 3371
	case frequencyKHz < 71500:
		return 3416
	case frequencyKHz < 85500:
		return 3527
	case frequencyKHz < 135500:
		return 3663
	case frequencyKHz < 163500:
		return 3654
	case frequencyKHz < 198500:
		return 3755
	case frequencyKHz < 250001:
		return 3894
	case frequencyKHz < 305500:
		return 3061
	case frequencyKHz < 353500:
		return 3188
	case frequencyKHz < 457500:
		return 3247
	case frequencyKHz < 609500:
		return 3352
	case frequencyKHz < 769500:
		return 3457
	case frequencyKHz < 841500:
		return 3512
	case frequencyKHz < 890000:
		return 3704
	case frequencyKHz < 906000:
		return 3479
	case frequencyKHz < 930000:
		return 3379
	case frequencyKHz < 946000:
		return 3263
	case frequencyKHz < 986000:
		return 3109
	default:
		return 2952
	}
}

// freiaRFGainTerrT is the DVB-T/T2 gain curve.
func freiaRFGainTerrT(agc150, max int32) int32 {
	switch {
	case agc150 < 7650:
		return max + 1651 - (119*agc150+12750)/25500
	case agc150 < 8925:
		return max + 3108 - (4913*agc150+12750)/25500
	case agc150 < 10200:
		return max + 3595 - (6307*agc150+12750)/25500
	case agc150 < 12750:
		return max + 4471 - (8548*agc150+12750)/25500
	case agc150 < 15300:
		return max + 3856 - (7282*agc150+12750)/25500
	case agc150 < 17085:
		return max + 2573 - (5193*agc150+12750)/25500
	case agc150 < 17850:
		return max + 3598 - (6587*agc150+12750)/25500
	case agc150 < 20400:
		return max + 3540 - (6572*agc150+12750)/25500
	case agc150 < 24225:
		return max + 3214 - (6150*agc150+12750)/25500
	case agc150 < 27285:
		return max + 5286 - (8291*agc150+12750)/25500
	case agc150 < 29325:
		return max + 9968 - (12661*agc150+12750)/25500
	case agc150 < 31875:
		return max + 19789 - (21231*agc150+12750)/25500
	default:
		return max - 6600
	}
}

// freiaRFGainTerrOther is the ISDB-T/cable/ATSC/J.83B gain curve.
func freiaRFGainTerrOther(agc150, max int32) int32 {
	switch {
	case agc150 < 7650:
		return max + 1653 - (86*agc150+12750)/25500
	case agc150 < 8925:
		return max + 3117 - (4907*agc150+12750)/25500
	case agc150 < 10200:
		return max + 3608 - (6311*agc150+12750)/25500
	case agc150 < 12750:
		return max + 4487 - (8560*agc150+12750)/25500
	case agc150 < 15300:
		return max + 3827 - (7204*agc150+12750)/25500
	case agc150 < 17085:
		return max + 3765 - (7079*agc150+12750)/25500
	case agc150 < 17850:
		return max + 3226 - (6272*agc150+12750)/25500
	case agc150 < 20400:
		return max + 3737 - (6983*agc150+12750)/25500
	case agc150 < 24225:
		return max + 4006 - (7286*agc150+12750)/25500
	case agc150 < 27285:
		return max + 1939 - (5128*agc150+12750)/25500
	case agc150 < 29325:
		return max + 9965 - (12650*agc150+12750)/25500
	case agc150 < 31875:
		return max + 19775 - (21211*agc150+12750)/25500
	default:
		return max - 6600
	}
}
