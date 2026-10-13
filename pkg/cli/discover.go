// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"flag"
	"os"
	"slices"
	"strings"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/scanner"
)

// discoverFlag is --discover, a switch that may also be given a list: reporting
// itself as a boolean flag is what lets it be written bare, and taking a value
// is what lets one band be asked for rather than all of them.
type discoverFlag struct {
	enabled bool
	systems []string
}

func (discover *discoverFlag) IsBoolFlag() bool { return true }

func (discover *discoverFlag) String() string {
	if !discover.enabled {
		return "false"
	}
	if len(discover.systems) == 0 {
		return "true"
	}
	return strings.Join(discover.systems, ",")
}

func (discover *discoverFlag) Set(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0":
		*discover = discoverFlag{}
		return nil
	case "true", "1", "":
		*discover = discoverFlag{enabled: true}
		return nil
	}
	*discover = discoverFlag{enabled: true, systems: strings.Split(value, ",")}
	return nil
}

// takeLeadingSystems reads a delivery system written after --discover rather
// than attached to it. The flag may be written bare, so the flag package does
// not take the next word as its value: it stops there and calls the rest
// arguments, which makes "--discover ISDBT --list" a request to scan three files
// that do not exist. Leading words naming a system are taken as the flag's value
// and what follows is parsed again as flags.
func (discover *discoverFlag) takeLeadingSystems(flags *flag.FlagSet) error {
	for {
		leading := leadingSystems(flags.Args())
		if len(leading) == 0 {
			return nil
		}
		for _, word := range leading {
			discover.systems = append(discover.systems, strings.Split(word, ",")...)
		}
		if err := flags.Parse(flags.Args()[len(leading):]); err != nil {
			return err
		}
	}
}

// leadingSystems is how many of the words after --discover name a delivery
// system it can work out on its own. A word is only taken this way when there is
// no file of that name, so a channel file called ISDBS still wins, and only the
// leading words are considered, since anything after them is whatever came next
// on the command line.
func leadingSystems(words []string) []string {
	systems := make([]string, 0, len(words))
	for _, word := range words {
		if _, err := os.Stat(word); err == nil {
			break
		}
		if !namesSystems(word) {
			break
		}
		systems = append(systems, word)
	}
	return systems
}

// namesSystems reports whether a word is delivery systems and nothing else. A
// word may be several, since the flag takes them that way as --discover=A,B.
// Every part has to name one: a list with a word that does not is likelier to be
// a file with a comma in its name.
func namesSystems(word string) bool {
	parts := strings.Split(word, ",")
	for _, part := range parts {
		if !slices.Contains(scanner.DiscoverableSystems, channelconfig.NormalizeDeliverySystem(part)) {
			return false
		}
	}
	return len(parts) > 0
}
