// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !unix && !windows

package progress

import "os"

func terminalColumns(*os.File) int { return 0 }
