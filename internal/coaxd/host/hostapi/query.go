// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package hostapi

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// Request is a tuning request as it arrived in a query string.
type Request struct {
	// Properties are the channel-file properties to tune with, ready for a
	// backend.
	Properties map[string]string
	// Ignored names the properties left out because they describe what a
	// multiplex carries rather than how to receive it.
	Ignored []string

	// Tuner names one tuner exactly. Empty means any that will serve.
	Tuner string
	// Priority is how much this request's work matters, from the
	// PriorityHeader. It does not come out of the query string -- see that
	// constant -- so it is filled in beside the parse rather than by it.
	Priority int
	// LockTimeout is how long to wait for the demodulator to lock.
	LockTimeout time.Duration
	// Settle is how long to hold the channel before re-reading the measurements:
	// a demodulator reports its lock before it has a carrier-to-noise figure, so
	// reading straight away often reports nothing.
	Settle time.Duration
	// WaitForLock says a stream should refuse rather than send nothing when the
	// channel does not lock.
	WaitForLock bool
	// Bytes and For bound a stream: it ends by itself after that many bytes, or
	// that long after the first of them. Zero means no bound.
	//
	// A bound is what lets a client keep its connection: stopping an endless
	// stream means closing the response, and a closed connection is a released
	// tuner, so a scan stopping ten streams gives its tuner back ten times. A
	// bounded stream is read to the end and the lease survives the channel.
	Bytes int
	For   time.Duration
	// RequireSync says a stream should refuse rather than send bytes that are
	// not packets. It catches a demodulator locked to noise, which no hardware
	// measurement reports. sync=off turns it off.
	RequireSync bool
}

// The shorthands a query may use instead of a channel-file property name, for
// the short URL a person types. A program should use the property names, which
// pass through untouched and cover every property.
var shorthands = map[string]string{
	"system":       "DELIVERY_SYSTEM",
	"freq":         "FREQUENCY",
	"stream_id":    "STREAM_ID",
	"bandwidth_hz": "BANDWIDTH_HZ",
	"symbol_rate":  "SYMBOL_RATE",
	"modulation":   "MODULATION",
	"polarization": "POLARIZATION",
	"rolloff":      "ROLLOFF",
	"lna":          tuning.LNAProperty,
	"gain":         tuning.GainProperty,
}

// The parameters that steer the request rather than describe the channel. They
// never reach a backend: the Linux one refuses a property it does not know.
var controls = map[string]bool{
	"tuner":        true,
	"lnb_power":    true,
	"lnbf":         true,
	"lock_timeout": true,
	"settle":       true,
	"begin":        true,
	"bytes":        true,
	"for":          true,
	"sync":         true,
}

// ParseRequest reads a tuning request out of a query string. Case decides what
// a parameter is: a name in capitals is a channel-file property passed through
// exactly as written, so a channel-file section needs no re-encoding, while a
// lower-case name is a shorthand or a control. Any other lower-case name is
// refused rather than ignored, so a misspelling is reported.
func ParseRequest(query url.Values, defaults Request) (Request, error) {
	request := defaults
	if request.Properties == nil {
		request.Properties = make(map[string]string)
	}
	properties := make(map[string]string)

	for name, values := range query {
		if len(values) == 0 {
			continue
		}
		value := values[0]
		if len(values) > 1 {
			return Request{}, fmt.Errorf("%s was given more than once", name)
		}

		if isProperty(name) {
			if _, clash := properties[name]; clash {
				return Request{}, fmt.Errorf("%s was given more than once", name)
			}
			properties[name] = value
			continue
		}

		if property, ok := shorthands[name]; ok {
			if _, clash := properties[property]; clash {
				return Request{}, fmt.Errorf("%s and %s are the same thing; give one of them", name, property)
			}
			properties[property] = value
			continue
		}

		if !controls[name] {
			return Request{}, fmt.Errorf("unknown parameter %q", name)
		}

		switch name {
		case "tuner":
			request.Tuner = value
		case "lnbf":
			if _, clash := properties[tuning.LNBProperty]; clash {
				return Request{}, fmt.Errorf("lnbf and %s are the same thing; give one of them", tuning.LNBProperty)
			}
			properties[tuning.LNBProperty] = value
		case "lnb_power":
			if _, clash := properties["VOLTAGE"]; clash {
				return Request{}, fmt.Errorf("lnb_power and VOLTAGE are the same thing; give one of them")
			}
			properties["VOLTAGE"] = value
		case "lock_timeout":
			duration, err := parseDuration(name, value)
			if err != nil {
				return Request{}, err
			}
			request.LockTimeout = duration
		case "settle":
			duration, err := parseDuration(name, value)
			if err != nil {
				return Request{}, err
			}
			request.Settle = duration
		case "bytes":
			count, err := strconv.Atoi(value)
			if err != nil || count < 0 {
				return Request{}, fmt.Errorf("bytes %q is not a number of bytes", value)
			}
			request.Bytes = count
		case "for":
			duration, err := parseDuration(name, value)
			if err != nil {
				return Request{}, err
			}
			request.For = duration
		case "begin":
			switch value {
			case "lock":
				request.WaitForLock = true
			case "now":
				request.WaitForLock = false
			default:
				return Request{}, fmt.Errorf("begin must be lock or now, not %q", value)
			}
		case "sync":
			switch value {
			case "on":
				request.RequireSync = true
			case "off":
				request.RequireSync = false
			default:
				return Request{}, fmt.Errorf("sync must be on or off, not %q", value)
			}
		}
	}

	if properties["DELIVERY_SYSTEM"] == "" {
		return Request{}, fmt.Errorf("a delivery system is required; give system or DELIVERY_SYSTEM")
	}
	if properties["FREQUENCY"] == "" {
		return Request{}, fmt.Errorf("a frequency is required; give freq or FREQUENCY")
	}

	// A channel naming the low-noise block gives the downlink frequency while
	// every backend takes the intermediate one, so converting here lets a DVB
	// tools channel file work against all of them.
	if err := tuning.ApplyLNBf(properties); err != nil {
		return Request{}, err
	}

	// Checked here rather than in the backend: a meaningless spelling is a
	// mistake in the request, and a backend would report it as a failed tune.
	if _, err := tuning.ParseLNB(properties); err != nil {
		return Request{}, err
	}
	// Likewise gain. Whether a board has one is the board's to refuse; this only
	// catches a spelling that is no setting at all.
	if _, err := tuning.ParseGain(properties); err != nil {
		return Request{}, err
	}

	// Properties describing the programmes inside a multiplex are set aside: a
	// backend handed one refuses the whole channel.
	request.Properties, request.Ignored = tuning.SelectTuning(properties)
	return request, nil
}

// isProperty reports whether a parameter name is a channel-file property, which
// is what capitals mean.
func isProperty(name string) bool {
	if name == "" {
		return false
	}
	for _, letter := range name {
		switch {
		case letter >= 'A' && letter <= 'Z':
		case letter >= '0' && letter <= '9', letter == '_':
		default:
			return false
		}
	}
	// A name of only digits and underscores is not a property name.
	return strings.IndexFunc(name, func(letter rune) bool {
		return letter >= 'A' && letter <= 'Z'
	}) >= 0
}

// parseDuration reads a duration, accepting a bare number as seconds so a typed
// URL needs no unit.
func parseDuration(name, value string) (time.Duration, error) {
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return 0, fmt.Errorf("%s %q is not a finite length of time", name, value)
		}
		if seconds < 0 {
			return 0, fmt.Errorf("%s cannot be negative", name)
		}
		if seconds > float64(math.MaxInt64)/float64(time.Second) {
			return 0, fmt.Errorf("%s %q is too long", name, value)
		}
		return time.Duration(seconds * float64(time.Second)), nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a length of time", name, value)
	}
	if duration < 0 {
		return 0, fmt.Errorf("%s cannot be negative", name)
	}
	return duration, nil
}

// SortedProperties lists a property map in a fixed order, so a report reads the
// same twice.
func SortedProperties(properties map[string]string) []string {
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
