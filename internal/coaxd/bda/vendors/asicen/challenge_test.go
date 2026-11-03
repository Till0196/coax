// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
package asicen

import (
	"encoding/hex"
	"testing"
)

func TestInitializationRoundKey(t *testing.T) {
	// FIPS 197, AES-128 key expansion example, round ten.
	var key [16]byte
	for i := range key {
		key[i] = byte(i)
	}
	got := lastRound(key)
	want, _ := hex.DecodeString("7f1d1113174a94e38ba707f3c5302b4d")
	if string(got[:]) != string(want) {
		t.Fatalf("got %x want %x", got, want)
	}
}
