// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import (
	"fmt"
	"strings"
)

type Delivery int

const (
	DeliveryTerrestrial Delivery = iota
	DeliveryCable
	DeliverySatellite
)

func Supports(deliverySystem string) bool {
	_, err := deliveryFor(normalize(deliverySystem))
	return err == nil
}

func deliveryFor(system string) (Delivery, error) {
	switch system {
	case "ISDBT", "DVBT", "DVBT2":
		return DeliveryTerrestrial, nil
	case "ISDBC", "DVBC", "DVBC2", "DVBCANNEXA", "DVBCANNEXB", "DVBCANNEXC", "J83A", "J83B", "J83C":
		return DeliveryCable, nil
	case "ISDBS", "ISDBS3", "DVBS", "DVBS2":
		return DeliverySatellite, nil
	default:
		return 0, fmt.Errorf("BDA delivery system %q is not supported", system)
	}
}

// normalize folds the spellings a delivery system or modulation is written in,
// the same way the rest of this program does. Channel files differ only in the
// separators -- DVB-C/ANNEX_A, DVBC/ANNEX A, dvbc_annex_a, J.83B all name one
// thing -- so every separator is dropped before comparing. The underscore in
// particular: keeping it once made dvbv5's own "DVBC/ANNEX_A" unsupported.
func normalize(value string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", "/", "", "_", "", ".", "", " ", "").Replace(strings.TrimSpace(value)))
}
