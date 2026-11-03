// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package siano reaches the control channel exposed by the Siano BDA driver.
package siano

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/device"
	bridge "github.com/Till0196/coax/internal/coaxd/usb/bridge/siano"
)

// The BDA stream uses client 201 and SmsMonitor uses 150. A separate client
// keeps their responses out of this handle's read queue, and vice versa.
const statisticsClient = 250

// ControlPath names this USB instance's NewLegacyInterface. The vendor library
// enumerates GUID 212cb866-fe82-40f2-94bf-5f0badef5931; using the selected tuner's
// complete instance path avoids choosing the first of several identical sticks.
func ControlPath(moniker string) (string, bool) {
	path := strings.TrimPrefix(strings.ToLower(moniker), "@device:pnp:")
	if !strings.HasPrefix(path, `\\?\usb#`) {
		return "", false
	}
	parts := strings.Split(path[len(`\\?\usb#`):], "#")
	if len(parts) != 3 || parts[1] == "" || !strings.HasPrefix(parts[2], "{") {
		return "", false
	}
	switch parts[0] {
	case "vid_187f&pid_0600", "vid_187f&pid_0601", "vid_3275&pid_0080":
	default:
		return "", false
	}
	return `\\?\usb#` + parts[0] + "#" + parts[1] + `#{212cb866-fe82-40f2-94bf-5f0badef5931}\NewLegacyInterface`, true
}

// Deliveries is what a Siano stick on its own driver receives: the driver
// loads the ISDB-T firmware and publishes the tuner for nothing else. Nil for
// another device.
func Deliveries(moniker string) []string {
	if _, ok := ControlPath(moniker); !ok {
		return nil
	}
	return []string{"ISDB-T"}
}

type exchanger interface {
	Exchange(request []byte) ([]byte, error)
}

// Reading is what one statistics reply says: the SNR, which is shown as the
// C/N, and the in-band power in dBm, the input level.
type Reading struct {
	SNR, Power device.Measurement
}

func readStatistics(channel exchanger) (Reading, error) {
	// sms_msg_hdr: type, source, destination, length, flags.
	request := []byte{0x8d, 0x02, statisticsClient, 11, 8, 0, 0, 0}
	reply, err := channel.Exchange(request)
	if err != nil {
		return Reading{}, err
	}
	if len(reply) < 12 || binary.LittleEndian.Uint16(reply) != bridge.MsgGetStatisticsExRes ||
		reply[3] != statisticsClient || int(binary.LittleEndian.Uint16(reply[4:])) != len(reply) ||
		binary.LittleEndian.Uint16(reply[6:])&4 != 0 || binary.LittleEndian.Uint32(reply[8:]) != 0 {
		return Reading{}, fmt.Errorf("invalid Siano statistics response")
	}
	return Reading{SNR: bridge.ISDBTSNR(reply[12:]), Power: bridge.ISDBTInBandPower(reply[12:])}, nil
}
