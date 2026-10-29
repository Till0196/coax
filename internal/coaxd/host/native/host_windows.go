// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package native

// BDA graphs are built in a process of their own, the host, which this daemon
// starts and talks to over the host's standard input and output.
//
// A vendor driver's graph has to be stopped before its handles are closed. The
// Digital Devices driver bugchecks when a process holding a running graph is
// terminated, and the LWire driver leaves the process unable to exit and the
// tuner unusable until it is unplugged. Nothing a terminated process does can
// prevent that, so the graphs are kept out of the process a person is likely
// to end: when this daemon goes, however it goes, the host sees its input close
// and stops every graph in order before it exits itself.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/rpc"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
	"github.com/Till0196/coax/internal/coaxd/host/userlock"
	"github.com/Till0196/coax/internal/coaxd/receiver"
	"github.com/Till0196/coax/internal/coaxd/trace"
)

// HostCommand is the argument the daemon starts its host with.
const HostCommand = "bda-host"

// hosted reports whether a tuner is opened in the host: every one Owns serves
// but the Earthsoft cards, which are not BDA.
func hosted(one pool.Detected) bool { return Owns(one) && !ownsEarthsoft(one.Backend) }

// Process creation flags: no console for the host, and out of any job this
// daemon was started in, so a job that ends this daemon does not end the host
// with it.
const (
	createNoWindow         = 0x08000000
	createBreakawayFromJob = 0x01000000
)

// host is the one host process, started when a tuner is first opened and again
// after one that ended.
var host struct {
	mutex  sync.Mutex
	client *rpc.Client
}

// hostCall makes one call to the host, starting it first if there is none.
func hostCall(method string, arguments, reply any) error {
	client, err := hostClient()
	if err != nil {
		return err
	}
	return fromHost(client.Call("Host."+method, arguments, reply))
}

func hostClient() (*rpc.Client, error) {
	return recoveryHostClient(context.Background())
}

func recoveryHostClient(ctx context.Context) (*rpc.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	host.mutex.Lock()
	defer host.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if host.client != nil {
		return host.client, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find this program to start the BDA host: %w", err)
	}
	if err := waitForEarlierHost(ctx); err != nil {
		return nil, err
	}
	command := exec.Command(executable, HostCommand, socketKey)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | createBreakawayFromJob}
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	diagnostics, err := command.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		// A job that does not allow breaking away refuses the flag; the host
		// then shares this daemon's job, which is no worse than being in it.
		command.SysProcAttr.CreationFlags = createNoWindow
		if err = command.Start(); err != nil {
			return nil, fmt.Errorf("start the BDA host: %w", err)
		}
	}
	go func() {
		lines := bufio.NewScanner(diagnostics)
		for lines.Scan() {
			trace.Forward(lines.Text())
		}
	}()
	client := rpc.NewClient(pipes{Reader: output, WriteCloser: input})
	host.client = client
	go func() {
		err := command.Wait()
		trace.Printf("BDA host ended: %v", err)
		host.mutex.Lock()
		if host.client == client {
			host.client = nil
		}
		host.mutex.Unlock()
		_ = client.Close()
	}()
	return client, nil
}

// socketKey names the socket this daemon serves, which the host's lock is
// named after. See SetSocket.
var socketKey string

// SetSocket names the socket this daemon serves.
func SetSocket(path string) {
	socketKey = userlock.Key(path)
}

// hostLock is the mutex a host holds for as long as it runs, so the daemon that
// replaces the one which started it can tell it is still giving tuners back.
func hostLock(key string) string { return "bda-host-" + key }

// pipes joins a process's output and input into one connection.
type pipes struct {
	io.Reader
	io.WriteCloser
}

// The calls the host answers.

type HostOpen struct{ Backend, Selector string }

type HostOpened struct {
	ID       int
	Backend  string
	Name     string
	Mode     string
	Settings []device.Setting
}

type HostSupports struct {
	ID       int
	Delivery string
}

type HostTune struct {
	ID         int
	Properties map[string]string
}

type HostRead struct {
	Stream int
	Max    int
}

type HostData struct {
	Data       []byte
	WouldBlock bool
}

// openHosted opens a tuner in the host.
func openHosted(backend, selector string) (openedDevice, error) {
	var opened HostOpened
	if err := hostCall("Open", HostOpen{Backend: backend, Selector: selector}, &opened); err != nil {
		return openedDevice{}, err
	}
	return openedDevice{
		device:   &hostedDevice{id: opened.ID, backend: opened.Backend, name: opened.Name},
		mode:     opened.Mode,
		settings: opened.Settings,
	}, nil
}

// hostedDevice is a tuner the host holds.
type hostedDevice struct {
	id            int
	backend, name string
}

func (d *hostedDevice) Backend() string { return d.backend }
func (d *hostedDevice) Name() string    { return d.name }

func (d *hostedDevice) Supports(delivery string) (bool, error) {
	var supported bool
	err := hostCall("Supports", HostSupports{ID: d.id, Delivery: delivery}, &supported)
	return supported, err
}

func (d *hostedDevice) Tune(properties map[string]string) error {
	return hostCall("Tune", HostTune{ID: d.id, Properties: properties}, &struct{}{})
}

func (d *hostedDevice) Status() (receiver.Status, error) {
	var status receiver.Status
	err := hostCall("Status", d.id, &status)
	return status, err
}

func (d *hostedDevice) OpenStream() (receiver.Stream, error) {
	var stream int
	if err := hostCall("OpenStream", d.id, &stream); err != nil {
		return nil, err
	}
	return &hostedStream{id: stream}, nil
}

func (d *hostedDevice) Close() error { return hostCall("Close", d.id, &struct{}{}) }

// hostedStream is a stream the host is reading for this daemon.
type hostedStream struct{ id int }

func (s *hostedStream) Read(ctx context.Context, buffer []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var data HostData
	if err := hostCall("Read", HostRead{Stream: s.id, Max: len(buffer)}, &data); err != nil {
		return 0, err
	}
	if data.WouldBlock {
		return 0, receiver.ErrWouldBlock
	}
	return copy(buffer, data.Data), nil
}

func (s *hostedStream) Close() error { return hostCall("CloseStream", s.id, &struct{}{}) }

// ReleaseHost closes the parent's RPC connection. The child finishes calls and
// stops its graphs before exiting, even when this daemon no longer waits.
func ReleaseHost() {
	host.mutex.Lock()
	client := host.client
	host.client = nil
	host.mutex.Unlock()
	if client != nil {
		_ = client.Close()
	}
}
