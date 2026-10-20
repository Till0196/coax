// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"reflect"

	"github.com/Till0196/coax/pkg/host"
)

// hostOptions are the options every subcommand that needs hardware has, since
// they all reach it the same way: through the daemon.
type hostOptions struct {
	address     string
	firmwareDir string
	noStart     bool
	verbose     bool
	// report says things on the side, when the caller asked to hear them. Never
	// nil once the command has been parsed.
	report func(string)
}

// command is the shape of a subcommand: a flag set named after it, the options
// it shares with the others, and the diagnostics writer.
//
// It is shared rather than repeated because the repetition was the bug: a
// subcommand that forgot to point its flag set at stderr wrote its usage into
// whatever the caller was piping the stream to, and one that forgot to refuse
// positional arguments quietly ignored them.
type command struct {
	flags  *flag.FlagSet
	stderr io.Writer
	host   hostOptions
	// hardware says whether this command reaches the daemon, and so whether it
	// carries the options for finding one.
	hardware bool
}

func newCommand(name string, stderr io.Writer) *command {
	flags := flag.NewFlagSet("coax "+name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return &command{flags: flags, stderr: stderr}
}

// newHardwareCommand is a subcommand that reaches the daemon, and so takes the
// options for finding one and for saying what it is doing.
func newHardwareCommand(name string, stderr io.Writer) *command {
	c := newCommand(name, stderr)
	c.hardware = true
	c.flags.StringVar(&c.host.address, "host", "",
		"tuner daemon to use: a socket path or host:port (the usual place for this system when omitted)")
	c.flags.StringVar(&c.host.firmwareDir, "firmware-dir", "",
		"firmware directory, passed on to a daemon this command starts; one already running keeps its own (OS default when omitted)")
	c.flags.BoolVar(&c.host.noStart, "no-start", false,
		"fail rather than starting a tuner daemon when none is running")
	c.flags.BoolVar(&c.host.verbose, "verbose", false, "show non-fatal diagnostics")
	return c
}

// alias gives a flag a second, shorter name: -o works wherever --output does.
// Both names write the same variable, and the help line points at the long one
// rather than repeating its description.
func alias(flags *flag.FlagSet, short, long string) {
	named := flags.Lookup(long)
	if named == nil {
		panic("cli: no --" + long + " to alias as -" + short)
	}
	flags.Var(named.Value, short, "alias for --"+long)
	// The long name prints the default, so the alias line only has to say
	// where to look. Claiming the zero value as its default is what keeps the
	// flag package from printing it twice: it shows a default only where one
	// differs from the zero value.
	flags.Lookup(short).DefValue = zeroDefault(named.Value)
}

// zeroDefault is what a flag of this kind prints when it holds nothing, which
// is the comparison the flag package makes before showing a default.
func zeroDefault(value flag.Value) string {
	kind := reflect.TypeOf(value)
	if kind.Kind() == reflect.Pointer {
		return reflect.New(kind.Elem()).Interface().(flag.Value).String()
	}
	return reflect.Zero(kind).Interface().(flag.Value).String()
}

func (c *command) parse(arguments []string) error {
	if err := c.flags.Parse(arguments); err != nil {
		return err
	}
	c.host.report = reporter(c.host.verbose, c.stderr)
	return nil
}

// parseOnly reads a command line that takes no positional arguments, and says
// so rather than ignoring them.
func (c *command) parseOnly(arguments []string) error {
	if err := c.parse(arguments); err != nil {
		return err
	}
	if c.flags.NArg() != 0 {
		return fmt.Errorf("%s does not accept positional arguments", c.flags.Name())
	}
	return nil
}

func (c *command) dial(ctx context.Context) (*host.Client, error) {
	return host.Dial(ctx, host.Options{
		Address:     c.host.address,
		NoStart:     c.host.noStart,
		FirmwareDir: c.host.firmwareDir,
		Report:      c.host.report,
		Agent:       "coax/" + version,
	})
}

func (c *command) report(line string) { c.host.report(line) }

// reporter is what --verbose turns diagnostics into: lines on stderr, or
// nothing at all.
func reporter(verbose bool, stderr io.Writer) func(string) {
	if !verbose {
		return func(string) {}
	}
	return func(line string) {
		_, _ = io.WriteString(stderr, "coax: "+line+"\n")
	}
}
