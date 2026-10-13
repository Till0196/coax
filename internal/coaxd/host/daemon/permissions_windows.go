// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemon

// openToClients does nothing on Windows: access to a Unix socket there is
// decided by the directory's inherited access-control list, and the Unix
// permission bits Chmod sets have no meaning for it.
func openToClients(string, bool) error { return nil }

// closeToOthers does nothing on Windows, for the same reason; descriptors are
// not handed over there at all.
func closeToOthers(string) error { return nil }
