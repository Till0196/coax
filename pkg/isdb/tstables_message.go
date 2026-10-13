// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"bytes"
	"encoding/binary"
)

// TableIDEMMMessage is the EMM message section (ARIB STD-B25 3.2.5): the
// messages of the conditional access system, on the PID the CAT names.
const TableIDEMMMessage = 0x85

// MessageInserter marks where in a common message the bytes a card's own
// message carries are put (TR-B15 5.10.2).
const MessageInserter = 0x1a

// CommonMessage is an EMM common message: one preset text that every receiver
// is sent alike. A message held for one card picks it by its number and may
// add a few bytes of its own, which is how the automatic display messages and
// the mail are written. ARIB STD-B25 3.2.5.1 and TR-B15 5.10.
type CommonMessage struct {
	// FixedMessageID is the preset text's number (table_id_extension, 0x0001
	// to 0xffff).
	FixedMessageID     uint16 `json:"fixed_message_id"`
	Version            byte   `json:"version"`
	BroadcasterGroupID byte   `json:"broadcaster_group_id"`
	// DeletionStatus 0x00 the viewer may close it, 0x01 the viewer may not,
	// 0x02 it is not shown (and one being shown is taken down). The fields
	// after it mean nothing when it is 0x02.
	DeletionStatus byte `json:"deletion_status"`
	// Durations is how long it is shown, hidden and shown again after a
	// channel is selected, in tenths of a minute. 0xff is for as long as the
	// channel is.
	Durations [3]byte `json:"durations"`
	// Cycle is how many times the three durations repeat.
	Cycle byte `json:"cycle"`
	// FormatVersion 0x01 is the only format defined (TR-B15 5.10.1).
	FormatVersion byte   `json:"format_version"`
	Message       []byte `json:"message,omitempty"`
}

// Position is the recommended position of format 0x01: the upper four bits
// across (0b0100 left, 0b0010 centre, 0b0001 right), the lower four down
// (0b0100 top, 0b0010 centre, 0b0001 bottom).
func (m *CommonMessage) Position() (byte, bool) {
	if m.FormatVersion != 0x01 || len(m.Message) == 0 {
		return 0, false
	}
	return m.Message[0], true
}

// Text is the text of format 0x01, in the eight-unit code of STD-B24, up to
// the NULL that ends it. Where it holds MessageInserter a card's own bytes go.
func (m *CommonMessage) Text() ([]byte, bool) {
	if m.FormatVersion != 0x01 || len(m.Message) == 0 {
		return nil, false
	}
	text := m.Message[1:]
	if end := bytes.IndexByte(text, 0); end >= 0 {
		text = text[:end]
	}
	return text, true
}

// Compose is the text with extra put in place of every inserter, ready for
// DecodeString.
func (m *CommonMessage) Compose(extra []byte) ([]byte, bool) {
	text, ok := m.Text()
	if !ok {
		return nil, false
	}
	return bytes.ReplaceAll(text, []byte{MessageInserter}, extra), true
}

// ParseCommonMessage reads an EMM common message section. The same table ID
// carries the messages for single cards under the extension 0x0000; those
// are not read here.
func ParseCommonMessage(section []byte) *CommonMessage {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDEMMMessage || header.Extension == 0 || len(header.Body) < 9 {
		return nil
	}
	body := header.Body
	length := int(binary.BigEndian.Uint16(body[7:]))
	if 9+length > len(body) {
		return nil
	}
	return &CommonMessage{
		FixedMessageID:     header.Extension,
		Version:            header.Version,
		BroadcasterGroupID: body[0],
		DeletionStatus:     body[1],
		Durations:          [3]byte{body[2], body[3], body[4]},
		Cycle:              body[5],
		FormatVersion:      body[6],
		Message:            append([]byte(nil), body[9:9+length]...),
	}
}
