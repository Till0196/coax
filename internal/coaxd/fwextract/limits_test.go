// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package fwextract

import (
	"archive/zip"
	"bytes"
	"testing"
)

// TestArchiveExpansionHasATotalBudget checks the aggregate rather than the
// per-member limit with two individually acceptable members.
func TestArchiveExpansionHasATotalBudget(t *testing.T) {
	var packed bytes.Buffer
	archive := zip.NewWriter(&packed)
	for _, name := range []string{"dvb-one.fw", "dvb-two.fw"} {
		member, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(bytes.Repeat([]byte{0x5a}, 16)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	budget := &expansionBudget{remaining: 24}
	if _, err := extractZip(packed.Bytes(), budget); err == nil {
		t.Fatal("two members exceeding the aggregate budget were accepted")
	}
}

func TestExpansionLimitDoesNotReturnTruncatedData(t *testing.T) {
	budget := &expansionBudget{remaining: 8}
	if _, err := budget.read(bytes.NewReader(bytes.Repeat([]byte{1}, 9)), nil); err == nil {
		t.Fatal("data beyond the expansion limit was silently truncated")
	}
}
