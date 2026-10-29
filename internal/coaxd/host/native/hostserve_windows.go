// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/rpc"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/Till0196/coax/internal/coaxd/bda"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
	"github.com/Till0196/coax/internal/coaxd/host/userlock"
	"github.com/Till0196/coax/internal/coaxd/pcie/tbs"
	"github.com/Till0196/coax/internal/coaxd/receiver"
)

// ServeHost answers the daemon that started this process, on its standard input
// and output, until that input closes. Then every tuner still open is closed,
// which stops its graph before its handles go. arguments name the daemon's
// socket (see SetSocket).
func ServeHost(input io.Reader, output io.WriteCloser, arguments []string) error {
	state := newHostState()
	if len(arguments) > 0 {
		if err := serveHostPipe(context.Background(), arguments[0], state); err != nil {
			return err
		}
		if _, existed, err := userlock.Create(hostLock(arguments[0])); err != nil {
			return err
		} else if existed {
			return fmt.Errorf("an earlier BDA host still holds this socket")
		}
	}
	served := &Host{devices: map[int]*onThread{}, streams: map[int]receiver.Stream{}, state: state}
	server := rpc.NewServer()
	if err := server.RegisterName("Host", served); err != nil {
		return err
	}
	state.set("serving")
	// Logging off or shutting down ends this process too, and it would end with
	// the graphs running: the daemon's input closing is not the only way out.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		state.set("closing")
		served.closeAll()
		state.set("closed")
		os.Exit(0)
	}()
	server.ServeConn(pipes{Reader: input, WriteCloser: output})
	state.set("closing")
	served.closeAll()
	state.set("closed")
	return nil
}

// Host holds the tuners the daemon opened. Its methods are the calls the daemon
// makes; see host_windows.go.
type Host struct {
	state   *hostState
	mutex   sync.Mutex
	next    int
	devices map[int]*onThread
	streams map[int]receiver.Stream
}

func (h *Host) device(id int) (*onThread, error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	device, ok := h.devices[id]
	if !ok {
		return nil, toHost(fmt.Errorf("%w: tuner %d is not open here", pool.ErrGone, id))
	}
	return device, nil
}

func (h *Host) stream(id int) (receiver.Stream, error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	stream, ok := h.streams[id]
	if !ok {
		return nil, toHost(fmt.Errorf("%w: stream %d is not open here", pool.ErrGone, id))
	}
	return stream, nil
}

func (h *Host) Open(arguments HostOpen, reply *HostOpened) error {
	opened, err := openHere(arguments.Backend, arguments.Selector)
	if err != nil {
		return toHost(err)
	}
	h.mutex.Lock()
	h.next++
	id := h.next
	h.devices[id] = opened.device.(*onThread)
	h.mutex.Unlock()
	*reply = HostOpened{ID: id, Backend: opened.device.Backend(), Name: opened.device.Name(), Mode: opened.mode, Settings: opened.settings}
	return nil
}

func (h *Host) Supports(arguments HostSupports, reply *bool) error {
	device, err := h.device(arguments.ID)
	if err != nil {
		return err
	}
	supported, err := device.Supports(arguments.Delivery)
	*reply = supported
	return toHost(err)
}

func (h *Host) Tune(arguments HostTune, _ *struct{}) error {
	device, err := h.device(arguments.ID)
	if err != nil {
		return err
	}
	return toHost(device.Tune(arguments.Properties))
}

func (h *Host) Status(id int, reply *receiver.Status) error {
	device, err := h.device(id)
	if err != nil {
		return err
	}
	status, err := device.Status()
	*reply = status
	return toHost(err)
}

func (h *Host) OpenStream(id int, reply *int) error {
	device, err := h.device(id)
	if err != nil {
		return err
	}
	stream, err := device.OpenStream()
	if err != nil {
		return toHost(err)
	}
	h.mutex.Lock()
	h.next++
	*reply = h.next
	h.streams[h.next] = stream
	h.mutex.Unlock()
	return nil
}

// Read hands back what the stream has, or says there is nothing yet. It does not
// wait: the daemon's reader paces itself, as it does with a stream of its own.
func (h *Host) Read(arguments HostRead, reply *HostData) error {
	stream, err := h.stream(arguments.Stream)
	if err != nil {
		return err
	}
	buffer := make([]byte, arguments.Max)
	read, err := stream.Read(context.Background(), buffer)
	if errors.Is(err, receiver.ErrWouldBlock) {
		reply.WouldBlock = read == 0
		err = nil
	}
	reply.Data = buffer[:read]
	return toHost(err)
}

func (h *Host) CloseStream(id int, _ *struct{}) error {
	h.mutex.Lock()
	stream, ok := h.streams[id]
	delete(h.streams, id)
	h.mutex.Unlock()
	if !ok {
		return nil
	}
	return toHost(stream.Close())
}

func (h *Host) Close(id int, _ *struct{}) error {
	h.mutex.Lock()
	device, ok := h.devices[id]
	delete(h.devices, id)
	h.mutex.Unlock()
	if !ok {
		return nil
	}
	// The device closes the streams it opened before itself.
	return toHost(device.Close())
}

// Recover is native.Recover's repair, made where the graphs are.
func (h *Host) Recover(_ struct{}, repaired *int) error {
	bda.RemoveLeftovers()
	h.state.set("recovering")
	defer h.state.set("serving")
	*repaired = tbs.Recover()
	return nil
}

// closeAll closes every tuner still open, each on its own thread.
func (h *Host) closeAll() {
	h.mutex.Lock()
	devices := make([]*onThread, 0, len(h.devices))
	for id, device := range h.devices {
		devices = append(devices, device)
		delete(h.devices, id)
	}
	h.streams = map[int]receiver.Stream{}
	h.mutex.Unlock()
	var group sync.WaitGroup
	for _, device := range devices {
		group.Add(1)
		go func() {
			defer group.Done()
			_ = device.Close()
		}()
	}
	group.Wait()
}
