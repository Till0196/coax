// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package tsdemux

// prioritise does nothing where the scheduler needs no telling. See the Windows
// file for what it is for.
func prioritise() {}
