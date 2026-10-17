package keychain

import (
	"testing"

	"github.com/stretchr/testify/require"

	"scoped-mcp/pkg/secret"
)

func TestErrorFromStatus(t *testing.T) {
	describe := func(int32) string { return "described" }
	tests := []struct {
		name    string
		status  int32
		wantErr error
	}{
		{name: "успех", status: errSecSuccess},
		{name: "errSecItemNotFound", status: errSecItemNotFound, wantErr: secret.ErrNotFound},
		{name: "errSecNoSuchKeychain", status: errSecNoSuchKeychain, wantErr: ErrNoKeychain},
		{name: "errSecUserCanceled", status: errSecUserCanceled, wantErr: ErrAccessDenied},
		{name: "errSecAuthFailed", status: errSecAuthFailed, wantErr: ErrAccessDenied},
		{name: "errSecInteractionNotAllowed", status: errSecInteractionNotAllowed, wantErr: ErrInteractionNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errorFromStatus(tt.status, describe)

			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestErrorFromStatusUnmappedCode(t *testing.T) {
	status := errSecDuplicateItem

	err := errorFromStatus(status, func(int32) string { return "The specified item already exists in the keychain." })

	var statusErr *StatusError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, &StatusError{Code: status, Message: "The specified item already exists in the keychain."}, statusErr)
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name       string
		secretName string
		wantErr    error
	}{
		{name: "обычное имя", secretName: "session"},
		{name: "имя с путём допустимо: это не файл", secretName: "../x"},
		{name: "пустое", secretName: "", wantErr: secret.ErrInvalidName},
		{name: "нулевой байт", secretName: "a\x00b", wantErr: secret.ErrInvalidName},
		{name: "невалидный UTF-8", secretName: "a\xffb", wantErr: secret.ErrInvalidName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateName(tt.secretName)

			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
