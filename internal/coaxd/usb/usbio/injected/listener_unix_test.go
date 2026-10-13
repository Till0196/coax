// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package injected

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio/backend"
)

// serveTest starts a listener on a socket of its own, stopped when the test ends.
func serveTest(t *testing.T, registry *Registry) string {
	t.Helper()
	usbNode = func(*os.File) error { return nil }
	t.Cleanup(func() { usbNode = isUSBNode })
	// A socket path is bounded by sun_path, far shorter than a file name may be;
	// a temporary directory name plus a short file stays inside it.
	directory, err := os.MkdirTemp("", "handover")
	if err != nil {
		t.Fatalf("make a socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "usb")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() { done <- Serve(listener, registry, nil) }()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not stop when its listener was closed")
		}
	})
	return path
}

// ask sends one request, with the descriptors it carries, and reads the answer.
func ask(t *testing.T, path string, asked request, descriptors ...int) response {
	t.Helper()
	address, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	connection, err := net.DialUnix("unix", nil, address)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	message, err := json.Marshal(asked)
	if err != nil {
		t.Fatalf("encode the request: %v", err)
	}
	var rights []byte
	if len(descriptors) > 0 {
		rights = syscall.UnixRights(descriptors...)
	}
	if _, _, err := connection.WriteMsgUnix(message, rights, nil); err != nil {
		t.Fatalf("send the request: %v", err)
	}
	buffer := make([]byte, maxRequestBytes)
	read, err := connection.Read(buffer)
	if err != nil {
		t.Fatalf("read the answer: %v", err)
	}
	var answered response
	if err := json.Unmarshal(buffer[:read], &answered); err != nil {
		t.Fatalf("decode the answer %q: %v", buffer[:read], err)
	}
	return answered
}

// handedOver makes a descriptor to pass, standing in for a device node.
func handedOver(t *testing.T) *os.File {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("make a descriptor to hand over: %v", err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	return reader
}

func TestAttachOverTheSocket(t *testing.T) {
	registry := New()
	t.Cleanup(func() { _ = registry.Close() })
	path := serveTest(t, registry)

	file := handedOver(t)
	answered := ask(t, path, request{
		Action:   "attach",
		Vendor:   0x0511,
		Product:  0x0045,
		Location: "001:004",
		Serial:   "ABC12345",
		Port:     "1-2.1",
	}, int(file.Fd()))
	if !answered.OK {
		t.Fatalf("attach was refused: %s", answered.Error)
	}
	if answered.Location != "001:004" {
		t.Errorf("attach listed the device at %q, want 001:004", answered.Location)
	}

	listed := registry.List()
	if len(listed) != 1 {
		t.Fatalf("the registry holds %d devices, want 1", len(listed))
	}
	if listed[0].ID.Vendor != 0x0511 || listed[0].ID.Product != 0x0045 {
		t.Errorf("the device was listed as %04x:%04x, want 0511:0045", listed[0].ID.Vendor, listed[0].ID.Product)
	}
	if listed[0].Serial != "ABC12345" || listed[0].Port != "1-2.1" {
		t.Errorf("the device was listed as %+v, want the serial and port that were sent", listed[0])
	}
}

func TestAttachTakesADescriptorOfItsOwn(t *testing.T) {
	registry := New()
	t.Cleanup(func() { _ = registry.Close() })
	path := serveTest(t, registry)

	file := handedOver(t)
	if answered := ask(t, path, request{Action: "attach", Location: "001:004"}, int(file.Fd())); !answered.OK {
		t.Fatalf("attach was refused: %s", answered.Error)
	}
	// The kernel duplicates a descriptor as it crosses, so closing the sender's
	// must leave the daemon's open -- an Android application closes its own the
	// moment the person revokes permission.
	if err := file.Close(); err != nil {
		t.Fatalf("close the sender's descriptor: %v", err)
	}

	var stat error
	found := registry.Found(func(handed *os.File) (backend.Device, error) {
		_, stat = handed.Stat()
		return nil, nil
	})
	if len(found) != 1 {
		t.Fatalf("the registry holds %d devices, want 1", len(found))
	}
	if _, err := found[0].Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if stat != nil {
		t.Errorf("the registry's descriptor was closed with the sender's: %v", stat)
	}
}

func TestAttachWithoutADescriptorIsRefused(t *testing.T) {
	registry := New()
	t.Cleanup(func() { _ = registry.Close() })
	path := serveTest(t, registry)

	answered := ask(t, path, request{Action: "attach", Location: "001:004"})
	if answered.OK {
		t.Fatal("an attach carrying no descriptor should be refused")
	}
	if registry.Len() != 0 {
		t.Errorf("a refused attach left %d devices behind", registry.Len())
	}
}

func TestDetachOverTheSocket(t *testing.T) {
	registry := New()
	t.Cleanup(func() { _ = registry.Close() })
	path := serveTest(t, registry)

	file := handedOver(t)
	if answered := ask(t, path, request{Action: "attach", Location: "001:004"}, int(file.Fd())); !answered.OK {
		t.Fatalf("attach was refused: %s", answered.Error)
	}
	if answered := ask(t, path, request{Action: "detach", Location: "001:004"}); !answered.OK {
		t.Fatalf("detach was refused: %s", answered.Error)
	}
	if registry.Len() != 0 {
		t.Errorf("the registry holds %d devices after a detach, want 0", registry.Len())
	}
	if answered := ask(t, path, request{Action: "detach", Location: "001:004"}); answered.OK {
		t.Error("detaching a device twice should be refused")
	}
}

func TestListOverTheSocket(t *testing.T) {
	registry := New()
	t.Cleanup(func() { _ = registry.Close() })
	path := serveTest(t, registry)

	for _, location := range []string{"001:004", "001:005"} {
		file := handedOver(t)
		if answered := ask(t, path, request{Action: "attach", Vendor: 0x0511, Location: location}, int(file.Fd())); !answered.OK {
			t.Fatalf("attach %s was refused: %s", location, answered.Error)
		}
	}
	answered := ask(t, path, request{Action: "list"})
	if !answered.OK {
		t.Fatalf("list was refused: %s", answered.Error)
	}
	if len(answered.Devices) != 2 {
		t.Fatalf("list gave %d devices, want 2", len(answered.Devices))
	}
	if answered.Devices[0].Location != "001:004" || answered.Devices[1].Location != "001:005" {
		t.Errorf("list gave %+v, want them in the order they were handed over", answered.Devices)
	}
}

func TestUnknownActionIsRefused(t *testing.T) {
	registry := New()
	t.Cleanup(func() { _ = registry.Close() })
	path := serveTest(t, registry)

	if answered := ask(t, path, request{Action: "reset"}); answered.OK {
		t.Error("an unknown action should be refused")
	}
}

// TestAttachRefusesWhatIsNotADevice: a pipe is not a tuner.
func TestAttachRefusesWhatIsNotADevice(t *testing.T) {
	registry := New()
	path := serveTest(t, registry)
	usbNode = isUSBNode
	answered := ask(t, path, request{Action: "attach", Vendor: 0x0511, Product: 0x0045}, int(handedOver(t).Fd()))
	if answered.OK {
		t.Error("attached a pipe")
	}
	if registry.Len() != 0 {
		t.Errorf("listed %d devices", registry.Len())
	}
}
