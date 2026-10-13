// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package isdb reads what is particular to Japanese digital broadcasting:
// service information on both transmission layers (PSI/SI in transport-stream
// sections, TLV-NIT and MH-SDT in MMT signaling), the STD-B24 character
// encoding those tables spell service names in, and TSMF.
//
// It also holds the analysis a channel scan runs over a captured sample --
// Analyze and DetectFormat -- which is where the two layers and the tables
// above are put together.
package isdb
