// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Command coaxd owns the tuner hardware and serves it over HTTP and PC/SC.
//
// Only one process can drive a USB tuner board -- the interface is claimed exclusively --
// yet one board is several tuners and a card slot that different programs want at once, so
// this process holds the boards and hands out shares.
//
// It is not meant to be managed: a client that finds nothing listening starts one, and it
// stops again once nothing has used it for a while. Run it with --idle 0 to keep it
// resident under a supervisor.
//
//	coaxd                                    serve until nobody needs it
//	coaxd --idle 0                           stay
//	coaxd --tray=false                       stay out of the notification area
//	coaxd --http 127.0.0.1:40772             also serve players that cannot
//	                                         open a Unix socket
//	coaxd --log coaxd.log                    keep a diagnostic log; COAXD_LOG
//	                                         does the same for a daemon a
//	                                         client starts
//
// The card is served over HTTP like everything else. A PC/SC application wants a socket of
// pcsc-lite's own instead, and that is made by "coax pcsc", on the machine running the
// application rather than this one.
//
//	coaxd load-asv52xx-firmware              start a cold PX-S3U-series board
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/Till0196/coax/internal/coaxd/build"
	"github.com/Till0196/coax/internal/coaxd/host/daemon"
	"github.com/Till0196/coax/internal/coaxd/host/native"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
	"github.com/Till0196/coax/internal/coaxd/trace"
	"github.com/Till0196/coax/internal/idle"
)

// Replaced at build time:
//
//	go build -ldflags "-X main.version=0.2.0 -X main.commit=$(git rev-parse HEAD)"
var (
	version = "0.1.1"
	commit  = ""
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "coaxd:", err)
		os.Exit(1)
	}
}

func run(arguments []string, stdout, stderr *os.File) error {
	if len(arguments) != 0 {
		switch arguments[0] {
		case native.HostCommand:
			// Its diagnostics go to the daemon, which writes them where its own go.
			trace.SetOutput(stderr)
			return native.ServeHost(os.Stdin, stdout, arguments[1:])
		case "load-asv52xx-firmware", "load-px3-firmware":
			return runLoadASV52xxFirmware(arguments[0], arguments[1:], stdout, stderr)
		case "help", "-h", "--help":
			printUsage(stderr)
			return flag.ErrHelp
		}
	}
	return serve(arguments, stdout, stderr)
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  coaxd [options]                      serve tuners and cards")
	fmt.Fprintln(w, "  coaxd load-asv52xx-firmware [options] start a cold PX-S3U-series board")
}

func serve(arguments []string, stdout, stderr *os.File) error {
	flags := flag.NewFlagSet("coaxd", flag.ContinueOnError)
	flags.SetOutput(stderr)
	socketDir := flags.String("socket-dir", "", "directory for the sockets (a per-user runtime directory when omitted)")
	httpSocket := flags.String("socket", "", "socket to serve tuners and cards on (inside --socket-dir when omitted)")
	usbSocket := flags.String("usb-socket", "", "socket to take USB device descriptors on, for Android and other systems where this process may not read the bus itself")
	public := flags.String("http", "", "also serve tuners on this host:port, for players that cannot open a Unix socket")
	allowRemote := flags.Bool("allow-remote", false, "let --http name an address other than the loopback")
	firmwareDir := flags.String("firmware-dir", "", "firmware directory (OS default when omitted)")
	dvbRoot := flags.String("dvb-root", "", "where the Linux DVB device nodes are (/dev/dvb when omitted); ignored on other systems")
	chardevRoot := flags.String("chardev-root", "", "where the px4_drv and pt3 character devices are (/dev when omitted); ignored on other systems")
	linger := flags.Duration("board-linger", pool.DefaultLinger, "how long a board stays open after its last user; 0 closes it at once")
	idleAfter := flags.Duration("idle", idle.DefaultInterval*time.Duration(idle.DefaultStrikes),
		"stop once nothing has used the daemon for this long; 0 stays running")
	quiet := flags.Bool("quiet", false, "say nothing but errors")
	showVersion := flags.Bool("version", false, "say which build this is and stop")
	logPath := flags.String("log", os.Getenv("COAXD_LOG"), "append a diagnostic log to this file: every line the daemon says, every error a client is sent, and what the boards did on the way (COAXD_LOG, so that a daemon a client starts keeps one too)")
	tray := flags.Bool("tray", true, "show an icon in the notification area to stop the daemon from, where the system has one; --tray=false leaves it out")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("coaxd takes no positional arguments; %q is not a subcommand", flags.Arg(0))
	}
	if *showVersion {
		// Answered without touching hardware: the usual reason to ask is that
		// the daemon will not start.
		fmt.Fprintln(stdout, build.Describe("coaxd", version, commit), runtime.GOARCH)
		return nil
	}

	sockets, err := daemon.DefaultSockets()
	if err != nil {
		return err
	}
	if *socketDir != "" {
		sockets = daemon.At(*socketDir, sockets.Private)
	}
	if *httpSocket != "" {
		sockets.HTTP = *httpSocket
	}

	report := func(line string) { fmt.Fprintln(stderr, "coaxd:", line) }
	if *quiet {
		report = func(string) {}
	}
	if *logPath != "" {
		logFile, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("open the log: %w", err)
		}
		defer logFile.Close()
		trace.SetOutput(logFile)
		defer trace.SetOutput(nil)
		trace.Printf("%s starting, pid %d", build.Describe("coaxd", version, commit), os.Getpid())
		said := report
		report = func(line string) {
			said(line)
			trace.Printf("%s", line)
		}
	}

	options := daemon.Options{
		Sockets:     sockets,
		Public:      *public,
		AllowRemote: *allowRemote,
		FirmwareDir: *firmwareDir,
		Linger:      *linger,
		Version:     version,
		Commit:      commit,
		Report:      report,
		Tray:        *tray,
	}
	// The flag is one length of time; the number of checks it splits into is
	// this program's business.
	if *idleAfter > 0 {
		options.Idle = idle.Rule{
			Interval: *idleAfter / time.Duration(idle.DefaultStrikes),
			Strikes:  idle.DefaultStrikes,
		}
	}

	options.DVBRoot = *dvbRoot
	options.ChardevRoot = *chardevRoot
	options.USBSocket = *usbSocket

	if !*quiet {
		// Announced only once the bind has won, so a daemon that lost to one
		// already running does not claim to be serving.
		options.Ready = func(sockets daemon.Sockets) {
			fmt.Fprintf(stdout, "serving %s\n", sockets.HTTP)
			fmt.Fprintln(stdout, "for PC/SC, run coax pcsc; it makes the socket to point PCSCLITE_CSOCK_NAME at")
		}
	}
	return daemon.Run(context.Background(), options)
}
