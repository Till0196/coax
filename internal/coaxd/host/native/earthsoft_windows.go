// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package native

import (
	"github.com/Till0196/coax/internal/coaxd/host/pool"
	"github.com/Till0196/coax/internal/coaxd/pcie/pt1"
	"github.com/Till0196/coax/internal/coaxd/pcie/pt3"
)

// ownsEarthsoft reports whether a backend name is one of the cards this
// program drives itself.
func ownsEarthsoft(backend string) bool {
	return backend == pt3.Backend || backend == pt1.Backend
}

// detectEarthsoft lists the receivers of any Earthsoft card. A missing card and
// a machine without the vendor's driver are both nothing to list rather than an
// error: this runs on every listing, on every machine.
func detectEarthsoft() []pool.Detected {
	var found []pool.Detected
	if cards, err := pt3.Devices(); err == nil {
		for _, one := range cards {
			found = append(found, earthsoftEntry(pt3.Backend, one.Name, one.Selector, one.Deliveries))
		}
	}
	if cards, err := pt1.Devices(); err == nil {
		for _, one := range cards {
			found = append(found, earthsoftEntry(pt1.Backend, one.Name, one.Selector, one.Deliveries))
		}
	}
	return found
}
