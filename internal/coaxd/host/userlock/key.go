// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package userlock is the named mutexes coaxd and its BDA host keep on Windows,
// one set per user, named after the socket they serve.
package userlock

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// Key names a socket in a mutex name. A hash, because a path holds characters
// a kernel object's name may not; folded to lower case, because two spellings
// of one path on Windows are one socket.
func Key(path string) string {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(strings.ToLower(path)))
	return fmt.Sprintf("%016x", sum.Sum64())
}
