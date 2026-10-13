// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package sony

import "errors"

// System is a delivery system the chip has a mode for.
type System int

const (
	SystemUnspecified System = iota
	SystemDVBT
	SystemDVBT2
	SystemDVBC
	SystemISDBT
	SystemJ83B
	SystemATSC
	SystemISDBS
	SystemISDBS3
	SystemDVBS
	SystemDVBS2
)

func (system System) String() string {
	switch system {
	case SystemDVBT:
		return "DVB-T"
	case SystemDVBT2:
		return "DVB-T2"
	case SystemDVBC:
		return "DVB-C"
	case SystemISDBT:
		return "ISDB-T"
	case SystemJ83B:
		return "J.83B"
	case SystemATSC:
		return "ATSC"
	case SystemDVBS:
		return "DVB-S"
	case SystemDVBS2:
		return "DVB-S2"
	case SystemISDBS:
		return "ISDB-S"
	case SystemISDBS3:
		return "ISDB-S3"
	default:
		return "unspecified"
	}
}

// State is the chip's power state.
type State int

const (
	StateUnknown State = iota
	StateSleep
	StateActive
)

// IsSatellite reports whether a system is received through the satellite side
// of a two-sided tuner; only one member of the family has one.
func (system System) IsSatellite() bool {
	switch system {
	case SystemISDBS, SystemISDBS3, SystemDVBS, SystemDVBS2:
		return true
	default:
		return false
	}
}

// ErrNoSystem is returned when a sequence is asked for a system the chip has
// no mode for.
var ErrNoSystem = errors.New("no delivery system selected")
