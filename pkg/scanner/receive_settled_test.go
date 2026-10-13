// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/mpegts"
	"github.com/Till0196/coax/pkg/tuner"
)

// A channel that has already named its services and its network is left before
// the bound: the analysis time is a maximum, not a duration to be served out.
// The bound has to be generous because a network table comes round every ten
// seconds on a satellite, so on a scan of a hundred channels the difference is
// the whole of the running time.
func TestASampleEndsWhenTheChannelHasSaidEverything(t *testing.T) {
	stream := &repeatingStream{chunk: completeMultiplex()}
	options := Options{
		DataTimeout:  time.Second,
		AnalysisTime: 4 * time.Second,
		PollInterval: 10 * time.Millisecond,
		MinimumBytes: 188 * 16,
		MaximumBytes: 8 << 20,
	}
	started := time.Now()
	sample, _, err := receiveSampleFrom(context.Background(), options, analysisSettled, stream, nil)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("receiveSample: %v", err)
	}
	if !analysisSettled(sample) {
		t.Fatal("the sample was returned without everything the report needs")
	}
	if elapsed >= options.AnalysisTime {
		t.Errorf("the capture took %v, which is the whole bound of %v; a settled channel should be left before it",
			elapsed, options.AnalysisTime)
	}
}

// A stream that never names its network is never settled, so the capture runs
// to the whole bound: an early end is a saving, never a different answer.
func TestASampleRunsToTheBoundWhenTheChannelNeverSaysEnough(t *testing.T) {
	// Packets with nothing in them at all: no tables, so nothing to settle.
	// Paced, so that the bound is reached before the byte limit is.
	stream := &repeatingStream{chunk: nullPackets(64), paced: true}
	options := Options{
		DataTimeout:  time.Second,
		AnalysisTime: 300 * time.Millisecond,
		PollInterval: 10 * time.Millisecond,
		MinimumBytes: 188 * 16,
		MaximumBytes: 8 << 20,
	}
	started := time.Now()
	if _, _, err := receiveSampleFrom(context.Background(), options, analysisSettled, stream, nil); err != nil {
		t.Fatalf("receiveSample: %v", err)
	}
	if elapsed := time.Since(started); elapsed < options.AnalysisTime {
		t.Errorf("the capture ended after %v, before the bound of %v; an unsettled channel has to be held for the whole of it",
			elapsed, options.AnalysisTime)
	}
}

// repeatingStream hands out the same chunk for as long as it is read. Paced, it
// answers ErrWouldBlock every other read, which is what a real device does and
// what keeps a test from filling its byte limit at the speed of memory.
type repeatingStream struct {
	chunk []byte
	paced bool
	idle  bool
}

func (stream *repeatingStream) Read(_ context.Context, buffer []byte) (int, error) {
	if stream.paced {
		if stream.idle = !stream.idle; !stream.idle {
			return 0, tuner.ErrWouldBlock
		}
	}
	return copy(buffer, stream.chunk), nil
}

func (stream *repeatingStream) Close() error { return nil }

var _ tuner.Stream = (*repeatingStream)(nil)

// completeMultiplex is a transport stream that has said everything a scan wants
// of it: a programme association table, a service description naming a service,
// and a network information table naming the network it belongs to.
func completeMultiplex() []byte {
	var out []byte
	out = append(out, sectionPacket(0x00, patSection())...)
	out = append(out, sectionPacket(0x11, sdtSection())...)
	out = append(out, sectionPacket(0x10, nitSection())...)
	out = append(out, nullPackets(29)...)
	return out
}

func patSection() []byte {
	// table_id 0x00, one programme.
	body := []byte{0x7f, 0xe0, 0xc1, 0x00, 0x00, 0x00, 0x01, 0xe1, 0x00}
	return longSection(0x00, body)
}

func sdtSection() []byte {
	name := []byte("a service")
	descriptor := append([]byte{0x48, byte(3 + len(name)), 0x01, 0x00, byte(len(name))}, name...)
	service := append([]byte{0x00, 0x65, 0x00, 0x80 | byte(len(descriptor)>>8), byte(len(descriptor))}, descriptor...)
	// transport_stream_id, version, section, last, original_network_id, reserved
	body := append([]byte{0x7f, 0xe0, 0xc1, 0x00, 0x00, 0x00, 0x04, 0xff}, service...)
	return longSection(0x42, body)
}

func nitSection() []byte {
	// One transport stream, described by nothing in particular.
	transports := []byte{0x7f, 0xe0, 0x00, 0x04, 0xf0, 0x00}
	body := []byte{0x7f, 0xe0, 0xc1, 0x00, 0x00, 0xf0, 0x00,
		0xf0 | byte(len(transports)>>8), byte(len(transports))}
	body = append(body, transports...)
	return longSection(0x40, body)
}

func longSection(tableID byte, body []byte) []byte {
	length := len(body) + 4
	section := []byte{tableID, 0xb0 | byte(length>>8), byte(length)}
	section = append(section, body...)
	return binary.BigEndian.AppendUint32(section, mpegts.CRC32(section))
}

func sectionPacket(pid uint16, section []byte) []byte {
	packet := make([]byte, mpegts.PacketSize)
	packet[0] = 0x47
	packet[1] = 0x40 | byte(pid>>8)
	packet[2] = byte(pid)
	packet[3] = 0x10
	packet[4] = 0x00 // pointer field
	copy(packet[5:], section)
	for i := 5 + len(section); i < len(packet); i++ {
		packet[i] = 0xff
	}
	return packet
}

func nullPackets(count int) []byte {
	var out []byte
	for range count {
		packet := make([]byte, mpegts.PacketSize)
		packet[0] = 0x47
		packet[1] = 0x1f
		packet[2] = 0xff
		packet[3] = 0x10
		out = append(out, packet...)
	}
	return out
}

// A multiplexing frame says how many carriers it is spread over. Where that is
// more than one, this carrier holds a fraction of every stream on it and none
// can ever be complete from here, so the header is all there is to wait for.
// Measured, a broadcast split three ways over a cable showed its network table
// once in eighty seconds.
func TestASplitCarrierIsSettledOnceItsHeaderIsIn(t *testing.T) {
	for _, test := range []struct {
		what     string
		carriers byte
		want     bool
	}{
		{"one carrier, and its slot has said nothing", 1, false},
		{"one of three, which is all it will ever be", 3, true},
	} {
		analysis := &isdb.Analysis{TSMF: &isdb.TSMFInfo{
			NumberOfCarriers: test.carriers,
			CarrierSequence:  1,
			RelativeStreams: []isdb.RelativeStreamInfo{
				{RelativeStream: 1, StreamID: 45280, OriginalNetworkID: 11, Format: "tlv"},
			},
		}}
		if got := multiplexSettled(analysis); got != test.want {
			t.Errorf("%s: settled = %v, want %v", test.what, got, test.want)
		}
	}
}

// The scan line names which carrier of a split multiplex this is: a third of a
// multiplex names a third of what is on it, and a line that did not say so
// would read as a broken channel.
func TestASplitCarrierSaysSoWhileItIsScanned(t *testing.T) {
	line := carrierLine(&isdb.TSMFInfo{
		NumberOfCarriers: 3, CarrierSequence: 2,
		RelativeStreams: []isdb.RelativeStreamInfo{
			{RelativeStream: 1, StreamID: 45280, OriginalNetworkID: 11, Format: "tlv"},
		},
	})
	if !strings.Contains(line, "carrier 2/3") {
		t.Errorf("the scan line does not say which carrier this is: %q", line)
	}
}

// The bootstrap that looks for a TLV network walks the transponders asking each
// for a TLV stream. Most are the older broadcasts and answer with transport
// packets, which is an answer: there is no TLV network table on a carrier that
// is not TLV, so waiting the ten seconds a network table can take would be
// spent per transponder on a stream that has none.
func TestTheTLVProbeGivesUpOnATransportStream(t *testing.T) {
	// A transport stream with nothing else in it: no service or network
	// tables, so the ordinary reading would wait for the whole bound.
	packets := nullPackets(64)
	if analysisSettled(packets) {
		t.Fatal("the sample settles on its own, so this proves nothing")
	}
	if !tlvProbeSettled(packets) {
		t.Error("the probe kept reading a stream that is plainly not TLV")
	}

	// A sample that has not started on a packet boundary says neither, and
	// giving up on it would pass over a transponder that does carry TLV.
	unknown := append([]byte{0x11, 0x22, 0x33}, make([]byte, 400)...)
	if tlvProbeSettled(unknown) {
		t.Error("the probe gave up on a sample it had not yet made sense of")
	}
}

// A carrier that is TLV is the one worth waiting on, since its network table is
// the whole point of the walk.
func TestTheTLVProbeStillWaitsForTheTableItCameFor(t *testing.T) {
	// TLV packets carrying nothing this can read: the format is right, so the
	// probe has not learnt anything yet and must keep reading.
	var tlvStream []byte
	for range 200 {
		tlvStream = append(tlvStream, 0x7f, 0xff, 0x00, 0x04, 0, 0, 0, 0)
	}
	if isdb.DetectFormat(tlvStream) != "tlv" {
		t.Fatalf("the sample is not detected as TLV, so this proves nothing")
	}
	if tlvProbeSettled(tlvStream) {
		t.Error("the probe gave up on a TLV carrier before its network table arrived")
	}
}

// The tables a scan waits for differ by an order of magnitude between the
// systems: over the air the network table comes round every second, from a
// satellite every ten -- and on a thin transponder it takes most of that ten to
// come over, so a capture beginning part way through waits for the next. One
// bound for both would be far too long for a terrestrial plan of a hundred
// channels or far too short for a satellite.
func TestTheBoundIsTakenFromWhatTheChannelIs(t *testing.T) {
	for _, test := range []struct {
		system string
		want   time.Duration
	}{
		{"ISDBT", terrestrialAnalysisTime},
		{"ISDB-T", terrestrialAnalysisTime},
		{"DVBT2", terrestrialAnalysisTime},
		{"ISDBS", satelliteAnalysisTime},
		{"ISDBS3", satelliteAnalysisTime},
		{"DVBS2", satelliteAnalysisTime},
		// A cable is given the satellite's figure although its own tables are
		// quick, because it carries satellite multiplexes remodulated onto it,
		// cadence and all.
		{"DVBC/ANNEX_A", satelliteAnalysisTime},
		{"DVBC/ANNEX_B", satelliteAnalysisTime},
		// Something nobody here has a name for is given room rather than cut
		// short.
		{"", satelliteAnalysisTime},
	} {
		if got := AnalysisTimeFor(test.system); got != test.want {
			t.Errorf("AnalysisTimeFor(%q) = %v, want %v", test.system, got, test.want)
		}
	}

	// What the caller asked for stands, for every channel alike.
	asked := Options{AnalysisTime: 3 * time.Second}
	if got := asked.analysisTime("ISDBS"); got != 3*time.Second {
		t.Errorf("an analysis time given on the command line became %v", got)
	}
	open := Options{}
	if got := open.analysisTime("ISDBT"); got != terrestrialAnalysisTime {
		t.Errorf("with none given, a terrestrial channel got %v", got)
	}
}

// A time named on the command line is a time to spend. The service tables of
// the network's other multiplexes come round slowly, and a caller who asked
// for twenty seconds of a transponder wants what arrives in the twentieth
// second: the names of the services on the multiplexes this one is not.
func TestANamedAnalysisTimeIsServedOut(t *testing.T) {
	stream := &repeatingStream{chunk: completeMultiplex(), paced: true}
	options := Options{
		DataTimeout:      time.Second,
		AnalysisTime:     300 * time.Millisecond,
		HoldAnalysisTime: true,
		PollInterval:     10 * time.Millisecond,
		MinimumBytes:     188 * 16,
		MaximumBytes:     8 << 20,
	}
	started := time.Now()
	sample, _, err := receiveSampleFrom(context.Background(), options, settledFor(options), stream, nil)
	if err != nil {
		t.Fatalf("receiveSample: %v", err)
	}
	// The channel said everything in its first chunk and was held anyway.
	if !analysisSettled(sample) {
		t.Fatal("the sample was returned without everything the report needs")
	}
	if elapsed := time.Since(started); elapsed < options.AnalysisTime {
		t.Errorf("the capture ended after %v, before the %v that was asked for",
			elapsed, options.AnalysisTime)
	}
}
