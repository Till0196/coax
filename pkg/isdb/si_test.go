// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"encoding/binary"
	"testing"

	"github.com/Till0196/coax/pkg/mpegts"
)

func TestPushMHSDTMessage(t *testing.T) {
	descriptorBody := []byte{0xa5, 0, 3, 'T', 'L', 'V'}
	descriptor := []byte{0x80, 0x19, byte(len(descriptorBody))}
	descriptor = append(descriptor, descriptorBody...)
	service := []byte{0, 181, 0xfc, 0, byte(len(descriptor))}
	service = append(service, descriptor...)
	body := []byte{0xb1, 0x12, 0xc1, 0, 0, 0, 4, 0xff}
	body = append(body, service...)
	section := longSection(TableIDMHSDTActual, body)
	state := NewState()
	state.PushMessage(MessageIDM2Section, section)
	if state.TLVStreamID != 0xb112 || state.OriginalNetworkID != 4 || state.Services[181].Name != "TLV" {
		t.Fatalf("state = %#v, services = %#v", state, state.Services)
	}
}

func longSection(tableID byte, body []byte) []byte {
	length := len(body) + 4
	section := []byte{tableID, 0xb0 | byte(length>>8), byte(length)}
	section = append(section, body...)
	section = binary.BigEndian.AppendUint32(section, mpegts.CRC32(section))
	return section
}

// TLV-SI carries text as UTF-8, unlike the terrestrial tables, which use the
// ARIB 8-unit code. Decoding one as the other does not fail: it produces
// well-formed but wrong Japanese.
func TestNetworkNameIsReadAsUTF8(t *testing.T) {
	const want = "高度BSデジタル放送"
	body := []byte(want)
	descriptors := append([]byte{0x40, byte(len(body))}, body...)
	network := &Network{}
	parseTLVNetworkDescriptors(descriptors, network)
	if network.Name != want {
		t.Fatalf("network name = %q, want %q", network.Name, want)
	}
}

// TestNetworkNameRejectsInvalidUTF8 keeps a mis-framed descriptor from being
// reported as a name.
func TestNetworkNameRejectsInvalidUTF8(t *testing.T) {
	body := []byte{0xff, 0xfe, 0xfd}
	network := &Network{}
	parseTLVNetworkDescriptors(append([]byte{0x40, byte(len(body))}, body...), network)
	if network.Name != "" {
		t.Fatalf("network name = %q, want it left empty", network.Name)
	}
}

// The advanced services are the only broadcast that says which button a service
// is on outright: ARIB TR-B39 36.1.2.3 requires the descriptor whenever a
// TLV-NIT [actual] is transmitted, and STD-B60 5.3.1.5 lays it out as a count
// and then five bytes per service. Everything else has to be guessed at from
// the service identifier.
func TestRemoteControlKeyDescriptorIsReadServiceByService(t *testing.T) {
	body := []byte{
		2,
		4, 0x00, 0x8d, 0xff, 0xff, // button 4 selects service 141
		1, 0x01, 0x91, 0xff, 0xff, // button 1 selects service 401
	}
	network := &Network{}
	parseTLVNetworkDescriptors(append([]byte{TagRemoteControlKey, byte(len(body))}, body...), network)
	if len(network.RemoteKeys) != 2 || network.RemoteKeys[141] != 4 || network.RemoteKeys[401] != 1 {
		t.Fatalf("remote keys = %v", network.RemoteKeys)
	}
}

// The count and the length are two statements about the same descriptor, and a
// stream that gets them out of step must not be read past the end of.
func TestRemoteControlKeyDescriptorStopsAtWhatIsThere(t *testing.T) {
	body := []byte{12, 4, 0x00, 0x8d, 0xff, 0xff}
	network := &Network{}
	parseTLVNetworkDescriptors(append([]byte{TagRemoteControlKey, byte(len(body))}, body...), network)
	if len(network.RemoteKeys) != 1 || network.RemoteKeys[141] != 4 {
		t.Fatalf("remote keys = %v, want the one entry the descriptor actually holds", network.RemoteKeys)
	}
}

// The package list says which services a TLV stream really carries, and it
// arrives in a PA message whose table index the streams here leave empty.
func TestPushPackageListMessage(t *testing.T) {
	table := []byte{TableIDPLT, 0x14, 0, 8, 1, 2, 0, 0x97, 0x00, 0xa0, 0x01, 0}
	message := append([]byte{0}, table...)
	state := NewState()
	state.PushMessage(MessageIDPA, message)
	if len(state.Carried) != 1 || state.Carried[0] != 151 {
		t.Fatalf("carried = %#v, want the one package the list names", state.Carried)
	}
	if state.InvalidSections != 0 {
		t.Errorf("invalid sections = %d, want none", state.InvalidSections)
	}
}

// A package list cut short is not a list of the services before the cut: the
// count promised more than arrived.
func TestATruncatedPackageListIsRefused(t *testing.T) {
	table := []byte{TableIDPLT, 0x14, 0, 8, 2, 2, 0, 0x97, 0x00, 0xa0, 0x01, 2}
	state := NewState()
	state.PushMessage(MessageIDPA, append([]byte{0}, table...))
	if len(state.Carried) != 0 || state.InvalidSections != 1 {
		t.Fatalf("carried = %#v, invalid = %d", state.Carried, state.InvalidSections)
	}
}

// Other tables ride in the same message and have to be stepped over by their
// own length.
func TestPackageListIsFoundAfterAnotherTable(t *testing.T) {
	message := []byte{0, 0x20, 0x60, 0, 3, 0xff, 0xff, 0xff}
	message = append(message, TableIDPLT, 0x14, 0, 8, 1, 2, 0, 0x97, 0x00, 0xa0, 0x01, 0)
	state := NewState()
	state.PushMessage(MessageIDPA, message)
	if len(state.Carried) != 1 || state.Carried[0] != 151 {
		t.Fatalf("carried = %#v", state.Carried)
	}
}

// An M2 section message is padded to its length with bytes that cannot begin a
// section. They are not a section that failed to check out.
func TestPaddingAfterTheSectionsIsNotAnInvalidSection(t *testing.T) {
	descriptorBody := []byte{0xa5, 0, 3, 'T', 'L', 'V'}
	descriptor := append([]byte{0x80, 0x19, byte(len(descriptorBody))}, descriptorBody...)
	service := append([]byte{0, 181, 0xfc, 0, byte(len(descriptor))}, descriptor...)
	body := append([]byte{0xb1, 0x12, 0xc1, 0, 0, 0, 4, 0xff}, service...)
	message := append(longSection(TableIDMHSDTActual, body), 0xff, 0xff, 0xff, 0xff, 0xff)
	state := NewState()
	state.PushMessage(MessageIDM2Section, message)
	if state.Services[181].Name != "TLV" || state.InvalidSections != 0 {
		t.Fatalf("services = %#v, invalid = %d", state.Services, state.InvalidSections)
	}
}

// A section that does not check out is left alone: nothing of it is read.
func TestASectionThatDoesNotCheckOutIsLeftAlone(t *testing.T) {
	descriptorBody := []byte{0xa5, 0, 3, 'T', 'L', 'V'}
	descriptor := append([]byte{0x80, 0x19, byte(len(descriptorBody))}, descriptorBody...)
	service := append([]byte{0, 181, 0xfc, 0, byte(len(descriptor))}, descriptor...)
	body := append([]byte{0xb1, 0x12, 0xc1, 0, 0, 0, 4, 0xff}, service...)
	section := longSection(TableIDMHSDTActual, body)
	section[len(section)-1] ^= 0xff
	state := NewState()
	state.PushMessage(MessageIDM2Section, section)
	if len(state.Services) != 0 || state.InvalidSections != 1 {
		t.Fatalf("services = %#v, invalid = %d", state.Services, state.InvalidSections)
	}
}

// The service table's text is UTF-8. Bytes that are not are no name at all.
func TestAServiceNameThatIsNotUTF8IsNotTaken(t *testing.T) {
	descriptorBody := []byte{0xa5, 0, 3, 0xff, 0xfe, 0xfd}
	descriptor := append([]byte{0x80, 0x19, byte(len(descriptorBody))}, descriptorBody...)
	service := append([]byte{0, 181, 0xfc, 0, byte(len(descriptor))}, descriptor...)
	body := append([]byte{0xb1, 0x12, 0xc1, 0, 0, 0, 4, 0xff}, service...)
	state := NewState()
	state.PushMessage(MessageIDM2Section, longSection(TableIDMHSDTActual, body))
	if got := state.Services[181]; got.Name != "" || got.Type != 0xa5 {
		t.Fatalf("service = %#v", got)
	}
}

// tlvNIT builds a TLV-NIT section with one stream entry.
func tlvNIT(version byte, networkDescriptors, streamDescriptors []byte) []byte {
	body := []byte{0x00, 0x0b, 0xc1 | version<<1, 0, 0}
	body = binary.BigEndian.AppendUint16(body, 0xf000|uint16(len(networkDescriptors)))
	body = append(body, networkDescriptors...)
	entry := []byte{0xb1, 0x12, 0x00, 0x0b}
	entry = binary.BigEndian.AppendUint16(entry, 0xf000|uint16(len(streamDescriptors)))
	entry = append(entry, streamDescriptors...)
	body = binary.BigEndian.AppendUint16(body, 0xf000|uint16(len(entry)))
	body = append(body, entry...)
	return longSection(TableIDTLVNITActual, body)
}

// A new version of the table lists the same streams. The stream already
// described is left as it was rather than listed twice.
func TestANewVersionOfTheNetworkTableDoesNotListAStreamTwice(t *testing.T) {
	services := []byte{0x41, 3, 0x00, 0x8d, 0x01}
	state := NewState()
	state.PushTLVSection(tlvNIT(1, nil, services))
	state.PushTLVSection(tlvNIT(2, nil, services))
	network := state.Networks[0x000b]
	if network == nil || len(network.Streams) != 1 {
		t.Fatalf("network = %#v", network)
	}
}

// The first word on a service's button stands.
func TestTheFirstRemoteControlKeyForAServiceStands(t *testing.T) {
	body := []byte{
		2,
		4, 0x00, 0x8d, 0xff, 0xff,
		7, 0x00, 0x8d, 0xff, 0xff,
	}
	network := &Network{}
	parseTLVNetworkDescriptors(append([]byte{TagRemoteControlKey, byte(len(body))}, body...), network)
	if network.RemoteKeys[141] != 4 {
		t.Fatalf("remote keys = %v", network.RemoteKeys)
	}
}

// A stream entry of the TLV-NIT carries the descriptors the transport stream
// NIT does, and they are read the same way.
func TestTheNetworkTableReadsTheStreamDescriptorsTheTransportTableDoes(t *testing.T) {
	descriptors := []byte{
		0xfb, 2, 0x00, 0x8d, // partial reception
		0xdd, 7, 0x00, 0x0a, 0x00, 0x00, 0x00, 0x00, 0x00, // connected transmission
	}
	state := NewState()
	state.PushTLVSection(tlvNIT(1, nil, descriptors))
	stream := state.Networks[0x000b].Streams[0]
	if len(stream.PartialReceptionServiceIDs) != 1 || stream.PartialReceptionServiceIDs[0] != 0x8d {
		t.Fatalf("partial reception = %v", stream.PartialReceptionServiceIDs)
	}
	if stream.Connected == nil {
		t.Fatal("the connected transmission descriptor was not read")
	}
}
