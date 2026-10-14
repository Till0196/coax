// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package injected

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/Till0196/coax/pkg/usbio/backend"
)

// The handing-over side gets a Unix socket of its own rather than an endpoint on
// the daemon's HTTP server, because a file descriptor crosses a process boundary
// only as a Unix socket's ancillary data. One request per connection: attach and
// detach are rare, and framing several messages plus their ancillary data into
// one stream would leave a descriptor attached to whichever byte the sender put
// it with.

// usbNode is the check a handed-over descriptor passes before it is attached;
// the tests stand pipes in for device nodes.
var usbNode = isUSBNode

// requestTimeout bounds a connection that says nothing.
const requestTimeout = 10 * time.Second

// maxRequestBytes bounds one request; the messages are a few fields of JSON.
const maxRequestBytes = 8192

type request struct {
	// Action is "attach", "detach" or "list".
	Action string `json:"action"`
	// Vendor and Product are the device's USB numbers, for an attach.
	Vendor  uint16 `json:"vendor,omitempty"`
	Product uint16 `json:"product,omitempty"`
	// Location names the device, for an attach that knows the bus numbering and
	// for every detach.
	Location string `json:"location,omitempty"`
	Serial   string `json:"serial,omitempty"`
	Port     string `json:"port,omitempty"`
}

type response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Location is where an attach listed the device.
	Location string       `json:"location,omitempty"`
	Devices  []wireDevice `json:"devices,omitempty"`
}

// wireDevice is a listed device as it goes over the socket; the domain types
// carry no JSON tags, because nothing but this socket sends them.
type wireDevice struct {
	Vendor   uint16 `json:"vendor"`
	Product  uint16 `json:"product"`
	Location string `json:"location"`
	Serial   string `json:"serial,omitempty"`
	Port     string `json:"port,omitempty"`
}

// Serve answers requests until the listener is closed, which is not an error.
func Serve(listener net.Listener, registry *Registry, report func(string)) error {
	for {
		connection, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		unix, ok := connection.(*net.UnixConn)
		if !ok {
			_ = connection.Close()
			continue
		}
		go func() {
			defer unix.Close()
			if err := handle(unix, registry); err != nil && report != nil {
				report(fmt.Sprintf("USB handover: %v", err))
			}
		}()
	}
}

func handle(connection *net.UnixConn, registry *Registry) error {
	_ = connection.SetDeadline(time.Now().Add(requestTimeout))
	message := make([]byte, maxRequestBytes)
	rights := make([]byte, syscall.CmsgSpace(4))
	read, rightsRead, _, _, err := connection.ReadMsgUnix(message, rights)
	if err != nil {
		return fmt.Errorf("read the request: %w", err)
	}
	files := adoptRights(rights[:rightsRead])
	// Every path from here must account for the descriptors: one neither attached
	// nor closed leaks for the life of the daemon.
	defer func() {
		for _, file := range files {
			if file != nil {
				_ = file.Close()
			}
		}
	}()

	var asked request
	if err := json.Unmarshal(message[:read], &asked); err != nil {
		return answer(connection, response{Error: fmt.Sprintf("the request is not JSON: %v", err)})
	}

	switch asked.Action {
	case "attach":
		if len(files) != 1 {
			return answer(connection, response{Error: fmt.Sprintf("attach carries exactly one descriptor, not %d", len(files))})
		}
		if err := usbNode(files[0]); err != nil {
			return answer(connection, response{Error: err.Error()})
		}
		location, err := registry.Attach(Device{
			ID:       backend.ID{Vendor: asked.Vendor, Product: asked.Product},
			Location: asked.Location,
			Serial:   asked.Serial,
			Port:     asked.Port,
			File:     files[0],
		})
		if err != nil {
			return answer(connection, response{Error: err.Error()})
		}
		// The registry owns it now.
		files[0] = nil
		return answer(connection, response{OK: true, Location: location})
	case "detach":
		if err := registry.Detach(asked.Location); err != nil {
			return answer(connection, response{Error: err.Error()})
		}
		return answer(connection, response{OK: true})
	case "list":
		return answer(connection, response{OK: true, Devices: wire(registry.List())})
	default:
		return answer(connection, response{Error: fmt.Sprintf("unknown action %q", asked.Action)})
	}
}

// adoptRights turns the descriptors a message carried into files, marking them
// close-on-exec: they arrive without it, and a daemon that starts anything would
// otherwise pass a tuner to it.
func adoptRights(rights []byte) []*os.File {
	if len(rights) == 0 {
		return nil
	}
	messages, err := syscall.ParseSocketControlMessage(rights)
	if err != nil {
		return nil
	}
	var files []*os.File
	for _, carried := range messages {
		descriptors, err := syscall.ParseUnixRights(&carried)
		if err != nil {
			continue
		}
		for _, descriptor := range descriptors {
			syscall.CloseOnExec(descriptor)
			files = append(files, os.NewFile(uintptr(descriptor), "usb-handover"))
		}
	}
	return files
}

func wire(attached []Attached) []wireDevice {
	devices := make([]wireDevice, 0, len(attached))
	for _, one := range attached {
		devices = append(devices, wireDevice{
			Vendor:   one.ID.Vendor,
			Product:  one.ID.Product,
			Location: one.Location,
			Serial:   one.Serial,
			Port:     one.Port,
		})
	}
	return devices
}

func answer(connection *net.UnixConn, reply response) error {
	encoded, err := json.Marshal(reply)
	if err != nil {
		return err
	}
	if _, err := connection.Write(append(encoded, '\n')); err != nil {
		return err
	}
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	return nil
}
