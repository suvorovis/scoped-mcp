//go:build darwin

package keychain

import (
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"scoped-mcp/pkg/secret"
)

const (
	testService       = "mcp-tg-test"
	testPassword      = "test-password"
	sessionSecretName = "session"
)

func TestMain(m *testing.M) {
	if err := setUserInteractionAllowed(false); err != nil {
		log.Fatal(err)
	}
	os.Exit(m.Run())
}

func newTestKeychain(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.keychain-db")
	require.NoError(t, createWithPassword(path, testPassword))
	return path
}

func TestStore(t *testing.T) {
	tests := []struct {
		name    string
		run     func(secretStore secret.Store) ([]byte, error)
		want    []byte
		wantErr error
	}{
		{
			name: "запись и чтение",
			run: func(secretStore secret.Store) ([]byte, error) {
				if err := secretStore.Set(sessionSecretName, []byte("v1")); err != nil {
					return nil, err
				}
				return secretStore.Get(sessionSecretName)
			},
			want: []byte("v1"),
		},
		{
			name: "перезапись через SecItemUpdate",
			run: func(secretStore secret.Store) ([]byte, error) {
				if err := secretStore.Set(sessionSecretName, []byte("v1")); err != nil {
					return nil, err
				}
				if err := secretStore.Set(sessionSecretName, []byte("v2 longer value")); err != nil {
					return nil, err
				}
				return secretStore.Get(sessionSecretName)
			},
			want: []byte("v2 longer value"),
		},
		{
			name: "бинарное значение с нулевым байтом",
			run: func(secretStore secret.Store) ([]byte, error) {
				if err := secretStore.Set(sessionSecretName, []byte{0, 1, 2, 0}); err != nil {
					return nil, err
				}
				return secretStore.Get(sessionSecretName)
			},
			want: []byte{0, 1, 2, 0},
		},
		{
			name: "разные имена не пересекаются",
			run: func(secretStore secret.Store) ([]byte, error) {
				if err := secretStore.Set("api_id", []byte("1")); err != nil {
					return nil, err
				}
				if err := secretStore.Set("api_hash", []byte("2")); err != nil {
					return nil, err
				}
				return secretStore.Get("api_id")
			},
			want: []byte("1"),
		},
		{
			name: "удаление",
			run: func(secretStore secret.Store) ([]byte, error) {
				if err := secretStore.Set(sessionSecretName, []byte("v1")); err != nil {
					return nil, err
				}
				if err := secretStore.Delete(sessionSecretName); err != nil {
					return nil, err
				}
				return secretStore.Get(sessionSecretName)
			},
			wantErr: secret.ErrNotFound,
		},
		{
			name:    "удаление отсутствующего",
			run:     func(secretStore secret.Store) ([]byte, error) { return nil, secretStore.Delete(sessionSecretName) },
			wantErr: secret.ErrNotFound,
		},
		{
			name:    "нет элемента",
			run:     func(secretStore secret.Store) ([]byte, error) { return secretStore.Get(sessionSecretName) },
			wantErr: secret.ErrNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretStore := New(newTestKeychain(t), testService)

			got, err := tt.run(secretStore)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestStoreMissingKeychain(t *testing.T) {
	secretStore := New(filepath.Join(t.TempDir(), "missing.keychain-db"), testService)

	_, err := secretStore.Get(sessionSecretName)
	require.ErrorIs(t, err, ErrNoKeychain)
	require.ErrorIs(t, secretStore.Set(sessionSecretName, []byte("v1")), ErrNoKeychain)
	require.ErrorIs(t, secretStore.Delete(sessionSecretName), ErrNoKeychain)
}

func TestStoreLockedKeychainWithoutDialogs(t *testing.T) {
	path := newTestKeychain(t)
	secretStore := New(path, testService)
	require.NoError(t, secretStore.Set(sessionSecretName, []byte("v1")))

	require.NoError(t, lockKeychain(path))

	_, err := secretStore.Get(sessionSecretName)
	require.ErrorIs(t, err, ErrAccessDenied)
}

func TestStoreRejectsInvalidNames(t *testing.T) {
	tests := []struct {
		name        string
		invalidName string
	}{
		{name: "пустое", invalidName: ""},
		{name: "нулевой байт", invalidName: "a\x00b"},
		{name: "невалидный UTF-8", invalidName: "a\xffb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretStore := New(filepath.Join(t.TempDir(), "missing.keychain-db"), testService)

			_, err := secretStore.Get(tt.invalidName)
			require.ErrorIs(t, err, secret.ErrInvalidName)
			require.ErrorIs(t, secretStore.Set(tt.invalidName, []byte("v")), secret.ErrInvalidName)
			require.ErrorIs(t, secretStore.Delete(tt.invalidName), secret.ErrInvalidName)
		})
	}
}

func TestCreateKeepsUserSearchList(t *testing.T) {
	before, err := searchListCount()
	require.NoError(t, err)

	path := newTestKeychain(t)

	after, err := searchListCount()
	require.NoError(t, err)
	require.Equal(t, before, after)
	_, err = os.Stat(path)
	require.NoError(t, err)
}

func TestStoreSurvivesRepeatedOperations(t *testing.T) {
	secretStore := New(newTestKeychain(t), testService)

	for i := range 50 {
		value := []byte{byte(i)}
		require.NoError(t, secretStore.Set(sessionSecretName, value))
		got, err := secretStore.Get(sessionSecretName)
		require.NoError(t, err)
		require.Equal(t, value, got)
	}
	require.NoError(t, secretStore.Delete(sessionSecretName))
}

func TestSupported(t *testing.T) {
	require.True(t, Supported())
}
