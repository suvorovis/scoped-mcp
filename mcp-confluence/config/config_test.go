package config

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/keychain"
	"scoped-mcp/pkg/secret/mocks"
)

func validConfig() Config {
	return Config{
		BaseURL:     "https://confluence.example.com",
		SecretsMode: SecretsModeEnv,
		ReadRoots:   []int64{100, 110},
		WriteRoots:  []int64{200},
		Tools:       Tools{GetPage: true, ListChildren: true, Search: true, ListComments: true, CreatePage: true, UpdatePage: true},
		Search:      Search{MaxResults: 50},
	}
}

func withReadToolsAndRootsOnly(cfg *Config) {
	cfg.Tools = Tools{GetPage: true}
	cfg.WriteRoots = nil
}

func withWriteToolsAndRootsOnly(cfg *Config) {
	cfg.Tools = Tools{UpdatePage: true}
	cfg.ReadRoots = nil
}

func withAttachments(mutate func(attachments *Attachments)) func(cfg *Config) {
	return func(cfg *Config) {
		attachments := Attachments{DownloadDir: "/home/bot/out/confluence", UploadDirs: []string{"/home/bot/out"}, Extensions: []string{".md", ".png"}, MaxBytes: 10 << 20}
		mutate(&attachments)
		cfg.Tools.GetAttachment = true
		cfg.Tools.UploadAttachment = true
		cfg.Attachments = &attachments
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(cfg *Config)
		wantErr error
	}{
		{name: "валидный конфиг", mutate: func(*Config) {}},
		{name: "только инструменты чтения без write_roots", mutate: withReadToolsAndRootsOnly},
		{name: "только инструменты записи без read_roots", mutate: withWriteToolsAndRootsOnly},
		{name: "max_results ровно 1", mutate: func(cfg *Config) { cfg.Search.MaxResults = 1 }},
		{name: "max_results на верхней границе", mutate: func(cfg *Config) { cfg.Search.MaxResults = SearchMaxResultsLimit }},
		{name: "раздел attachments", mutate: withAttachments(func(*Attachments) {})},
		{name: "только get_attachment с разделом attachments", mutate: func(cfg *Config) {
			withAttachments(func(*Attachments) {})(cfg)
			cfg.Tools.UploadAttachment = false
		}},
		{name: "download_dir от ~/", mutate: withAttachments(func(a *Attachments) { a.DownloadDir = "~/agent-out/confluence" })},
		{name: "каталог upload_dirs от ~/", mutate: withAttachments(func(a *Attachments) { a.UploadDirs = []string{"~/agent-out"} })},
		{name: "max_bytes на верхней границе", mutate: withAttachments(func(a *Attachments) { a.MaxBytes = AttachmentsMaxBytesLimit })},
		{name: "расширение в верхнем регистре допустимо", mutate: withAttachments(func(a *Attachments) { a.Extensions = []string{".MD", ".png"} })},

		{name: "base_url http", mutate: func(cfg *Config) { cfg.BaseURL = "http://confluence.example.com" }, wantErr: ErrBaseURL},
		{name: "base_url без схемы", mutate: func(cfg *Config) { cfg.BaseURL = "confluence.example.com" }, wantErr: ErrBaseURL},
		{name: "base_url с query", mutate: func(cfg *Config) { cfg.BaseURL = "https://confluence.example.com/?x=1" }, wantErr: ErrBaseURL},
		{name: "base_url с fragment", mutate: func(cfg *Config) { cfg.BaseURL = "https://confluence.example.com/#top" }, wantErr: ErrBaseURL},
		{name: "base_url пустой", mutate: func(cfg *Config) { cfg.BaseURL = "" }, wantErr: ErrBaseURL},
		{name: "base_url с пользователем", mutate: func(cfg *Config) { cfg.BaseURL = "https://bot@confluence.example.com" }, wantErr: ErrBaseURL},
		{name: "base_url с пользователем и паролем", mutate: func(cfg *Config) { cfg.BaseURL = "https://bot:secret@confluence.example.com" }, wantErr: ErrBaseURL},
		{name: "base_url не разбирается", mutate: func(cfg *Config) { cfg.BaseURL = "https://bot:secret@confluence.example.com/%zz" }, wantErr: ErrBaseURL},

		{name: "ни один инструмент не включён", mutate: func(cfg *Config) { cfg.Tools = Tools{} }, wantErr: ErrNoTools},
		{name: "get_attachment без раздела attachments", mutate: func(cfg *Config) { cfg.Tools.GetAttachment = true }, wantErr: ErrAttachmentsSectionMismatch},
		{name: "upload_attachment без раздела attachments", mutate: func(cfg *Config) { cfg.Tools.UploadAttachment = true }, wantErr: ErrAttachmentsSectionMismatch},
		{name: "раздел attachments без инструментов вложений", mutate: func(cfg *Config) {
			cfg.Attachments = &Attachments{DownloadDir: "/home/bot/out/confluence", UploadDirs: []string{"/home/bot/out"}, Extensions: []string{".md"}, MaxBytes: 1024}
		}, wantErr: ErrAttachmentsSectionMismatch},

		{name: "запись без write_roots", mutate: func(cfg *Config) { cfg.WriteRoots = nil }, wantErr: ErrWriteRootsRequired},
		{name: "чтение без корней вообще", mutate: func(cfg *Config) {
			withReadToolsAndRootsOnly(cfg)
			cfg.ReadRoots = nil
		}, wantErr: ErrReadZoneEmpty},
		{name: "корень чтения ноль", mutate: func(cfg *Config) { cfg.ReadRoots = []int64{0} }, wantErr: ErrRootID},
		{name: "корень записи отрицательный", mutate: func(cfg *Config) { cfg.WriteRoots = []int64{-5} }, wantErr: ErrRootID},
		{name: "повтор внутри read_roots", mutate: func(cfg *Config) { cfg.ReadRoots = []int64{100, 100} }, wantErr: ErrDuplicateRoot},
		{name: "повтор внутри write_roots", mutate: func(cfg *Config) { cfg.WriteRoots = []int64{200, 200} }, wantErr: ErrDuplicateRoot},
		{name: "один id в read_roots и write_roots", mutate: func(cfg *Config) { cfg.WriteRoots = []int64{110} }, wantErr: ErrDuplicateRoot},

		{name: "max_results ноль", mutate: func(cfg *Config) { cfg.Search.MaxResults = 0 }, wantErr: ErrSearchMaxResults},
		{name: "max_results выше верхней границы", mutate: func(cfg *Config) { cfg.Search.MaxResults = SearchMaxResultsLimit + 1 }, wantErr: ErrSearchMaxResults},

		{name: "download_dir пуст", mutate: withAttachments(func(a *Attachments) { a.DownloadDir = "" }), wantErr: ErrAttachmentsDownloadDir},
		{name: "download_dir относительный", mutate: withAttachments(func(a *Attachments) { a.DownloadDir = "out/confluence" }), wantErr: ErrAttachmentsDownloadDir},
		{name: "download_dir от ~user не раскрывается", mutate: withAttachments(func(a *Attachments) { a.DownloadDir = "~bot/out" }), wantErr: ErrAttachmentsDownloadDir},
		{name: "upload_dirs пуст", mutate: withAttachments(func(a *Attachments) { a.UploadDirs = nil }), wantErr: ErrAttachmentsUploadDirs},
		{name: "каталог загрузки — пустая строка", mutate: withAttachments(func(a *Attachments) { a.UploadDirs = []string{""} }), wantErr: ErrAttachmentsUploadDirs},
		{name: "каталог загрузки относительный", mutate: withAttachments(func(a *Attachments) { a.UploadDirs = []string{"out"} }), wantErr: ErrAttachmentsUploadDirs},
		{name: "второй каталог загрузки относительный", mutate: withAttachments(func(a *Attachments) { a.UploadDirs = []string{"/a/out", "./out"} }), wantErr: ErrAttachmentsUploadDirs},
		{name: "extensions пуст", mutate: withAttachments(func(a *Attachments) { a.Extensions = nil }), wantErr: ErrAttachmentsExtensions},
		{name: "расширение без точки", mutate: withAttachments(func(a *Attachments) { a.Extensions = []string{"md"} }), wantErr: ErrAttachmentsExtensions},
		{name: "расширение из одной точки", mutate: withAttachments(func(a *Attachments) { a.Extensions = []string{"."} }), wantErr: ErrAttachmentsExtensions},
		{name: "расширение со слэшем", mutate: withAttachments(func(a *Attachments) { a.Extensions = []string{".tar/gz"} }), wantErr: ErrAttachmentsExtensions},
		{name: "расширение повторяется в другом регистре", mutate: withAttachments(func(a *Attachments) { a.Extensions = []string{".md", ".MD"} }), wantErr: ErrAttachmentsExtensions},
		{name: "max_bytes ноль", mutate: withAttachments(func(a *Attachments) { a.MaxBytes = 0 }), wantErr: ErrAttachmentsMaxBytes},
		{name: "max_bytes отрицательный", mutate: withAttachments(func(a *Attachments) { a.MaxBytes = -1 }), wantErr: ErrAttachmentsMaxBytes},
		{name: "max_bytes выше верхней границы", mutate: withAttachments(func(a *Attachments) { a.MaxBytes = AttachmentsMaxBytesLimit + 1 }), wantErr: ErrAttachmentsMaxBytes},
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

const fullYAML = `
base_url: https://confluence.example.com
secrets: keychain
keychain_path: ~/Library/Keychains/scoped-mcp.keychain-db

read_roots: [123456789, 123456790]
write_roots: [987654321]

tools:
  get_page: true
  list_children: true
  search: true
  list_comments: true
  list_attachments: true
  get_attachment: true
  create_page: true
  update_page: true
  upload_attachment: true

attachments:
  download_dir: ~/agent-out/confluence
  upload_dirs: [~/agent-out, /var/reports/]
  extensions: [.png, .svg, .drawio, .md, .txt, .json, .pdf]
  max_bytes: 10485760

search:
  max_results: 50
`

var fullConfig = Config{
	BaseURL:      "https://confluence.example.com",
	SecretsMode:  SecretsModeKeychain,
	SecretsDir:   DefaultSecretsDir,
	KeychainPath: "~/Library/Keychains/scoped-mcp.keychain-db",
	ReadRoots:    []int64{123456789, 123456790},
	WriteRoots:   []int64{987654321},
	Tools:        Tools{GetPage: true, ListChildren: true, Search: true, ListComments: true, ListAttachments: true, GetAttachment: true, CreatePage: true, UpdatePage: true, UploadAttachment: true},
	Attachments: &Attachments{
		DownloadDir: "~/agent-out/confluence",
		UploadDirs:  []string{"~/agent-out", "/var/reports/"},
		Extensions:  []string{".png", ".svg", ".drawio", ".md", ".txt", ".json", ".pdf"},
		MaxBytes:    10485760,
	},
	Search: Search{MaxResults: 50},
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    Config
		wantErr error
	}{
		{name: "полный конфиг с вложениями", yaml: fullYAML, want: fullConfig},
		{
			name: "base_url с завершающим слэшем нормализуется, max_results по умолчанию",
			yaml: "base_url: https://confluence.example.com/\nread_roots: [1]\ntools: {get_page: true}\n",
			want: Config{BaseURL: "https://confluence.example.com", SecretsMode: SecretsModeEnv, SecretsDir: DefaultSecretsDir, KeychainPath: DefaultKeychainPath, ReadRoots: []int64{1}, Tools: Tools{GetPage: true}, Search: Search{MaxResults: DefaultSearchMaxResults}},
		},
		{
			name: "search без max_results оставляет умолчание",
			yaml: "base_url: https://confluence.example.com\nread_roots: [1]\ntools: {get_page: true}\nsearch: {}\n",
			want: Config{BaseURL: "https://confluence.example.com", SecretsMode: SecretsModeEnv, SecretsDir: DefaultSecretsDir, KeychainPath: DefaultKeychainPath, ReadRoots: []int64{1}, Tools: Tools{GetPage: true}, Search: Search{MaxResults: DefaultSearchMaxResults}},
		},
		{
			name: "режим file со своим secrets_dir",
			yaml: "base_url: https://confluence.example.com\nsecrets: file\nsecrets_dir: /tmp/secrets\nread_roots: [1]\ntools: {get_page: true}\n",
			want: Config{BaseURL: "https://confluence.example.com", SecretsMode: SecretsModeFile, SecretsDir: "/tmp/secrets", KeychainPath: DefaultKeychainPath, ReadRoots: []int64{1}, Tools: Tools{GetPage: true}, Search: Search{MaxResults: DefaultSearchMaxResults}},
		},
		{name: "неизвестный режим secrets", yaml: "base_url: https://confluence.example.com\nsecrets: vault\nread_roots: [1]\ntools: {get_page: true}\n", wantErr: ErrSecretsMode},
		{name: "пустой режим secrets", yaml: "base_url: https://confluence.example.com\nsecrets: \"\"\nread_roots: [1]\ntools: {get_page: true}\n", wantErr: ErrSecretsMode},
		{name: "без раздела tools", yaml: "base_url: https://confluence.example.com\nread_roots: [1]\n", wantErr: ErrNoTools},
		{name: "неизвестный ключ в tools отклоняется", yaml: "base_url: https://confluence.example.com\nread_roots: [1]\ntools: {get_page: true, delete_page: true}\n", wantErr: ErrDecode},
		{name: "опечатка в ключе base_uri отклоняется", yaml: "base_uri: https://confluence.example.com\nread_roots: [1]\ntools: {get_page: true}\n", wantErr: ErrDecode},
		{name: "id корня строкой отклоняется", yaml: "base_url: https://confluence.example.com\nread_roots: [abc]\ntools: {get_page: true}\n", wantErr: ErrDecode},
		{name: "невалидный YAML", yaml: "base_url: [", wantErr: ErrDecode},
		{name: "ошибка валидации пробрасывается", yaml: "base_url: https://confluence.example.com\ntools: {update_page: true}\n", wantErr: ErrWriteRootsRequired},
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

func TestZoneRoots(t *testing.T) {
	t.Run("корни зон отсортированы и объединены", func(t *testing.T) {
		cfg := Config{ReadRoots: []int64{300, 100}, WriteRoots: []int64{250, 200}}

		require.Equal(t, []string{"100", "200", "250", "300"}, cfg.ReadZoneRoots())
		require.Equal(t, []string{"200", "250"}, cfg.WriteZoneRoots())
	})
	t.Run("исходные списки не меняются", func(t *testing.T) {
		cfg := Config{ReadRoots: []int64{300, 100}, WriteRoots: []int64{250, 200}}
		readRootsBefore := slices.Clone(cfg.ReadRoots)
		writeRootsBefore := slices.Clone(cfg.WriteRoots)

		cfg.ReadZoneRoots()
		cfg.WriteZoneRoots()

		require.Equal(t, readRootsBefore, cfg.ReadRoots)
		require.Equal(t, writeRootsBefore, cfg.WriteRoots)
	})
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "путь от ~/ раскрывается в домашний каталог", in: "~/.config/mcp-confluence/config.yaml", want: filepath.Join(home, ".config/mcp-confluence/config.yaml")},
		{name: "только тильда", in: "~", want: home},
		{name: "абсолютный путь", in: "/var/config.yaml", want: "/var/config.yaml"},
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

func TestExpandHomeUnknownHome(t *testing.T) {
	t.Setenv("HOME", "")

	_, err := ExpandHome("~/out")

	require.ErrorIs(t, err, ErrHomeUnknown)
}

func TestLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(fullYAML), 0o600))
	want := fullConfig
	want.SecretsDir = filepath.Join(home, ".config/mcp-confluence/secrets")
	want.KeychainPath = filepath.Join(home, "Library/Keychains/scoped-mcp.keychain-db")
	wantAttachments := *fullConfig.Attachments
	wantAttachments.DownloadDir = filepath.Join(home, "agent-out/confluence")
	wantAttachments.UploadDirs = []string{filepath.Join(home, "agent-out"), "/var/reports"}
	want.Attachments = &wantAttachments

	got, err := Load(path)

	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestLoadRejectsAttachmentDirs(t *testing.T) {
	absoluteHome := t.TempDir()
	linkToHome := filepath.Join(t.TempDir(), "home-link")
	require.NoError(t, os.Symlink(absoluteHome, linkToHome))
	type rejectCase struct {
		name         string
		home         string
		dir          string
		inUploadDirs bool
		wantErr      error
	}
	tests := []rejectCase{
		{name: "download_dir от ~/ при относительном HOME", home: "relative-home", dir: "~/out", wantErr: ErrAttachmentsDownloadDir},
		{name: "download_dir от ~/ без HOME", home: "", dir: "~/out", wantErr: ErrAttachmentsDownloadDir},
		{name: "download_dir — домашний", home: absoluteHome, dir: absoluteHome, wantErr: ErrAttachmentsDownloadDir},
		{name: "download_dir — домашний ~", home: absoluteHome, dir: "~", wantErr: ErrAttachmentsDownloadDir},
		{name: "download_dir над домашним ~/..", home: absoluteHome, dir: "~/..", wantErr: ErrAttachmentsDownloadDir},
		{name: "download_dir — домашний через несуществующий/..", home: absoluteHome, dir: filepath.Join(absoluteHome, "missing") + "/..", wantErr: ErrAttachmentsDownloadDir},
		{name: "download_dir — корень", home: absoluteHome, dir: "/", wantErr: ErrAttachmentsDownloadDir},
		{name: "download_dir — симлинк на домашний", home: absoluteHome, dir: linkToHome, wantErr: ErrAttachmentsDownloadDir},
		{name: "upload_dirs от ~/ без HOME", home: "", dir: "~/out", inUploadDirs: true, wantErr: ErrAttachmentsUploadDirs},
		{name: "upload_dirs — домашний", home: absoluteHome, dir: absoluteHome, inUploadDirs: true, wantErr: ErrAttachmentsUploadDirs},
		{name: "upload_dirs над домашним", home: absoluteHome, dir: filepath.Dir(absoluteHome), inUploadDirs: true, wantErr: ErrAttachmentsUploadDirs},
		{name: "upload_dirs — корень через ..", home: absoluteHome, dir: "/usr/..", inUploadDirs: true, wantErr: ErrAttachmentsUploadDirs},
		{name: "upload_dirs — симлинк на домашний", home: absoluteHome, dir: linkToHome, inUploadDirs: true, wantErr: ErrAttachmentsUploadDirs},
	}
	if accountHome := existingAccountHome(); accountHome != "" {
		tests = append(tests, rejectCase{name: "download_dir — домашний из учётной записи при другом HOME", home: absoluteHome, dir: accountHome, wantErr: ErrAttachmentsDownloadDir})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", tt.home)
			downloadDir, uploadDir := "/var/out/confluence", "/var/out"
			if tt.inUploadDirs {
				uploadDir = tt.dir
			} else {
				downloadDir = tt.dir
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			configYAML := "base_url: https://confluence.example.com\nread_roots: [1]\ntools: {get_attachment: true}\n" +
				"attachments: {download_dir: \"" + downloadDir + "\", upload_dirs: [\"" + uploadDir + "\"], extensions: [.md], max_bytes: 1024}\n"
			require.NoError(t, os.WriteFile(path, []byte(configYAML), 0o600))

			_, err := Load(path)

			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func existingAccountHome() string {
	account, err := user.Current()
	if err != nil {
		return ""
	}
	if _, err := os.Stat(account.HomeDir); err != nil {
		return ""
	}
	return account.HomeDir
}

func TestHomeGuardIsInsideDirWithoutCrossingHomeOrRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	realHome, err := filepath.EvalSymlinks(home)
	require.NoError(t, err)
	out := filepath.Join(realHome, "out")
	require.NoError(t, os.MkdirAll(filepath.Join(out, "sub"), 0o700))
	tests := []struct {
		name string
		path string
		dir  string
		want bool
	}{
		{name: "файл в каталоге внутри домашнего", path: filepath.Join(out, "x.md"), dir: out, want: true},
		{name: "файл в подкаталоге каталога", path: filepath.Join(out, "sub", "x.md"), dir: out, want: true},
		{name: "каталог — домашний", path: filepath.Join(out, "x.md"), dir: realHome, want: false},
		{name: "каталог выше домашнего", path: filepath.Join(out, "x.md"), dir: filepath.Dir(realHome), want: false},
		{name: "каталог — корень", path: "/usr/x.md", dir: "/", want: false},
		{name: "файл вне каталога", path: filepath.Join(realHome, "x.md"), dir: out, want: false},
		{name: "stat предка файла падает — отказ", path: filepath.Join(out, "missing", "x.md"), dir: out, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, NewHomeGuard().IsInsideDirWithoutCrossingHomeOrRoot(tt.path, tt.dir))
		})
	}
}

func TestAttachmentsAllowsExtension(t *testing.T) {
	attachments := Attachments{Extensions: []string{".md", ".PNG"}}
	tests := []struct {
		fileName string
		want     bool
	}{
		{fileName: "report.md", want: true},
		{fileName: "REPORT.MD", want: true},
		{fileName: "scheme.png", want: true},
		{fileName: "scheme.drawio.png", want: true},
		{fileName: ".md", want: true},
		{fileName: "report.md.txt"},
		{fileName: "md"},
		{fileName: "report."},
		{fileName: "report"},
	}
	for _, tt := range tests {
		t.Run(tt.fileName, func(t *testing.T) {
			require.Equal(t, tt.want, attachments.AllowsExtension(tt.fileName))
		})
	}
}

func TestTokenStore(t *testing.T) {
	envToken := []byte("env-pat")
	fileToken := []byte("file-pat")
	tests := []struct {
		name              string
		config            func(home string) Config
		wantLocation      func(home string) string
		wantWritableStore bool
		wantToken         []byte
	}{
		{
			name:         "env: только чтение из MCP_CONFLUENCE_TOKEN",
			config:       func(string) Config { return Config{SecretsMode: SecretsModeEnv} },
			wantLocation: func(string) string { return TokenEnvVar },
			wantToken:    envToken,
		},
		{
			name: "file: токен в secrets_dir/token",
			config: func(home string) Config {
				return Config{SecretsMode: SecretsModeFile, SecretsDir: filepath.Join(home, "secrets")}
			},
			wantLocation:      func(home string) string { return filepath.Join(home, "secrets", "token") },
			wantWritableStore: true,
			wantToken:         fileToken,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(TokenEnvVar, string(envToken))
			require.NoError(t, os.MkdirAll(filepath.Join(home, "secrets"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(home, "secrets", TokenSecretName), fileToken, 0o600))
			cfg := tt.config(home)

			tokenStore, err := cfg.TokenStore()

			require.NoError(t, err)
			require.Equal(t, tt.wantLocation(home), tokenStore.Location)
			require.Equal(t, tt.wantWritableStore, tokenStore.WritableStore != nil)
			token, err := tokenStore.Reader.Get(TokenSecretName)
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

	tokenStore, err := Config{SecretsMode: SecretsModeKeychain, KeychainPath: keychainPath}.TokenStore()

	require.NoError(t, err)
	require.Equal(t, keychainPath, tokenStore.Location)
	require.NotNil(t, tokenStore.WritableStore)
	_, err = tokenStore.Reader.Get(TokenSecretName)
	require.ErrorIs(t, err, keychain.ErrNoKeychain)
}

func TestTokenStoreRejectsUnknownMode(t *testing.T) {
	_, err := Config{SecretsMode: "vault"}.TokenStore()

	require.ErrorIs(t, err, ErrSecretsMode)
}

func TestLoadToken(t *testing.T) {
	errRead := errors.New("read")
	tests := []struct {
		name      string
		stored    []byte
		storeErr  error
		want      string
		wantCause error
	}{
		{name: "токен как есть", stored: []byte("pat-secret"), want: "pat-secret"},
		{name: "пробелы и перевод строки обрезаются", stored: []byte("  pat-secret\r\n"), want: "pat-secret"},
		{name: "пустой токен", stored: []byte(" \n"), wantCause: ErrTokenUnavailable},
		{name: "нет секрета", stored: []byte("pat-secret"), storeErr: secret.ErrNotFound, wantCause: secret.ErrNotFound},
		{name: "ошибка хранилища сохраняется в цепочке ошибок", stored: []byte("pat-secret"), storeErr: errRead, wantCause: errRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenReader := mocks.NewMockReader(gomock.NewController(t))
			tokenReader.EXPECT().Get(TokenSecretName).Return(tt.stored, tt.storeErr)

			got, err := LoadToken(tokenReader)

			if tt.wantCause != nil {
				require.ErrorIs(t, err, ErrTokenUnavailable)
				require.ErrorIs(t, err, tt.wantCause)
				require.NotContains(t, err.Error(), "pat-secret")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
