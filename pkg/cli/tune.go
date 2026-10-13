// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Till0196/coax/pkg/host"
	"github.com/Till0196/coax/pkg/isdb"
)

// runTune receives one channel and writes it out. This is what a recording front
// end runs, one process per tuner: it tunes, copies bytes, and stops when it is
// stopped. Stopping is all that ends a recording -- the tuner belongs to this
// connection, and closing it gives the tuner back.
func runTune(arguments []string, stdout, stderr io.Writer) error {
	command := newHardwareCommand("tune", stderr)
	flags := command.flags

	system := flags.String("system", "", "delivery system, as a channel file spells it: ISDBT, ISDBS, DVBT2, ...")
	channel := flags.String("ch", "", "channel by name: a television channel (1 to 12, 13 to 62, or C13 to C63 on a cable), a satellite one written either way it is written -- as recpt1 does (BS01, BS01_1, ND02) or as its transport stream identifier (16401, 45168) -- a 124/128 degree east transponder (JCSAT3A/JD5), or a section of the file given to --channels")
	channels := flags.String("channels", "", "a dvbv5 channel file --ch names a section of, for a channel needing more than a name can carry")
	frequency := flags.String("freq", "", "frequency in Hz, or kHz for satellite, as a channel file writes it")
	streamID := flags.String("stream-id", "", "relative stream to select: an ISDB-S TSID, a DVB-T2 pipe, or a TSID multiplexed into a TSMF carrier")
	lnbPower := flags.String("lnb-power", "", "LNB supply for a satellite channel: off, low or high (11 and 13 mean low, 15 and 18 mean high); none when omitted, as recpt1 and recisdb do")
	noSync := flags.Bool("no-sync", false, "take the stream even when it is not transport packets, instead of refusing a tuner that locked to noise")
	gain := flags.String("gain", "", "front-end gain, on hardware that has a setting: auto, min, max, or a level with its unit such as +13dB; the tuner's own default when omitted")
	satNumber := flags.String("sat-number", "", "position on a DiSEqC switch, 0 to 3, when the dish reaches this tuner through one; nothing is sent when this is omitted")
	lnbf := flags.String("lnbf", "", "low-noise block on the dish, when --freq is a downlink frequency rather than an intermediate one: UNIVERSAL, EXTENDED, ENHANCED, STANDARD, DBS, C-BAND, 110BS, JCSAT3A, JCSAT4B")
	property := newProperties(flags)
	tunerID := flags.String("tuner", "", "tuner to use, as `coax list` names it (any that will serve when omitted)")
	lockTimeout := flags.Duration("lock-timeout", 5*time.Second, "how long to wait for the demodulator to lock")
	priority := flags.Int("priority", 0, "how much this work matters, against everything else asking the daemon for a tuner: nothing free and a higher number takes a tuner from a lower one, the same number never does, and whoever asked first keeps it")
	output := flags.String("output", "-", "where to write the stream, or - for stdout")
	withPCSC := flags.Bool("pcsc", false,
		"also make sure a card socket is being served, for a decoder reading this stream; it stops on its own once nothing is using it")
	alias(flags, "o", "output")
	alias(flags, "t", "tuner")
	alias(flags, "f", "freq")
	if err := command.parseOnly(arguments); err != nil {
		return err
	}

	properties, err := channelProperties(channelRequest{
		system: *system, channel: *channel, channels: *channels,
		frequency: *frequency, streamID: *streamID,
		lnbPower: *lnbPower, lnbf: *lnbf, gain: *gain,
		satNumber: *satNumber, extra: property.values,
	})
	if err != nil {
		return err
	}

	// A front end stops a recording by signalling this process, so that is
	// what has to end the copy cleanly.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client, err := command.dial(ctx)
	if err != nil {
		return err
	}

	// A decoder at the other end of this stream needs the card, and asked for,
	// this makes sure the socket it looks for is being served -- one for the
	// machine, since the card behind it is shared and held only for the length of
	// an exchange. It is not the default because plenty of streams are not
	// decoded.
	//
	// The connection is held for as long as the stream runs, which is what tells
	// the socket it is still wanted; nothing is said down it.
	if *withPCSC {
		held, err := ensurePCSC(ctx, command.host, command.report)
		if err != nil {
			// A stream nothing decodes is still a stream, and refusing to
			// carry it would help nobody.
			command.report("no card socket: " + err.Error())
		} else {
			defer held.Close()
			command.report("cards are at " + DefaultPCSCSocket())
		}
	}
	// No bounds: a recording ends when whoever started it stops it, which is
	// what an unbounded stream is for.
	stream, chosen, err := client.Stream(ctx, properties, host.TuneOptions{
		Tuner:       *tunerID,
		LockTimeout: *lockTimeout,
		NoSync:      *noSync,
		Priority:    *priority,
	}, host.Bounds{})
	if err != nil {
		return err
	}
	defer stream.Close()
	command.report("receiving on " + chosen)

	writer, closeWriter, err := outputWriter(*output, stdout)
	if err != nil {
		return err
	}
	defer closeWriter()

	// A satellite demodulator is told the identifier and hands back that stream
	// alone; a cable multiplex arrives whole and has to be separated here. The
	// flag means the same either way, so the stream goes through a filter that
	// does nothing unless what arrives turns out to be multiplexed.
	if wanted, ok := transportStreamID(*streamID); ok {
		filter := isdb.NewFilter(writer, wanted, command.host.report)
		defer filter.Close()
		writer = filter
	}

	// The context ending closes the stream from under the copy, which is how a
	// signal stops it.
	go func() {
		<-ctx.Done()
		_ = stream.Close()
	}()
	if _, err := io.Copy(writer, stream); err != nil && ctx.Err() == nil {
		return fmt.Errorf("receive the stream: %w", err)
	}
	return nil
}
