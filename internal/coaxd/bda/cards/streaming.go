// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cards

import "sync"

// streaming counts the running graphs on each card, so a mode switch can refuse
// to pull the standard out from under a recording. It is per card, not per
// tuner, because a switch restarts the card's other tuners too.
var (
	streamingMutex sync.Mutex
	streaming      = map[string]int{}
)

// Hold records that one more graph is running on a card.
func Hold(card string) {
	if card == "" {
		return
	}
	streamingMutex.Lock()
	defer streamingMutex.Unlock()
	streaming[card]++
}

// Release records that one of a card's graphs has stopped.
func Release(card string) {
	if card == "" {
		return
	}
	streamingMutex.Lock()
	defer streamingMutex.Unlock()
	if streaming[card] <= 1 {
		delete(streaming, card)
		return
	}
	streaming[card]--
}

// Streaming reports how many graphs are running on a card.
func Streaming(card string) int {
	streamingMutex.Lock()
	defer streamingMutex.Unlock()
	return streaming[card]
}
