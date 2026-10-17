package main

import (
	"errors"
	"testing"

	"github.com/gotd/td/session"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/file"
	"scoped-mcp/pkg/secret/keychain"
	"scoped-mcp/pkg/secret/mocks"
)

func TestSessionStorageLoad(t *testing.T) {
	tests := []struct {
		name          string
		storedSession []byte
		getErr        error
		wantErr       error
	}{
		{name: "сессия есть → данные возвращаются", storedSession: []byte(`{"dc":2}`)},
		{name: "нет сессии → gotd ErrNotFound", getErr: secret.ErrNotFound, wantErr: session.ErrNotFound},
		{name: "ошибка прав файла → пробрасывается как есть", getErr: file.ErrPermissions, wantErr: file.ErrPermissions},
		{name: "отказ связки ключей → пробрасывается как есть", getErr: keychain.ErrAccessDenied, wantErr: keychain.ErrAccessDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretStore := mocks.NewMockStore(gomock.NewController(t))
			secretStore.EXPECT().Get("session").Return(tt.storedSession, tt.getErr)
			stores := config.SecretStores{SessionStore: secretStore, SessionName: "session"}

			got, err := newSessionStorage(stores).LoadSession(t.Context())

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.storedSession, got)
		})
	}
}

func TestSessionStorageStore(t *testing.T) {
	errSet := errors.New("set")
	tests := []struct {
		name    string
		setErr  error
		wantErr error
	}{
		{name: "запись удалась → без ошибки"},
		{name: "ошибка хранилища → пробрасывается как есть", setErr: errSet, wantErr: errSet},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretStore := mocks.NewMockStore(gomock.NewController(t))
			secretStore.EXPECT().Set("session", []byte(`{"dc":2}`)).Return(tt.setErr)
			stores := config.SecretStores{SessionStore: secretStore, SessionName: "session"}

			err := newSessionStorage(stores).StoreSession(t.Context(), []byte(`{"dc":2}`))

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestRequireSession(t *testing.T) {
	anySession := []byte("{}")
	tests := []struct {
		name    string
		getErr  error
		wantErr error
	}{
		{name: "сессия есть → проверка пройдена"},
		{name: "нет сессии → подсказка login", getErr: secret.ErrNotFound, wantErr: errSessionMissing},
		{name: "права файла → ошибка прав пробрасывается", getErr: file.ErrPermissions, wantErr: file.ErrPermissions},
		{name: "нет связки → ErrNoKeychain пробрасывается", getErr: keychain.ErrNoKeychain, wantErr: keychain.ErrNoKeychain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretStore := mocks.NewMockStore(gomock.NewController(t))
			secretStore.EXPECT().Get("session").Return(anySession, tt.getErr)
			stores := config.SecretStores{SessionStore: secretStore, SessionName: "session"}

			err := requireSession(t.Context(), newSessionStorage(stores))

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
