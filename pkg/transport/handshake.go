package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

type settlementHandshake func(tm *Manager, tr Transport) (*Entry, error)

func (handshake settlementHandshake) Do(tm *Manager, tr Transport, timeout time.Duration) (*Entry, error) {
	var entry *Entry
	errCh := make(chan error, 1)
	go func() {
		e, err := handshake(tm, tr)
		entry = e
		errCh <- err
	}()
	select {
	case err := <-errCh:
		return entry, err
	case <-time.After(timeout):
		return nil, errors.New("deadline exceeded")
	}
}

func settlementInitiatorHandshake(public bool) settlementHandshake {
	return func(tm *Manager, tr Transport) (*Entry, error) {
		entry := &Entry{
			ID:       MakeTransportID(tr.Edges()[0], tr.Edges()[1], tr.Type()),
			EdgeKeys: tr.Edges(),
			Type:     tr.Type(),
			Public:   public,
		}

		// sEntry := &SignedEntry{Entry: entry, Signatures: [2]cipher.Sig{entry.Signature(tm.config.SecKey)}}

		sEntry := NewSignedEntry(entry, tm.config.PubKey, tm.config.SecKey)
		if err := validateSignedEntry(sEntry, tr, tm.config.PubKey); err != nil {
			return nil, fmt.Errorf("NewSignedEntry: %s", err)
		}

		if err := json.NewEncoder(tr).Encode(sEntry); err != nil {
			return nil, fmt.Errorf("write: %s", err)
		}

		if err := json.NewDecoder(tr).Decode(sEntry); err != nil {
			return nil, fmt.Errorf("read: %s", err)
		}

		//  Verifying remote signature
		if err := verifySig(sEntry, tm.Remote(tr.Edges())); err != nil {
			return nil, err
		}

		newEntry := tm.walkEntries(func(e *Entry) bool { return *e == *sEntry.Entry }) == nil
		if newEntry {
			tm.addEntry(entry)
		}

		return sEntry.Entry, nil
	}
}

func settlementResponderHandshake(tm *Manager, tr Transport) (*Entry, error) {
	sEntry := &SignedEntry{}
	if err := json.NewDecoder(tr).Decode(sEntry); err != nil {
		return nil, fmt.Errorf("read: %s", err)
	}

	// it must be tm.Local() ?
	if err := validateSignedEntry(sEntry, tr, tm.Remote(tr.Edges())); err != nil {
		return nil, err
	}

	// Write second signature
	// sEntry.Signatures[1] = sEntry.Entry.Signature(tm.config.SecKey)
	sEntry.Sign(tm.Local(), tm.config.SecKey)

	newEntry := tm.walkEntries(func(e *Entry) bool { return *e == *sEntry.Entry }) == nil

	var err error
	if sEntry.Entry.Public {
		if !newEntry {
			_, err = tm.config.DiscoveryClient.UpdateStatuses(context.Background(), &Status{ID: sEntry.Entry.ID, IsUp: true})
		} else {
			err = tm.config.DiscoveryClient.RegisterTransports(context.Background(), sEntry)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("entry set: %s", err)
	}

	if err := json.NewEncoder(tr).Encode(sEntry); err != nil {
		return nil, fmt.Errorf("write: %s", err)
	}

	if newEntry {
		tm.addEntry(sEntry.Entry)
	}

	return sEntry.Entry, nil
}

func validateSignedEntry(sEntry *SignedEntry, tr Transport, pk cipher.PubKey) error {
	entry := sEntry.Entry
	if entry.Type != tr.Type() {
		return errors.New("invalid entry type")
	}

	if entry.Edges() != tr.Edges() {
		return errors.New("invalid entry edges")
	}

	// Weak check here
	if sEntry.Signatures[0].Null() && sEntry.Signatures[1].Null() {
		return errors.New("invalid entry signature")
	}

	return verifySig(sEntry, pk)
}

func verifySig(sEntry *SignedEntry, pk cipher.PubKey) error {
	return cipher.VerifyPubKeySignedPayload(pk, sEntry.Signature(pk), sEntry.Entry.ToBinary())
}
