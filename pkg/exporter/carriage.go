// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

import (
	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/report"
)

// carriedStream is one stream a scanned carrier turned out to hold, and the one
// reading of a scan result every channel format is written from. What they do
// with it differs: a Mirakurun list writes the services and throws the stream
// away, a channel file the reverse.
type carriedStream struct {
	// relative is which stream of a multiplexing frame this is, and whether it
	// is one at all. A carrier with no frame on it is one stream and has no
	// number to give.
	relative    int
	hasRelative bool
	// streamID identifies the stream: a transport stream identifier, or a TLV
	// one for the advanced services.
	streamID uint16
	// networkID is the network the stream belongs to, settled. See
	// carriedStreams for where it is taken from.
	networkID uint16
	// networkName is what that network calls itself, out of the network name
	// descriptor of its own table. Empty where the sample did not carry one.
	networkName string
	// remoteKeyID is the button a receiver puts this stream on, out of the ARIB TS
	// information descriptor. Zero means the stream did not say, which is every
	// stream that is not terrestrial: everything else is worked out from the
	// service identifier instead.
	remoteKeyID byte
	// remoteKeys is which button each service is on, where the stream said so
	// service by service. Only the advanced services do; see
	// isdb.RemoteControlKeyInfo.
	remoteKeys map[uint16]byte
	// services are what the stream said it carries. A stream that described
	// itself and named nothing is not told apart from one never read: neither
	// has anything to write.
	services []isdb.ServiceInfo
}

// carriedStreams reads a scanned result as the streams it holds, and says
// whether they arrived multiplexed into a frame. That decides how a stream is
// asked for later: a satellite demodulator is told an identifier and hands back
// that stream alone, while a multiplexing frame arrives whole and is separated
// by its header.
//
// The network identifier is settled once, in the order the tables should be
// believed in: what the frame header said about the slot, then what the stream
// said about itself, then the network it declares membership of. A header that
// left it at zero should not lose a stream that says what it is.
func carriedStreams(result report.Result) (streams []carriedStream, multiplexed bool) {
	if result.Analysis == nil {
		return nil, false
	}
	if frame := result.Analysis.TSMF; frame != nil {
		streams = make([]carriedStream, 0, len(frame.RelativeStreams))
		for _, slot := range frame.RelativeStreams {
			stream := carriedStream{
				relative:    slot.RelativeStream,
				hasRelative: true,
				streamID:    slot.StreamID,
				networkID:   slot.OriginalNetworkID,
			}
			stream.takeTransport(slot.Transport)
			stream.takeTLV(slot.TLV)
			streams = append(streams, stream)
		}
		return streams, true
	}
	// A carrier that is not a frame is one stream. The analysis fills in
	// exactly one of the two multiplexings -- a capture is transport packets or
	// it is TLV -- so the second call only runs when the first found nothing.
	var stream carriedStream
	if stream.takeTransport(result.Analysis.Transport) || stream.takeTLV(result.Analysis.TLV) {
		streams = append(streams, stream)
	}
	return streams, false
}

func (stream *carriedStream) takeTransport(transport *isdb.TransportInfo) bool {
	if transport == nil {
		return false
	}
	if stream.streamID == 0 {
		stream.streamID = transport.TransportStreamID
	}
	stream.services = append(stream.services, transport.Services...)
	stream.settleNetwork(transport.OriginalNetworkID, transport.NetworkID)
	stream.takeRemoteKey(transport)
	if stream.networkName == "" {
		for _, network := range transport.Networks {
			// The stream's own network, which the table marks as such: a
			// network table describes its neighbours too, and their names are
			// theirs.
			if network.Actual && network.Name != "" {
				stream.networkName = network.Name
				break
			}
		}
	}
	return true
}

// takeRemoteKey reads the stream's own entry out of the network table it
// carries. A network information table describes every multiplex of the
// network, so the entry with this stream's identifier is this stream and the
// rest are neighbours.
func (stream *carriedStream) takeRemoteKey(transport *isdb.TransportInfo) {
	if stream.remoteKeyID != 0 {
		return
	}
	for _, network := range transport.Networks {
		for _, described := range network.Transports {
			if described.TransportStreamID == transport.TransportStreamID && described.RemoteControlKeyID != 0 {
				stream.remoteKeyID = described.RemoteControlKeyID
				return
			}
		}
	}
}

func (stream *carriedStream) takeTLV(tlv *isdb.TLVInfo) bool {
	if tlv == nil {
		return false
	}
	if stream.streamID == 0 {
		stream.streamID = tlv.TLVStreamID
	}
	// What the stream holds, not every service channel its network defines. See
	// isdb.TLVInfo.Carried: a TLV carrier's service table lists the lot, and
	// writing them all made every carrier of one network look like it held the
	// same first service.
	stream.services = append(stream.services, tlv.Carried()...)
	stream.settleNetwork(tlv.OriginalNetworkID, tlv.NetworkID)
	if stream.networkName == "" {
		for _, network := range tlv.Networks {
			if network.Name != "" && (network.NetworkID == stream.networkID || stream.networkID == 0) {
				stream.networkName = network.Name
				break
			}
		}
	}
	for _, assigned := range tlv.RemoteControlKeys {
		if stream.remoteKeys == nil {
			stream.remoteKeys = make(map[uint16]byte, len(tlv.RemoteControlKeys))
		}
		stream.remoteKeys[assigned.ServiceID] = assigned.RemoteControlKeyID
	}
	return true
}

func (stream *carriedStream) settleNetwork(declared, member uint16) {
	if stream.networkID != 0 {
		return
	}
	if declared != 0 {
		stream.networkID = declared
		return
	}
	stream.networkID = member
}
