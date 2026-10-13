// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package cli is the subcommands, and nothing else: each reads a command line,
// reaches the daemon where it needs hardware, and hands the work to the package
// that does it.
package cli

import (
	"flag"
	"fmt"
	"io"
)

func Run(arguments []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	if len(arguments) == 0 {
		printUsage(stderr)
		return fmt.Errorf("a subcommand is required")
	}
	switch arguments[0] {
	case "list", "devices":
		return runList(arguments[1:], stdout, stderr)
	case "scan":
		return runScan(arguments[1:], stdout, stderr)
	case "tune":
		return runTune(arguments[1:], stdout, stderr)
	case "export":
		return runExport(arguments[1:], stdin, stdout, stderr)
	case "card":
		return runCard(arguments[1:], stdout, stderr)
	case "pcsc":
		return runPCSC(arguments[1:], stdout, stderr)
	case "version", "-v", "--version":
		return runVersion(arguments[1:], stdout)
	case "help", "-h", "--help":
		printUsage(stderr)
		return flag.ErrHelp
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown subcommand %q", arguments[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  coax list [--tuners] [--json]")
	fmt.Fprintln(w, "  coax scan [options] [dvbv5_channels.conf ...]")
	fmt.Fprintln(w, "  coax scan --discover [options]")
	fmt.Fprintln(w, "  coax export --format mirakurun --input scan.json [options]")
	fmt.Fprintln(w, "  coax tune [options]")
	fmt.Fprintln(w, "  coax card [options] [APDU ...]")
	fmt.Fprintln(w, "  coax pcsc [options]")
	fmt.Fprintln(w, "  coax version")
}
