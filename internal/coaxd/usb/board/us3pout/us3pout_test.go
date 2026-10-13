// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package us3pout

import "testing"

func TestSupports(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"ISDB-T":  true,
		"ISDB_S":  true,
		"ISDB-S3": false,
		"DVBT":    false,
	}
	for delivery, expected := range tests {
		if actual := Supports(delivery); actual != expected {
			t.Errorf("Supports(%q) = %v; want %v", delivery, actual, expected)
		}
	}
}
