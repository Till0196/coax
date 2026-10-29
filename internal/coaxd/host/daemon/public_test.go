// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemon

import "testing"

func TestThePortIsNotMultipath(t *testing.T) {
	config := publicListenConfig()
	if config.MultipathTCP() {
		t.Fatal("the port would be opened with Multipath TCP")
	}
}
