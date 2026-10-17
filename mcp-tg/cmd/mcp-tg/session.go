package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/session"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/pkg/secret"
)

var errSessionMissing = errors.New("session not found, run `mcp-tg login`")

type sessionStorage struct {
	secretStore secret.Store
	secretName  string
}

func newSessionStorage(stores config.SecretStores) sessionStorage {
	return sessionStorage{secretStore: stores.SessionStore, secretName: stores.SessionName}
}

func (storage sessionStorage) LoadSession(context.Context) ([]byte, error) {
	data, err := storage.secretStore.Get(storage.secretName)
	if errors.Is(err, secret.ErrNotFound) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	return data, nil
}

func (storage sessionStorage) StoreSession(_ context.Context, data []byte) error {
	if err := storage.secretStore.Set(storage.secretName, data); err != nil {
		return fmt.Errorf("store session: %w", err)
	}
	return nil
}

func requireSession(ctx context.Context, clientSession session.Storage) error {
	_, err := clientSession.LoadSession(ctx)
	if errors.Is(err, session.ErrNotFound) {
		return errSessionMissing
	}
	return err
}
