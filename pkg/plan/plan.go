// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package plan holds the frequency plans a Japanese receiver works to.
//
// They are here rather than beside the code that walks them because a scan, a
// channel name and a channel definition file all read the same table, and three
// copies of it is three chances for a channel to be scanned at one frequency,
// tuned at another, and written out under a third. Nothing here talks to
// hardware or reads a file.
package plan

// Carrier is one position in a plan: what it is called, and where it is. The
// unit is the one that plan's channel files write -- hertz on a cable or
// through the air, kilohertz off a satellite -- which is the DVB API's own
// division. Each plan says which unit it is in.
type Carrier struct {
	Name string
	// Frequency is in the unit of the plan this carrier came from.
	Frequency int
}
