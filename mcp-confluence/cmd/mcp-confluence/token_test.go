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
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-confluence/config"
	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/keychain"
	secretmocks "scoped-mcp/pkg/secret/mocks"
)

const (
	keychainPath           = "/kc/scoped-mcp.keychain-db"
	creatingKeychainNotice = "Creating keychain " + keychainPath + ": choose its password in the macOS dialog\n"
	cancelDuringReadDelay  = 20 * time.Millisecond
	unansweredInputLimit   = 5 * time.Second
)

func TestTokenPrompterPrompts(t *testing.T) {
	errTerminal := errors.New("terminal")
	tests := []struct {
		name             string
		input            string
		inTerminal       bool
		disableEchoErr   error
		cancelBeforeRead bool
		cancelDuringRead bool
		wantToken        string
		wantPrompt       string
		wantEchoDisabled bool
		wantErr          error
	}{
		{name: "без терминала читается видимой строкой", input: "pat-secret\n", wantToken: "pat-secret", wantPrompt: tokenPromptLabel},
		{name: "ввод без перевода строки (EOF)", input: "pat-secret", wantToken: "pat-secret", wantPrompt: tokenPromptLabel},
		{name: "пробелы обрезаются", input: "  pat-secret \n", wantToken: "pat-secret", wantPrompt: tokenPromptLabel},
		{name: "пустая строка", input: "\n", wantPrompt: tokenPromptLabel, wantErr: errEmptyInput},
		{name: "в терминале читается при выключенном эхе", input: "pat-secret\n", inTerminal: true, wantToken: "pat-secret", wantPrompt: tokenPromptLabel + "\n", wantEchoDisabled: true},
		{name: "в терминале пустой ввод", input: " \n", inTerminal: true, wantPrompt: tokenPromptLabel + "\n", wantEchoDisabled: true, wantErr: errEmptyInput},
		{name: "эхо не выключается → ввод не читается", input: "pat-secret\n", inTerminal: true, disableEchoErr: errTerminal, wantPrompt: tokenPromptLabel, wantErr: errTerminal},
		{name: "ctx отменён до чтения → ни приглашения, ни выключения эха", input: "pat-secret\n", inTerminal: true, cancelBeforeRead: true, wantErr: context.Canceled},
		{name: "Ctrl+C во время ввода в терминале → эхо включено, строка закрыта", inTerminal: true, cancelDuringRead: true, wantPrompt: tokenPromptLabel + "\n", wantEchoDisabled: true, wantErr: context.Canceled},
		{name: "Ctrl+C во время видимого ввода → строка закрыта", cancelDuringRead: true, wantPrompt: tokenPromptLabel + "\n", wantErr: context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var input io.Reader = strings.NewReader(tt.input)
			if tt.cancelDuringRead {
				unansweredInput, inputWriter := io.Pipe()
				time.AfterFunc(cancelDuringReadDelay, cancel)
				time.AfterFunc(unansweredInputLimit, func() { _ = inputWriter.Close() })
				input = unansweredInput
			}
			if tt.cancelBeforeRead {
				cancel()
			}
			var out bytes.Buffer
			prompter := tokenPrompter{in: bufio.NewReader(input), out: &out}
			echoOn, echoWasDisabled := true, false
			if tt.inTerminal {
				prompter.disableEcho = func() (func() error, error) {
					if tt.disableEchoErr != nil {
						return nil, tt.disableEchoErr
					}
					echoOn, echoWasDisabled = false, true
					return func() error {
						echoOn = true
						return nil
					}, nil
				}
			}

			gotToken, err := prompter.token(ctx)

			require.Equal(t, tt.wantPrompt, out.String())
			require.Equal(t, tt.wantEchoDisabled, echoWasDisabled)
			require.True(t, echoOn)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantToken, gotToken)
		})
	}
}

func TestDisableEchoIfTerminalOutsideTerminal(t *testing.T) {
	regularFile, err := os.CreateTemp(t.TempDir(), "stdin")
	require.NoError(t, err)
	t.Cleanup(func() { _ = regularFile.Close() })
	tests := []struct {
		name  string
		stdin io.Reader
	}{
		{name: "поток не из файла", stdin: strings.NewReader("pat\n")},
		{name: "обычный файл", stdin: regularFile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Nil(t, disableEchoIfTerminal(tt.stdin))
		})
	}
}

func TestEnsureKeychain(t *testing.T) {
	errCreate := errors.New("create")
	tests := []struct {
		name       string
		probeErr   error
		createErr  error
		wantCreate bool
		wantOut    string
		wantErr    error
	}{
		{name: "связки нет → создаётся с подсказкой про диалог", probeErr: keychain.ErrNoKeychain, wantCreate: true, wantOut: creatingKeychainNotice},
		{name: "связка и токен есть → не создаётся"},
		{name: "связка есть без токена → не создаётся", probeErr: secret.ErrNotFound},
		{name: "доступ отклонён → не создаётся, ошибку покажет запись", probeErr: keychain.ErrAccessDenied},
		{name: "ошибка создания пробрасывается", probeErr: keychain.ErrNoKeychain, createErr: errCreate, wantCreate: true, wantOut: creatingKeychainNotice, wantErr: errCreate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writableStore := secretmocks.NewMockStore(gomock.NewController(t))
			writableStore.EXPECT().Get(config.TokenSecretName).Return([]byte("t"), tt.probeErr)
			var createdPath string
			createKeychain := func(path string) error {
				createdPath = path
				return tt.createErr
			}
			var out bytes.Buffer

			err := ensureKeychain(keychainPath, writableStore, createKeychain, &out)

			require.Equal(t, tt.wantOut, out.String())
			requireKeychainCreated(t, tt.wantCreate, createdPath)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func requireKeychainCreated(t *testing.T, wantCreate bool, createdPath string) {
	t.Helper()
	if wantCreate {
		require.Equal(t, keychainPath, createdPath)
		return
	}
	require.Empty(t, createdPath)
}

func TestStoreToken(t *testing.T) {
	errSet := errors.New("set")
	savedNotice := "Token saved to /secrets/token\n"
	tests := []struct {
		name        string
		secretsMode config.SecretsMode
		input       string
		probeErr    error
		setErr      error
		wantProbe   bool
		wantSet     bool
		wantCreate  bool
		wantOut     string
		wantErr     error
	}{
		{name: "env → ошибка без вопросов и записи", secretsMode: config.SecretsModeEnv, input: "pat\n", wantErr: errTokenModeEnv},
		{name: "file → спрашивает и пишет", secretsMode: config.SecretsModeFile, input: "pat\n", wantSet: true, wantOut: tokenPromptLabel + savedNotice},
		{name: "file: пустой ввод → без записи", secretsMode: config.SecretsModeFile, input: "\n", wantOut: tokenPromptLabel, wantErr: errEmptyInput},
		{name: "file: ошибка записи пробрасывается", secretsMode: config.SecretsModeFile, input: "pat\n", setErr: errSet, wantSet: true, wantOut: tokenPromptLabel, wantErr: errSet},
		{name: "keychain есть → пишет без создания", secretsMode: config.SecretsModeKeychain, input: "pat\n", wantProbe: true, wantSet: true, wantOut: tokenPromptLabel + savedNotice},
		{name: "keychain нет → создаёт и пишет", secretsMode: config.SecretsModeKeychain, input: "pat\n", probeErr: keychain.ErrNoKeychain, wantProbe: true, wantCreate: true, wantSet: true, wantOut: creatingKeychainNotice + tokenPromptLabel + savedNotice},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writableStore := secretmocks.NewMockStore(gomock.NewController(t))
			if tt.wantProbe {
				writableStore.EXPECT().Get(config.TokenSecretName).Return([]byte("t"), tt.probeErr)
			}
			if tt.wantSet {
				writableStore.EXPECT().Set(config.TokenSecretName, []byte("pat")).Return(tt.setErr)
			}
			var createdPath string
			createKeychain := func(path string) error {
				createdPath = path
				return nil
			}
			var out bytes.Buffer
			prompter := tokenPrompter{in: bufio.NewReader(strings.NewReader(tt.input)), out: &out}
			cfg := config.Config{SecretsMode: tt.secretsMode, KeychainPath: keychainPath}
			tokenStore := config.TokenStore{Reader: writableStore, WritableStore: writableStore, Location: "/secrets/token"}
			if tt.secretsMode == config.SecretsModeEnv {
				tokenStore.WritableStore = nil
			}

			err := storeToken(t.Context(), cfg, tokenStore, prompter, createKeychain, &out)

			require.Equal(t, tt.wantOut, out.String())
			requireKeychainCreated(t, tt.wantCreate, createdPath)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
