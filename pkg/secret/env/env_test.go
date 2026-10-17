package env_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/env"
)

const envPrefix = "SECRET_TEST_"

func TestReaderGet(t *testing.T) {
	tests := []struct {
		name        string
		envVariable string
		envValue    string
		secretName  string
		want        []byte
		wantErr     error
	}{
		{name: "переменная задана", envVariable: envPrefix + "API_HASH", envValue: "abc", secretName: "api_hash", want: []byte("abc")},
		{name: "имя с цифрой", envVariable: envPrefix + "API_ID2", envValue: "123", secretName: "api_id2", want: []byte("123")},
		{name: "переменная пустая", envVariable: envPrefix + "API_HASH", envValue: "", secretName: "api_hash", wantErr: secret.ErrNotFound},
		{name: "задана только чужая переменная", envVariable: envPrefix + "OTHER", envValue: "x", secretName: "api_hash", wantErr: secret.ErrNotFound},
		{name: "имя в верхнем регистре", envVariable: envPrefix + "API_HASH", envValue: "abc", secretName: "API_HASH", wantErr: secret.ErrInvalidName},
		{name: "имя с дефисом", envVariable: envPrefix + "API_HASH", envValue: "abc", secretName: "api-hash", wantErr: secret.ErrInvalidName},
		{name: "пустое имя", envVariable: envPrefix + "API_HASH", envValue: "abc", secretName: "", wantErr: secret.ErrInvalidName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.envVariable, tt.envValue)

			got, err := env.New(envPrefix).Get(tt.secretName)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
