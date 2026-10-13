// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !darwin && !windows

package host

// maxSocketPath is the size of sun_path in <sys/un.h>.
const maxSocketPath = 108
