// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"bytes"
	"testing"
)

func TestAnEMMCommonMessageIsReadAndComposed(t *testing.T) {
	body := []byte{
		0x12,             // the broadcaster group
		0x01,             // the viewer may not close it
		0x0a, 0x05, 0xff, // one minute, half a minute, then for good
		0x02, // twice
		0x01, // format 0x01
	}
	message := []byte{0x41, 0xb0, 0xa2, 0x1a, 0xcd, 0xcd, 0x00, 0xff}
	body = append(body, 0x00, byte(len(message)))
	body = append(body, message...)
	read := ParseCommonMessage(long(TableIDEMMMessage, 0x0123, body))
	if read == nil {
		t.Fatal("not read")
	}
	if read.FixedMessageID != 0x0123 || read.BroadcasterGroupID != 0x12 || read.DeletionStatus != 0x01 ||
		read.Durations != [3]byte{0x0a, 0x05, 0xff} || read.Cycle != 2 {
		t.Errorf("read %+v", read)
	}
	if position, ok := read.Position(); !ok || position != 0x41 {
		t.Errorf("position %#x", position)
	}
	if text, ok := read.Text(); !ok || !bytes.Equal(text, []byte{0xb0, 0xa2, 0x1a, 0xcd, 0xcd}) {
		t.Errorf("text %x", text)
	}
	composed, ok := read.Compose([]byte{0xc5, 0xc4})
	if !ok || !bytes.Equal(composed, []byte{0xb0, 0xa2, 0xc5, 0xc4, 0xcd, 0xcd}) {
		t.Errorf("composed %x", composed)
	}
}

func TestTheMessagesForSingleCardsAreNotCommonOnes(t *testing.T) {
	if ParseCommonMessage(long(TableIDEMMMessage, 0x0000, make([]byte, 20))) != nil {
		t.Error("read a message for a single card as a common one")
	}
}
