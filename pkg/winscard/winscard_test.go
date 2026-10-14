// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package winscard

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeProvider stands in for both kinds of provider: a built-in slot and a
// forwarding one look the same from here, which is the property under test.
type fakeProvider struct {
	name    string
	readers []string
	groups  []string
	// establishErr fails EstablishContext, the way an absent board or a
	// resource manager that is not running would.
	establishErr error
	// readersErr fails enumeration on an established context.
	readersErr error
	// change is delivered to a waiter to end its wait.
	change chan State
	// contexts counts the provider contexts established, to show they are
	// established once and lazily.
	contexts int
	released int
	// cards records every card the provider handed out.
	cards []*fakeCard
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) EstablishContext(scope Scope) (ProviderContext, error) {
	if p.establishErr != nil {
		return nil, p.establishErr
	}
	p.contexts++
	return &fakeContext{provider: p}, nil
}

type fakeContext struct{ provider *fakeProvider }

func (c *fakeContext) Readers(groups []string) ([]string, error) {
	if c.provider.readersErr != nil {
		return nil, c.provider.readersErr
	}
	return c.provider.readers, nil
}

func (c *fakeContext) ReaderGroups() ([]string, error) { return c.provider.groups, nil }

func (c *fakeContext) Connect(reader string, share ShareMode, protocols Protocol) (Card, Protocol, error) {
	if !protocols.Has(ProtocolT1) && share != ShareDirect {
		return nil, ProtocolUndefined, ErrProtoMismatch
	}
	card := &fakeCard{provider: c.provider, reader: reader}
	c.provider.cards = append(c.provider.cards, card)
	return card, ProtocolT1, nil
}

func (c *fakeContext) GetStatusChange(ctx context.Context, timeout time.Duration, states []ReaderState) ([]ReaderState, error) {
	answered := make([]ReaderState, len(states))
	copy(answered, states)
	if c.provider.change == nil {
		for index := range answered {
			answered[index].Event = answered[index].Current
		}
		return answered, nil
	}
	select {
	case event := <-c.provider.change:
		for index := range answered {
			answered[index].Event = event
		}
		return answered, nil
	case <-ctx.Done():
		return nil, ErrCancelled
	}
}

func (c *fakeContext) Release() error {
	c.provider.released++
	return nil
}

type fakeCard struct {
	provider    *fakeProvider
	reader      string
	transmitted [][]byte
	disposition Disposition
	closed      bool
}

func (c *fakeCard) Reconnect(share ShareMode, protocols Protocol, initialization Disposition) (Protocol, error) {
	return ProtocolT1, nil
}

func (c *fakeCard) Disconnect(disposition Disposition) error {
	c.closed = true
	c.disposition = disposition
	return nil
}

func (c *fakeCard) BeginTransaction() error                { return nil }
func (c *fakeCard) EndTransaction(d Disposition) error     { return nil }
func (c *fakeCard) Control(uint32, []byte) ([]byte, error) { return nil, ErrUnsupportedFeature }
func (c *fakeCard) GetAttrib(Attribute) ([]byte, error)    { return nil, ErrUnsupportedFeature }
func (c *fakeCard) SetAttrib(Attribute, []byte) error      { return ErrUnsupportedFeature }
func (c *fakeCard) Status() (Status, error) {
	return Status{State: CardSpecific, Protocol: ProtocolT1, ATR: []byte{0x3b, 0xf0}}, nil
}

func (c *fakeCard) Transmit(send []byte, sendProtocol Protocol) ([]byte, Protocol, error) {
	c.transmitted = append(c.transmitted, send)
	return []byte{0x90, 0x00}, ProtocolT1, nil
}

// testService builds a service over a built-in provider and an external one,
// in the order a real service would hold them, and returns all three.
func testService() (*Service, *fakeProvider, *fakeProvider) {
	builtIn := &fakeProvider{
		name:    "px4",
		readers: []string{"PLEX PX-W3U4 Smart Card Reader (ID: 0123456789ABCDEF)"},
		groups:  []string{DefaultReaderGroup},
	}
	external := &fakeProvider{
		name:    "native",
		readers: []string{"SCM Microsystems Inc. SCR3310 0", DefaultReaderGroup},
		groups:  []string{DefaultReaderGroup},
	}
	return NewService(builtIn, external), builtIn, external
}

func TestListReadersPutsBuiltInFirstAndDropsDuplicates(t *testing.T) {
	service, builtIn, external := testService()
	external.readers = append([]string{builtIn.readers[0]}, external.readers...)

	handle, err := service.EstablishContext(ScopeSystem)
	if err != nil {
		t.Fatalf("establish a context: %v", err)
	}
	readers, err := service.ListReaders(handle, nil)
	if err != nil {
		t.Fatalf("list readers: %v", err)
	}
	if len(readers) != 3 {
		t.Fatalf("listed %d readers, want 3: %v", len(readers), readers)
	}
	if readers[0] != builtIn.readers[0] {
		t.Errorf("listed %q first, want the built-in reader %q", readers[0], builtIn.readers[0])
	}
	for _, name := range readers[1:] {
		if name == builtIn.readers[0] {
			t.Errorf("the built-in reader %q was listed twice", name)
		}
	}
}

func TestEstablishContextDoesNotReachProviders(t *testing.T) {
	service, builtIn, external := testService()
	if _, err := service.EstablishContext(ScopeSystem); err != nil {
		t.Fatalf("establish a context: %v", err)
	}
	if builtIn.contexts != 0 || external.contexts != 0 {
		t.Fatalf("established %d and %d provider contexts, want none until one is needed",
			builtIn.contexts, external.contexts)
	}
}

func TestListReadersSurvivesOneProviderFailing(t *testing.T) {
	service, builtIn, _ := testService()
	builtIn.establishErr = errors.New("no board is attached")

	handle, _ := service.EstablishContext(ScopeSystem)
	readers, err := service.ListReaders(handle, nil)
	if err != nil {
		t.Fatalf("list readers with the built-in provider down: %v", err)
	}
	if len(readers) == 0 {
		t.Fatal("listed no readers, want the external ones")
	}
}

func TestListReadersReportsNoneWhenEveryProviderIsEmpty(t *testing.T) {
	service, builtIn, external := testService()
	builtIn.readers = nil
	external.readers = nil

	handle, _ := service.EstablishContext(ScopeSystem)
	if _, err := service.ListReaders(handle, nil); !errors.Is(err, ErrNoReadersAvailable) {
		t.Fatalf("listed readers with none attached: %v, want %v", err, ErrNoReadersAvailable)
	}
}

func TestConnectRoutesToTheProviderOwningTheReader(t *testing.T) {
	service, builtIn, external := testService()
	handle, _ := service.EstablishContext(ScopeSystem)

	card, protocol, err := service.Connect(handle, external.readers[0], ShareShared, ProtocolAny)
	if err != nil {
		t.Fatalf("connect to the external reader: %v", err)
	}
	if protocol != ProtocolT1 {
		t.Errorf("agreed %v, want %v", protocol, ProtocolT1)
	}
	if len(external.cards) != 1 || len(builtIn.cards) != 0 {
		t.Fatalf("connected %d built-in and %d external cards, want 0 and 1",
			len(builtIn.cards), len(external.cards))
	}
	response, _, err := service.Transmit(card, ProtocolT1, []byte{0x90, 0x30, 0x00, 0x00, 0x00})
	if err != nil {
		t.Fatalf("transmit: %v", err)
	}
	if len(response) != 2 {
		t.Errorf("received %d bytes, want the two status bytes", len(response))
	}
	if len(external.cards[0].transmitted) != 1 {
		t.Errorf("the provider saw %d APDUs, want 1", len(external.cards[0].transmitted))
	}
}

func TestConnectRejectsAnUnknownReader(t *testing.T) {
	service, _, _ := testService()
	handle, _ := service.EstablishContext(ScopeSystem)
	if _, _, err := service.Connect(handle, "Nothing 0", ShareShared, ProtocolAny); !errors.Is(err, ErrUnknownReader) {
		t.Fatalf("connected to an unknown reader: %v, want %v", err, ErrUnknownReader)
	}
}

func TestStatusNamesTheReaderTheCallerConnectedBy(t *testing.T) {
	service, builtIn, _ := testService()
	handle, _ := service.EstablishContext(ScopeSystem)
	card, _, err := service.Connect(handle, builtIn.readers[0], ShareShared, ProtocolT1)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	status, err := service.Status(card)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(status.Readers) != 1 || status.Readers[0] != builtIn.readers[0] {
		t.Errorf("named %v, want %q", status.Readers, builtIn.readers[0])
	}
}

func TestDisconnectInvalidatesTheHandle(t *testing.T) {
	service, builtIn, _ := testService()
	handle, _ := service.EstablishContext(ScopeSystem)
	card, _, _ := service.Connect(handle, builtIn.readers[0], ShareShared, ProtocolT1)

	if err := service.Disconnect(card, UnpowerCard); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if got := builtIn.cards[0].disposition; got != UnpowerCard {
		t.Errorf("disconnected with %v, want %v", got, UnpowerCard)
	}
	if _, _, err := service.Transmit(card, ProtocolT1, []byte{0x00}); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("transmitted on a disconnected handle: %v, want %v", err, ErrInvalidHandle)
	}
}

func TestReleaseContextClosesTheCardsUnderIt(t *testing.T) {
	service, builtIn, _ := testService()
	handle, _ := service.EstablishContext(ScopeSystem)
	card, _, _ := service.Connect(handle, builtIn.readers[0], ShareShared, ProtocolT1)

	if err := service.ReleaseContext(handle); err != nil {
		t.Fatalf("release the context: %v", err)
	}
	if !builtIn.cards[0].closed {
		t.Error("the card outlived the context that opened it")
	}
	if builtIn.released != 1 {
		t.Errorf("released %d provider contexts, want 1", builtIn.released)
	}
	if _, _, err := service.Transmit(card, ProtocolT1, []byte{0x00}); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("transmitted on a released context: %v, want %v", err, ErrInvalidHandle)
	}
	if err := service.IsValidContext(handle); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("the released context is still valid: %v", err)
	}
}

func TestGetStatusChangeAnswersAnUnknownReaderAtOnce(t *testing.T) {
	service, _, _ := testService()
	handle, _ := service.EstablishContext(ScopeSystem)

	answered, err := service.GetStatusChange(handle, Infinite, []ReaderState{
		{Reader: "Unplugged 0", Current: StateUnaware},
	})
	if err != nil {
		t.Fatalf("wait on an unknown reader: %v", err)
	}
	if !answered[0].Event.Has(StateUnknown) || !answered[0].Event.Has(StateChanged) {
		t.Errorf("answered %#x, want StateUnknown and StateChanged", uint32(answered[0].Event))
	}
}

func TestGetStatusChangeReturnsOnTheFirstProviderToMove(t *testing.T) {
	service, builtIn, external := testService()
	builtIn.change = make(chan State, 1)
	external.change = make(chan State, 1)
	handle, _ := service.EstablishContext(ScopeSystem)

	builtIn.change <- StatePresent | StateChanged
	answered, err := service.GetStatusChange(handle, Infinite, []ReaderState{
		{Reader: builtIn.readers[0], Current: StateEmpty},
		{Reader: external.readers[0], Current: StateEmpty},
	})
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !answered[0].Event.Has(StatePresent) {
		t.Errorf("the built-in reader answered %#x, want StatePresent", uint32(answered[0].Event))
	}
	if answered[1].Event.Has(StateChanged) {
		t.Errorf("the external reader answered a change nobody reported: %#x", uint32(answered[1].Event))
	}
}

func TestGetStatusChangeTimesOut(t *testing.T) {
	service, builtIn, external := testService()
	builtIn.change = make(chan State)
	external.change = make(chan State)
	handle, _ := service.EstablishContext(ScopeSystem)

	_, err := service.GetStatusChange(handle, 20*time.Millisecond, []ReaderState{
		{Reader: builtIn.readers[0], Current: StateEmpty},
	})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("waited out: %v, want %v", err, ErrTimeout)
	}
}

func TestCancelUnblocksAWaitButNotTheNextOne(t *testing.T) {
	service, builtIn, _ := testService()
	builtIn.change = make(chan State, 1)
	handle, _ := service.EstablishContext(ScopeSystem)
	// Bind the provider before the wait, so that Cancel below cannot land
	// before the wait has started.
	if _, err := service.ListReaders(handle, nil); err != nil {
		t.Fatalf("list readers: %v", err)
	}

	failures := make(chan error, 1)
	go func() {
		_, err := service.GetStatusChange(handle, Infinite, []ReaderState{
			{Reader: builtIn.readers[0], Current: StateEmpty},
		})
		failures <- err
	}()
	// Give the wait time to reach the provider before cancelling it.
	time.Sleep(10 * time.Millisecond)
	if err := service.Cancel(handle); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := <-failures; !errors.Is(err, ErrCancelled) {
		t.Fatalf("the cancelled wait returned %v, want %v", err, ErrCancelled)
	}

	builtIn.change <- StatePresent | StateChanged
	if _, err := service.GetStatusChange(handle, Infinite, []ReaderState{
		{Reader: builtIn.readers[0], Current: StateEmpty},
	}); err != nil {
		t.Fatalf("the wait after a cancel inherited it: %v", err)
	}
}

func TestAttributesMatchTheirClassAndTag(t *testing.T) {
	// The identifiers are composed from a class and a tag rather than allotted,
	// so each constant can be checked against what it is meant to be.
	cases := []struct {
		name  string
		named Attribute
		class uint16
		tag   uint16
	}{
		{"SCARD_ATTR_VENDOR_NAME", AttrVendorName, ClassVendorInfo, 0x0100},
		{"SCARD_ATTR_VENDOR_IFD_TYPE", AttrVendorIFDType, ClassVendorInfo, 0x0101},
		{"SCARD_ATTR_VENDOR_IFD_VERSION", AttrVendorIFDVersion, ClassVendorInfo, 0x0102},
		{"SCARD_ATTR_VENDOR_IFD_SERIAL_NO", AttrVendorIFDSerialNo, ClassVendorInfo, 0x0103},
		{"SCARD_ATTR_CHANNEL_ID", AttrChannelID, ClassCommunications, 0x0110},
		{"SCARD_ATTR_ASYNC_PROTOCOL_TYPES", AttrAsyncProtocolTypes, ClassProtocol, 0x0120},
		{"SCARD_ATTR_MAX_IFSD", AttrMaxIFSD, ClassProtocol, 0x0125},
		{"SCARD_ATTR_SYNC_PROTOCOL_TYPES", AttrSyncProtocolTypes, ClassProtocol, 0x0126},
		{"SCARD_ATTR_POWER_MGMT_SUPPORT", AttrPowerMgmtSupport, ClassPowerMgmt, 0x0131},
		{"SCARD_ATTR_CHARACTERISTICS", AttrCharacteristics, ClassMechanical, 0x0150},
		{"SCARD_ATTR_MAXINPUT", AttrMaxInput, ClassVendorDefined, 0xA007},
		{"SCARD_ATTR_CURRENT_PROTOCOL_TYPE", AttrCurrentProtocol, ClassIFDProtocol, 0x0201},
		{"SCARD_ATTR_CURRENT_IFSC", AttrCurrentIFSC, ClassIFDProtocol, 0x0207},
		{"SCARD_ATTR_CURRENT_IFSD", AttrCurrentIFSD, ClassIFDProtocol, 0x0208},
		{"SCARD_ATTR_ICC_PRESENCE", AttrICCPresence, ClassICCState, 0x0300},
		{"SCARD_ATTR_ICC_INTERFACE_STATUS", AttrICCInterfaceState, ClassICCState, 0x0301},
		{"SCARD_ATTR_CURRENT_IO_STATE", AttrCurrentIOState, ClassICCState, 0x0302},
		{"SCARD_ATTR_ATR_STRING", AttrATRString, ClassICCState, 0x0303},
		{"SCARD_ATTR_ICC_TYPE_PER_ATR", AttrICCTypePerATR, ClassICCState, 0x0304},
		{"SCARD_ATTR_DEVICE_UNIT", AttrDeviceUnit, ClassSystem, 0x0001},
		{"SCARD_ATTR_DEVICE_IN_USE", AttrDeviceInUse, ClassSystem, 0x0002},
		{"SCARD_ATTR_DEVICE_FRIENDLY_NAME_A", AttrDeviceFriendlyA, ClassSystem, 0x0003},
		{"SCARD_ATTR_DEVICE_SYSTEM_NAME_A", AttrDeviceSystemA, ClassSystem, 0x0004},
		{"SCARD_ATTR_DEVICE_FRIENDLY_NAME_W", AttrDeviceFriendlyW, ClassSystem, 0x0005},
		{"SCARD_ATTR_DEVICE_SYSTEM_NAME_W", AttrDeviceSystemW, ClassSystem, 0x0006},
	}
	for _, one := range cases {
		if composed := AttributeValue(one.class, one.tag); one.named != composed {
			t.Errorf("%s is 0x%08X, want 0x%08X", one.name, uint32(one.named), uint32(composed))
		}
	}
}

func TestCodeOfAndCodeName(t *testing.T) {
	if code := CodeOf(nil); code != Success {
		t.Errorf("a nil error is %v, want %v", code, Success)
	}
	if code := CodeOf(ErrSharingViolation); code != ErrSharingViolation {
		t.Errorf("a code is %v, want %v", code, ErrSharingViolation)
	}
	if code := CodeOf(errors.New("something else")); code != ErrInternalError {
		t.Errorf("a plain error is %v, want %v", code, ErrInternalError)
	}
	if name := ErrRemovedCard.String(); name != "SCARD_W_REMOVED_CARD" {
		t.Errorf("named %q, want SCARD_W_REMOVED_CARD", name)
	}
}
