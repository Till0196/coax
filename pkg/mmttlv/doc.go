// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package mmttlv reads the MMT/TLV transmission layer: TLV packets, the MMTP
// headers inside them, and the MMT signaling messages those carry.
//
// Nothing here is particular to Japanese broadcasting. TLV and MMT are what
// ISDB-S3 happens to transmit over, but the layer itself says only how bytes
// are framed and reassembled -- what the signaling means belongs to isdb.
package mmttlv
