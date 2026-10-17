package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/keychain"
	"scoped-mcp/pkg/secret/mocks"
)

func validConfig() Config {
	return Config{
		BaseURL:      "https://gitlab.example.com",
		SecretsMode:  SecretsModeEnv,
		SecretsDir:   DefaultSecretsDir,
		KeychainPath: DefaultKeychainPath,
		Tools:        Tools{AddMRNote: true},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(cfg *Config)
		wantErr error
	}{
		{name: "валидный конфиг", mutate: func(*Config) {}},
		{name: "все четыре инструмента", mutate: func(cfg *Config) {
			cfg.Tools = Tools{AddMRNote: true, AddMRLineComment: true, ReplyToDiscussion: true, AddDraftNote: true}
		}},
		{name: "только add_draft_note", mutate: func(cfg *Config) { cfg.Tools = Tools{AddDraftNote: true} }},
		{name: "режим secrets file", mutate: func(cfg *Config) { cfg.SecretsMode = SecretsModeFile }},
		{name: "base_url с портом допустим", mutate: func(cfg *Config) { cfg.BaseURL = "https://gitlab.example.com:8443" }},

		{name: "base_url http", mutate: func(cfg *Config) { cfg.BaseURL = "http://gitlab.example.com" }, wantErr: ErrBaseURL},
		{name: "base_url без схемы", mutate: func(cfg *Config) { cfg.BaseURL = "gitlab.example.com" }, wantErr: ErrBaseURL},
		{name: "base_url пустой", mutate: func(cfg *Config) { cfg.BaseURL = "" }, wantErr: ErrBaseURL},
		{name: "base_url с путём", mutate: func(cfg *Config) { cfg.BaseURL = "https://gitlab.example.com/gitlab" }, wantErr: ErrBaseURL},
		{name: "base_url с путём / (срезается только в Parse)", mutate: func(cfg *Config) { cfg.BaseURL = "https://gitlab.example.com/" }, wantErr: ErrBaseURL},
		{name: "base_url с query", mutate: func(cfg *Config) { cfg.BaseURL = "https://gitlab.example.com?x=1" }, wantErr: ErrBaseURL},
		{name: "base_url с fragment", mutate: func(cfg *Config) { cfg.BaseURL = "https://gitlab.example.com#top" }, wantErr: ErrBaseURL},
		{name: "base_url с пользователем", mutate: func(cfg *Config) { cfg.BaseURL = "https://bot@gitlab.example.com" }, wantErr: ErrBaseURL},
		{name: "base_url с пользователем и паролем", mutate: func(cfg *Config) { cfg.BaseURL = "https://bot:secret@gitlab.example.com" }, wantErr: ErrBaseURL},
		{name: "base_url без хоста с портом", mutate: func(cfg *Config) { cfg.BaseURL = "https://:443" }, wantErr: ErrBaseURL},
		{name: "base_url с паролем и неразборчивым портом", mutate: func(cfg *Config) { cfg.BaseURL = "https://bot:secret@gitlab.example.com:port" }, wantErr: ErrBaseURL},

		{name: "неизвестный режим secrets", mutate: func(cfg *Config) { cfg.SecretsMode = "vault" }, wantErr: ErrSecretsMode},
		{name: "пустой режим secrets", mutate: func(cfg *Config) { cfg.SecretsMode = "" }, wantErr: ErrSecretsMode},

		{name: "secrets_dir абсолютный допустим", mutate: func(cfg *Config) { cfg.SecretsDir = "/var/mcp-gitlab/secrets" }},
		{name: "keychain_path абсолютный допустим", mutate: func(cfg *Config) { cfg.KeychainPath = "/var/scoped-mcp.keychain-db" }},
		{name: "secrets_dir относительный", mutate: func(cfg *Config) { cfg.SecretsDir = "secrets" }, wantErr: ErrSecretsPath},
		{name: "secrets_dir от текущего каталога", mutate: func(cfg *Config) { cfg.SecretsDir = "./secrets" }, wantErr: ErrSecretsPath},
		{name: "secrets_dir от ~user не раскрывается", mutate: func(cfg *Config) { cfg.SecretsDir = "~bot/secrets" }, wantErr: ErrSecretsPath},
		{name: "secrets_dir ~ отклоняется", mutate: func(cfg *Config) { cfg.SecretsDir = "~" }, wantErr: ErrSecretsPath},
		{name: "secrets_dir пустой", mutate: func(cfg *Config) { cfg.SecretsDir = "" }, wantErr: ErrSecretsPath},
		{name: "keychain_path относительный", mutate: func(cfg *Config) { cfg.KeychainPath = "scoped-mcp.keychain-db" }, wantErr: ErrSecretsPath},
		{name: "keychain_path от ~user не раскрывается", mutate: func(cfg *Config) { cfg.KeychainPath = "~bot/scoped-mcp.keychain-db" }, wantErr: ErrSecretsPath},
		{name: "keychain_path пустой", mutate: func(cfg *Config) { cfg.KeychainPath = "" }, wantErr: ErrSecretsPath},

		{name: "ни один инструмент не включён", mutate: func(cfg *Config) { cfg.Tools = Tools{} }, wantErr: ErrNoTools},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)

			err := cfg.validate()

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

const specYAML = `
base_url: https://gitlab.example.com
secrets: keychain
keychain_path: ~/Library/Keychains/scoped-mcp.keychain-db

tools:
  add_mr_note: true
  add_mr_line_comment: true
  reply_to_discussion: true
  add_draft_note: true
`

func specConfig() Config {
	return Config{
		BaseURL:      "https://gitlab.example.com",
		SecretsMode:  SecretsModeKeychain,
		SecretsDir:   DefaultSecretsDir,
		KeychainPath: "~/Library/Keychains/scoped-mcp.keychain-db",
		Tools:        Tools{AddMRNote: true, AddMRLineComment: true, ReplyToDiscussion: true, AddDraftNote: true},
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    Config
		wantErr error
	}{
		{name: "конфиг из спеки", yaml: specYAML, want: specConfig()},
		{
			name: "base_url с завершающим слэшем нормализуется, secrets по умолчанию env",
			yaml: "base_url: https://gitlab.example.com/\ntools: {add_mr_note: true}\n",
			want: Config{BaseURL: "https://gitlab.example.com", SecretsMode: SecretsModeEnv, SecretsDir: DefaultSecretsDir, KeychainPath: DefaultKeychainPath, Tools: Tools{AddMRNote: true}},
		},
		{
			name: "только reply_to_discussion",
			yaml: "base_url: https://gitlab.example.com\ntools: {reply_to_discussion: true}\n",
			want: Config{BaseURL: "https://gitlab.example.com", SecretsMode: SecretsModeEnv, SecretsDir: DefaultSecretsDir, KeychainPath: DefaultKeychainPath, Tools: Tools{ReplyToDiscussion: true}},
		},
		{
			name: "режим file со своим secrets_dir",
			yaml: "base_url: https://gitlab.example.com\nsecrets: file\nsecrets_dir: /tmp/secrets\ntools: {add_mr_note: true}\n",
			want: Config{BaseURL: "https://gitlab.example.com", SecretsMode: SecretsModeFile, SecretsDir: "/tmp/secrets", KeychainPath: DefaultKeychainPath, Tools: Tools{AddMRNote: true}},
		},
		{name: "неизвестный режим secrets", yaml: "base_url: https://gitlab.example.com\nsecrets: vault\ntools: {add_mr_note: true}\n", wantErr: ErrSecretsMode},
		{name: "пустой режим secrets", yaml: "base_url: https://gitlab.example.com\nsecrets: \"\"\ntools: {add_mr_note: true}\n", wantErr: ErrSecretsMode},
		{name: "удалённый ключ token_path отклоняется", yaml: "base_url: https://gitlab.example.com\ntoken_path: /t\ntools: {add_mr_note: true}\n", wantErr: ErrMalformed},
		{name: "без раздела tools", yaml: "base_url: https://gitlab.example.com\n", wantErr: ErrNoTools},
		{name: "все флаги tools выключены", yaml: "base_url: https://gitlab.example.com\ntools: {add_mr_note: false}\n", wantErr: ErrNoTools},
		{name: "неизвестный ключ в tools отклоняется", yaml: "base_url: https://gitlab.example.com\ntools: {add_mr_note: true, publish_draft: true}\n", wantErr: ErrMalformed},
		{name: "опечатка в ключе base_uri отклоняется", yaml: "base_uri: https://gitlab.example.com\ntools: {add_mr_note: true}\n", wantErr: ErrMalformed},
		{name: "невалидный YAML", yaml: "base_url: [", wantErr: ErrMalformed},
		{name: "пустой документ", yaml: "", wantErr: ErrMalformed},
		{name: "base_url с путём отклоняется", yaml: "base_url: https://gitlab.example.com/gitlab\ntools: {add_mr_note: true}\n", wantErr: ErrBaseURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse([]byte(tt.yaml))

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestToolsFlags(t *testing.T) {
	tests := []struct {
		name                   string
		tools                  Tools
		wantAnyEnabled         bool
		wantNeedsDiffPositions bool
	}{
		{name: "ничего не включено", tools: Tools{}},
		{name: "только add_mr_note", tools: Tools{AddMRNote: true}, wantAnyEnabled: true},
		{name: "только reply_to_discussion", tools: Tools{ReplyToDiscussion: true}, wantAnyEnabled: true},
		{name: "только add_mr_line_comment", tools: Tools{AddMRLineComment: true}, wantAnyEnabled: true, wantNeedsDiffPositions: true},
		{name: "только add_draft_note", tools: Tools{AddDraftNote: true}, wantAnyEnabled: true, wantNeedsDiffPositions: true},
		{name: "add_mr_note и reply_to_discussion", tools: Tools{AddMRNote: true, ReplyToDiscussion: true}, wantAnyEnabled: true},
		{name: "все четыре", tools: Tools{AddMRNote: true, AddMRLineComment: true, ReplyToDiscussion: true, AddDraftNote: true}, wantAnyEnabled: true, wantNeedsDiffPositions: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantAnyEnabled, tt.tools.AnyEnabled())
			require.Equal(t, tt.wantNeedsDiffPositions, tt.tools.NeedsDiffPositions())
		})
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "путь от ~/ раскрывается в домашний каталог", in: "~/.config/mcp-gitlab/token", want: filepath.Join(home, ".config/mcp-gitlab/token")},
		{name: "только тильда", in: "~", want: home},
		{name: "абсолютный путь", in: "/var/token", want: "/var/token"},
		{name: "тильда внутри имени", in: "/data/~x", want: "/data/~x"},
		{name: "пустой путь остаётся пустым", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandHome(tt.in)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestExpandHomeUnknownHome(t *testing.T) {
	t.Setenv("HOME", "")

	_, err := ExpandHome("~/out")

	require.ErrorIs(t, err, ErrHomeUnknown)
}

func TestLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(specYAML), 0o600))

	got, err := Load(path)

	require.NoError(t, err)
	want := specConfig()
	want.SecretsDir = filepath.Join(home, ".config/mcp-gitlab/secrets")
	want.KeychainPath = filepath.Join(home, "Library/Keychains/scoped-mcp.keychain-db")
	require.Equal(t, want, got)
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))

	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestTokenStore(t *testing.T) {
	envToken := []byte("env-pat")
	fileToken := []byte("file-pat")
	tests := []struct {
		name         string
		config       func(baseDir string) Config
		wantLocation func(baseDir string) string
		wantWritable bool
		wantToken    []byte
	}{
		{
			name:         "env: только чтение из MCP_GITLAB_TOKEN",
			config:       func(string) Config { return Config{SecretsMode: SecretsModeEnv} },
			wantLocation: func(string) string { return TokenEnvVar },
			wantToken:    envToken,
		},
		{
			name: "file: токен в secrets_dir/token",
			config: func(baseDir string) Config {
				return Config{SecretsMode: SecretsModeFile, SecretsDir: filepath.Join(baseDir, "secrets")}
			},
			wantLocation: func(baseDir string) string { return filepath.Join(baseDir, "secrets", "token") },
			wantWritable: true,
			wantToken:    fileToken,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseDir := t.TempDir()
			t.Setenv(TokenEnvVar, string(envToken))
			require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "secrets"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(baseDir, "secrets", TokenSecretName), fileToken, 0o600))
			cfg := tt.config(baseDir)

			tokenStore := cfg.TokenStore()

			require.Equal(t, tt.wantLocation(baseDir), tokenStore.Location)
			require.False(t, tokenStore.UsesKeychain())
			require.Equal(t, tt.wantWritable, tokenStore.Writable())
			token, err := tokenStore.TokenReader.Get(TokenSecretName)
			require.NoError(t, err)
			require.Equal(t, tt.wantToken, token)
		})
	}
}

func TestTokenStoreKeychain(t *testing.T) {
	if !keychain.Supported() {
		t.Skip("keychain is darwin-only")
	}
	keychainPath := filepath.Join(t.TempDir(), "missing.keychain-db")

	tokenStore := Config{SecretsMode: SecretsModeKeychain, KeychainPath: keychainPath}.TokenStore()

	require.Equal(t, keychainPath, tokenStore.Location)
	require.True(t, tokenStore.UsesKeychain())
	require.True(t, tokenStore.Writable())
	_, err := tokenStore.TokenReader.Get(TokenSecretName)
	require.ErrorIs(t, err, keychain.ErrNoKeychain)
}

func TestTokenStorePanicsOnUnvalidatedMode(t *testing.T) {
	require.Panics(t, func() { Config{SecretsMode: "vault"}.TokenStore() })
}

func TestLoadToken(t *testing.T) {
	errRead := errors.New("read")
	tests := []struct {
		name     string
		stored   []byte
		storeErr error
		want     string
		wantErr  error
	}{
		{name: "токен как есть", stored: []byte("glpat-secret"), want: "glpat-secret"},
		{name: "пробелы и перевод строки обрезаются", stored: []byte("  glpat-secret\r\n"), want: "glpat-secret"},
		{name: "пустой токен", stored: []byte(" \n"), wantErr: ErrTokenUnavailable},
		{name: "нет секрета", stored: []byte("glpat-secret"), storeErr: secret.ErrNotFound, wantErr: secret.ErrNotFound},
		{name: "ошибка хранилища сохраняется в цепочке ошибок", stored: []byte("glpat-secret"), storeErr: errRead, wantErr: errRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenReader := mocks.NewMockReader(gomock.NewController(t))
			tokenReader.EXPECT().Get(TokenSecretName).Return(tt.stored, tt.storeErr)

			got, err := LoadToken(tokenReader)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, ErrTokenUnavailable)
				require.ErrorIs(t, err, tt.wantErr)
				require.NotContains(t, err.Error(), "glpat-secret")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
