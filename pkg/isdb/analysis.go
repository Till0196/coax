// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"math"

	"github.com/Till0196/coax/pkg/mpegts"
)

const (
	tsPacketSize = mpegts.PacketSize
	tsmfPID      = HeaderPID
)

func megabits(packets [][]byte) *float64 {
	return megabitsPerSecond(mpegts.Bitrate(packets))
}

// megabitsPerSecond rounds a rate in bits per second to two decimal places,
// and reports nothing when there was no clock to measure against.
func megabitsPerSecond(rate float64, ok bool) *float64 {
	if !ok {
		return nil
	}
	value := math.Round(rate/1e4) / 100
	return &value
}

type RelativeStreamInfo struct {
	RelativeStream    int    `json:"relative_stream"`
	StreamID          uint16 `json:"stream_id"`
	OriginalNetworkID uint16 `json:"original_network_id"`
	Format            string `json:"format"`
	SlotCount         int    `json:"slot_count"`
	// ReceiveStatus is how the sender says the stream is being received:
	// nought normal, one poor, two not received.
	ReceiveStatus byte `json:"receive_status,omitempty"`
	// BitrateMbps is this stream's own rate, measured the same way as the
	// multiplex it is a slot of.
	BitrateMbps *float64       `json:"bitrate_mbps,omitempty"`
	Transport   *TransportInfo `json:"transport,omitempty"`
	TLV         *TLVInfo       `json:"tlv,omitempty"`
}

type TSMFInfo struct {
	// BitrateMbps is how fast the carrier runs, worked out from the slots
	// rather than measured directly. A TSMF multiplex has no clock of its own:
	// every clock in it belongs to one of the streams sharing it.
	BitrateMbps      *float64 `json:"bitrate_mbps,omitempty"`
	GroupID          byte     `json:"group_id"`
	NumberOfCarriers byte     `json:"number_of_carriers"`
	CarrierSequence  byte     `json:"carrier_sequence"`
	FramePosition    byte     `json:"frame_position"`
	NumberOfFrames   byte     `json:"number_of_frames"`
	Version          byte     `json:"version"`
	RelativeMode     byte     `json:"relative_mode"`
	FrameType        byte     `json:"frame_type"`
	// Emergency is the emergency warning start signal.
	Emergency bool `json:"emergency,omitempty"`
	// DecodedFrames is how many frame headers the sample yielded.
	DecodedFrames   int                  `json:"decoded_frames,omitempty"`
	RelativeStreams []RelativeStreamInfo `json:"relative_streams"`
}

type Analysis struct {
	// BitrateMbps is how fast the multiplex runs, read off the sender's own
	// clock rather than off how long the capture took: the programme clock
	// reference of a transport stream, or the transmission timestamps of a TLV
	// stream's MMTP packets. Absent for a stream that carries no clock, or too
	// short a piece of one to divide by.
	BitrateMbps *float64       `json:"bitrate_mbps,omitempty"`
	Transport   *TransportInfo `json:"transport,omitempty"`
	TSMF        *TSMFInfo      `json:"tsmf,omitempty"`
	TLV         *TLVInfo       `json:"tlv,omitempty"`
}

func Analyze(sample []byte, format string) *Analysis {
	if format == "tlv" {
		// Received as TLV rather than lifted out of a transport stream, which
		// is how the ISDB-S3 satellite services arrive.
		if info, rate := analyzeTLV(sample); info != nil {
			return &Analysis{TLV: info, BitrateMbps: rate}
		}
		return nil
	}
	if format != "ts" {
		return nil
	}
	packets := mpegts.SplitPackets(sample)
	if len(packets) == 0 {
		return nil
	}
	analysis := &Analysis{Transport: AnalyzePSI(packets), BitrateMbps: megabits(packets)}
	header := Find(packets)
	if header == nil {
		if analysis.Transport == nil {
			return nil
		}
		return analysis
	}

	// TSMF slots contain independent streams. Keep MPEG-2 TS and TLV parsing
	// in their respective protocol packages and only combine their reports here.
	analysis.Transport = nil
	// The rate goes the same way, and for the same reason: the clocks it was read
	// from belong to streams that are only a share of these packets. Where a
	// multiplex carries no transport stream at all, a stray byte pattern that
	// looks like a clock reference is taken for one -- a channel reported at 0.01
	// Mbps. The rate comes from the slots instead.
	analysis.BitrateMbps = nil
	header, streams := Demultiplex(packets, header)
	info := &TSMFInfo{
		GroupID:          header.GroupID,
		NumberOfCarriers: header.NumberOfCarriers,
		CarrierSequence:  header.CarrierSequence,
		FramePosition:    header.FramePosition,
		NumberOfFrames:   header.NumberOfFrames,
		Version:          header.Version,
		RelativeMode:     header.RelativeMode,
		FrameType:        header.FrameType,
		Emergency:        header.Emergency,
		DecodedFrames:    streams.Frames,
	}
	for _, relative := range header.RelativeStreams {
		result := RelativeStreamInfo{
			RelativeStream:    relative.Number,
			StreamID:          relative.StreamID,
			OriginalNetworkID: relative.OriginalNetworkID,
			Format:            relative.Format,
			SlotCount:         relative.SlotCount,
			ReceiveStatus:     relative.ReceiveStatus,
		}
		if relative.Format == "ts" && len(streams.TS[relative.Number]) > 0 {
			result.Transport = AnalyzePSI(streams.TS[relative.Number])
			result.BitrateMbps = megabits(streams.TS[relative.Number])
		} else if relative.Format == "tlv" && len(streams.TLV[relative.Number]) > 0 {
			result.TLV, result.BitrateMbps = analyzeTLV(streams.TLV[relative.Number])
			// A transport stream's rate counts the packets it travels in;
			// the TLV rate above counts only the bytes lifted out of them.
			// Scaled back onto the carrying packets, the two are the same
			// measurement and can be compared and added.
			result.BitrateMbps = inSlotTerms(result.BitrateMbps,
				len(streams.TS[relative.Number]), len(streams.TLV[relative.Number]))
		}
		info.RelativeStreams = append(info.RelativeStreams, result)
	}
	info.BitrateMbps = carrierRate(info.RelativeStreams)
	analysis.TSMF = info
	return analysis
}

// inSlotTerms scales a rate measured over a payload onto the packets that
// carried it, so that a TLV slot and a transport-stream slot are quoted the
// same way.
func inSlotTerms(rate *float64, packets, payloadBytes int) *float64 {
	if rate == nil || packets <= 0 || payloadBytes <= 0 {
		return nil
	}
	value := math.Round(*rate*float64(packets)*tsPacketSize/float64(payloadBytes)*100) / 100
	return &value
}

// carrierRate is how fast the carrier runs, from what its slots were measured
// at. A frame is one header packet and SlotCount slots, all the same size and
// evenly spaced, so the measured slots stand for the frame in proportion to how
// many there are; unused slots count as carrying nothing.
//
// This is the figure that tells the modulation apart on a cable feed -- 29.16
// Mbps is 64QAM and 38.88 Mbps is 256QAM -- which nothing else here reports.
func carrierRate(relatives []RelativeStreamInfo) *float64 {
	total, slots := 0.0, 0
	for _, relative := range relatives {
		if relative.BitrateMbps == nil || relative.SlotCount <= 0 {
			continue
		}
		total += *relative.BitrateMbps
		slots += relative.SlotCount
	}
	if slots <= 0 {
		return nil
	}
	value := math.Round(total*float64(SlotCount+1)/float64(slots)*100) / 100
	return &value
}
