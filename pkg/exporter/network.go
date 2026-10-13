// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

// What a network identifier says a channel is, which is the one thing the two
// channel formats read the same way.
//
// Both a Mirakurun channel list and a channel definition file file a channel by
// what it is rather than by how it arrived, and both take that from the
// original network identifier the stream declares. BS carried over a cable
// transmodulation declares the network BS does, and that is the point: it is
// still BS.

// channelTypeForNetwork says what a network is, from the identifier it declares
// itself by.
//
// The answer is this program's own vocabulary, which is finer than the one a
// front end takes: CS4K and CATV are real distinctions worth keeping in a scan
// report and in a channel definition file, and are not among the types
// Mirakurun accepts. mirakurunChannelType folds them onto what it does accept.
func channelTypeForNetwork(originalNetworkID uint16) string {
	switch {
	case originalNetworkID == 4:
		return "BS"
	case originalNetworkID == 6 || originalNetworkID == 7:
		return "CS"
	case originalNetworkID == 0x000a || originalNetworkID == 0x0001 || originalNetworkID == 0x0003:
		return "SKY"
	case originalNetworkID == 0x000b:
		return "BS4K"
	case originalNetworkID == 0x000c:
		return "CS4K"
	case originalNetworkID >= 0xfff7:
		// A cable operator's own carriage, under an identifier from the block
		// operators name themselves from.
		return "CATV"
	case originalNetworkID >= 0x7880 && originalNetworkID <= 0x7fe8:
		return "GR"
	case originalNetworkID != 0:
		return "OTHER"
	default:
		return ""
	}
}
