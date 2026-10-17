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

	"scoped-mcp/mcp-gitlab/config"
	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/keychain"
	secretmocks "scoped-mcp/pkg/secret/mocks"
)

const (
	keychainPath           = "/kc/scoped-mcp.keychain-db"
	secretsFilePath        = "/secrets/token"
	existingToken          = "existing-pat"
	creatingKeychainNotice = "Creating keychain " + keychainPath + ": choose its password in the macOS dialog\n"
	cancelDuringReadDelay  = 20 * time.Millisecond
	unansweredInputLimit   = 5 * time.Second
)

type cancelMoment int

const (
	noCancel cancelMoment = iota
	cancelBeforeRead
	cancelDuringRead
)

func TestTokenPrompterAsksToken(t *testing.T) {
	errTerminal := errors.New("terminal")
	tests := []struct {
		name               string
		input              string
		inTerminal         bool
		hiddenInputErr     error
		cancel             cancelMoment
		wantToken          string
		wantPrompt         string
		wantInputWasHidden bool
		wantErr            error
	}{
		{name: "без терминала читается видимой строкой", input: "pat-secret\n", wantToken: "pat-secret", wantPrompt: tokenPromptLabel},
		{name: "ввод без перевода строки (EOF)", input: "pat-secret", wantToken: "pat-secret", wantPrompt: tokenPromptLabel},
		{name: "пробелы обрезаются", input: "  pat-secret \n", wantToken: "pat-secret", wantPrompt: tokenPromptLabel},
		{name: "пустая строка", input: "\n", wantPrompt: tokenPromptLabel, wantErr: errEmptyInput},
		{name: "в терминале читается скрытым вводом", input: "pat-secret\n", inTerminal: true, wantToken: "pat-secret", wantPrompt: tokenPromptLabel + "\n", wantInputWasHidden: true},
		{name: "в терминале пустой ввод", input: " \n", inTerminal: true, wantPrompt: tokenPromptLabel + "\n", wantInputWasHidden: true, wantErr: errEmptyInput},
		{name: "скрытый ввод не включается → ввод не читается", input: "pat-secret\n", inTerminal: true, hiddenInputErr: errTerminal, wantPrompt: tokenPromptLabel, wantErr: errTerminal},
		{name: "ctx отменён до чтения → ни приглашения, ни скрытого ввода", input: "pat-secret\n", inTerminal: true, cancel: cancelBeforeRead, wantErr: context.Canceled},
		{name: "Ctrl+C во время ввода в терминале → терминал восстановлен, строка закрыта", inTerminal: true, cancel: cancelDuringRead, wantPrompt: tokenPromptLabel + "\n", wantInputWasHidden: true, wantErr: context.Canceled},
		{name: "Ctrl+C во время видимого ввода → строка закрыта", cancel: cancelDuringRead, wantPrompt: tokenPromptLabel + "\n", wantErr: context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var input io.Reader = strings.NewReader(tt.input)
			switch tt.cancel {
			case noCancel:
			case cancelBeforeRead:
				cancel()
			case cancelDuringRead:
				unansweredInput, inputWriter := io.Pipe()
				time.AfterFunc(cancelDuringReadDelay, cancel)
				time.AfterFunc(unansweredInputLimit, func() { _ = inputWriter.Close() })
				input = unansweredInput
			}
			var out bytes.Buffer
			prompter := tokenPrompter{in: bufio.NewReader(input), out: &out}
			inputHidden, inputWasHidden := false, false
			if tt.inTerminal {
				prompter.enterHiddenInput = func() (func() error, error) {
					if tt.hiddenInputErr != nil {
						return nil, tt.hiddenInputErr
					}
					inputHidden, inputWasHidden = true, true
					return func() error {
						inputHidden = false
						return nil
					}, nil
				}
			}

			gotToken, err := prompter.askToken(ctx)

			require.Equal(t, tt.wantPrompt, out.String())
			require.Equal(t, tt.wantInputWasHidden, inputWasHidden)
			require.False(t, inputHidden)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantToken, gotToken)
		})
	}
}

func TestTerminalHiddenInputOutsideTerminal(t *testing.T) {
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
			require.Nil(t, terminalHiddenInput(tt.stdin))
		})
	}
}

func TestCreateKeychainIfMissing(t *testing.T) {
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
			keychainReader := secretmocks.NewMockReader(gomock.NewController(t))
			keychainReader.EXPECT().Get(config.TokenSecretName).Return([]byte(existingToken), tt.probeErr)
			var createdPath string
			createKeychain := func(path string) error {
				createdPath = path
				return tt.createErr
			}
			var out bytes.Buffer

			err := createKeychainIfMissing(keychainPath, keychainReader, createKeychain, &out)

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
	tests := []struct {
		name        string
		secretsMode config.SecretsMode
		input       string
		probeErr    error
		setErr      error
		wantSet     bool
		wantCreate  bool
		wantOut     string
		wantErr     error
	}{
		{name: "env → ошибка без вопросов и записи", secretsMode: config.SecretsModeEnv, input: "pat\n", wantErr: errTokenModeEnv},
		{name: "file → спрашивает и пишет", secretsMode: config.SecretsModeFile, input: "pat\n", wantSet: true, wantOut: tokenPromptLabel + savedNotice(secretsFilePath)},
		{name: "file: пустой ввод → без записи", secretsMode: config.SecretsModeFile, input: "\n", wantOut: tokenPromptLabel, wantErr: errEmptyInput},
		{name: "file: ошибка записи пробрасывается", secretsMode: config.SecretsModeFile, input: "pat\n", setErr: errSet, wantSet: true, wantOut: tokenPromptLabel, wantErr: errSet},
		{name: "keychain есть → пишет без создания", secretsMode: config.SecretsModeKeychain, input: "pat\n", wantSet: true, wantOut: tokenPromptLabel + savedNotice(keychainPath)},
		{name: "keychain нет → создаёт и пишет", secretsMode: config.SecretsModeKeychain, input: "pat\n", probeErr: keychain.ErrNoKeychain, wantCreate: true, wantSet: true, wantOut: creatingKeychainNotice + tokenPromptLabel + savedNotice(keychainPath)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretStore := secretmocks.NewMockStore(gomock.NewController(t))
			if tt.secretsMode == config.SecretsModeKeychain {
				secretStore.EXPECT().Get(config.TokenSecretName).Return([]byte(existingToken), tt.probeErr)
			}
			if tt.wantSet {
				secretStore.EXPECT().Set(config.TokenSecretName, []byte("pat")).Return(tt.setErr)
			}
			var createdPath string
			createKeychain := func(path string) error {
				createdPath = path
				return nil
			}
			var out bytes.Buffer
			prompter := tokenPrompter{in: bufio.NewReader(strings.NewReader(tt.input)), out: &out}

			err := storeToken(t.Context(), testTokenStore(tt.secretsMode, secretStore), prompter, createKeychain)

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

func savedNotice(location string) string {
	return "Token saved to " + location + "\n"
}

func testTokenStore(secretsMode config.SecretsMode, secretStore secret.Store) config.TokenStore {
	switch secretsMode {
	case config.SecretsModeFile:
		return config.TokenStore{SecretsMode: secretsMode, TokenReader: secretStore, TokenWriter: secretStore, Location: secretsFilePath}
	case config.SecretsModeKeychain:
		return config.TokenStore{SecretsMode: secretsMode, TokenReader: secretStore, TokenWriter: secretStore, Location: keychainPath}
	default:
		return config.TokenStore{SecretsMode: secretsMode, TokenReader: secretStore, Location: config.TokenEnvVar}
	}
}
