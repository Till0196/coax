// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package bda

import (
	"strconv"

	"github.com/Till0196/coax/pkg/win32/directshow"
)

// Settings is what the driver says about how the signal is being demodulated.
//
// It is read from the tuner's demodulator node -- the driver's answer, not the
// hardware's -- deliberately: on cards whose command interface is shared with
// the driver, reaching the hardware while streaming stops the stream (measured
// on a Digital Devices MAX M4), while a node takes the driver's lock instead.
// So it is safe to ask mid-recording.
//
// Some drivers report what they measured, some hand back what they were asked
// for, and there is no way to tell which from here. Unset values are omitted.
// The vocabulary is BDA's, from DVB and ATSC, so an ISDB-S or ISDB-T tuner has
// little to report.
func (device *Device) Settings() [][2]string {
	if device.session == nil {
		return nil
	}
	demodulator := device.session.Demodulator()
	if demodulator == nil {
		return nil
	}
	defer demodulator.Release()
	settings := make([][2]string, 0, 6)
	add := func(name, value string) {
		if value != "" {
			settings = append(settings, [2]string{name, value})
		}
	}

	var modulation directshow.ModulationType
	if err := demodulator.Get_ModulationType(&modulation); err == nil {
		add("MODULATION", modulationName(modulation))
	}
	var innerMethod directshow.FECMethod
	if err := demodulator.Get_InnerFECMethod(&innerMethod); err == nil {
		add("INNER_FEC", fecMethodName(innerMethod))
	}
	var innerRate directshow.BinaryConvolutionCodeRate
	if err := demodulator.Get_InnerFECRate(&innerRate); err == nil {
		add("INNER_FEC_RATE", codeRateName(innerRate))
	}
	var outerMethod directshow.FECMethod
	if err := demodulator.Get_OuterFECMethod(&outerMethod); err == nil {
		add("OUTER_FEC", fecMethodName(outerMethod))
	}
	var outerRate directshow.BinaryConvolutionCodeRate
	if err := demodulator.Get_OuterFECRate(&outerRate); err == nil {
		add("OUTER_FEC_RATE", codeRateName(outerRate))
	}
	var symbolRate uint32
	if err := demodulator.Get_SymbolRate(&symbolRate); err == nil && symbolRate != 0 {
		add("SYMBOL_RATE", strconv.FormatUint(uint64(symbolRate), 10))
	}
	return settings
}

// The names below are the ones a channel file uses, so a value read back reads
// the same way it was asked for. The driver's "not set" and "not defined" both
// render as nothing: they mean no answer, not an answer of zero.

func modulationName(value directshow.ModulationType) string {
	switch value {
	case directshow.BDA_MOD_16QAM:
		return "QAM/16"
	case directshow.BDA_MOD_32QAM:
		return "QAM/32"
	case directshow.BDA_MOD_64QAM:
		return "QAM/64"
	case directshow.BDA_MOD_128QAM:
		return "QAM/128"
	case directshow.BDA_MOD_256QAM:
		return "QAM/256"
	case directshow.BDA_MOD_QPSK:
		return "QPSK"
	case directshow.BDA_MOD_8PSK:
		return "8PSK"
	case directshow.BDA_MOD_16APSK:
		return "16APSK"
	case directshow.BDA_MOD_32APSK:
		return "32APSK"
	case directshow.BDA_MOD_8VSB:
		return "8VSB"
	case directshow.BDA_MOD_16VSB:
		return "16VSB"
	}
	return ""
}

func fecMethodName(value directshow.FECMethod) string {
	if value == directshow.BDA_FEC_VITERBI {
		return "VITERBI"
	}
	if value == directshow.BDA_FEC_RS_204_188 {
		return "RS(204,188)"
	}
	return ""
}

func codeRateName(value directshow.BinaryConvolutionCodeRate) string {
	switch value {
	case directshow.BDA_BCC_RATE_1_2:
		return "1/2"
	case directshow.BDA_BCC_RATE_2_3:
		return "2/3"
	case directshow.BDA_BCC_RATE_3_4:
		return "3/4"
	case directshow.BDA_BCC_RATE_4_5:
		return "4/5"
	case directshow.BDA_BCC_RATE_5_6:
		return "5/6"
	case directshow.BDA_BCC_RATE_7_8:
		return "7/8"
	}
	return ""
}
