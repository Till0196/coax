// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mb86a20s

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestFlattenedDigests(t *testing.T) {
	for _, c := range []struct {
		name   string
		table  [][2]byte
		count  int
		digest string
	}{
		{"initA20S", initA20S, 266, "ebc9223341618bf7"},
		{"resetA20S", resetA20S, 5, "64a726e2156a67d3"},
		{"initA21S", initA21S, 241, "ebd315b412b87b22"},
		{"resetA21S", resetA21S, 7, "e88ca055906e8c43"},
	} {
		if len(c.table) != c.count {
			t.Errorf("%s: %d writes; want %d", c.name, len(c.table), c.count)
		}
		flat := make([]byte, 0, len(c.table)*2)
		for _, w := range c.table {
			flat = append(flat, w[0], w[1])
		}
		sum := sha256.Sum256(flat)
		if got := hex.EncodeToString(sum[:])[:16]; got != c.digest {
			t.Errorf("%s: sha256 %s; want %s", c.name, got, c.digest)
		}
	}
}
