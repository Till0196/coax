// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package graph

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/bda/filters"
	"github.com/Till0196/coax/internal/coaxd/bda/tuning"
)

type changesRecorder struct {
	calls []string
	fail  string
}

func (c *changesRecorder) step(name string) error {
	c.calls = append(c.calls, name)
	if c.fail == name {
		return errors.New(name)
	}
	return nil
}
func (c *changesRecorder) StartChanges() error  { return c.step("start") }
func (c *changesRecorder) CheckChanges() error  { return c.step("check") }
func (c *changesRecorder) CommitChanges() error { return c.step("commit") }

func TestDDTuneTransaction(t *testing.T) {
	for _, test := range []struct {
		fail string
		want []string
	}{
		{"", []string{"start", "write", "check", "commit"}},
		{"start", []string{"start"}},
		{"write", []string{"start", "write", "start", "commit"}},
		{"check", []string{"start", "write", "check", "start", "commit"}},
		{"commit", []string{"start", "write", "check", "commit", "start", "commit"}},
	} {
		t.Run(test.fail, func(t *testing.T) {
			c := &changesRecorder{fail: test.fail}
			err := ddTransaction(c, func() error { return c.step("write") })
			if (err != nil) != (test.fail != "") || !reflect.DeepEqual(c.calls, test.want) {
				t.Fatalf("calls=%v err=%v", c.calls, err)
			}
		})
	}
}

func TestDDGraphNetworkFollowsPublishedFilter(t *testing.T) {
	tuner := filters.Moniker{Name: "Digital Devices DVB-S2 Tuner 1", Display: `@device:pnp:\\?\dd_dvb#ven_dd01`}
	for _, system := range []string{"ISDBT", "ISDBS", "DVBCANNEXB", "DVBC2"} {
		channel := tuning.TuneProfile{System: system, Delivery: tuning.DeliveryCable, FrequencyKHz: 111000, StreamID: 16400}
		network := ddNetworkProfile(tuner, channel)
		if network.System != "DVBS2" || network.Delivery != tuning.DeliverySatellite || network.FrequencyKHz != channel.FrequencyKHz || channel.System != system {
			t.Fatalf("channel=%+v network=%+v", channel, network)
		}
	}
}
