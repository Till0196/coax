// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/plan"
	"github.com/Till0196/coax/pkg/report"
)

// The channel definition file BonDriver_PX4 reads, and BonDriver_PT3-ST before it, is five
// tab-separated columns:
//
//	$<space name>	<space id>
//	<channel name>	<space id>	<channel id>	<physical channel>	<TSID>
//
// There is no column for the delivery system: it is a parameter to the reader, one per
// file, and the two are merged afterwards. So BS off a satellite and BS off a cable
// transmodulation live in different files rather than in differently named spaces.
//
// The TSID column means the same thing in both: which stream of the multiplex to take.

// Systems are named the way the files are, so a reader knows what to load a file
// as from what it is called.
const (
	SystemTerrestrial = "T"
	SystemSatellite   = "S"
	SystemCable       = "C"
	// SystemHikari is the same 128 degree east service carried over an optical
	// distribution, remodulated onto a run of cable carriers. What arrives is a
	// cable signal, so it is a system of its own.
	SystemHikari = "H"
	// SystemDVBS2 is the 128 degree east service, which is a satellite like
	// the others and shares nothing else with them: a different orbital slot,
	// a different modulation, and transponders under a numbering of their own.
	SystemDVBS2 = "S2"
)

// chsetSystem says which file a result belongs in, and whether it belongs in
// one at all.
func chsetSystem(system string) (string, bool) {
	// Normalized so that a channel file writing ISDB-C and one writing
	// DVBC/ANNEX_A land in the same file.
	switch channelconfig.NormalizeDeliverySystem(system) {
	case "ISDBT":
		return SystemTerrestrial, true
	case "ISDBS", "ISDBS3":
		// The advanced services are the same satellite on the same dish and one
		// BonDriver reads both, so ISDB-S3 shares ISDB-S's file. What separates a TLV
		// stream from a transport stream is its identifier, which picks the space rather than
		// the file.
		return SystemSatellite, true
	case "DVBC/ANNEX_A":
		return SystemCable, true
	case "DVBS2":
		return SystemDVBS2, true
	case "DVBC/ANNEX_B":
		return SystemHikari, true
	default:
		return "", false
	}
}

// WriteChSet writes the channels of one delivery system. Results the mapping
// cannot place are returned rather than dropped, since the answer is usually to
// give the physical channel a number by hand.
func WriteChSet(w io.Writer, system string, results []report.Result) (skipped []string, err error) {
	spaces, skipped := place(system, results)
	if len(spaces) == 0 {
		return skipped, fmt.Errorf("no %s channels to write", SystemDisplayName(system))
	}

	var file strings.Builder
	file.WriteString(";\r\n; channel definitions (" + SystemDisplayName(system) + "), written by coax\r\n")
	file.WriteString("; load this file as " + SystemDisplayName(system) +
		"; the file it is has to say so, because the format does not\r\n;\r\n")
	file.WriteString("; spaces ($name<TAB>space id)\r\n")
	for id, space := range spaces {
		fmt.Fprintf(&file, "$%s\t%d\r\n", space.name, id)
	}
	file.WriteString(";\r\n; channels (name<TAB>space id<TAB>channel id<TAB>physical channel<TAB>TSID)\r\n")
	for id, space := range spaces {
		fmt.Fprintf(&file, "; [%s]\r\n", space.name)
		for index, channel := range space.channels {
			fmt.Fprintf(&file, "%s\t%d\t%d\t%d\t%d\r\n",
				chsetName(channel), id, index, channel.ptxCh, channel.selector)
		}
	}

	// The driver reads every name as code page 932 and has no other case, so
	// the file is written in it. Nothing here should fail to encode: chsetName
	// keeps unencodable names out, and everything else written is ASCII.
	encoded, ok := cp932(file.String())
	if !ok {
		return skipped, fmt.Errorf("a %s channel name cannot be written in code page 932", SystemDisplayName(system))
	}
	_, err = w.Write(encoded)
	return skipped, err
}

// chsetName is what a channel is called in the file: the carrier, and the
// service at the front of it where the scan learnt one -- the same rule the
// Mirakurun list is written under. The service is added after a space, since
// the format's separator is a tab.
//
// A name code page 932 cannot hold is left off rather than mangled: this format
// has no second encoding to fall back to. Some spaces keep the carrier's name
// alone.
func chsetName(channel placedChannel) string {
	if carrierOnlyNames[channel.space] {
		return channel.name
	}
	for _, service := range channel.stream.services {
		if service.Name == "" || !televisionServiceType(service.Type) {
			continue
		}
		named := channel.name + " " + service.Name
		if !fitsCP932(named) {
			break
		}
		return named
	}
	return channel.name
}

// carrierOnlyNames are the channel spaces where the carrier's own name is the
// whole name. Naming a carrier after the service at the front of it only says
// anything where the carrier is that service's; on the platforms that sell
// carriage by the slot one slot holds unrelated channels and which is first
// changes as contracts come and go, so the name would differ between two scans
// of a feed that did not.
var carrierOnlyNames = map[string]bool{
	"CS": true, "CS4K": true, "SKY": true, "CATV": true, "OTHER": true,
}

// SystemDisplayName is what a delivery system is called in a message and in
// the heading of the file written for it.
func SystemDisplayName(system string) string {
	switch system {
	case SystemTerrestrial:
		return "ISDB-T"
	case SystemSatellite:
		return "ISDB-S"
	case SystemCable:
		return "ISDB-C"
	case SystemDVBS2:
		return "DVB-S2"
	case SystemHikari:
		return "DVB-C ANNEX_B"
	default:
		return system
	}
}

// cablePTXChannel gives a physical channel number to a channel that arrived
// over a cable of one sort or another. The Japanese cable band plan names its
// channels, while the optical distribution has only frequencies, so the two are
// read from different things.
func cablePTXChannel(system string, result report.Result) (int, bool) {
	if system == SystemHikari {
		hz, err := strconv.Atoi(strings.TrimSpace(result.Properties["FREQUENCY"]))
		if err != nil {
			return 0, false
		}
		return plan.OpticalPTXChannel(hz)
	}
	return plan.PTXChannel(result.Name)
}

// carrierName is what the carrier a channel arrived on is called: everywhere
// but the optical distribution, the name the channel file used. The optical
// distribution has no such plan -- its carriers are an even grid of 6.5 MHz
// from 73.75 MHz -- so the frequency names it, in megahertz rather than the
// kilohertz the file writes.
func carrierName(system string, result report.Result) string {
	if system != SystemHikari {
		return result.Name
	}
	hz, err := strconv.Atoi(strings.TrimSpace(result.Properties["FREQUENCY"]))
	if err != nil {
		return result.Name
	}
	return strconv.FormatFloat(float64(hz)/1e6, 'f', -1, 64) + "MHz"
}

// soleStreamName is what a carrier holding one stream is called, there being
// no place on it to name. A channel number takes Ch after it, the way the
// drivers write one; a frequency does not, since "73.75MHzCh" reads as nothing.
func soleStreamName(system string, result report.Result) string {
	name := carrierName(system, result)
	if system == SystemHikari {
		return name
	}
	return name + terrestrialNameSuffix
}

// terrestrialNameSuffix is the Ch a channel number takes after it, the way the
// drivers write one.
const terrestrialNameSuffix = "Ch"

// satellitePTXChannel gives a physical channel number, and the transponder's
// name, to a satellite frequency in kHz.
//
// Which frequency that is differs by plan: a 110 degree channel is the
// intermediate frequency the tuner is handed, while a 124/128 degree one is the
// downlink, since a block has to be named there anyway and a named block
// converts for itself. The two are kept apart by delivery system rather than by
// frequency.
//
// Both numberings are positions in their plan; the first two ISDB-S bands are
// BonDriver_PX4's own numbering and are not ours to change. A frequency neither
// plan recognises is reported rather than guessed at.
func satellitePTXChannel(system, frequency string) (ptxCh int, name string, ok bool) {
	kHz, err := strconv.Atoi(strings.TrimSpace(frequency))
	if err != nil {
		return 0, "", false
	}
	switch system {
	case SystemSatellite:
		if transponder, found := plan.SatelliteTransponderAt(kHz); found {
			return transponder.PTXChannel, transponder.Name, true
		}
	case SystemDVBS2:
		if transponder, found := plan.SkyTransponderAt(kHz); found {
			return transponder.PTXChannel, transponder.Name, true
		}
	}
	return 0, "", false
}

// ChSetSystems is the delivery systems a report has channels for, in the order
// the files are written.
func ChSetSystems(value report.Report) []string {
	seen := map[string]bool{}
	for result := range foundIn(value.Results) {
		if system, ok := chsetSystem(result.Properties["DELIVERY_SYSTEM"]); ok {
			seen[system] = true
		}
	}
	var systems []string
	for _, system := range []string{SystemTerrestrial, SystemSatellite, SystemCable, SystemDVBS2, SystemHikari} {
		if seen[system] {
			systems = append(systems, system)
		}
	}
	return systems
}
