// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package fwextract

import (
	"path"
	"regexp"
	"strings"
)

// Firmware a vendor ships as a file rather than hiding inside a driver. TBS
// puts theirs in the same archive as their Linux driver, under the name the
// driver asks for it by, so there is nothing to recover: the file is the image.
// The names are the Linux ones, which is the point: a chip takes the same image
// whatever the operating system.

// distributedName matches the naming convention the Linux firmware collection
// uses for this class of hardware: what the blob is for, which chip, and which
// revision. Deliberately narrow -- firmware.bin might be anything, while
// dvb-demod-si2183-b60-01.fw is a demodulator image named the way the driver
// that needs it will ask. The prefixes are that collection's subsystems.
//
// Siano's images keep an older convention of their own -- what the image
// serves, the chip family, .inp -- which the Linux driver asks for by exactly
// that name (isdbt_rio.inp). Its Windows driver ships them as files beside the
// .inf, fm_radio_rio.inp among them, which no Linux collection carries.
var distributedName = regexp.MustCompile(`^((dvb|sms1xxx|v4l|ngene|as10x)-[a-z0-9._-]+\.fw|(dvb|dvbh|isdbt|tdmb|cmmb|fm_radio)_[a-z0-9_]+\.inp)$`)

// IsDistributedFirmware reports whether a file is firmware as it stands,
// wanting nothing recovered from it.
func IsDistributedFirmware(name string, data []byte) bool {
	if !distributedName.MatchString(strings.ToLower(path.Base(name))) {
		return false
	}
	// A plausible size for a microcontroller or demodulator image: big enough
	// to be one, small enough that a mis-named archive is not.
	return len(data) >= 1024 && len(data) <= 4<<20
}

// Collect walks the containers in data and returns every firmware image in it:
// the ones a driver has to be taken apart for, and the ones the vendor shipped
// as files. The whole-package answer to FindFirmware's single one: a vendor
// archive holds a bridge microcontroller, a demodulator and sometimes a tuner.
func Collect(data []byte, name string) []Firmware {
	return collect(data, name, 0, map[string]bool{}, newExpansionBudget())
}

func collect(data []byte, name string, depth int, seen map[string]bool, budget *expansionBudget) []Firmware {
	if depth > unwrapDepthLimit {
		return nil
	}
	if IsDistributedFirmware(name, data) {
		base := path.Base(strings.ReplaceAll(name, `\`, "/"))
		if seen[base] {
			return nil
		}
		seen[base] = true
		return []Firmware{{Kind: "file", FileName: base, Image: data}}
	}
	members, kind, err := openContainer(data, name, budget)
	if err != nil || kind == "" {
		for _, extract := range extractors {
			if firmware, err := extract(data); err == nil {
				if seen[firmware.FileName] {
					return nil
				}
				seen[firmware.FileName] = true
				return []Firmware{firmware}
			}
		}
		return nil
	}
	var found []Firmware
	for _, member := range members {
		found = append(found, collect(member.Data, member.Name, depth+1, seen, budget)...)
	}
	return found
}
