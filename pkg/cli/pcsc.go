// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/Till0196/coax/internal/idle"
	"github.com/Till0196/coax/internal/pcscd"
	"github.com/Till0196/coax/internal/sockdir"
	"github.com/Till0196/coax/internal/winscard"
	"github.com/Till0196/coax/internal/winscard/daemoncard"
	"github.com/Till0196/coax/pkg/host"
)

// runPCSC serves the socket a PC/SC client library connects to, backed by a
// daemon's card over HTTP.
//
// The socket belongs here rather than in the daemon because it belongs to the
// machine running the application, not to the machine holding the card:
// pcsc-lite's protocol is C structures in the host's own byte order and layout,
// which is sound between two programs on one machine and nothing to send over a
// network. Serving it here also makes a card in this machine and a card in
// another reachable exactly the same way.
func runPCSC(arguments []string, stdout, stderr io.Writer) error {
	c := newHardwareCommand("pcsc", stderr)
	socket := c.flags.String("socket", "",
		"socket to serve PC/SC on; point clients at it with PCSCLITE_CSOCK_NAME (the usual place for this system when omitted)")
	idleAfter := c.flags.Duration("idle", 0,
		"stop once nothing has been connected for this long; 0 stays running")
	c.flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: coax pcsc [options]")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Serves a PC/SC socket for the cards a tuner daemon has, so that a decoder")
		fmt.Fprintln(stderr, "or any other PC/SC application can reach them by being pointed at it.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Run by hand it stays, because the applications that connect expect the")
		fmt.Fprintln(stderr, "socket to be there before they start. `coax tune --pcsc` starts one of")
		fmt.Fprintln(stderr, "these with --idle set instead, for a stream that is about to be decoded.")
		fmt.Fprintln(stderr)
		c.flags.PrintDefaults()
	}
	if err := c.parseOnly(arguments); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := c.dial(ctx)
	if err != nil {
		return err
	}

	path := *socket
	if path == "" {
		path = DefaultPCSCSocket()
	}
	listener, err := listenPCSC(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
		// The path is this process's to clean up: a socket file left behind is
		// one the next run has to decide whether to remove, and deciding that
		// wrongly takes a live socket away from a process still using it.
		_ = os.Remove(path)
	}()

	provider := daemoncard.New(client)
	table := pcscd.NewTable(daemoncard.FromClient(client), 0, false)
	defer table.Close()
	service := winscard.NewService(provider)
	server := pcscd.New(service, table, c.report)
	table.OnEvent(server.Notify)

	fmt.Fprintf(stdout, "serving PC/SC on %s\n", path)
	fmt.Fprintf(stdout, "point clients at it with PCSCLITE_CSOCK_NAME=%s\n", path)

	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()

	// The idle rule counts clients rather than requests, because a PC/SC client
	// waiting on a card says nothing for as long as the card does not move -- and
	// a stream is a client here too. See internal/idle.
	stopping := make(chan struct{})
	if *idleAfter > 0 {
		go func() {
			rule := idle.Rule{
				Interval: *idleAfter / time.Duration(idle.DefaultStrikes),
				Strikes:  idle.DefaultStrikes,
				Count:    server.Connections,
			}
			if rule.Wait(ctx) {
				c.report("nothing has used the card socket; stopping")
				close(stopping)
			}
		}()
	}

	select {
	case <-ctx.Done():
	case <-stopping:
	case err := <-served:
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}
	// Closing the listener ends the accept loop; the clients still connected
	// end when their own sockets close with this process.
	_ = listener.Close()
	<-served
	return nil
}

// ensurePCSC makes sure a card socket is being served, starting one that stops
// again when nothing is using it.
//
// One socket serves every stream on the machine. The card behind it is shared
// and held only for the length of an exchange, so several decoders on one card
// is the ordinary case, and what this has to get right is only that exactly one
// server is running. That is also why the socket is not tied to a stream.
//
// The connection it returns is what says this program is a user of the socket:
// held for as long as the stream runs, it keeps the server from reaping itself
// before a decoder has connected. Nothing is said down it.
func ensurePCSC(ctx context.Context, options hostOptions, report func(string)) (net.Conn, error) {
	path := DefaultPCSCSocket()
	if err := sockdir.Check(filepath.Dir(path)); err == nil {
		if held, err := net.DialTimeout("unix", path, pcscProbe); err == nil {
			return held, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := startPCSC(options, path); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(pcscStartDeadline)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pcscStartPoll):
		}
		if held, err := net.DialTimeout("unix", path, pcscProbe); err == nil {
			report("started a card socket at " + path)
			return held, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("started a card socket but nothing answered on %s within %s",
				path, pcscStartDeadline)
		}
	}
}

// startPCSC launches this same program as the card socket, detached: it
// outlives the stream that needed it, and stops on its own once nothing is
// connected.
func startPCSC(options hostOptions, path string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find this program to start a card socket with: %w", err)
	}
	arguments := []string{"pcsc", "--socket", path, "--idle", pcscIdle.String()}
	if options.address != "" {
		arguments = append(arguments, "--host", options.address)
	}
	if options.firmwareDir != "" {
		arguments = append(arguments, "--firmware-dir", options.firmwareDir)
	}
	served := exec.Command(self, arguments...)
	served.Stdin, served.Stdout, served.Stderr = nil, nil, nil
	host.Detach(served)
	if err := served.Start(); err != nil {
		return fmt.Errorf("start %s pcsc: %w", self, err)
	}
	return served.Process.Release()
}

const (
	// pcscProbe bounds the wait for an answer on an existing socket; a server
	// on this machine answers at once or not at all.
	pcscProbe = time.Second
	// pcscStartPoll and pcscStartDeadline are how one just started is waited
	// for: often enough not to be noticed, bounded so a socket that cannot be
	// served does not hold up a recording.
	pcscStartPoll     = 50 * time.Millisecond
	pcscStartDeadline = 10 * time.Second
	// pcscIdle is how long a socket started this way stays after its last
	// client. Long enough that the gap between two programmes does not cost a
	// restart, short enough that one started by mistake does not linger.
	pcscIdle = time.Minute
)

// listenPCSC opens the socket, refusing to take one a live server is already
// on. A path left behind by a process that was killed is removed; one that
// still answers is not, since removing it would leave the server that owns it
// listening where nothing can reach it.
func listenPCSC(path string) (net.Listener, error) {
	if err := sockdir.Prepare(filepath.Dir(path), pcscPrivate()); err != nil {
		return nil, err
	}
	if probe, err := net.Dial("unix", path); err == nil {
		_ = probe.Close()
		return nil, fmt.Errorf("something is already serving PC/SC on %s", path)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove the socket left behind at %s: %w", path, err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	// Readable by this user only. The daemon's copy of this socket had to be
	// open to everyone, because a daemon started by root serves programs run by
	// others; this one is the user's own.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("set the permissions on %s: %w", path, err)
	}
	return listener, nil
}

// DefaultPCSCSocket is where this puts its socket when nobody said: beside the
// daemon's, under the same per-user runtime directory, so a machine set up for
// one is set up for the other. The environment variable is pcsc-lite's own,
// honoured so that a machine already pointed somewhere stays pointed there.
func DefaultPCSCSocket() string {
	if named := os.Getenv("PCSCLITE_CSOCK_NAME"); named != "" {
		return named
	}
	return filepath.Join(pcscRuntimeDir(), "pcscd.comm")
}

// pcscPrivate reports whether the socket's directory is this user's alone, as
// the daemon decides for its own: not when it was named, and not the /run
// directory a daemon run as root shares with everyone.
func pcscPrivate() bool {
	if os.Getenv("PCSCLITE_CSOCK_NAME") != "" || os.Getenv("COAXD_SOCKET_DIR") != "" {
		return false
	}
	return runtime.GOOS != "linux" || os.Getuid() != 0
}

// pcscRuntimeDir mirrors the daemon's, since the two sit side by side.
func pcscRuntimeDir() string {
	if named := os.Getenv("COAXD_SOCKET_DIR"); named != "" {
		return named
	}
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = os.Getenv("TEMP")
		}
		return filepath.Join(base, "coaxd")
	case "darwin":
		if base := os.Getenv("TMPDIR"); base != "" {
			return filepath.Join(base, "coaxd")
		}
		return filepath.Join(os.TempDir(), "coaxd-"+strconv.Itoa(os.Getuid()))
	default:
		if os.Getuid() == 0 {
			return "/run/coaxd"
		}
		if base := os.Getenv("XDG_RUNTIME_DIR"); base != "" {
			return filepath.Join(base, "coaxd")
		}
		return filepath.Join(os.TempDir(), "coaxd-"+strconv.Itoa(os.Getuid()))
	}
}
