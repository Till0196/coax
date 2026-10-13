// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package tray

import "context"

// Run does nothing where there is no notification area. It returns at once,
// leaving the caller to wait on whatever else ends it.
func Run(ctx context.Context, options Options) { <-ctx.Done() }
