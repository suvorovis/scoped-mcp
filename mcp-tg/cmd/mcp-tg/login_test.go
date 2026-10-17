package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/file"
	"scoped-mcp/pkg/secret/keychain"
	"scoped-mcp/pkg/secret/mocks"
)

const keychainPath = "/kc/scoped-mcp.keychain-db"

func codeWithEmptySentCode(a terminalAuthenticator, ctx context.Context) (string, error) {
	return a.Code(ctx, &tg.AuthSentCode{})
}

func askAPIID(a terminalAuthenticator, _ context.Context) (string, error) {
	return a.apiID()
}

func askAPIHash(a terminalAuthenticator, _ context.Context) (string, error) {
	return a.apiHash()
}

func TestTerminalAuthenticatorPrompts(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		ask        func(a terminalAuthenticator, ctx context.Context) (string, error)
		want       string
		wantPrompt string
		wantErr    error
	}{
		{name: "телефон", input: "+79990000000\n", ask: terminalAuthenticator.Phone, want: "+79990000000", wantPrompt: "Phone (international format): "},
		{name: "код с пробелами", input: "  12345 \n", ask: codeWithEmptySentCode, want: "12345", wantPrompt: "Code: "},
		{name: "пароль", input: "s3cret\n", ask: terminalAuthenticator.Password, want: "s3cret", wantPrompt: "2FA password: "},
		{name: "api_id читается видимой строкой", input: "12345\n", ask: askAPIID, want: "12345", wantPrompt: "api_id: "},
		{name: "api_hash без терминала читается видимой строкой", input: "abc\n", ask: askAPIHash, want: "abc", wantPrompt: "api_hash: "},
		{name: "ввод без перевода строки (EOF)", input: "12345", ask: codeWithEmptySentCode, want: "12345", wantPrompt: "Code: "},
		{name: "пустая строка", input: "\n", ask: terminalAuthenticator.Phone, wantPrompt: "Phone (international format): ", wantErr: errEmptyInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			authenticator := terminalAuthenticator{in: bufio.NewReader(strings.NewReader(tt.input)), out: &out}

			got, err := tt.ask(authenticator, t.Context())

			require.Equal(t, tt.wantPrompt, out.String())
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTerminalAuthenticatorSecretInput(t *testing.T) {
	errTerminal := errors.New("terminal")
	tests := []struct {
		name             string
		enteredSecret    string
		enteredSecretErr error
		ask              func(a terminalAuthenticator, ctx context.Context) (string, error)
		want             string
		wantPrompt       string
		wantErr          error
	}{
		{name: "пароль с пробелами обрезается", enteredSecret: " s3cret ", ask: terminalAuthenticator.Password, want: "s3cret", wantPrompt: "2FA password: \n"},
		{name: "пустой пароль → ошибка пустого ввода", enteredSecret: "", ask: terminalAuthenticator.Password, wantPrompt: "2FA password: \n", wantErr: errEmptyInput},
		{name: "ошибка терминала пробрасывается", enteredSecretErr: errTerminal, ask: terminalAuthenticator.Password, wantPrompt: "2FA password: \n", wantErr: errTerminal},
		{name: "api_hash в терминале читается без эха", enteredSecret: "abc", ask: askAPIHash, want: "abc", wantPrompt: "api_hash: \n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			authenticator := terminalAuthenticator{
				in:  bufio.NewReader(strings.NewReader("visible-line-must-not-be-read\n")),
				out: &out,
				readSecret: func() ([]byte, error) {
					return []byte(tt.enteredSecret), tt.enteredSecretErr
				},
			}

			got, err := tt.ask(authenticator, t.Context())

			require.Equal(t, tt.wantPrompt, out.String())
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSecretReaderIfTerminalOutsideTerminal(t *testing.T) {
	regularFile, err := os.CreateTemp(t.TempDir(), "stdin")
	require.NoError(t, err)
	t.Cleanup(func() { _ = regularFile.Close() })
	tests := []struct {
		name  string
		stdin io.Reader
	}{
		{name: "поток не из файла", stdin: strings.NewReader("s3cret\n")},
		{name: "обычный файл", stdin: regularFile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Nil(t, secretReaderIfTerminal(tt.stdin))
		})
	}
}

func TestTerminalAuthenticatorRefusesSignUp(t *testing.T) {
	authenticator := terminalAuthenticator{in: bufio.NewReader(strings.NewReader("")), out: &bytes.Buffer{}}

	_, err := authenticator.SignUp(t.Context())
	require.ErrorIs(t, err, errSignUpUnsupported)

	err = authenticator.AcceptTermsOfService(t.Context(), tg.HelpTermsOfService{})
	require.ErrorIs(t, err, errSignUpUnsupported)
}

func TestPromptMissingKeys(t *testing.T) {
	errSet := errors.New("set")
	tests := []struct {
		name            string
		input           string
		apiIDErr        error
		apiHashErr      error
		setErr          error
		wantAPIHashRead bool
		wantSet         map[string]string
		wantPrompts     string
		wantErr         error
	}{
		{name: "обоих ключей нет → спрашиваются и записываются оба", input: "12345\nabc\n", apiIDErr: secret.ErrNotFound, apiHashErr: secret.ErrNotFound, wantAPIHashRead: true, wantSet: map[string]string{"api_id": "12345", "api_hash": "abc"}, wantPrompts: "api_id: api_hash: "},
		{name: "нет только api_hash → спрашивается только он", input: "abc\n", apiHashErr: secret.ErrNotFound, wantAPIHashRead: true, wantSet: map[string]string{"api_hash": "abc"}, wantPrompts: "api_hash: "},
		{name: "оба есть → без вопросов, ввод не читается", input: "must-not-be-read\n", wantAPIHashRead: true},
		{name: "ошибка прав при чтении api_id пробрасывается, api_hash не читается", apiIDErr: file.ErrPermissions, wantErr: file.ErrPermissions},
		{name: "отказ связки на api_id пробрасывается, api_hash не читается", apiIDErr: keychain.ErrAccessDenied, wantErr: keychain.ErrAccessDenied},
		{name: "пустой ввод api_id → ошибка без записи", input: "\n", apiIDErr: secret.ErrNotFound, wantPrompts: "api_id: ", wantErr: errEmptyInput},
		{name: "нечисловой api_id → ошибка без записи", input: "12a45\n", apiIDErr: secret.ErrNotFound, wantPrompts: "api_id: ", wantErr: config.ErrAPIIDInvalid},
		{name: "ошибка записи api_id пробрасывается", input: "12345\n", apiIDErr: secret.ErrNotFound, setErr: errSet, wantSet: map[string]string{"api_id": "12345"}, wantPrompts: "api_id: ", wantErr: errSet},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyStore := mocks.NewMockStore(gomock.NewController(t))
			keyStore.EXPECT().Get("api_id").Return([]byte("1"), tt.apiIDErr)
			apiHashReads := 0
			if tt.wantAPIHashRead {
				apiHashReads = 1
			}
			keyStore.EXPECT().Get("api_hash").Return([]byte("h"), tt.apiHashErr).Times(apiHashReads)
			for name, value := range tt.wantSet {
				keyStore.EXPECT().Set(name, []byte(value)).Return(tt.setErr)
			}
			var out bytes.Buffer
			prompter := terminalAuthenticator{in: bufio.NewReader(strings.NewReader(tt.input)), out: &out}

			err := promptMissingKeys(keyStore, prompter)

			require.Equal(t, tt.wantPrompts, out.String())
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestEnsureKeychain(t *testing.T) {
	creatingNotice := "Creating keychain " + keychainPath + ": choose its password in the macOS dialog\n"
	errCreate := errors.New("create")
	tests := []struct {
		name       string
		getErr     error
		createErr  error
		wantCreate bool
		wantOut    string
		wantErr    error
	}{
		{name: "связки нет → создаётся с подсказкой про диалог", getErr: keychain.ErrNoKeychain, wantCreate: true, wantOut: creatingNotice},
		{name: "связка и ключ есть → не создаётся"},
		{name: "связка есть без ключа → не создаётся, ключ спросит promptMissingKeys", getErr: secret.ErrNotFound},
		{name: "доступ отклонён → не создаётся, ошибку покажет чтение ключей", getErr: keychain.ErrAccessDenied},
		{name: "ошибка создания пробрасывается", getErr: keychain.ErrNoKeychain, createErr: errCreate, wantCreate: true, wantOut: creatingNotice, wantErr: errCreate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyStore := mocks.NewMockStore(gomock.NewController(t))
			keyStore.EXPECT().Get("api_id").Return([]byte("1"), tt.getErr)
			var createdPath string
			createKeychain := func(path string) error {
				createdPath = path
				return tt.createErr
			}
			var out bytes.Buffer

			err := ensureKeychain(keychainPath, keyStore, createKeychain, &out)

			require.Equal(t, tt.wantOut, out.String())
			if tt.wantCreate {
				require.Equal(t, keychainPath, createdPath)
			} else {
				require.Empty(t, createdPath)
			}
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestProvisionKeys(t *testing.T) {
	tests := []struct {
		name        string
		secretsMode config.SecretsMode
		withWriter  bool
		apiIDReads  int
		wantPrompts string
	}{
		{name: "env без записи ключей → ничего не спрашивается", secretsMode: config.SecretsModeEnv},
		{name: "env с записью ключей → хранилище не трогается, ничего не спрашивается", secretsMode: config.SecretsModeEnv, withWriter: true},
		{name: "file → дозапрос без проверки связки", secretsMode: config.SecretsModeFile, withWriter: true, apiIDReads: 1, wantPrompts: "api_id: "},
		{name: "keychain со связкой → проверка связки, затем дозапрос", secretsMode: config.SecretsModeKeychain, withWriter: true, apiIDReads: 2, wantPrompts: "api_id: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stores := config.SecretStores{}
			if tt.withWriter {
				keyStore := mocks.NewMockStore(gomock.NewController(t))
				if tt.apiIDReads > 0 {
					keyStore.EXPECT().Get("api_id").Return(nil, secret.ErrNotFound).Times(tt.apiIDReads)
					keyStore.EXPECT().Get("api_hash").Return([]byte("h"), nil)
					keyStore.EXPECT().Set("api_id", []byte("12345")).Return(nil)
				}
				stores.KeyWriter = keyStore
			}
			var out bytes.Buffer
			prompter := terminalAuthenticator{in: bufio.NewReader(strings.NewReader("12345\n")), out: &out}
			createKeychain := func(string) error {
				require.Fail(t, "keychain must not be created")
				return nil
			}

			err := provisionKeys(config.Config{SecretsMode: tt.secretsMode, KeychainPath: keychainPath}, stores, prompter, createKeychain, &out)

			require.NoError(t, err)
			require.Equal(t, tt.wantPrompts, out.String())
		})
	}
}
