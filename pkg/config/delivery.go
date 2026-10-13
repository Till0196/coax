// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package config

import (
	"slices"
	"strings"
)

// A delivery system goes by several names: after the standard that defines it
// (ISDB-C), after the annex of the ITU recommendation that describes it (J.83
// Annex C), and after whatever the DVB API spells it (DVBC/ANNEX_A), hyphenated
// or not.
//
// Everything deciding what a channel is uses this list. Nothing here rewrites
// what is sent to a tuner: a channel file's DELIVERY_SYSTEM is passed on as
// written.
var deliverySystemNames = map[string]string{
	"ISDBT":  "ISDBT",
	"ISDB-T": "ISDBT",

	"ISDBS":  "ISDBS",
	"ISDB-S": "ISDBS",

	// The advanced satellite standard, which carries the MMT/TLV services. A
	// channel file normally writes these as ISDBS with a TLV stream identifier in
	// STREAM_ID, because the Linux DVB API has no delivery system number for
	// ISDB-S3. Backends that do have a name for it spell ISDBS3, so a file may
	// too. See DeliverySystemSubstitutes.
	"ISDBS3":  "ISDBS3",
	"ISDB-S3": "ISDBS3",

	"DVBT":  "DVBT",
	"DVB-T": "DVBT",

	"DVBT2":  "DVBT2",
	"DVB-T2": "DVBT2",

	"DVBS":  "DVBS",
	"DVB-S": "DVBS",

	"DVBS2":  "DVBS2",
	"DVB-S2": "DVBS2",

	// The Japanese cable band. The annex that describes it is C; what the
	// channel files write, and what the tuners lock with, is A.
	"DVBC/ANNEX_A":  "DVBC/ANNEX_A",
	"DVB-C/ANNEX_A": "DVBC/ANNEX_A",
	"DVBC/ANNEX_C":  "DVBC/ANNEX_A",
	"DVB-C/ANNEX_C": "DVBC/ANNEX_A",
	"ANNEX_C":       "DVBC/ANNEX_A",
	"J.83C":         "DVBC/ANNEX_A",
	"J83C":          "DVBC/ANNEX_A",
	"ISDBC":         "DVBC/ANNEX_A",
	"ISDB-C":        "DVBC/ANNEX_A",
	"DVBC":          "DVBC/ANNEX_A",
	"DVB-C":         "DVBC/ANNEX_A",

	// The optical distribution the 124/128 degree east service is remodulated
	// onto, which derives from the other annex of the same recommendation and
	// is what the demodulator drivers call J.83B.
	"DVBC/ANNEX_B":  "DVBC/ANNEX_B",
	"DVB-C/ANNEX_B": "DVBC/ANNEX_B",
	"ANNEX_B":       "DVBC/ANNEX_B",
	"J.83B":         "DVBC/ANNEX_B",
	"J83B":          "DVBC/ANNEX_B",

	"DVBC2":  "DVBC2",
	"DVB-C2": "DVBC2",
}

// PreferredDeliverySystem is the name to write a system down under and to say
// it by: what this country calls the standard, rather than what the DVB API
// calls the demodulator.
//
// The two differ in one place. Japanese cable is ITU-T J.83 Annex C at 5.274
// Msym/s, and the Linux DVB API knows it only as Annex A. Writing DVBC/ANNEX_A
// in a report meant for this country invites a reader to believe European
// figures would do, so a report says ISDBC and the DVB name is put on at the
// point of asking a tuner.
func PreferredDeliverySystem(system string) string {
	if canonical := NormalizeDeliverySystem(system); canonical == "DVBC/ANNEX_A" {
		return "ISDBC"
	}
	return NormalizeDeliverySystem(system)
}

// deliverySystemSubstitutes are the systems a tuner may advertise instead of
// one it receives, because the two share a front end.
//
// ISDB-S and ISDB-S3 are the pair, standing in for each other both ways: one
// demodulator receives both, and which name is advertised is an accident of the
// interface. Passing a channel over for a spelling would refuse it on the very
// hardware that receives it.
var deliverySystemSubstitutes = map[string][]string{
	"ISDBS":  {"ISDBS3"},
	"ISDBS3": {"ISDBS"},
}

// DeliverySystemSubstitutes are the other delivery systems a tuner may
// advertise for a front end that receives this one. It decides whether a tuner
// is worth trying, and translates nothing: a channel naming ISDB-S3 is still
// sent as ISDB-S3.
func DeliverySystemSubstitutes(system string) []string {
	return deliverySystemSubstitutes[CanonicalDeliverySystem(system)]
}

// NormalizeDeliverySystem gives a delivery system its one spelling, or nothing
// when the name is not one this knows.
func NormalizeDeliverySystem(name string) string {
	return deliverySystemNames[strings.ToUpper(strings.Trim(strings.TrimSpace(name), "\"'"))]
}

// CanonicalDeliverySystem gives a delivery system one spelling to compare by,
// whether or not this knows the name. A known name comes back canonical, so
// DVB-C/ANNEX_A and ISDBC are recognised as each other; an unknown one comes
// back stripped of the punctuation people vary, so a system added to a backend
// before it is added here still matches itself.
func CanonicalDeliverySystem(name string) string {
	if canonical := NormalizeDeliverySystem(name); canonical != "" {
		return canonical
	}
	return deliverySystemPunctuation.Replace(strings.ToUpper(strings.Trim(strings.TrimSpace(name), "\"'")))
}

var deliverySystemPunctuation = strings.NewReplacer("-", "", "_", "", " ", "")

// DeliverySystemAliases are the other names a delivery system answers to, in
// order, for a message that has to list them.
func DeliverySystemAliases(system string) []string {
	var names []string
	for name, canonical := range deliverySystemNames {
		if canonical == system && name != system {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
