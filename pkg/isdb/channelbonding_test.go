// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import "testing"

// TestChannelBondingSaysWhereEachCarrierIs. The TLV services on a cable
// are placed by the cable multiple carrier transmission delivery system
// descriptor rather than by the satellite one or the ordinary cable one, and a
// reader looking for those two finds an operator's table saying nothing about
// where anything is. The bytes are one feed's own.
func TestChannelBondingSaysWhereEachCarrierIs(t *testing.T) {
	// The stream that was tuned when this was captured: 153 MHz, which is what
	// its own record says.
	one := ParseChannelBondingCable([]byte{
		0x01, 0x53, 0x00, 0x00, 0xff, 0x22, 0x05, 0x00, 0x52, 0x74, 0x0f, 0x06,
	})
	if len(one) != 1 {
		t.Fatalf("read %d carriers, want 1", len(one))
	}
	carrier := one[0]
	if carrier.FrequencyHz != 153_000_000 {
		t.Errorf("frequency %d, want 153000000", carrier.FrequencyHz)
	}
	if carrier.SymbolRate != 5_274_000 {
		t.Errorf("symbol rate %d, want 5274000", carrier.SymbolRate)
	}
	if got := carrier.CableModulation(); got != "QAM/256" {
		t.Errorf("modulation %q, want QAM/256", got)
	}
	if got := carrier.InnerFEC(); got != "NONE" {
		t.Errorf("inner FEC %q, want NONE", got)
	}
	// The byte the frame type and the outer code share: 0x22 is frame type 2
	// and RS(204,188), not twelve bits of reserved and a code.
	if carrier.FrameType != 2 {
		t.Errorf("frame type %d, want 2", carrier.FrameType)
	}
	if carrier.FECOuter != 2 {
		t.Errorf("outer FEC %d, want 2", carrier.FECOuter)
	}
	if carrier.GroupID != 6 {
		t.Errorf("group %d, want 6", carrier.GroupID)
	}

	// A multiplex too wide for one carrier is sent over several, and the
	// descriptor lists all of them -- with the same group on each, which is
	// what says they are to be put back together.
	split := ParseChannelBondingCable([]byte{
		0x00, 0x93, 0x00, 0x00, 0xff, 0x22, 0x05, 0x00, 0x52, 0x74, 0x0f, 0x08,
		0x00, 0x99, 0x00, 0x00, 0xff, 0x22, 0x05, 0x00, 0x52, 0x74, 0x0f, 0x08,
		0x01, 0x05, 0x00, 0x00, 0xff, 0x22, 0x05, 0x00, 0x52, 0x74, 0x0f, 0x08,
	})
	want := []uint32{93_000_000, 99_000_000, 105_000_000}
	if len(split) != len(want) {
		t.Fatalf("read %d carriers of a split frame, want %d", len(split), len(want))
	}
	for index, frequency := range want {
		if split[index].FrequencyHz != frequency {
			t.Errorf("carrier %d is at %d, want %d", index, split[index].FrequencyHz, frequency)
		}
		if split[index].GroupID != 8 {
			t.Errorf("carrier %d is in group %d, want 8 on every carrier of the multiplex",
				index, split[index].GroupID)
		}
	}

	// A record that did not arrive whole is left off rather than half-read,
	// and one whose digits are not digits is not believed.
	if got := ParseChannelBondingCable([]byte{0x01, 0x53, 0x00, 0x00, 0xff}); got != nil {
		t.Errorf("a truncated record was read: %v", got)
	}
	if got := ParseChannelBondingCable([]byte{
		0xab, 0xcd, 0x00, 0x00, 0xff, 0x22, 0x05, 0x00, 0x52, 0x74, 0x0f, 0x06,
	}); got != nil {
		t.Errorf("a record that is not decimal was read: %v", got)
	}
}

// TestSystemManagementTellsBroadcastFromNot. ARIB STD-B10 6.2.21 makes this
// descriptor mandatory in either the PMT or the network table, and its top two
// bits are the whole answer: a multiplex an operator carries for its own
// purposes says non-broadcasting, and a scan that could not tell would file it
// beside the television.
func TestSystemManagementTellsBroadcastFromNot(t *testing.T) {
	// Broadcasting, identifier 3, additional 0x01.
	if got := ParseSystemManagement([]byte{0x03, 0x01}); got == nil {
		t.Fatal("a broadcast descriptor was refused")
	} else if !got.Broadcasting || got.BroadcastingIdentifier != 3 || got.AdditionalIdentification != 1 {
		t.Errorf("read %+v", *got)
	}
	// The two non-broadcasting values, and the undefined one -- which is not a
	// promise that it is a broadcast either.
	for _, flag := range []byte{0x40, 0x80, 0xc0} {
		got := ParseSystemManagement([]byte{flag, 0x00})
		if got == nil || got.Broadcasting {
			t.Errorf("%#02x was read as a broadcast", flag)
		}
	}
	// Whatever followed the identifier is kept rather than dropped.
	if got := ParseSystemManagement([]byte{0x03, 0x01, 0xaa, 0xbb}); got == nil ||
		len(got.Extra) != 2 || got.Extra[0] != 0xaa {
		t.Errorf("the trailing bytes were lost: %+v", got)
	}
	// Too short to hold the identifier at all.
	if ParseSystemManagement([]byte{0x03}) != nil {
		t.Error("a truncated descriptor was read")
	}
}

// TestTheOtherNetworkTableIsRead. A cable carrying both generations sends the
// TLV network table twice: 0x40 for the network the stream belongs to, and 0x41
// for a neighbour. Reading only the first loses the 2K multiplexes on the same
// lead. The bytes are one feed's own -- the older network's entry, placed with
// the ordinary cable descriptor.
func TestTheOtherNetworkTableIsRead(t *testing.T) {
	// table_id 0x41, network 4, one stream: 16625 with a 0x44 at 279 MHz.
	stream := []byte{
		0xb0, 0xf1, 0x00, 0x04, // tlv_stream_id, original_network_id
		0xf0, 0x12, // descriptors_length 18
		0x41, 0x03, 0x03, 0xa1, 0xa4, // service_list
		0x44, 0x0b, 0x02, 0x79, 0x00, 0x00, 0xff, 0x12, 0x03, 0x00, 0x52, 0x74, 0x0f,
	}
	body := []byte{
		0x00, 0x04, 0xc1, 0x00, 0x00, // network 4, version, section numbers
		0xf0, 0x00, // no network descriptors
		0xf0, byte(len(stream)), // the stream loop
	}
	body = append(body, stream...)
	section := crcSection(TableIDTLVNITOther, body)

	state := NewState()
	state.PushTLVSection(section)
	network, ok := state.Networks[4]
	if !ok {
		t.Fatalf("the other network was not read: %v", state.Networks)
	}
	if len(network.Streams) != 1 {
		t.Fatalf("read %d streams, want 1", len(network.Streams))
	}
	one := network.Streams[0]
	if one.TLVStreamID != 0xb0f1 || one.Cable == nil {
		t.Fatalf("read %+v", one)
	}
	if one.Cable.FrequencyHz != 279_000_000 || one.Cable.CableModulation() != "QAM/64" {
		t.Errorf("the older multiplex is at %d, %q", one.Cable.FrequencyHz, one.Cable.CableModulation())
	}
	// The stream still belongs to its own network, not to the one it was
	// merely told about.
	if state.NetworkID == 4 {
		t.Error("the other network's identifier was taken as the stream's own")
	}
}
