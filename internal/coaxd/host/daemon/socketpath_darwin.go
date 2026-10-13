// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemon

// maxSocketPath is the size of sun_path on the BSD side, which is four bytes
// shorter than on Linux. It matters here: a per-user $TMPDIR on macOS is
// already about fifty characters before anything of ours is added.
const maxSocketPath = 104
