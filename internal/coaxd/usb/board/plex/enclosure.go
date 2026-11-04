// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package plex

import (
	"strings"

	"github.com/Till0196/coax/pkg/usbio"
)

// PartnerEnclosure names the enclosure a device makes with the one other device
// of product partner behind the same hub: the lower of the two locations, the
// same for both. Empty when there is no such device, or more than one.
func PartnerEnclosure(attached []usbio.Attached, own usbio.Attached, partner uint16) string {
	var found []usbio.Attached
	for _, candidate := range attached {
		if candidate.Location == own.Location || candidate.ID != (usbio.ID{Vendor: own.ID.Vendor, Product: partner}) {
			continue
		}
		if hubOf(candidate.Port) != hubOf(own.Port) {
			continue
		}
		found = append(found, candidate)
	}
	if len(found) != 1 {
		return ""
	}
	return min(own.Location, found[0].Location)
}

// hubOf is what a port path hangs off: "1-2" for "1-2.3", and the bus "1" for
// a device on a root port, "1-2".
func hubOf(port string) string {
	if cut := strings.LastIndexByte(port, '.'); cut >= 0 {
		return port[:cut]
	}
	if cut := strings.IndexByte(port, '-'); cut >= 0 {
		return port[:cut]
	}
	return port
}
