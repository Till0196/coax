// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package fwextract

// Member is one file taken out of a container.
type Member struct {
	Name string
	Data []byte
}
