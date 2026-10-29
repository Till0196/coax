// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package daemon runs the tuner host.
//
// It is meant to be invisible: a client that finds nothing listening starts one
// and waits for it to answer, and it stops again once nobody has wanted it for
// a while. Where a machine wants it resident, the idle rule is switched off and
// the process run under whatever supervises the rest of the system.
package daemon

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"runtime"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/native"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
	"github.com/Till0196/coax/internal/coaxd/host/server"
	"github.com/Till0196/coax/internal/coaxd/host/tray"
	"github.com/Till0196/coax/internal/coaxd/usb"
	"github.com/Till0196/coax/internal/idle"
	"github.com/Till0196/coax/internal/winscard/usbcard"
	"github.com/Till0196/coax/pkg/usbio/injected"
	"github.com/Till0196/coax/pkg/winscard"
)

// Options settle how a daemon runs.
type Options struct {
	// Sockets are the files to listen on.
	Sockets Sockets
	// Public is a "host:port" to serve tuners on as well, for players that cannot
	// open a Unix socket. Empty means none: a port is reachable by every program
	// on the machine and a socket is not.
	Public string
	// AllowRemote permits Public to name an address other than the loopback.
	AllowRemote bool
	// USBSocket is where a platform USB service hands device descriptors over,
	// empty for nowhere.
	//
	// It is Android's way in: an application there may not read /dev/bus/usb, so
	// the platform asks the person for permission and hands back an open
	// descriptor. What arrives is a usbfs node like any other. See
	// pkg/usbio/injected.
	USBSocket string

	// ChardevRoot is where the px4_drv and pt3 character devices are, empty for
	// /dev.
	ChardevRoot string
	// DVBRoot is where the Linux DVB device nodes are, empty for /dev/dvb.
	DVBRoot string
	// FirmwareDir holds the blobs the firmware-loading boards need.
	FirmwareDir string
	// Linger is how long a board stays open after its last user goes.
	Linger time.Duration
	// Idle ends the daemon once nothing has used it. A zero Interval keeps it
	// running for ever, which is what a recording server wants.
	Idle idle.Rule

	// Version and Commit describe the build.
	Version string
	Commit  string
	// Report receives a line about anything worth saying.
	Report func(string)
	// Tray asks for a notification-area icon while the daemon serves, where the
	// system has one. It buys a way out a person can see: on Windows the obvious
	// alternative is Task Manager's End task, which takes the process away with no
	// notice, leaving a Digital Devices card that keeps its tuner until the
	// machine is restarted. See docs/boards/dd.md. A session with no desktop has
	// nowhere to put one, which is not a failure.
	Tray bool
	// Ready is called once the sockets are bound and the daemon is answering, so
	// that nothing announces itself before it is true: a second daemon started by
	// mistake loses the bind and stops, and must not have claimed to be serving
	// first.
	Ready func(Sockets)
}

// Run serves until the context is done, a signal arrives, or nothing has needed
// the daemon for long enough.
func Run(ctx context.Context, options Options) error {
	if options.Report == nil {
		options.Report = func(string) {}
	}

	keepGoingOnBrokenPipe()

	ctx, stop := signal.NotifyContext(ctx, interruptSignals()...)
	defer stop()

	// Whatever ends the daemon -- a signal, the idle rule, the icon's Stop -- ends
	// it by cancelling this. One way out, so one teardown to get right.
	ctx, asked := context.WithCancel(ctx)
	defer asked()

	native.SetRoot(options.DVBRoot)
	native.SetChardevRoot(options.ChardevRoot)
	native.SetSocket(options.Sockets.HTTP)

	// Claimed before anything is opened. See TakeClaim.
	claim, err := TakeClaim(options.Sockets.HTTP, options.Sockets.Private)
	if err != nil {
		return err
	}
	// Released last of all, after the tuners have gone back.
	defer claim.Close()

	// A previous run that was killed outright never got to give its tuners back to
	// the drivers they were taken from. Nothing was written down about it: the
	// cards are asked, and only the ones that answer wrongly are put right.
	//
	// It runs beside the daemon rather than before it. Putting a tuner back is the
	// vendor driver opening and closing hardware, which can outlast the client
	// that started this daemon. See pool.Options.Settled.
	recovered := make(chan error, 1)
	recoveryContext, stopRecovery := context.WithCancel(ctx)
	defer func() { stopRecovery(); native.ReleaseHost() }()
	settled := pool.NewSettlement(recoveryContext, recovered)
	go func() {
		repaired, err := native.Recover(recoveryContext)
		recovered <- err
		if err != nil && recoveryContext.Err() == nil {
			options.Report("startup repair: " + err.Error())
		}
		if repaired > 0 {
			options.Report(fmt.Sprintf("put %d tuner(s) back after an earlier run ended without releasing them", repaired))
		}
	}()

	supervisor := pool.New(registry{cold: &coldBoot{firmwareDir: options.FirmwareDir, report: options.Report}}, pool.Options{
		FirmwareDir: options.FirmwareDir,
		Linger:      options.Linger,
		Settled:     settled,
		Settling:    native.Recovering(),
	})
	defer supervisor.Close()

	// The card provider draws its boards from the pool, so that a card session and a
	// recording on the same enclosure share one handle instead of fighting for it.
	provider := usbcard.New(usbcard.Config{
		FirmwareDir: options.FirmwareDir,
		Boards:      supervisor.Cards(),
	})

	httpServer, listener, err := listenHTTP(supervisor, provider, options)
	if err != nil {
		return err
	}
	defer listener.Close()

	usbListener, err := listenUSB(options)
	if err != nil {
		// As with the card socket: a tuner already in hand keeps working.
		options.Report("the USB handover socket could not be opened: " + err.Error())
	}
	if usbListener != nil {
		defer usbListener.Close()
		// The descriptors handed over are this process's to close, and they go back
		// last, after the boards holding duplicates of them have.
		defer injected.Default.Close()
	}

	var group sync.WaitGroup
	serve(&group, func() error { return httpServer.Serve(listener) }, options.Report)
	if usbListener != nil {
		serve(&group, func() error { return injected.Serve(usbListener, injected.Default, options.Report) }, options.Report)
	}
	public, publicListener, err := listenPublic(supervisor, provider, options)
	if err != nil {
		return err
	}
	if publicListener != nil {
		defer publicListener.Close()
		serve(&group, func() error { return public.Serve(publicListener) }, options.Report)
	}

	if options.Ready != nil {
		// Whoever asked to be told says it their own way.
		options.Ready(options.Sockets)
	} else {
		options.Report("serving " + options.Sockets.HTTP)
	}

	// How many clients are attached, which is what the idle rule counts down on
	// and what the icon asks about before it stops anything. Every listener
	// counts: leaving the port out had the idle rule stop a daemon in the middle
	// of a stream. A PC/SC client is counted through the HTTP connection its `coax
	// pcsc` holds.
	attached := func() int {
		return httpServer.connections() + public.connections()
	}

	if options.Tray {
		trayDone := make(chan struct{})
		go func() {
			defer close(trayDone)
			tray.Run(ctx, tray.Options{
				Title:   "coaxd",
				Version: options.Version,
				Commit:  options.Commit,
				Stop:    asked,
				Busy:    attached,
				Report:  options.Report,
			})
		}()
		// The icon goes before the tuners are given back, so nobody is left looking at
		// an icon for something on its way out. Waited for, but not indefinitely: its
		// thread can be inside a message box waiting for a person.
		defer func() {
			select {
			case <-trayDone:
			case <-time.After(trayGrace):
				options.Report("the notification-area icon is still waiting for an answer; stopping without it")
			}
		}()
	}

	rule := options.Idle
	if rule.Count == nil {
		rule.Count = attached
	}
	if rule.Interval > 0 {
		if rule.Wait(ctx) {
			options.Report("nothing has used the daemon; stopping")
		}
	} else {
		<-ctx.Done()
	}

	// Everything watching for the stop should hear it before the teardown. The idle
	// rule ends the wait without cancelling anything, so without this the icon would
	// wait for a context that only ends when Run returns.
	asked()

	// A daemon that cannot finish stopping is worse than one that crashes: it
	// holds the tuners, answers nobody, and leaves a Digital Devices graph running
	// that only a person can stop. Exiting is not an option either -- a graph left
	// running while the process dies bugchecks Windows -- so it says so at
	// intervals, with stacks.
	defer sayIfStuck(options.Report)()

	// Closing the listeners ends the accept loops; the streams still running end
	// when their own connections are closed with them.
	_ = listener.Close()
	if publicListener != nil {
		_ = publicListener.Close()
	}
	if usbListener != nil {
		_ = usbListener.Close()
	}
	shutdown, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	_ = httpServer.Shutdown(shutdown)
	if publicListener != nil {
		_ = public.Shutdown(shutdown)
	}
	group.Wait()
	return nil
}

// trayGrace is how long the icon is given to take itself down before the daemon
// stops anyway: long enough for a message loop to notice, far short of a person
// reading.
const trayGrace = 2 * time.Second

// stuckReport is how long the teardown is given before it is called stuck, and
// how often to say so after that.
const stuckReport = 30 * time.Second

// sayIfStuck watches the teardown and reports what is holding it up. The returned
// function ends the watch, and is deferred so it runs after the teardown.
func sayIfStuck(report func(string)) func() {
	finished := make(chan struct{})
	go func() {
		for round := 1; ; round++ {
			select {
			case <-finished:
				return
			case <-time.After(stuckReport):
			}
			stacks := make([]byte, 1<<20)
			stacks = stacks[:runtime.Stack(stacks, true)]
			report(fmt.Sprintf("still stopping after %s; the tuners are still held. What everything is waiting on:\n%s",
				time.Duration(round)*stuckReport, stacks))
		}
	}()
	return func() { close(finished) }
}

// shutdownGrace is how long a stream is given to finish once the daemon has
// decided to stop: long enough for a client to notice the end of the response,
// short enough that stopping does not look like hanging.
const shutdownGrace = 3 * time.Second

// serve runs one accept loop, ignoring the error a closed listener produces.
func serve(group *sync.WaitGroup, run func() error, report func(string)) {
	group.Add(1)
	go func() {
		defer group.Done()
		if err := run(); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			report("server: " + err.Error())
		}
	}()
}

// hosted is an HTTP server together with the tuner host attached to it.
type hosted struct {
	*http.Server
	host *server.Server
}

func (h hosted) connections() int {
	if h.host == nil {
		return 0
	}
	return h.host.Connections()
}

// listenHTTP opens the socket everything is served on.
func listenHTTP(supervisor *pool.Supervisor, provider *usbcard.Provider, options Options) (hosted, net.Listener, error) {
	host := server.New(supervisor, server.Options{
		Version: options.Version,
		Commit:  options.Commit,
		Cards:   cards{provider: provider},
		Report:  options.Report,
	})
	listener, err := Listen(options.Sockets.HTTP, options.Sockets.Private)
	if err != nil {
		return hosted{}, nil, err
	}
	httpServer := &http.Server{Handler: host.Handler(), ReadHeaderTimeout: readHeaderTimeout}
	host.Attach(httpServer)
	return hosted{Server: httpServer, host: host}, listener, nil
}

// listenPublic opens the port, when one was asked for. It serves what the socket
// serves, the card included: see server.Handler.
func listenPublic(supervisor *pool.Supervisor, provider *usbcard.Provider, options Options) (hosted, net.Listener, error) {
	if options.Public == "" {
		return hosted{}, nil, nil
	}
	if err := checkPublic(options.Public, options.AllowRemote); err != nil {
		return hosted{}, nil, err
	}
	host := server.New(supervisor, server.Options{
		Version: options.Version,
		Commit:  options.Commit,
		Cards:   cards{provider: provider},
		Report:  options.Report,
		Port:    true,
		AnyHost: options.AllowRemote,
	})
	config := publicListenConfig()
	listener, err := config.Listen(context.Background(), "tcp", options.Public)
	if err != nil {
		return hosted{}, nil, fmt.Errorf("listen on %s: %w", options.Public, err)
	}
	httpServer := &http.Server{Handler: host.Handler(), ReadHeaderTimeout: readHeaderTimeout}
	host.Attach(httpServer)
	options.Report("also serving http://" + options.Public)
	return hosted{Server: httpServer, host: host}, listener, nil
}

// Go listens with Multipath TCP by default on Linux, which coaxd does not use.
func publicListenConfig() net.ListenConfig {
	var config net.ListenConfig
	config.SetMultipathTCP(false)
	return config
}

// readHeaderTimeout is how long a client has to finish saying what it wants.
// Only the request is timed: a connection waiting between requests is a client
// keeping its tuner, and a stream times its own writes (see server.copyStream).
const readHeaderTimeout = 10 * time.Second

// checkPublic refuses to put the daemon on the network by accident. Any address
// but the loopback is reachable by the network, and this daemon has no notion
// of who is asking -- whoever reaches the port gets the tuners and the card --
// so it has to be said deliberately.
func checkPublic(address string, allowRemote bool) error {
	if allowRemote {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("--http %q is not a host and port: %w", address, err)
	}
	if host == "" {
		return fmt.Errorf("--http %q would serve on every address; give 127.0.0.1 or pass --allow-remote", address)
	}
	if parsed := net.ParseIP(host); parsed == nil || !parsed.IsLoopback() {
		return fmt.Errorf("--http %q would serve beyond this machine; pass --allow-remote if that is meant", address)
	}
	return nil
}

// listenUSB opens the socket a platform USB service hands descriptors over on,
// and answers with nothing at all when none was asked for.
func listenUSB(options Options) (net.Listener, error) {
	if options.USBSocket == "" {
		return nil, nil
	}
	listener, err := Listen(options.USBSocket, options.Sockets.Private)
	if err != nil {
		return nil, err
	}
	// Whatever is attached becomes a tuner every client is served, and a detach
	// takes one away, so only this daemon's own user hands devices over, even
	// where the HTTP socket is open to everyone.
	if err := closeToOthers(options.USBSocket); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

// registry is where the pool finds hardware: the USB boards this program drives
// itself, and the tuners the operating system already drives, wrapped to look the
// same. Both are claimed exclusively.
type registry struct {
	// cold starts the USB boards still waiting for their firmware, nil for none.
	cold *coldBoot
}

func (r registry) Detect() ([]pool.Detected, error) {
	r.cold.run()
	attached, usbErr := usbBoards()
	native, nativeErr := native.Detect()
	switch {
	case usbErr != nil && nativeErr != nil:
		// Neither source could look. The USB one is reported, its failure being the
		// one with something to do about it.
		return nil, usbErr
	case usbErr != nil && len(native) == 0:
		return nil, usbErr
	case nativeErr != nil && len(attached) == 0:
		return nil, nativeErr
	}
	// One source failing while the other found something is not worth failing
	// over: a machine with no /dev/bus/usb and a DVB card in it has tuners.
	return append(withoutClaimed(attached), native...), nil
}

func (registry) Open(one pool.Detected, options device.Options) (device.Board, error) {
	if native.Owns(one) {
		return native.Open(one, options)
	}
	return usb.OpenBoard(one.Backend, options)
}

// withoutClaimed drops the USB boards an operating system driver already has. A
// board px4_drv has bound to appears in both listings, and only the character
// device can be opened -- the USB half answers EBUSY -- so leaving both in
// would let a client pick the wrong one. Taking the device away from the driver
// is not this program's business: somebody installed px4_drv on purpose.
func withoutClaimed(attached []pool.Detected) []pool.Detected {
	return withoutClaimedBy(attached, native.Claimed())
}

func withoutClaimedBy(attached []pool.Detected, claimed map[string]bool) []pool.Detected {
	if len(claimed) == 0 {
		return attached
	}
	kept := make([]pool.Detected, 0, len(attached))
	for _, one := range attached {
		if claimed[one.Selector] {
			continue
		}
		kept = append(kept, one)
	}
	return kept
}

// coldBoot starts the boards that boot from the host before they are listed. A
// PX-W3U3 comes up under its bridge maker's identifiers and is not listed as a
// tuner until it has its firmware, so without this it would never be opened.
type coldBoot struct {
	firmwareDir string
	report      func(string)

	mutex sync.Mutex
	// last is the failure last reported, so that one which lasts -- a firmware
	// file that is not there -- is said once rather than at every listing.
	last string
}

func (c *coldBoot) run() {
	if c == nil {
		return
	}
	err := usb.BootCold(usb.Options{FirmwareDir: c.firmwareDir})
	c.mutex.Lock()
	defer c.mutex.Unlock()
	message := ""
	if err != nil {
		message = err.Error()
	}
	if message != "" && message != c.last && c.report != nil {
		c.report("a board waiting for its firmware could not be started: " + message)
	}
	c.last = message
}

// usbBoards lists the USB boards, with the tuner layout each model is known to
// have.
func usbBoards() ([]pool.Detected, error) {
	attached, err := usb.Detect()
	if err != nil {
		return nil, err
	}
	found := make([]pool.Detected, 0, len(attached))
	for _, one := range attached {
		found = append(found, pool.Detected{
			Backend:     one.Backend,
			Selector:    one.Device,
			Description: one.Description,
			Serial:      one.Serial,
			Port:        one.Port,
			Location:    one.Device,
			USB:         one.ID,
			Deliveries:  one.Deliveries,
			Tuners:      usb.Layout(one.Backend, one.ID),
		})
	}
	return found, nil
}

// cards presents the card provider as the slots the HTTP layer serves.
type cards struct{ provider *usbcard.Provider }

func (c cards) Readers() ([]server.CardInfo, error) {
	context, err := c.provider.EstablishContext(winscard.ScopeSystem)
	if err != nil {
		return nil, err
	}
	defer context.Release()
	names, err := context.Readers(nil)
	if err != nil {
		return nil, err
	}
	readers := make([]server.CardInfo, 0, len(names))
	for _, name := range names {
		readers = append(readers, server.CardInfo{Reader: name})
	}
	return readers, nil
}

func (c cards) Present(reader string) (bool, error) {
	handle, err := c.provider.OpenReader(reader)
	if err != nil {
		return false, err
	}
	defer handle.Close()
	return handle.Present()
}

func (c cards) Session(reader string, apdus [][]byte) ([]byte, [][]byte, error) {
	handle, err := c.provider.OpenReader(reader)
	if err != nil {
		return nil, nil, err
	}
	defer handle.Close()
	return handle.Session(apdus)
}

// Connect opens a card through the provider's own resource-manager context, so
// the HTTP client gets the same connection a linked program would, sharing and
// all.
func (c cards) Connect(reader string) (server.CardConn, error) {
	context, err := c.provider.EstablishContext(winscard.ScopeSystem)
	if err != nil {
		return nil, err
	}
	card, protocol, err := context.Connect(reader, winscard.ShareShared, winscard.ProtocolT1)
	if err != nil {
		_ = context.Release()
		return nil, err
	}
	held := &heldCard{context: context, card: card, protocol: protocol}
	if status, err := card.Status(); err == nil {
		held.atr = status.ATR
	}
	if room, err := card.GetAttrib(winscard.AttrMaxInput); err == nil && len(room) >= 4 {
		held.maxInput = int(binary.LittleEndian.Uint32(room))
	}
	return held, nil
}

// Watch waits for a slot to change. The board is held open for the whole wait
// rather than opened per look, because opening is what powers a card up.
func (c cards) Watch(ctx context.Context, reader string, since uint32, wait time.Duration) (server.CardState, error) {
	handle, err := c.provider.OpenReader(reader)
	if err != nil {
		return server.CardState{}, err
	}
	defer handle.Close()

	look := func() (server.CardState, error) {
		present, err := handle.Present()
		if err != nil {
			return server.CardState{}, err
		}
		state := server.CardState{
			Events:  uint32(handle.Events()),
			Present: present,
			Sharing: handle.Sharing(),
		}
		if present {
			state.ATR = handle.ATR()
		}
		return state, nil
	}

	state, err := look()
	if err != nil || state.Events != since || wait <= 0 {
		state.Changed = err == nil && state.Events != since
		return state, err
	}

	ticker := time.NewTicker(usbcard.DefaultPollInterval)
	defer ticker.Stop()
	deadline := time.After(wait)
	for {
		select {
		case <-ctx.Done():
			return server.CardState{}, ctx.Err()
		case <-deadline:
			// The wait ran out rather than the slot moving, so what is reported is the
			// state as it stands.
			return state, nil
		case <-ticker.C:
		}
		state, err = look()
		if err != nil {
			return server.CardState{}, err
		}
		if state.Events != since {
			state.Changed = true
			return state, nil
		}
	}
}

// heldCard is one open card, with the provider context it was opened through: the
// two are released together, a context outliving its card holding a board open.
type heldCard struct {
	context  winscard.ProviderContext
	card     winscard.Card
	protocol winscard.Protocol
	atr      []byte
	maxInput int
}

func (h *heldCard) ATR() []byte      { return h.atr }
func (h *heldCard) Protocol() string { return h.protocol.String() }
func (h *heldCard) MaxInput() int    { return h.maxInput }

func (h *heldCard) Transmit(send []byte) ([]byte, error) {
	response, _, err := h.card.Transmit(send, h.protocol)
	return response, err
}

func (h *heldCard) BeginTransaction() error { return h.card.BeginTransaction() }

func (h *heldCard) EndTransaction(disposition string) error {
	return h.card.EndTransaction(dispositionOf(disposition))
}

func (h *heldCard) Close(disposition string) error {
	err := h.card.Disconnect(dispositionOf(disposition))
	if released := h.context.Release(); err == nil {
		err = released
	}
	return err
}

// dispositionOf reads the name a request used. An unknown one leaves the card
// alone: the caller was refused before getting here, and a disposition that is
// not understood should not power a card down under everyone sharing it.
func dispositionOf(named string) winscard.Disposition {
	switch named {
	case "reset":
		return winscard.ResetCard
	case "unpower":
		return winscard.UnpowerCard
	case "eject":
		return winscard.EjectCard
	default:
		return winscard.LeaveCard
	}
}
