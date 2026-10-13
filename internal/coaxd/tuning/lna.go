// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

// LNAProperty is the older channel-file key for the front end's gain, spelled
// as the Linux DVB API spells its property, DTV_LNA. It is a synonym for
// GainProperty with no vocabulary of its own: gain.go reads either key, and
// "off"/"on" are the same ends "min"/"max" name.
const LNAProperty = "LNA"
