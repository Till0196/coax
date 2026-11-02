// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package session

func Watch(stop func()) (finish func(), err error) { return func() {}, nil }
