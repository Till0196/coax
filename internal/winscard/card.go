// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package winscard

// Connect opens a card in a reader, SCardConnect. protocols is the set the
// caller will accept and the return value is the one that was agreed; a
// built-in card interface speaks T=1 only, so asking for T=0 alone gets
// ErrProtoMismatch. The handle is this package's own and deliberately does not
// reveal which provider answered -- the card table is the only mapping.
func (s *Service) Connect(handle ContextHandle, reader string, share ShareMode, protocols Protocol) (CardHandle, Protocol, error) {
	entry, err := s.lookupContext(handle)
	if err != nil {
		return 0, ProtocolUndefined, err
	}
	switch share {
	case ShareExclusive, ShareShared:
	case ShareDirect:
		// A direct connection is to the reader, not a card: the one case where
		// naming no protocol is meaningful.
	default:
		return 0, ProtocolUndefined, ErrInvalidValue
	}
	if share != ShareDirect && protocols == ProtocolUndefined {
		return 0, ProtocolUndefined, ErrInvalidValue
	}
	provider, providerContext, err := entry.locate(reader)
	if err != nil {
		return 0, ProtocolUndefined, err
	}
	card, active, err := providerContext.Connect(reader, share, protocols)
	if err != nil {
		return 0, ProtocolUndefined, err
	}

	s.mutex.Lock()
	s.nextHandle++
	cardHandle := CardHandle(s.nextHandle)
	record := &serviceCard{
		owner:    entry,
		handle:   cardHandle,
		provider: provider,
		reader:   reader,
		card:     card,
	}
	s.cards[cardHandle] = record
	s.mutex.Unlock()

	// The context may have been released while the provider was connecting.
	// Registering first and checking afterwards keeps that race from leaving a
	// card open that no table knows about: either the release finds it here and
	// closes it, or this does.
	entry.mutex.Lock()
	if entry.released {
		entry.mutex.Unlock()
		s.mutex.Lock()
		delete(s.cards, cardHandle)
		s.mutex.Unlock()
		_ = card.Disconnect(LeaveCard)
		return 0, ProtocolUndefined, ErrInvalidHandle
	}
	entry.cards[cardHandle] = record
	entry.mutex.Unlock()
	return cardHandle, active, nil
}

// Reconnect changes the share mode or protocol of an open connection,
// SCardReconnect, applying initialization to the card first. The handle stays
// valid whether or not it succeeds.
func (s *Service) Reconnect(handle CardHandle, share ShareMode, protocols Protocol, initialization Disposition) (Protocol, error) {
	record, err := s.lookupCard(handle)
	if err != nil {
		return ProtocolUndefined, err
	}
	if err := checkDisposition(initialization); err != nil {
		return ProtocolUndefined, err
	}
	return record.card.Reconnect(share, protocols, initialization)
}

// Disconnect closes a connection, SCardDisconnect, applying disposition to the
// card. The handle is invalid afterwards even when the provider reports a
// failure: the connection is gone either way, and keeping the handle usable
// would only let an APDU into a card whose session was already given up.
func (s *Service) Disconnect(handle CardHandle, disposition Disposition) error {
	if err := checkDisposition(disposition); err != nil {
		return err
	}
	s.mutex.Lock()
	record, ok := s.cards[handle]
	if !ok {
		s.mutex.Unlock()
		return ErrInvalidHandle
	}
	delete(s.cards, handle)
	s.mutex.Unlock()

	record.owner.mutex.Lock()
	delete(record.owner.cards, handle)
	record.owner.mutex.Unlock()

	return record.card.Disconnect(disposition)
}

// BeginTransaction claims the card until EndTransaction, SCardBeginTransaction.
// There is one slot per enclosure however many tuners share it, so this is how
// a caller keeps a sequence of APDUs together against every other application
// on the machine.
func (s *Service) BeginTransaction(handle CardHandle) error {
	record, err := s.lookupCard(handle)
	if err != nil {
		return err
	}
	return record.card.BeginTransaction()
}

// EndTransaction releases the claim, SCardEndTransaction, applying disposition
// to the card.
func (s *Service) EndTransaction(handle CardHandle, disposition Disposition) error {
	record, err := s.lookupCard(handle)
	if err != nil {
		return err
	}
	if err := checkDisposition(disposition); err != nil {
		return err
	}
	return record.card.EndTransaction(disposition)
}

// Status reports the state, protocol and ATR of a connected card,
// SCardStatus.
func (s *Service) Status(handle CardHandle) (Status, error) {
	record, err := s.lookupCard(handle)
	if err != nil {
		return Status{}, err
	}
	status, err := record.card.Status()
	if err != nil {
		return Status{}, err
	}
	// Fill in for a provider that does not name the reader: the caller is
	// entitled to get back the name it connected by.
	if len(status.Readers) == 0 {
		status.Readers = []string{record.reader}
	}
	return status, nil
}

// Transmit sends one command APDU and returns the response including its two
// status bytes, SCardTransmit. sendProtocol is the protocol the command is
// framed for, the SCARD_IO_REQUEST a caller would pass on Windows; the
// response's protocol comes back with it.
func (s *Service) Transmit(handle CardHandle, sendProtocol Protocol, send []byte) ([]byte, Protocol, error) {
	record, err := s.lookupCard(handle)
	if err != nil {
		return nil, ProtocolUndefined, err
	}
	if len(send) == 0 {
		return nil, ProtocolUndefined, ErrInvalidParameter
	}
	return record.card.Transmit(send, sendProtocol)
}

// Control sends a reader control code, SCardControl.
func (s *Service) Control(handle CardHandle, code uint32, send []byte) ([]byte, error) {
	record, err := s.lookupCard(handle)
	if err != nil {
		return nil, err
	}
	return record.card.Control(code, send)
}

// GetAttrib reads a reader or card attribute, SCardGetAttrib.
func (s *Service) GetAttrib(handle CardHandle, id Attribute) ([]byte, error) {
	record, err := s.lookupCard(handle)
	if err != nil {
		return nil, err
	}
	return record.card.GetAttrib(id)
}

// SetAttrib writes a reader or card attribute, SCardSetAttrib.
func (s *Service) SetAttrib(handle CardHandle, id Attribute, value []byte) error {
	record, err := s.lookupCard(handle)
	if err != nil {
		return err
	}
	return record.card.SetAttrib(id, value)
}

// checkDisposition rejects a disposition outside the four, before a provider
// has to decide what an unknown one means.
func checkDisposition(disposition Disposition) error {
	switch disposition {
	case LeaveCard, ResetCard, UnpowerCard, EjectCard:
		return nil
	}
	return ErrInvalidValue
}
