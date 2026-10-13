// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbio

import "fmt"

// Descriptor types from the USB specification.
const (
	descriptorTypeConfiguration = 0x02
	descriptorTypeInterface     = 0x04
	descriptorTypeEndpoint      = 0x05
)

// TransferType is the kind of transfer an endpoint carries.
type TransferType byte

// The transfer types encoded in an endpoint descriptor's bmAttributes.
const (
	TransferControl TransferType = iota
	TransferIsochronous
	TransferBulk
	TransferInterrupt
)

// Endpoint describes one endpoint of an interface.
type Endpoint struct {
	Address       byte
	Type          TransferType
	MaxPacketSize uint16
}

// In reports whether the endpoint carries device-to-host transfers.
func (endpoint Endpoint) In() bool { return endpoint.Address&0x80 != 0 }

// Endpoints reports the endpoints of an interface's alternate setting 0, for
// device families that assign endpoint numbers per model rather than fixing
// them in the driver. The configuration descriptor comes over the control pipe.
func (transport *Transport) Endpoints(interfaceNumber int) ([]Endpoint, error) {
	value := uint16(descriptorTypeConfiguration) << 8
	header := make([]byte, 9)
	if _, err := transport.Control(0x80, 0x06, value, 0, header, 1000); err != nil {
		return nil, fmt.Errorf("read configuration descriptor header: %w", err)
	}
	total := int(header[2]) | int(header[3])<<8
	if total < len(header) {
		return nil, fmt.Errorf("configuration descriptor reports only %d bytes", total)
	}
	full := make([]byte, total)
	read, err := transport.Control(0x80, 0x06, value, 0, full, 1000)
	if err != nil {
		return nil, fmt.Errorf("read configuration descriptor: %w", err)
	}
	return parseEndpoints(full[:read], interfaceNumber)
}

func parseEndpoints(data []byte, interfaceNumber int) ([]Endpoint, error) {
	var endpoints []Endpoint
	collecting := false
	for offset := 0; offset+2 <= len(data); {
		length := int(data[offset])
		if length < 2 || offset+length > len(data) {
			break
		}
		switch data[offset+1] {
		case descriptorTypeInterface:
			if length < 5 {
				return nil, fmt.Errorf("short interface descriptor at %d", offset)
			}
			collecting = int(data[offset+2]) == interfaceNumber && data[offset+3] == 0
		case descriptorTypeEndpoint:
			if !collecting {
				break
			}
			if length < 6 {
				return nil, fmt.Errorf("short endpoint descriptor at %d", offset)
			}
			endpoints = append(endpoints, Endpoint{
				Address:       data[offset+2],
				Type:          TransferType(data[offset+3] & 3),
				MaxPacketSize: uint16(data[offset+4]) | uint16(data[offset+5])<<8,
			})
		}
		offset += length
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("USB interface %d has no endpoints", interfaceNumber)
	}
	return endpoints, nil
}
