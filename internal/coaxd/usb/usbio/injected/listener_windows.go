// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package injected

import (
	"net"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio/backend"
)

// Serve reports that descriptors cannot be handed over on Windows: filling the
// registry from another process needs a Unix socket's ancillary data, which
// Windows has no equivalent of. The registry itself works anywhere.
func Serve(net.Listener, *Registry, func(string)) error { return backend.ErrUnsupported }
