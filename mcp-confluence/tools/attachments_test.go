package tools_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-confluence/config"
	"scoped-mcp/mcp-confluence/confluence"
	"scoped-mcp/mcp-confluence/confluence/mocks"
	"scoped-mcp/mcp-confluence/tools"
)

var (
	reportAttachment = confluence.Attachment{ID: "501", Title: "report.md", MediaType: "text/markdown", Size: 8, Version: 2}
	schemeAttachment = confluence.Attachment{ID: "502", Title: "scheme.drawio.png", MediaType: "image/png", Size: 1024, Version: 1}
)

func withDownloadDir(t *testing.T) (config.Config, string) {
	t.Helper()
	downloadDir := filepath.Join(t.TempDir(), "confluence")
	cfg := toolConfig
	attachments := *toolConfig.Attachments
	attachments.DownloadDir = downloadDir
	cfg.Attachments = &attachments
	return cfg, downloadDir
}

func TestListAttachments(t *testing.T) {
	tests := []struct {
		name    string
		pageID  string
		expect  func(attachmentsClient *mocks.MockAttachments)
		want    tools.ListAttachmentsOutput
		wantErr error
	}{
		{
			name: "два вложения", pageID: "101",
			expect: func(attachmentsClient *mocks.MockAttachments) {
				attachmentsClient.EXPECT().ListAttachments(gomock.Any(), "101").Return([]confluence.Attachment{reportAttachment, schemeAttachment}, nil)
			},
			want: tools.ListAttachmentsOutput{Attachments: []tools.AttachmentOutput{
				{ID: "501", Title: "report.md", MediaType: "text/markdown", Size: 8, Version: 2},
				{ID: "502", Title: "scheme.drawio.png", MediaType: "image/png", Size: 1024, Version: 1},
			}},
		},
		{
			name: "id с префиксом att отдаётся как есть", pageID: "101",
			expect: func(attachmentsClient *mocks.MockAttachments) {
				attachmentsClient.EXPECT().ListAttachments(gomock.Any(), "101").Return([]confluence.Attachment{{ID: "att507", Title: "notes.md", Version: 1}}, nil)
			},
			want: tools.ListAttachmentsOutput{Attachments: []tools.AttachmentOutput{{ID: "att507", Title: "notes.md", Version: 1}}},
		},
		{
			name: "без вложений", pageID: "101",
			expect: func(attachmentsClient *mocks.MockAttachments) {
				attachmentsClient.EXPECT().ListAttachments(gomock.Any(), "101").Return([]confluence.Attachment{}, nil)
			},
			want: tools.ListAttachmentsOutput{Attachments: []tools.AttachmentOutput{}},
		},
		{
			name: "ошибка клиента", pageID: "300",
			expect: func(attachmentsClient *mocks.MockAttachments) {
				attachmentsClient.EXPECT().ListAttachments(gomock.Any(), "300").Return(nil, confluence.ErrNotFound)
			},
			wantErr: confluence.ErrNotFound,
		},
		{name: "page_id не число", pageID: "x", wantErr: tools.ErrInvalidContentID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolset, clients := newToolset(t, toolConfig)
			if tt.expect != nil {
				tt.expect(clients.attachments)
			}

			got, err := toolset.ListAttachments(t.Context(), tools.ListAttachmentsInput{PageID: tt.pageID})

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGetAttachment(t *testing.T) {
	dotDotAttachment := confluence.Attachment{ID: "503", Title: "..", Version: 1}
	slashAttachment := confluence.Attachment{ID: "504", Title: "sub/evil.md", Version: 1}
	dotFileAttachment := confluence.Attachment{ID: "505", Title: ".zshrc", Version: 1}
	traversalAttachment := confluence.Attachment{ID: "506", Title: "../../.zshrc", Version: 1}
	prefixedAttachment := confluence.Attachment{ID: "att507", Title: "notes.md", Version: 1}
	listed := []confluence.Attachment{reportAttachment, schemeAttachment, dotDotAttachment, slashAttachment, dotFileAttachment, traversalAttachment, prefixedAttachment}
	listedOnPage101 := func(attachmentsClient *mocks.MockAttachments) *gomock.Call {
		return attachmentsClient.EXPECT().ListAttachments(gomock.Any(), "101").Return(listed, nil)
	}
	listedThenDownloaded := func(attachment confluence.Attachment, content []byte, downloadErr error) func(attachmentsClient *mocks.MockAttachments) {
		return func(attachmentsClient *mocks.MockAttachments) {
			gomock.InOrder(
				listedOnPage101(attachmentsClient),
				attachmentsClient.EXPECT().DownloadAttachment(gomock.Any(), "101", attachment, int64(1024)).Return(content, downloadErr),
			)
		}
	}
	tests := []struct {
		name         string
		input        tools.GetAttachmentInput
		expect       func(attachmentsClient *mocks.MockAttachments)
		existingFile string
		wantFile     string
		wantContent  string
		wantErr      error
	}{
		{
			name: "сохраняется в download_dir под именем вложения", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "501"},
			expect:   listedThenDownloaded(reportAttachment, []byte("# Отчёт\n"), nil),
			wantFile: "report.md", wantContent: "# Отчёт\n",
		},
		{
			name: "attachment_id с префиксом att, в списке с префиксом", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "att507"},
			expect:   listedThenDownloaded(prefixedAttachment, []byte("notes"), nil),
			wantFile: "notes.md", wantContent: "notes",
		},
		{
			name: "attachment_id без префикса, в списке с префиксом att", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "507"},
			expect:   listedThenDownloaded(prefixedAttachment, []byte("notes"), nil),
			wantFile: "notes.md", wantContent: "notes",
		},
		{
			name: "attachment_id с префиксом att, в списке без префикса", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "att501"},
			expect:   listedThenDownloaded(reportAttachment, []byte("# Отчёт\n"), nil),
			wantFile: "report.md", wantContent: "# Отчёт\n",
		},
		{
			name: "существующий файл перезаписывается с правами 0600", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "501"},
			expect:       listedThenDownloaded(reportAttachment, []byte("new"), nil),
			existingFile: "report.md", wantFile: "report.md", wantContent: "new",
		},
		{
			name: "dot-файл получает префикс", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "505"},
			expect:   listedThenDownloaded(dotFileAttachment, []byte("x"), nil),
			wantFile: "_.zshrc", wantContent: "x",
		},
		{
			name: "title со слэшем → ErrUnsafeAttachmentTitle, скачивания нет", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "504"},
			expect:  func(attachmentsClient *mocks.MockAttachments) { listedOnPage101(attachmentsClient) },
			wantErr: tools.ErrUnsafeAttachmentTitle,
		},
		{
			name: "title с выходом из каталога → ErrUnsafeAttachmentTitle, скачивания нет", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "506"},
			expect:  func(attachmentsClient *mocks.MockAttachments) { listedOnPage101(attachmentsClient) },
			wantErr: tools.ErrUnsafeAttachmentTitle,
		},
		{
			name: "title .. → ErrUnsafeAttachmentTitle, скачивания нет", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "503"},
			expect:  func(attachmentsClient *mocks.MockAttachments) { listedOnPage101(attachmentsClient) },
			wantErr: tools.ErrUnsafeAttachmentTitle,
		},
		{
			name: "вложения с таким id нет → ErrNotFound", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "999"},
			expect:  func(attachmentsClient *mocks.MockAttachments) { listedOnPage101(attachmentsClient) },
			wantErr: confluence.ErrNotFound,
		},
		{
			name: "страница недоступна", input: tools.GetAttachmentInput{PageID: "300", AttachmentID: "501"},
			expect: func(attachmentsClient *mocks.MockAttachments) {
				attachmentsClient.EXPECT().ListAttachments(gomock.Any(), "300").Return(nil, confluence.ErrNotFound)
			},
			wantErr: confluence.ErrNotFound,
		},
		{
			name: "слишком большое вложение — файл не создаётся", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "502"},
			expect:  listedThenDownloaded(schemeAttachment, nil, confluence.ErrAttachmentTooLarge),
			wantErr: confluence.ErrAttachmentTooLarge,
		},
		{name: "page_id не число", input: tools.GetAttachmentInput{PageID: "x", AttachmentID: "501"}, wantErr: tools.ErrInvalidContentID},
		{name: "attachment_id не число", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "x"}, wantErr: tools.ErrInvalidAttachmentID},
		{name: "attachment_id — только att", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "att"}, wantErr: tools.ErrInvalidAttachmentID},
		{name: "attachment_id с ведущим нулём после att", input: tools.GetAttachmentInput{PageID: "101", AttachmentID: "att0507"}, wantErr: tools.ErrInvalidAttachmentID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, downloadDir := withDownloadDir(t)
			if tt.existingFile != "" {
				require.NoError(t, os.MkdirAll(downloadDir, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(downloadDir, tt.existingFile), []byte("old"), 0o644))
			}
			toolset, clients := newToolset(t, cfg)
			if tt.expect != nil {
				tt.expect(clients.attachments)
			}

			got, err := toolset.GetAttachment(t.Context(), tt.input)

			require.ErrorIs(t, err, tt.wantErr)
			if tt.wantErr != nil {
				require.Equal(t, tools.GetAttachmentOutput{}, got)
				require.NoDirExists(t, downloadDir)
				return
			}
			wantPath := filepath.Join(downloadDir, tt.wantFile)
			require.Equal(t, tools.GetAttachmentOutput{Path: wantPath}, got)
			content, err := os.ReadFile(wantPath)
			require.NoError(t, err)
			require.Equal(t, tt.wantContent, string(content))
			info, err := os.Lstat(wantPath)
			require.NoError(t, err)
			require.True(t, info.Mode().IsRegular())
			require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			entries, err := os.ReadDir(downloadDir)
			require.NoError(t, err)
			require.Len(t, entries, 1)
		})
	}
}

func TestGetAttachmentReplacesSymlinkInsteadOfFollowingIt(t *testing.T) {
	cfg, downloadDir := withDownloadDir(t)
	outsidePath := filepath.Join(t.TempDir(), "outside.md")
	require.NoError(t, os.WriteFile(outsidePath, []byte("outside"), 0o600))
	require.NoError(t, os.MkdirAll(downloadDir, 0o700))
	require.NoError(t, os.Symlink(outsidePath, filepath.Join(downloadDir, reportAttachment.Title)))
	toolset, clients := newToolset(t, cfg)
	clients.attachments.EXPECT().ListAttachments(gomock.Any(), "101").Return([]confluence.Attachment{reportAttachment}, nil)
	clients.attachments.EXPECT().DownloadAttachment(gomock.Any(), "101", reportAttachment, int64(1024)).Return([]byte("new"), nil)

	got, err := toolset.GetAttachment(t.Context(), tools.GetAttachmentInput{PageID: "101", AttachmentID: "501"})

	require.NoError(t, err)
	info, err := os.Lstat(got.Path)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
	outsideContent, err := os.ReadFile(outsidePath)
	require.NoError(t, err)
	require.Equal(t, "outside", string(outsideContent))
}

func TestGetAttachmentDownloadWriteFails(t *testing.T) {
	longTitleAttachment := confluence.Attachment{ID: "507", Title: strings.Repeat("a", 256) + ".md", Version: 1}
	tests := []struct {
		name               string
		attachment         confluence.Attachment
		occupyDownloadPath bool
	}{
		{name: "на месте download_dir обычный файл", attachment: reportAttachment, occupyDownloadPath: true},
		{name: "имя длиннее 255 байт", attachment: longTitleAttachment},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, downloadDir := withDownloadDir(t)
			if tt.occupyDownloadPath {
				require.NoError(t, os.WriteFile(downloadDir, []byte("not a directory"), 0o600))
			}
			toolset, clients := newToolset(t, cfg)
			gomock.InOrder(
				clients.attachments.EXPECT().ListAttachments(gomock.Any(), "101").Return([]confluence.Attachment{tt.attachment}, nil),
				clients.attachments.EXPECT().DownloadAttachment(gomock.Any(), "101", tt.attachment, int64(1024)).Return([]byte("new"), nil),
			)

			got, err := toolset.GetAttachment(t.Context(), tools.GetAttachmentInput{PageID: "101", AttachmentID: tt.attachment.ID})

			require.ErrorIs(t, err, tools.ErrDownloadWrite)
			require.Equal(t, tools.GetAttachmentOutput{}, got)
			entries, _ := os.ReadDir(downloadDir)
			require.Empty(t, entries)
		})
	}
}

type dirEntrySnapshot struct {
	info    os.FileInfo
	content string
}

func snapshotDir(t *testing.T, dir string) map[string]dirEntrySnapshot {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	snapshot := make(map[string]dirEntrySnapshot, len(entries))
	for _, entry := range entries {
		entryPath := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(entryPath)
		require.NoError(t, err)
		content, err := os.ReadFile(entryPath)
		require.NoError(t, err)
		snapshot[entry.Name()] = dirEntrySnapshot{info: info, content: string(content)}
	}
	return snapshot
}

func requireSameDirSnapshot(t *testing.T, want, got map[string]dirEntrySnapshot) {
	t.Helper()
	require.Len(t, got, len(want))
	for name, wantEntry := range want {
		gotEntry, found := got[name]
		require.True(t, found, name)
		require.True(t, os.SameFile(wantEntry.info, gotEntry.info), name)
		require.Equal(t, wantEntry.content, gotEntry.content, name)
	}
}

func TestGetAttachmentRefusesToOverwriteSecretFile(t *testing.T) {
	writeFile := func(t *testing.T, path string) string {
		t.Helper()
		require.NoError(t, os.WriteFile(path, []byte("secret"), 0o600))
		return path
	}
	tests := []struct {
		name    string
		title   string
		arrange func(t *testing.T, downloadDir string, cfg *config.Config) (toolsetConfigPath string)
	}{
		{
			name: "конфиг — обычный файл в download_dir", title: "config.yaml",
			arrange: func(t *testing.T, downloadDir string, _ *config.Config) string {
				return writeFile(t, filepath.Join(downloadDir, "config.yaml"))
			},
		},
		{
			name: "путь конфига — симлинк в download_dir на файл вне его", title: "config.yaml",
			arrange: func(t *testing.T, downloadDir string, _ *config.Config) string {
				configInDownloadDir := filepath.Join(downloadDir, "config.yaml")
				require.NoError(t, os.Symlink(writeFile(t, filepath.Join(t.TempDir(), "config.yaml")), configInDownloadDir))
				return configInDownloadDir
			},
		},
		{
			name: "только Lstat: путь конфига ведёт через симлинк на download_dir к симлинку на файл вне его", title: "config.yaml",
			arrange: func(t *testing.T, downloadDir string, _ *config.Config) string {
				require.NoError(t, os.Symlink(writeFile(t, filepath.Join(t.TempDir(), "config.yaml")), filepath.Join(downloadDir, "config.yaml")))
				linkToDownloadDir := filepath.Join(t.TempDir(), "link")
				require.NoError(t, os.Symlink(downloadDir, linkToDownloadDir))
				return filepath.Join(linkToDownloadDir, "config.yaml")
			},
		},
		{
			name: "только Stat: путь конфига — симлинк вне download_dir на файл в нём", title: "config.yaml",
			arrange: func(t *testing.T, downloadDir string, _ *config.Config) string {
				linkToConfig := filepath.Join(t.TempDir(), "config.yaml")
				require.NoError(t, os.Symlink(writeFile(t, filepath.Join(downloadDir, "config.yaml")), linkToConfig))
				return linkToConfig
			},
		},
		{
			name: "только лексически: файла токена ещё нет, download_dir — secrets_dir", title: config.TokenSecretName,
			arrange: func(_ *testing.T, downloadDir string, cfg *config.Config) string {
				cfg.SecretsMode = config.SecretsModeFile
				cfg.SecretsDir = downloadDir
				return configPath
			},
		},
		{
			name: "файл токена в download_dir", title: config.TokenSecretName,
			arrange: func(t *testing.T, downloadDir string, cfg *config.Config) string {
				cfg.SecretsMode = config.SecretsModeFile
				cfg.SecretsDir = downloadDir
				writeFile(t, filepath.Join(downloadDir, config.TokenSecretName))
				return configPath
			},
		},
		{
			name: "связка ключей в download_dir", title: "scoped-mcp.keychain-db",
			arrange: func(t *testing.T, downloadDir string, cfg *config.Config) string {
				cfg.KeychainPath = writeFile(t, filepath.Join(downloadDir, "scoped-mcp.keychain-db"))
				return configPath
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, downloadDir := withDownloadDir(t)
			require.NoError(t, os.MkdirAll(downloadDir, 0o700))
			toolsetConfigPath := tt.arrange(t, downloadDir, &cfg)
			downloadDirBefore := snapshotDir(t, downloadDir)
			secretAttachment := confluence.Attachment{ID: "508", Title: tt.title, Version: 1}
			toolset, clients := newToolsetWithConfigPath(t, cfg, toolsetConfigPath)
			gomock.InOrder(
				clients.attachments.EXPECT().ListAttachments(gomock.Any(), "101").Return([]confluence.Attachment{secretAttachment}, nil),
				clients.attachments.EXPECT().DownloadAttachment(gomock.Any(), "101", secretAttachment, int64(1024)).Return([]byte("evil"), nil),
			)

			got, err := toolset.GetAttachment(t.Context(), tools.GetAttachmentInput{PageID: "101", AttachmentID: "508"})

			require.ErrorIs(t, err, tools.ErrAttachmentSecretFile)
			require.Equal(t, tools.GetAttachmentOutput{}, got)
			requireSameDirSnapshot(t, downloadDirBefore, snapshotDir(t, downloadDir))
		})
	}
}

func TestSafeFileName(t *testing.T) {
	tests := []struct {
		title   string
		want    string
		wantErr error
	}{
		{title: "report.md", want: "report.md"},
		{title: "отчёт v2,final.png", want: "отчёт v2,final.png"},
		{title: ".zshrc", want: "_.zshrc"},
		{title: ".ssh", want: "_.ssh"},
		{title: "...", want: "_..."},
		{title: "a\u2215b.md", want: "a\u2215b.md"},
		{title: "a\uff0fb.md", want: "a\uff0fb.md"},
		{title: "../../.zshrc", wantErr: tools.ErrUnsafeAttachmentTitle},
		{title: "a/b", wantErr: tools.ErrUnsafeAttachmentTitle},
		{title: `a\b`, wantErr: tools.ErrUnsafeAttachmentTitle},
		{title: "", wantErr: tools.ErrUnsafeAttachmentTitle},
		{title: ".", wantErr: tools.ErrUnsafeAttachmentTitle},
		{title: "..", wantErr: tools.ErrUnsafeAttachmentTitle},
		{title: "a\x00b.md", wantErr: tools.ErrUnsafeAttachmentTitle},
		{title: "a\nb.md", wantErr: tools.ErrUnsafeAttachmentTitle},
		{title: "a\u200bb.md", wantErr: tools.ErrUnsafeAttachmentTitle},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			got, err := tools.SafeFileName(tt.title)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestUploadAttachment(t *testing.T) {
	uploadDir := t.TempDir()
	realUploadDir, err := filepath.EvalSymlinks(uploadDir)
	require.NoError(t, err)
	reportPath := filepath.Join(realUploadDir, "отчёт.md")
	require.NoError(t, os.WriteFile(reportPath, []byte("# Отчёт\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(realUploadDir, "run.sh"), []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(realUploadDir, "big.md"), make([]byte, 1025), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(realUploadDir, "a;b.md"), []byte("x"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(realUploadDir, "dir.md"), 0o700))
	linkPath := filepath.Join(realUploadDir, "link.md")
	require.NoError(t, os.Symlink(reportPath, linkPath))
	outsidePath := filepath.Join(t.TempDir(), "outside.md")
	require.NoError(t, os.WriteFile(outsidePath, []byte("x"), 0o600))
	linkToOutsidePath := filepath.Join(realUploadDir, "link-to-outside.md")
	require.NoError(t, os.Symlink(outsidePath, linkToOutsidePath))
	configInUploadDir := filepath.Join(realUploadDir, "config.yaml")
	require.NoError(t, os.WriteFile(configInUploadDir, []byte("x"), 0o600))
	linkToConfigPath := filepath.Join(realUploadDir, "link-to-config.yaml")
	require.NoError(t, os.Symlink(configInUploadDir, linkToConfigPath))
	secretsDir := filepath.Join(realUploadDir, "secrets")
	require.NoError(t, os.Mkdir(secretsDir, 0o700))
	tokenPath := filepath.Join(secretsDir, config.TokenSecretName)
	require.NoError(t, os.WriteFile(tokenPath, []byte("x"), 0o600))
	hardLinkToTokenPath := filepath.Join(realUploadDir, "token-copy.md")
	require.NoError(t, os.Link(tokenPath, hardLinkToTokenPath))
	keychainPath := filepath.Join(realUploadDir, "scoped-mcp.keychain-db")
	require.NoError(t, os.WriteFile(keychainPath, []byte("x"), 0o600))
	cfg := toolConfig
	cfg.SecretsMode = config.SecretsModeFile
	cfg.SecretsDir = secretsDir
	cfg.KeychainPath = keychainPath
	attachments := *toolConfig.Attachments
	attachments.UploadDirs = []string{uploadDir}
	attachments.Extensions = []string{".md", ".yaml", ".keychain-db"}
	cfg.Attachments = &attachments
	uploaded := confluence.Attachment{ID: "501", Title: "отчёт.md", MediaType: "text/markdown", Size: 8, Version: 1}
	tests := []struct {
		name    string
		input   tools.UploadAttachmentInput
		expect  func(attachmentsClient *mocks.MockAttachments)
		want    tools.UploadAttachmentOutput
		wantErr error
	}{
		{
			name: "файл из upload_dirs загружается под своим именем", input: tools.UploadAttachmentInput{PageID: "201", Path: reportPath},
			expect: func(attachmentsClient *mocks.MockAttachments) {
				attachmentsClient.EXPECT().UploadAttachment(gomock.Any(), "201", confluence.AttachmentFile{Name: "отчёт.md", Content: []byte("# Отчёт\n")}).Return(uploaded, nil)
			},
			want: tools.UploadAttachmentOutput{ID: "501", Title: "отчёт.md", Version: 1},
		},
		{
			name: "симлинк: имя и содержимое целевого файла", input: tools.UploadAttachmentInput{PageID: "201", Path: linkPath},
			expect: func(attachmentsClient *mocks.MockAttachments) {
				attachmentsClient.EXPECT().UploadAttachment(gomock.Any(), "201", confluence.AttachmentFile{Name: "отчёт.md", Content: []byte("# Отчёт\n")}).Return(uploaded, nil)
			},
			want: tools.UploadAttachmentOutput{ID: "501", Title: "отчёт.md", Version: 1},
		},
		{
			name: "ошибка клиента", input: tools.UploadAttachmentInput{PageID: "201", Path: reportPath},
			expect: func(attachmentsClient *mocks.MockAttachments) {
				attachmentsClient.EXPECT().UploadAttachment(gomock.Any(), "201", gomock.Any()).Return(confluence.Attachment{}, confluence.ErrMaybeWritten)
			},
			wantErr: confluence.ErrMaybeWritten,
		},
		{name: "page_id не число", input: tools.UploadAttachmentInput{PageID: "x", Path: reportPath}, wantErr: tools.ErrInvalidContentID},
		{name: "пустой путь", input: tools.UploadAttachmentInput{PageID: "201", Path: ""}, wantErr: tools.ErrInvalidAttachmentPath},
		{name: "относительный путь", input: tools.UploadAttachmentInput{PageID: "201", Path: "отчёт.md"}, wantErr: tools.ErrInvalidAttachmentPath},
		{name: "файла нет", input: tools.UploadAttachmentInput{PageID: "201", Path: filepath.Join(realUploadDir, "missing.md")}, wantErr: tools.ErrInvalidAttachmentPath},
		{name: "имя не годится для вложения", input: tools.UploadAttachmentInput{PageID: "201", Path: filepath.Join(realUploadDir, "a;b.md")}, wantErr: tools.ErrInvalidAttachmentPath},
		{name: "вне upload_dirs", input: tools.UploadAttachmentInput{PageID: "201", Path: outsidePath}, wantErr: tools.ErrAttachmentOutsideDirs},
		{name: "каталог вместо файла", input: tools.UploadAttachmentInput{PageID: "201", Path: filepath.Join(realUploadDir, "dir.md")}, wantErr: tools.ErrAttachmentNotRegularFile},
		{name: "расширение не из конфига", input: tools.UploadAttachmentInput{PageID: "201", Path: filepath.Join(realUploadDir, "run.sh")}, wantErr: tools.ErrAttachmentExtensionNotAllowed},
		{name: "больше max_bytes", input: tools.UploadAttachmentInput{PageID: "201", Path: filepath.Join(realUploadDir, "big.md")}, wantErr: tools.ErrAttachmentFileTooLarge},
		{name: "файл конфига с разрешённым расширением", input: tools.UploadAttachmentInput{PageID: "201", Path: configInUploadDir}, wantErr: tools.ErrAttachmentSecretFile},
		{name: "файл токена без расширения", input: tools.UploadAttachmentInput{PageID: "201", Path: tokenPath}, wantErr: tools.ErrAttachmentSecretFile},
		{name: "связка ключей с разрешённым расширением", input: tools.UploadAttachmentInput{PageID: "201", Path: keychainPath}, wantErr: tools.ErrAttachmentSecretFile},
		{name: "симлинк внутри upload_dirs на файл вне каталогов", input: tools.UploadAttachmentInput{PageID: "201", Path: linkToOutsidePath}, wantErr: tools.ErrAttachmentOutsideDirs},
		{name: "жёсткая ссылка на файл токена", input: tools.UploadAttachmentInput{PageID: "201", Path: hardLinkToTokenPath}, wantErr: tools.ErrAttachmentSecretFile},
		{name: "симлинк на файл конфига", input: tools.UploadAttachmentInput{PageID: "201", Path: linkToConfigPath}, wantErr: tools.ErrAttachmentSecretFile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolset, clients := newToolsetWithConfigPath(t, cfg, configInUploadDir)
			if tt.expect != nil {
				tt.expect(clients.attachments)
			}

			got, err := toolset.UploadAttachment(t.Context(), tt.input)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}
