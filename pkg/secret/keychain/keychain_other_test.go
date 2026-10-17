//go:build !darwin

package keychain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnsupported(t *testing.T) {
	require.False(t, Supported())
	require.ErrorIs(t, Create("/tmp/x.keychain-db"), ErrUnsupported)

	secretStore := New("/tmp/x.keychain-db", "mcp-tg")

	_, err := secretStore.Get("session")
	require.ErrorIs(t, err, ErrUnsupported)
	require.ErrorIs(t, secretStore.Set("session", []byte("v")), ErrUnsupported)
	require.ErrorIs(t, secretStore.Delete("session"), ErrUnsupported)
}
