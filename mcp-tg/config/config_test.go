package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/file"
	"scoped-mcp/pkg/secret/keychain"
	"scoped-mcp/pkg/secret/mocks"
)

func withDefaults(cfg Config) Config {
	if cfg.SecretsMode == "" {
		cfg.SecretsMode = SecretsModeEnv
	}
	if cfg.SecretsDir == "" {
		cfg.SecretsDir = DefaultSecretsDir
	}
	if cfg.KeychainPath == "" {
		cfg.KeychainPath = DefaultKeychainPath
	}
	return cfg
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    Config
		wantErr error
	}{
		{
			name: "полный конфиг",
			yaml: `
session_path: /tmp/session.json
chats:
  - id: 1
    type: channel
    access_hash: 42
    title: "Team oncall"
  - id: 2
    type: user
    access_hash: 43
  - id: 3
    type: chat
`,
			want: withDefaults(Config{
				SessionPath: "/tmp/session.json",
				Chats: []Chat{
					{ID: 1, Type: ChatTypeChannel, AccessHash: 42, Title: "Team oncall"},
					{ID: 2, Type: ChatTypeUser, AccessHash: 43},
					{ID: 3, Type: ChatTypeChat},
				},
			}),
		},
		{
			name: "пустой список чатов допустим на этапе разбора",
			yaml: "session_path: /tmp/session.json\n",
			want: withDefaults(Config{SessionPath: "/tmp/session.json"}),
		},
		{
			name: "режим env по умолчанию, пути секретов по умолчанию",
			yaml: "session_path: /tmp/session.json\n",
			want: Config{SecretsMode: SecretsModeEnv, SessionPath: "/tmp/session.json", SecretsDir: DefaultSecretsDir, KeychainPath: DefaultKeychainPath},
		},
		{
			name: "режим env явно",
			yaml: "secrets: env\nsession_path: /tmp/session.json\n",
			want: withDefaults(Config{SessionPath: "/tmp/session.json"}),
		},
		{
			name: "режим file без session_path, свой secrets_dir",
			yaml: "secrets: file\nsecrets_dir: /tmp/secrets\n",
			want: withDefaults(Config{SecretsMode: SecretsModeFile, SecretsDir: "/tmp/secrets"}),
		},
		{
			name: "режим keychain без session_path, свой keychain_path (только darwin)",
			yaml: "secrets: keychain\nkeychain_path: /tmp/test.keychain-db\n",
			want: withDefaults(Config{SecretsMode: SecretsModeKeychain, KeychainPath: "/tmp/test.keychain-db"}),
		},
		{
			name: "session_path в режиме file сохраняется, но не обязателен",
			yaml: "secrets: file\nsession_path: /tmp/session.json\n",
			want: withDefaults(Config{SecretsMode: SecretsModeFile, SessionPath: "/tmp/session.json"}),
		},
		{
			name:    "неизвестный режим secrets",
			yaml:    "secrets: vault\nsession_path: /s\n",
			wantErr: ErrSecretsMode,
		},
		{
			name:    "пустой режим secrets",
			yaml:    "secrets: \"\"\nsession_path: /s\n",
			wantErr: ErrSecretsMode,
		},
		{
			name: "папки с заголовком и без",
			yaml: `
session_path: /tmp/session.json
chats:
  - id: 1
    type: chat
folders:
  - id: 3
    title: Claude
  - id: 5
`,
			want: withDefaults(Config{
				SessionPath: "/tmp/session.json",
				Chats:       []Chat{{ID: 1, Type: ChatTypeChat}},
				Folders:     []Folder{{ID: 3, Title: "Claude"}, {ID: 5}},
			}),
		},
		{
			name: "только folders без chats допустимо на этапе разбора",
			yaml: "session_path: /tmp/session.json\nfolders:\n  - {id: 3}\n",
			want: withDefaults(Config{SessionPath: "/tmp/session.json", Folders: []Folder{{ID: 3}}}),
		},
		{
			name:    "нулевой id папки",
			yaml:    "session_path: /s\nfolders:\n  - {id: 0}\n",
			wantErr: ErrFolderIDInvalid,
		},
		{
			name:    "отрицательный id папки",
			yaml:    "session_path: /s\nfolders:\n  - {id: -3}\n",
			wantErr: ErrFolderIDInvalid,
		},
		{
			name:    "папка без id",
			yaml:    "session_path: /s\nfolders:\n  - {title: Claude}\n",
			wantErr: ErrFolderIDInvalid,
		},
		{
			name:    "повтор id папки",
			yaml:    "session_path: /s\nfolders:\n  - {id: 3}\n  - {id: 3, title: Other}\n",
			wantErr: ErrDuplicateFolder,
		},
		{
			name:    "неизвестное поле папки",
			yaml:    "session_path: /s\nfolders:\n  - {id: 3, name: Claude}\n",
			wantErr: ErrMalformed,
		},
		{
			name:    "нет session_path в режиме env",
			yaml:    "chats:\n  - {id: 1, type: chat}\n",
			wantErr: ErrSessionPathMissing,
		},
		{
			name:    "неизвестный тип",
			yaml:    "session_path: /s\nchats:\n  - {id: 1, type: group}\n",
			wantErr: ErrChatType,
		},
		{
			name:    "повтор id и типа",
			yaml:    "session_path: /s\nchats:\n  - {id: 1, type: chat}\n  - {id: 1, type: chat}\n",
			wantErr: ErrDuplicateChat,
		},
		{
			name: "одинаковый id разного типа допустим",
			yaml: "session_path: /s\nchats:\n  - {id: 1, type: chat}\n  - {id: 1, type: user, access_hash: 7}\n",
			want: withDefaults(Config{
				SessionPath: "/s",
				Chats:       []Chat{{ID: 1, Type: ChatTypeChat}, {ID: 1, Type: ChatTypeUser, AccessHash: 7}},
			}),
		},
		{
			name:    "user без access_hash",
			yaml:    "session_path: /s\nchats:\n  - {id: 1, type: user}\n",
			wantErr: ErrAccessHashMissing,
		},
		{
			name:    "channel без access_hash",
			yaml:    "session_path: /s\nchats:\n  - {id: 1, type: channel}\n",
			wantErr: ErrAccessHashMissing,
		},
		{
			name:    "id в формате Bot API",
			yaml:    "session_path: /s\nchats:\n  - {id: -1001234567890, type: channel, access_hash: 1}\n",
			wantErr: ErrChatIDInvalid,
		},
		{
			name:    "отрицательный id базовой группы",
			yaml:    "session_path: /s\nchats:\n  - {id: -42, type: chat}\n",
			wantErr: ErrChatIDInvalid,
		},
		{
			name:    "нулевой id",
			yaml:    "session_path: /s\nchats:\n  - {id: 0, type: user, access_hash: 1}\n",
			wantErr: ErrChatIDInvalid,
		},
		{
			name:    "нет id",
			yaml:    "session_path: /s\nchats:\n  - {type: chat}\n",
			wantErr: ErrChatIDInvalid,
		},
		{
			name:    "неизвестное поле",
			yaml:    "session_path: /s\nsessoin_path: /x\n",
			wantErr: ErrMalformed,
		},
		{
			name:    "синтаксическая ошибка",
			yaml:    "session_path: [/s\n",
			wantErr: ErrMalformed,
		},
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

func TestRequireAllowlist(t *testing.T) {
	tests := []struct {
		name    string
		chats   []Chat
		folders []Folder
		wantErr error
	}{
		{name: "только chats", chats: []Chat{{ID: 1, Type: ChatTypeChat}}},
		{name: "только folders", folders: []Folder{{ID: 3}}},
		{name: "и chats, и folders", chats: []Chat{{ID: 1, Type: ChatTypeChat}}, folders: []Folder{{ID: 3}}},
		{name: "пусто", wantErr: ErrEmptyAllowlist},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Config{Chats: tt.chats, Folders: tt.folders}.RequireAllowlist()
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestMergeChats(t *testing.T) {
	explicitChannel := Chat{ID: 300, Type: ChatTypeChannel, AccessHash: 3, Title: "Team oncall"}
	folderChannel := Chat{ID: 300, Type: ChatTypeChannel, AccessHash: 3}
	folderGroup := Chat{ID: 200, Type: ChatTypeChat}
	folderUser := Chat{ID: 100, Type: ChatTypeUser, AccessHash: 1}
	tests := []struct {
		name        string
		explicit    []Chat
		fromFolders []Chat
		want        []Chat
	}{
		{name: "без пересечений: сначала chats, затем папки", explicit: []Chat{explicitChannel}, fromFolders: []Chat{folderGroup, folderUser}, want: []Chat{explicitChannel, folderGroup, folderUser}},
		{name: "пересечение по (тип, ID): остаётся запись из chats с её title", explicit: []Chat{explicitChannel}, fromFolders: []Chat{folderGroup, folderChannel}, want: []Chat{explicitChannel, folderGroup}},
		{name: "одинаковый ID разного типа — обе записи", explicit: []Chat{{ID: 100, Type: ChatTypeChat}}, fromFolders: []Chat{folderUser}, want: []Chat{{ID: 100, Type: ChatTypeChat}, folderUser}},
		{name: "повтор внутри папок", fromFolders: []Chat{folderGroup, folderUser, folderGroup}, want: []Chat{folderGroup, folderUser}},
		{name: "только папки", fromFolders: []Chat{folderUser}, want: []Chat{folderUser}},
		{name: "только chats", explicit: []Chat{explicitChannel}, want: []Chat{explicitChannel}},
		{name: "оба пустых → пустой список, не nil", want: []Chat{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, MergeChats(tt.explicit, tt.fromFolders))
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
		{name: "тильда", in: "~/.config/mcp-tg/session.json", want: filepath.Join(home, ".config/mcp-tg/session.json")},
		{name: "только тильда", in: "~", want: home},
		{name: "абсолютный путь", in: "/var/session.json", want: "/var/session.json"},
		{name: "тильда внутри имени", in: "/data/~x", want: "/data/~x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandHome(tt.in)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("session_path: ~/session.json\nsecrets_dir: ~/secrets\nkeychain_path: ~/test.keychain-db\nchats:\n  - {id: 1, type: chat}\n"), 0o600))

	got, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, Config{
		SecretsMode:  SecretsModeEnv,
		SessionPath:  filepath.Join(home, "session.json"),
		SecretsDir:   filepath.Join(home, "secrets"),
		KeychainPath: filepath.Join(home, "test.keychain-db"),
		Chats:        []Chat{{ID: 1, Type: ChatTypeChat}},
	}, got)
}

func TestLoadExpandsDefaultSecretPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("secrets: file\n"), 0o600))

	got, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, Config{
		SecretsMode:  SecretsModeFile,
		SecretsDir:   filepath.Join(home, ".config/mcp-tg/secrets"),
		KeychainPath: filepath.Join(home, "Library/Keychains/scoped-mcp.keychain-db"),
	}, got)
}

func TestLoadCredentials(t *testing.T) {
	tests := []struct {
		name       string
		apiID      []byte
		apiIDErr   error
		apiHash    []byte
		apiHashErr error
		want       Credentials
		wantErrs   []error
	}{
		{name: "оба ключа есть", apiID: []byte("12345"), apiHash: []byte("abc"), want: Credentials{APIID: 12345, APIHash: "abc"}},
		{name: "перевод строки и пробелы из файла обрезаются", apiID: []byte(" 12345\n"), apiHash: []byte("abc\n"), want: Credentials{APIID: 12345, APIHash: "abc"}},
		{name: "нет api_id → api_id недоступен, причина ErrNotFound", apiIDErr: secret.ErrNotFound, wantErrs: []error{ErrAPIIDUnavailable, secret.ErrNotFound}},
		{name: "api_id из одних пробелов → api_id недоступен, причина ErrNotFound", apiID: []byte(" \n"), wantErrs: []error{ErrAPIIDUnavailable, secret.ErrNotFound}},
		{name: "api_id не число → ErrAPIIDInvalid", apiID: []byte("x1"), wantErrs: []error{ErrAPIIDInvalid}},
		{name: "нет api_hash → api_hash недоступен, причина ErrNotFound", apiID: []byte("12345"), apiHashErr: secret.ErrNotFound, wantErrs: []error{ErrAPIHashUnavailable, secret.ErrNotFound}},
		{name: "api_hash из одних пробелов → api_hash недоступен, причина ErrNotFound", apiID: []byte("12345"), apiHash: []byte("\n"), wantErrs: []error{ErrAPIHashUnavailable, secret.ErrNotFound}},
		{name: "нет доступа к файлу api_id из-за прав → api_id недоступен, причина ErrPermissions", apiIDErr: file.ErrPermissions, wantErrs: []error{ErrAPIIDUnavailable, file.ErrPermissions}},
		{name: "доступ к связке отклонён на api_hash → api_hash недоступен, причина ErrAccessDenied", apiID: []byte("12345"), apiHashErr: keychain.ErrAccessDenied, wantErrs: []error{ErrAPIHashUnavailable, keychain.ErrAccessDenied}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyReader := mocks.NewMockReader(gomock.NewController(t))
			keyReader.EXPECT().Get(SecretAPIID).Return(tt.apiID, tt.apiIDErr)
			keyReader.EXPECT().Get(SecretAPIHash).Return(tt.apiHash, tt.apiHashErr).MaxTimes(1)

			got, err := LoadCredentials(keyReader)

			if len(tt.wantErrs) > 0 {
				for _, wantErr := range tt.wantErrs {
					require.ErrorIs(t, err, wantErr)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestLoadCredentialsHidesInvalidAPIID(t *testing.T) {
	keyReader := mocks.NewMockReader(gomock.NewController(t))
	keyReader.EXPECT().Get(SecretAPIID).Return([]byte("not-a-number-9f2a"), nil)

	_, err := LoadCredentials(keyReader)

	require.ErrorIs(t, err, ErrAPIIDInvalid)
	require.NotContains(t, err.Error(), "9f2a")
}

func TestSecretStores(t *testing.T) {
	envAPIID := []byte("7")
	fileAPIID := []byte("8")
	tests := []struct {
		name                string
		config              func(home string) Config
		wantSessionName     string
		wantSessionLocation func(home string) string
		wantKeyWriter       bool
		wantAPIID           []byte
	}{
		{
			name: "env: сессия в каталоге session_path, ключи из MCP_TG_*",
			config: func(home string) Config {
				return Config{SecretsMode: SecretsModeEnv, SessionPath: filepath.Join(home, "cfg", "session.json")}
			},
			wantSessionName:     "session.json",
			wantSessionLocation: func(home string) string { return filepath.Join(home, "cfg", "session.json") },
			wantAPIID:           envAPIID,
		},
		{
			name: "file: всё в secrets_dir",
			config: func(home string) Config {
				return Config{SecretsMode: SecretsModeFile, SecretsDir: filepath.Join(home, "secrets")}
			},
			wantSessionName:     "session",
			wantSessionLocation: func(home string) string { return filepath.Join(home, "secrets", "session") },
			wantKeyWriter:       true,
			wantAPIID:           fileAPIID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(envPrefix+strings.ToUpper(SecretAPIID), string(envAPIID))
			require.NoError(t, os.MkdirAll(filepath.Join(home, "secrets"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(home, "secrets", SecretAPIID), fileAPIID, 0o600))

			stores, err := tt.config(home).SecretStores()

			require.NoError(t, err)
			require.Equal(t, tt.wantSessionName, stores.SessionName)
			require.Equal(t, tt.wantSessionLocation(home), stores.SessionLocation)
			require.Equal(t, tt.wantKeyWriter, stores.KeyWriter != nil)
			require.NoError(t, stores.SessionStore.Set(stores.SessionName, []byte("blob")))
			info, err := os.Stat(tt.wantSessionLocation(home))
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			apiID, err := stores.KeyReader.Get(SecretAPIID)
			require.NoError(t, err)
			require.Equal(t, tt.wantAPIID, apiID)
		})
	}
}

func TestSecretStoresKeychain(t *testing.T) {
	if !keychain.Supported() {
		t.Skip("keychain is darwin-only")
	}
	keychainPath := filepath.Join(t.TempDir(), "missing.keychain-db")

	stores, err := Config{SecretsMode: SecretsModeKeychain, KeychainPath: keychainPath}.SecretStores()

	require.NoError(t, err)
	require.Equal(t, SecretSession, stores.SessionName)
	require.Equal(t, keychainPath, stores.SessionLocation)
	require.NotNil(t, stores.KeyWriter)
	_, err = stores.KeyReader.Get(SecretAPIID)
	require.ErrorIs(t, err, keychain.ErrNoKeychain)
}

func TestSecretStoresRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr error
	}{
		{name: "неизвестный режим отклоняется", config: Config{SecretsMode: "vault"}, wantErr: ErrSecretsMode},
		{name: "env без session_path отклоняется", config: Config{SecretsMode: SecretsModeEnv}, wantErr: ErrSessionPathMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.config.SecretStores()

			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
