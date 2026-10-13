// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"flag"
	"fmt"
	"strconv"
	"strings"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/plan"
)

// transportStreamID reads what --stream-id named, when it named a transport
// stream. A DVB-T2 pipe is a number too small to be mistaken for one and is
// answered for by the front end rather than here.
func transportStreamID(value string) (uint16, bool) {
	number, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0, false
	}
	return uint16(number), true
}

type channelRequest struct {
	system    string
	channel   string
	channels  string
	frequency string
	streamID  string
	lnbPower  string
	lnbf      string
	gain      string
	satNumber string
	extra     map[string]string
}

// given reports whether anything here describes a channel, which is what tells
// a command that takes a channel either way which it was asked for. The
// controls are not part of it: a gain or an LNB supply says how to receive a
// channel, not which one.
func (request channelRequest) given() bool {
	return request.system != "" || request.channel != "" || request.frequency != "" ||
		request.streamID != "" || len(request.extra) != 0
}

// channelProperties builds the channel: a channel file's section as the ground,
// the flags laid over it, and a channel named rather than described worked out
// against the plans.
func channelProperties(request channelRequest) (map[string]string, error) {
	system, channel, frequency := request.system, request.channel, request.frequency
	streamID, lnbPower, lnbf, gain := request.streamID, request.lnbPower, request.lnbf, request.gain

	properties := make(map[string]string, len(request.extra)+5)
	// A channel file, where one was given, is the ground the rest is laid on: its
	// section supplies the properties and the command line covers them. That is
	// the arrangement szap-s2j is used under.
	if request.channels != "" {
		section, err := channelFileSection(request.channels, channel)
		if err != nil {
			return nil, err
		}
		for name, value := range section {
			properties[strings.ToUpper(name)] = value
		}
		channel = ""
	}
	// What -P said, kept apart from what the file said: a property written on
	// the command line beside a flag meaning the same is a contradiction, while
	// one in a file that a flag covers is a default being overridden.
	written := make(map[string]bool, len(request.extra))
	for name, value := range request.extra {
		name = strings.ToUpper(name)
		properties[name] = value
		written[name] = true
	}
	if request.satNumber != "" {
		if written["SAT_NUMBER"] {
			return nil, fmt.Errorf("--sat-number and -P SAT_NUMBER are two ways of saying the same thing; give one of them")
		}
		properties["SAT_NUMBER"] = request.satNumber
	}
	if lnbf != "" {
		if written["LNB"] {
			return nil, fmt.Errorf("--lnbf and -P LNB are two ways of saying the same thing; give one of them")
		}
		// The names are the daemon's to know: a copy here would be one more
		// thing to keep in step. A name it does not know comes back with the
		// list.
		properties["LNB"] = lnbf
	}
	if lnbPower != "" {
		if written["VOLTAGE"] {
			return nil, fmt.Errorf("--lnb-power and -P VOLTAGE are two ways of saying the same thing; give one of them")
		}
		setting, err := normalizeLNBPower(lnbPower)
		if err != nil {
			return nil, err
		}
		properties["VOLTAGE"] = setting
	}
	if gain != "" {
		for _, key := range []string{"GAIN", "LNA"} {
			if written[key] {
				return nil, fmt.Errorf("--gain and -P %s are two ways of saying the same thing; give one of them", key)
			}
		}
		// The spelling is checked here so a typo is caught before a tuner is
		// claimed; which step a level lands on is the daemon's to decide, since
		// only it knows what the hardware has.
		setting, err := normalizeGain(gain)
		if err != nil {
			return nil, err
		}
		properties["GAIN"] = setting
	}
	if system != "" {
		properties["DELIVERY_SYSTEM"] = system
	}
	if frequency != "" {
		if request.channels != "" {
			return nil, fmt.Errorf("--channels names where the channel is, and --freq says it again; give one of them")
		}
		properties["FREQUENCY"] = frequency
	}
	if streamID != "" {
		properties["STREAM_ID"] = streamID
	}
	if channel != "" {
		if _, given := properties["FREQUENCY"]; given {
			return nil, fmt.Errorf("--ch and --freq are two ways of saying the same thing; give one of them")
		}
		hz, terrestrial := plan.TerrestrialHz(channel)
		if !terrestrial {
			// Not a channel of the terrestrial plan, so it may be a satellite one:
			// BS01_1 the way recpt1 writes them, or the bare transport stream identifier
			// the Japanese DVB channel files name their sections by. A 124/128 degree
			// east carrier instead names its transponder and satellite.
			if transponder, sky := plan.SkyCarrier(channel); sky {
				// The downlink, and the block that looks at it: the block mixes the downlink
				// down and puts the 22 kHz tone on the lead, which is the only thing telling
				// the two satellites apart. What the caller wrote stands.
				properties["FREQUENCY"] = strconv.Itoa(transponder.DownlinkKHz)
				if lnbf == "" {
					setDefault(properties, "LNB", transponder.Satellite)
				}
				setDefault(properties, "POLARIZATION", transponder.Polarization())
				setDefault(properties, "SYMBOL_RATE", strconv.Itoa(transponder.SymbolRate))
				setDefault(properties, "MODULATION", transponder.Modulation)
				if properties["DELIVERY_SYSTEM"] == "" {
					properties["DELIVERY_SYSTEM"] = transponder.DeliverySystem
				}
				return properties, nil
			}
			// A name plainly meant for the 124/128 degree east plan is answered against
			// it: the satellite parser would report that JCSAT3A/JD99 is not a BS or ND
			// transponder, which is true and unhelpful.
			if plan.IsSkyName(channel) {
				return nil, fmt.Errorf("--ch %q is not a transponder of the 124/128 degree east plan, which is %s",
					channel, plan.SkyCarrierNames())
			}
			satellite, satelliteErr := channelconfig.ParseSatelliteChannel(channel)
			if satelliteErr != nil {
				return nil, fmt.Errorf("--ch %q is none of a television channel (%s), a satellite one, or a 124/128 degree east transponder (%s): %w",
					channel, plan.TelevisionChannelNames(), plan.SkyCarrierNames(), satelliteErr)
			}
			if satellite.HasStream && streamID != "" {
				return nil, fmt.Errorf("--ch %s and --stream-id are two ways of naming the stream; give one of them", channel)
			}
			properties["FREQUENCY"] = strconv.Itoa(satellite.FrequencyKHz)
			switch {
			case satellite.HasStreamID:
				// Written as an identifier, so passed on as one: that is what the DVB API
				// wants and what px4_drv reads a value of twelve or more as. The daemon is
				// the side that knows which its driver has.
				properties["STREAM_ID"] = strconv.Itoa(int(satellite.StreamID))
			case satellite.HasStream:
				// Written as a place, so it stays a place: which stream sits
				// in a place is in the transmission control block, not the
				// name, so the identifier cannot be recovered from it.
				properties["STREAM_ID"] = strconv.Itoa(satellite.Stream)
			}
			if properties["DELIVERY_SYSTEM"] == "" {
				properties["DELIVERY_SYSTEM"] = "ISDBS"
			}
			return properties, nil
		}
		properties["FREQUENCY"] = strconv.Itoa(hz)
		if properties["DELIVERY_SYSTEM"] == "" {
			// A physical channel number only means anything in the terrestrial
			// plan, so naming one says which system it is.
			properties["DELIVERY_SYSTEM"] = "ISDBT"
		}
	}
	if properties["DELIVERY_SYSTEM"] == "" {
		return nil, fmt.Errorf("a delivery system is required; give --system")
	}
	if properties["FREQUENCY"] == "" {
		return nil, fmt.Errorf("a channel is required; give --ch or --freq")
	}
	return properties, nil
}

// setDefault fills in a property a plan knows and the caller did not write.
func setDefault(properties map[string]string, name, value string) {
	if _, given := properties[name]; !given {
		properties[name] = value
	}
}

// lnbPowerSettings are the spellings --lnb-power accepts, and what each
// becomes. The two levels are named rather than numbered because the voltages
// differ from one tuner to the next, so a number here names a level and not a
// promise. The daemon settles what each means; the table is here so a
// meaningless spelling is refused early.
var lnbPowerSettings = map[string]string{
	"OFF": "off", "0": "off", "NONE": "off",
	"LOW": "low", "11": "low", "13": "low",
	"HIGH": "high", "ON": "high", "15": "high", "18": "high",
}

// normalizeGain checks the spelling only: what a level means depends on the
// hardware at the other end.
func normalizeGain(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	switch strings.ToUpper(trimmed) {
	case "AUTO", "MIN", "MAX", "OFF", "ON":
		return strings.ToLower(trimmed), nil
	}
	number := trimmed
	if !strings.HasSuffix(strings.ToUpper(number), "DB") {
		return "", fmt.Errorf("--gain %q is not a gain setting; write auto, min, max, or a level with its unit such as +13dB", value)
	}
	if _, err := strconv.ParseFloat(strings.TrimSpace(number[:len(number)-2]), 64); err != nil {
		return "", fmt.Errorf("--gain %q is not a gain setting; the level is not a number of decibels", value)
	}
	return trimmed, nil
}

func normalizeLNBPower(value string) (string, error) {
	setting, ok := lnbPowerSettings[strings.ToUpper(strings.TrimSpace(value))]
	if !ok {
		return "", fmt.Errorf("--lnb-power %q is not a supply setting; write off, low or high (0 and none mean off; 11 and 13 mean low; on, 15 and 18 mean high)", value)
	}
	return setting, nil
}

// channelFileSection reads one section of a dvbv5 channel file, which is how a
// channel needing more than a name can carry is asked for by name: a 124/128
// degree east carrier takes six properties. It is also the shape the service is
// already received under.
//
// The section is looked for by its exact name first, then without regard to
// case, because a channel list writes the name a person typed. There is never a
// fallback to the built-in plans: a file with a section called 27 means that
// section.
func channelFileSection(path string, name string) (map[string]string, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("--channels needs --ch to say which section of %s to tune", path)
	}
	entries, err := channelconfig.Parse(path, "")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Name == name {
			return entry.Properties, nil
		}
	}
	wanted := strings.ToUpper(strings.TrimSpace(name))
	for _, entry := range entries {
		if strings.ToUpper(entry.Name) == wanted {
			return entry.Properties, nil
		}
	}
	return nil, fmt.Errorf("%s has no section called %q; it has %s", path, name, sectionNames(entries))
}

// sectionNames lists what a file holds, for the message above, cut short: the
// point is to show the spelling, not to reprint the file.
func sectionNames(entries []channelconfig.Entry) string {
	const most = 12
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if len(names) == most {
			return strings.Join(names, ", ") + fmt.Sprintf(" and %d more", len(entries)-most)
		}
		names = append(names, entry.Name)
	}
	return strings.Join(names, ", ")
}

// properties collects repeated -P NAME=VALUE options, for a channel needing
// something these flags do not name.
type properties struct{ values map[string]string }

func newProperties(flags *flag.FlagSet) *properties {
	collected := &properties{values: map[string]string{}}
	flags.Var(collected, "P", "an extra channel-file property, as NAME=VALUE; may be given more than once")
	return collected
}

func (p *properties) String() string {
	if len(p.values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(p.values))
	for name, value := range p.values {
		parts = append(parts, name+"="+value)
	}
	return strings.Join(parts, ",")
}

func (p *properties) Set(value string) error {
	name, setting, found := strings.Cut(value, "=")
	if !found || strings.TrimSpace(name) == "" {
		return fmt.Errorf("a property is written NAME=VALUE, not %q", value)
	}
	p.values[strings.ToUpper(strings.TrimSpace(name))] = setting
	return nil
}
