// Copyright (c) Ralph Metzler; Marcus Metzler; Digital Devices; MaxLinear; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package mxl58x

import (
	"fmt"
	"time"
)

// Which of the four RF inputs a frontend listens to is encoded in two bits,
// and in Quattro mode those two bits are what SetVoltage and SetTone move:
// rfInputLowVoltage is set on the inputs fed at 13 V and clear on the 18 V
// ones, rfInputToneOff on the inputs held without the 22 kHz tone.
const (
	rfInputLowVoltage = 2
	rfInputToneOff    = 1
)

// The firmware's tone burst values, MXL_HYDRA_DISEQC_TONE_CTRL_E.
const (
	toneBurstNone = 0
	toneBurstA    = 1
	toneBurstB    = 2
)

// diseqcMessageMax is MXL_HYDRA_DISEQC_MAX_PKT_SIZE, the firmware's buffer.
const diseqcMessageMax = 32

// RFInput reports which of the card's four inputs this frontend listens to.
// In Quattro mode it moves as bands are chosen.
func (demod *Demod) RFInput() uint32 { return demod.rfInput }

// SetVoltage selects the low-noise block supply: high is 18 V, false 13 V, off
// no supply at all. The supply is never the chip's -- on this card it is the
// FPGA's GPIO block. In Quattro mode the four inputs are already held at the
// four band and polarisation combinations, so 18 V means moving onto an input
// already at 18 V, and off is treated as 13 V for want of an unpowered input.
// In Direct mode the request goes to the board through Config.Voltage; with
// none configured it is refused rather than reported as done.
func (demod *Demod) SetVoltage(high, off bool) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	if demod.config.Multiswitch == Quattro {
		if high && !off {
			demod.rfInput &^= rfInputLowVoltage
		} else {
			demod.rfInput |= rfInputLowVoltage
		}
		return nil
	}
	if demod.config.Voltage == nil {
		return fmt.Errorf("MxL58x frontend %d cannot switch its supply: the low-noise block regulator"+
			" is the card's, and no way to reach it was configured", demod.config.DemodIndex)
	}
	return demod.config.Voltage.SetInputVoltage(demod.rfInput, high, off)
}

// SetTone puts the continuous 22 kHz tone on the aerial lead, or takes it off.
// Unlike the supply the tone is the chip's, so in Direct mode this really does
// start and stop it; in Quattro mode it moves between the inputs held with and
// without the tone.
func (demod *Demod) SetTone(on bool) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	if demod.config.Multiswitch == Quattro {
		if on {
			demod.rfInput &^= rfInputToneOff
		} else {
			demod.rfInput |= rfInputToneOff
		}
		return nil
	}
	return demod.setInputTone(demod.rfInput, on)
}

func (demod *Demod) setInputTone(rfInput uint32, on bool) error {
	flag := uint32(0)
	if on {
		flag = 1
	}
	return demod.sendCommand(commandDiSEqCContinuous, words(rfInput, flag))
}

// diseqcSettleTime is the wait after handing a message to the firmware, which
// acknowledges nothing: the only thing keeping one message off the last.
const diseqcSettleTime = 100 * time.Millisecond

// SendDiSEqC sends one master command on this frontend's RF input; the firmware
// modulates it onto the lead itself, hence a command rather than registers.
func (demod *Demod) SendDiSEqC(message []byte) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	if len(message) < 1 || len(message) > diseqcMessageMax {
		return fmt.Errorf("a DiSEqC message is 1 to %d bytes, not %d", diseqcMessageMax, len(message))
	}
	if err := demod.sendCommand(commandDiSEqCMessage,
		demod.diseqcPayload(message, toneBurstNone)); err != nil {
		return err
	}
	time.Sleep(diseqcSettleTime)
	return nil
}

// SendBurst sends the one-bit tone burst that selects between two low-noise
// blocks on a simple switch. b selects burst B; false selects burst A.
func (demod *Demod) SendBurst(b bool) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	burst := uint32(toneBurstA)
	if b {
		burst = toneBurstB
	}
	return demod.sendCommand(commandDiSEqCMessage, demod.diseqcPayload(nil, burst))
}

// diseqcPayload lays out MXL_HYDRA_DISEQC_TX_MSG_T: controller, message length,
// the message in a fixed thirty-two byte buffer, and the tone burst. The buffer
// goes out whole however short the message -- the firmware reads a fixed-size
// structure and the burst field sits after it.
func (demod *Demod) diseqcPayload(message []byte, burst uint32) []byte {
	payload := make([]byte, 0, 44)
	payload = append(payload, words(demod.rfInput, uint32(len(message)))...)
	buffer := make([]byte, diseqcMessageMax)
	copy(buffer, message)
	payload = append(payload, buffer...)
	return append(payload, words(burst)...)
}
