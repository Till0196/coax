// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Command fwextract recovers a firmware image from the vendor package that
// carries it, so no firmware binary has to be committed or redistributed.
//
// Give it a vendor driver archive, any container inside it, or a bare driver;
// containers are recognised by content, not file name. Dropping packages onto
// the program is the same as naming them as arguments; see ownsConsole.
//
// Recovers ITE IT9175/IT9179, ITE IT930x and ASICEN ASV52xx images from the
// drivers carrying them, and takes the images a vendor ships as plain files out
// of the same archives.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Till0196/coax/internal/coaxd/fwextract"
)

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil && err != flag.ErrHelp {
		fmt.Fprintf(os.Stderr, "fwextract: %v\n", err)
	}
	// A console made for this program closes with it, so on a drop the output
	// would show only for as long as the extraction took.
	if ownsConsole() {
		fmt.Fprint(os.Stderr, "\nPress Enter to close this window. ")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
	if err != nil {
		os.Exit(1)
	}
}

func run(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("fwextract", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var output string
	flags.StringVar(&output, "o", "", "write the image here (default: the firmware's own name, beside the package it came out of)")
	flags.StringVar(&output, "output", "", "write the image here (default: the firmware's own name, beside the package it came out of)")
	var all bool
	flags.BoolVar(&all, "all", false, "write every firmware image in the package, into --output as a directory")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage:")
		fmt.Fprintln(stderr, "  fwextract [--output FILE] <driver package or driver>")
		fmt.Fprintln(stderr, "  fwextract --all [--output DIR] <driver package>...")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Or drop the packages onto the program, which is the same thing.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Accepts a zip, a gzip, a bzip2, a tar, an InstallShield setup launcher,")
		fmt.Fprintln(stderr, "an .msi, a .cab, a Windows .sys or a Linux .ko.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Recovers an image from a driver that hides one, and takes the images a")
		fmt.Fprintln(stderr, "vendor ships as files -- dvb-usb-id5520se.fw, isdbt_rio.inp and the like --")
		fmt.Fprintln(stderr, "as they are.")
		fmt.Fprintln(stderr, "They are the Linux firmware files, which is what the chips take whatever")
		fmt.Fprintln(stderr, "the system: the same file serves a board here on Windows or macOS.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	inputs := flags.Args()
	if len(inputs) == 0 {
		flags.Usage()
		return fmt.Errorf("at least one input is required")
	}
	// --output names one file; only --all makes it a directory.
	if len(inputs) > 1 && output != "" && !all {
		return fmt.Errorf("--output names one file, and %d packages were given; drop them one at a time, or use --all with a directory", len(inputs))
	}

	failed := 0
	for _, input := range inputs {
		if len(inputs) > 1 {
			fmt.Fprintf(stdout, "%s\n", filepath.Base(input))
		}
		err := extract(input, output, all, stdout)
		if err == nil {
			continue
		}
		// One package that carries nothing does not abandon the others.
		if len(inputs) == 1 {
			return err
		}
		fmt.Fprintf(stderr, "fwextract: %s: %v\n", filepath.Base(input), err)
		failed++
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d packages carried nothing that could be read", failed, len(inputs))
	}
	return nil
}

// extract takes what one package carries. Without --output the image lands beside
// the package, not in the working directory: a drop gives a full path, and the
// working directory then belongs to whatever started the program.
func extract(input, output string, all bool, stdout io.Writer) error {
	data, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	if all {
		directory := output
		if directory == "" {
			directory = filepath.Dir(input)
		}
		return writeAll(data, filepath.Base(input), directory, stdout)
	}
	firmware, trace, err := fwextract.FindFirmware(data, filepath.Base(input))
	if err != nil {
		return err
	}
	target := output
	if target == "" {
		target = filepath.Join(filepath.Dir(input), firmware.FileName)
	}
	if err := os.WriteFile(target, firmware.Image, 0o644); err != nil {
		return err
	}
	digest := sha256.Sum256(firmware.Image)
	fmt.Fprint(stdout, trace.String())
	fmt.Fprintf(stdout, "%-8s %d bytes -> %s\n", firmware.Kind, len(firmware.Image), target)
	fmt.Fprintf(stdout, "%-8s %s\n", "sha256", hex.EncodeToString(digest[:]))
	if firmware.Note != "" {
		fmt.Fprintf(stdout, "%-8s %s\n", "note", firmware.Note)
	}
	return nil
}

// writeAll takes every image a package carries: provisioning a board needs the
// bridge microcontroller, the demodulator and sometimes the tuner.
func writeAll(data []byte, name, directory string, stdout io.Writer) error {
	found := fwextract.Collect(data, name)
	if len(found) == 0 {
		return fwextract.ErrNotFound
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	for _, firmware := range found {
		path := filepath.Join(directory, firmware.FileName)
		if err := os.WriteFile(path, firmware.Image, 0o644); err != nil {
			return err
		}
		digest := sha256.Sum256(firmware.Image)
		fmt.Fprintf(stdout, "%-8s %8d bytes  %s  -> %s\n",
			firmware.Kind, len(firmware.Image), hex.EncodeToString(digest[:8]), path)
	}
	return nil
}
