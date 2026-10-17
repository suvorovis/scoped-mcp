package tools_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-jira/config"
	"scoped-mcp/mcp-jira/jira"
	"scoped-mcp/mcp-jira/jira/mocks"
	"scoped-mcp/mcp-jira/tools"
)

const (
	attachIssueKey   = "PROJ-7790"
	attachMaxBytes   = 16
	rootPlaceholder  = "{root}"
	reportContent    = "# report"
	schemeContent    = "png"
	notesContent     = "notes"
	planContent      = "plan"
	attachedIssueURL = "https://jira.example.com/browse/PROJ-7790"
	attachedReportID = "10501"
	attachedSchemeID = "10502"
)

type attachFixture struct {
	root       string
	cfg        config.Config
	configPath string
}

func newAttachFixture(t *testing.T) attachFixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	out := filepath.Join(root, "out")
	writeFile(t, filepath.Join(out, "report.md"), reportContent)
	writeFile(t, filepath.Join(out, "scheme.PNG"), schemeContent)
	writeFile(t, filepath.Join(out, "notes.txt"), notesContent)
	writeFile(t, filepath.Join(out, "big.md"), strings.Repeat("x", attachMaxBytes+1))
	writeFile(t, filepath.Join(out, "run.sh"), "echo")
	writeFile(t, filepath.Join(out, config.TokenSecretName), "pat-secret")
	writeFile(t, filepath.Join(out, "scoped-mcp.keychain-db"), "keychain")
	writeFile(t, filepath.Join(out, "config.yaml"), "base_url: x")
	writeFile(t, filepath.Join(root, "outside", "secret.md"), "secret")
	writeFile(t, filepath.Join(root, "out2", "report.md"), reportContent)
	writeFile(t, filepath.Join(root, "shared", "plan.md"), planContent)
	writeFile(t, filepath.Join(out, "locked.md"), "locked")
	writeFile(t, filepath.Join(out, `back\slash.md`), "backslash")
	writeFile(t, filepath.Join(out, "line\nbreak.md"), "line break")
	writeFile(t, filepath.Join(out, "delete\x7fchar.md"), "delete char")
	require.NoError(t, os.Chmod(filepath.Join(out, "locked.md"), 0o000))
	require.NoError(t, os.Mkdir(filepath.Join(out, "dir.md"), 0o700))
	require.NoError(t, syscall.Mkfifo(filepath.Join(out, "fifo.md"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(out, "report.md"), filepath.Join(out, "link-to-report.md")))
	require.NoError(t, os.Symlink(filepath.Join(root, "outside", "secret.md"), filepath.Join(out, "link-outside.md")))
	require.NoError(t, os.Symlink(filepath.Join(out, config.TokenSecretName), filepath.Join(out, "link-token.md")))
	require.NoError(t, os.Link(filepath.Join(out, config.TokenSecretName), filepath.Join(out, "hardlink-token.md")))
	require.NoError(t, os.Symlink(filepath.Join(root, "shared"), filepath.Join(root, "shared-link")))

	cfg := toolConfig
	cfg.Tools.AttachFiles = true
	cfg.SecretsDir = out
	cfg.KeychainPath = filepath.Join(out, "scoped-mcp.keychain-db")
	cfg.Attachments = &config.Attachments{
		Dirs:       []string{out, filepath.Join(root, "shared-link")},
		Extensions: []string{".md", ".png", ".txt"},
		MaxBytes:   attachMaxBytes,
	}
	return attachFixture{root: root, cfg: cfg, configPath: filepath.Join(out, "config.yaml")}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func inOut(name string) string {
	return rootPlaceholder + "/out/" + name
}

func (f attachFixture) paths(pathTemplates []string) []string {
	paths := make([]string, 0, len(pathTemplates))
	for _, pathTemplate := range pathTemplates {
		paths = append(paths, strings.ReplaceAll(pathTemplate, rootPlaceholder, f.root))
	}
	return paths
}

type attachCall struct {
	name       string
	content    string
	attachment jira.Attachment
	err        error
	onUpload   func(root string)
}

func (c attachCall) failing(err error) attachCall {
	c.attachment = jira.Attachment{}
	c.err = err
	return c
}

func (c attachCall) whileUploading(hook func(root string)) attachCall {
	c.onUpload = hook
	return c
}

func expectAttachments(calls ...attachCall) func(attacher *mocks.MockAttacher, root string) {
	return func(attacher *mocks.MockAttacher, root string) {
		ordered := make([]any, 0, len(calls))
		for _, call := range calls {
			file := jira.AttachmentFile{Name: call.name, Content: []byte(call.content)}
			ordered = append(ordered, attacher.EXPECT().AttachFile(gomock.Any(), attachIssueKey, file).DoAndReturn(
				func(_ any, _ string, _ jira.AttachmentFile) (jira.Attachment, error) {
					if call.onUpload != nil {
						call.onUpload(root)
					}
					return call.attachment, call.err
				}))
		}
		gomock.InOrder(ordered...)
	}
}

var (
	reportAttached = attachCall{name: "report.md", content: reportContent, attachment: jira.Attachment{ID: attachedReportID}}
	schemeAttached = attachCall{name: "scheme.PNG", content: schemeContent, attachment: jira.Attachment{ID: attachedSchemeID}}
)

func growNotes(root string) {
	_ = os.WriteFile(filepath.Join(root, "out", "notes.txt"), []byte(strings.Repeat("x", attachMaxBytes+1)), 0o600)
}

func replaceNotes(root string) {
	notes := filepath.Join(root, "out", "notes.txt")
	replacement := notes + ".new"
	_ = os.WriteFile(replacement, []byte(notesContent), 0o600)
	_ = os.Rename(replacement, notes)
}

func removeNotes(root string) {
	_ = os.Remove(filepath.Join(root, "out", "notes.txt"))
}

func lockNotes(root string) {
	_ = os.Chmod(filepath.Join(root, "out", "notes.txt"), 0o000)
}

func replaceNotesWithFIFO(root string) {
	notes := filepath.Join(root, "out", "notes.txt")
	replacement := notes + ".fifo"
	_ = syscall.Mkfifo(replacement, 0o600)
	_ = os.Rename(replacement, notes)
}

func TestAttachFiles(t *testing.T) {
	errNetwork := errors.New("connection reset")
	attachedReport := tools.AttachedFile{Name: "report.md", ID: attachedReportID}
	tests := []struct {
		name          string
		issueKey      string
		pathTemplates []string
		expect        func(attacher *mocks.MockAttacher, root string)
		want          tools.AttachFilesOutput
		wantErr       error
	}{
		{
			name:          "два файла по абсолютному пути и от ~/ в порядке paths",
			issueKey:      attachIssueKey,
			pathTemplates: []string{inOut("report.md"), "~/out/scheme.PNG"},
			expect:        expectAttachments(reportAttached, schemeAttached),
			want: tools.AttachFilesOutput{
				IssueKey: attachIssueKey,
				URL:      attachedIssueURL,
				Attached: []tools.AttachedFile{attachedReport, {Name: "scheme.PNG", ID: attachedSchemeID}},
			},
		},
		{
			name:          "симлинк внутри каталога → имя реального файла",
			issueKey:      attachIssueKey,
			pathTemplates: []string{inOut("link-to-report.md")},
			expect:        expectAttachments(reportAttached),
			want:          tools.AttachFilesOutput{IssueKey: attachIssueKey, URL: attachedIssueURL, Attached: []tools.AttachedFile{attachedReport}},
		},
		{
			name:          "каталог из dirs — симлинк, файл по реальному пути",
			issueKey:      attachIssueKey,
			pathTemplates: []string{rootPlaceholder + "/shared/plan.md"},
			expect:        expectAttachments(attachCall{name: "plan.md", content: planContent, attachment: jira.Attachment{ID: "1"}}),
			want:          tools.AttachFilesOutput{IssueKey: attachIssueKey, URL: attachedIssueURL, Attached: []tools.AttachedFile{{Name: "plan.md", ID: "1"}}},
		},
		{
			name:          "задача второго проекта из конфига",
			issueKey:      "OTHER-1",
			pathTemplates: []string{inOut("report.md")},
			expect: func(attacher *mocks.MockAttacher, _ string) {
				attacher.EXPECT().AttachFile(gomock.Any(), "OTHER-1", gomock.Any()).Return(jira.Attachment{ID: "1"}, nil)
			},
			want: tools.AttachFilesOutput{IssueKey: "OTHER-1", URL: "https://jira.example.com/browse/OTHER-1", Attached: []tools.AttachedFile{{Name: "report.md", ID: "1"}}},
		},

		{name: "ключ задачи строчными", issueKey: "proj-1", pathTemplates: []string{inOut("report.md")}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "задача проекта не из конфига", issueKey: "SECRET-1", pathTemplates: []string{inOut("report.md")}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "числовой ID задачи", issueKey: "10001", pathTemplates: []string{inOut("report.md")}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "ключ задачи проверяется раньше путей", issueKey: "SECRET-1", pathTemplates: []string{"relative.md"}, wantErr: tools.ErrIssueKeyInvalid},

		{name: "пустой список путей", issueKey: attachIssueKey, pathTemplates: nil, wantErr: tools.ErrAttachmentPath},
		{name: "пустой путь", issueKey: attachIssueKey, pathTemplates: []string{""}, wantErr: tools.ErrAttachmentPath},
		{name: "относительный путь", issueKey: attachIssueKey, pathTemplates: []string{"out/report.md"}, wantErr: tools.ErrAttachmentPath},
		{name: "путь от ~user", issueKey: attachIssueKey, pathTemplates: []string{"~bot/out/report.md"}, wantErr: tools.ErrAttachmentPath},
		{name: "файла нет", issueKey: attachIssueKey, pathTemplates: []string{inOut("missing.md")}, wantErr: tools.ErrAttachmentPath},
		{name: "путь повторяется", issueKey: attachIssueKey, pathTemplates: []string{inOut("report.md"), "~/out/report.md"}, wantErr: tools.ErrAttachmentPath},
		{name: "файл повторяется через симлинк", issueKey: attachIssueKey, pathTemplates: []string{inOut("report.md"), inOut("link-to-report.md")}, wantErr: tools.ErrAttachmentPath},
		{name: "файл вне каталогов", issueKey: attachIssueKey, pathTemplates: []string{rootPlaceholder + "/outside/secret.md"}, wantErr: tools.ErrAttachmentOutsideDirs},
		{name: "каталог с тем же префиксом", issueKey: attachIssueKey, pathTemplates: []string{rootPlaceholder + "/out2/report.md"}, wantErr: tools.ErrAttachmentOutsideDirs},
		{name: "выход из каталога через ..", issueKey: attachIssueKey, pathTemplates: []string{inOut("../outside/secret.md")}, wantErr: tools.ErrAttachmentOutsideDirs},
		{name: "симлинк наружу", issueKey: attachIssueKey, pathTemplates: []string{inOut("link-outside.md")}, wantErr: tools.ErrAttachmentOutsideDirs},
		{name: "файл токена", issueKey: attachIssueKey, pathTemplates: []string{inOut(config.TokenSecretName)}, wantErr: tools.ErrAttachmentForbiddenFile},
		{name: "симлинк на файл токена", issueKey: attachIssueKey, pathTemplates: []string{inOut("link-token.md")}, wantErr: tools.ErrAttachmentForbiddenFile},
		{name: "жёсткая ссылка на файл токена", issueKey: attachIssueKey, pathTemplates: []string{inOut("hardlink-token.md")}, wantErr: tools.ErrAttachmentForbiddenFile},
		{name: "файл связки keychain", issueKey: attachIssueKey, pathTemplates: []string{inOut("scoped-mcp.keychain-db")}, wantErr: tools.ErrAttachmentForbiddenFile},
		{name: "файл конфига: запрет проверяется раньше расширения", issueKey: attachIssueKey, pathTemplates: []string{inOut("config.yaml")}, wantErr: tools.ErrAttachmentForbiddenFile},
		{name: "каталог", issueKey: attachIssueKey, pathTemplates: []string{inOut("dir.md")}, wantErr: tools.ErrAttachmentNotRegularFile},
		{name: "именованный канал", issueKey: attachIssueKey, pathTemplates: []string{inOut("fifo.md")}, wantErr: tools.ErrAttachmentNotRegularFile},
		{name: "расширение не из конфига", issueKey: attachIssueKey, pathTemplates: []string{inOut("run.sh")}, wantErr: tools.ErrAttachmentExtension},
		{name: "файл больше max_bytes", issueKey: attachIssueKey, pathTemplates: []string{inOut("big.md")}, wantErr: tools.ErrAttachmentTooLarge},
		{name: "плохой второй путь — ничего не загружается", issueKey: attachIssueKey, pathTemplates: []string{inOut("report.md"), inOut("run.sh")}, wantErr: tools.ErrAttachmentExtension},
		{name: "нечитаемый второй файл — ничего не загружается", issueKey: attachIssueKey, pathTemplates: []string{inOut("report.md"), inOut("locked.md")}, wantErr: tools.ErrAttachmentPath},
		{name: "обратный слэш в имени второго файла — ничего не загружается", issueKey: attachIssueKey, pathTemplates: []string{inOut("report.md"), inOut(`back\slash.md`)}, wantErr: tools.ErrAttachmentPath},
		{name: "перевод строки в имени второго файла — ничего не загружается", issueKey: attachIssueKey, pathTemplates: []string{inOut("report.md"), inOut("line\nbreak.md")}, wantErr: tools.ErrAttachmentPath},
		{name: "символ DEL в имени второго файла — ничего не загружается", issueKey: attachIssueKey, pathTemplates: []string{inOut("report.md"), inOut("delete\x7fchar.md")}, wantErr: tools.ErrAttachmentPath},

		{
			name:          "первый файл не загружен → ничего не приложено",
			issueKey:      attachIssueKey,
			pathTemplates: []string{inOut("report.md"), inOut("scheme.PNG")},
			expect:        expectAttachments(reportAttached.failing(jira.ErrUnauthorized)),
			wantErr: &tools.AttachFilesError{
				IssueKey: attachIssueKey, Attached: []tools.AttachedFile{}, StoppedAt: "report.md", NotAttempted: []string{"scheme.PNG"}, Err: jira.ErrUnauthorized,
			},
		},
		{
			name:          "второй файл не загружен → первый в ошибке, третий не загружается",
			issueKey:      attachIssueKey,
			pathTemplates: []string{inOut("report.md"), inOut("scheme.PNG"), inOut("notes.txt")},
			expect:        expectAttachments(reportAttached, schemeAttached.failing(errNetwork)),
			wantErr: &tools.AttachFilesError{
				IssueKey: attachIssueKey, Attached: []tools.AttachedFile{attachedReport}, StoppedAt: "scheme.PNG", NotAttempted: []string{"notes.txt"}, Err: errNetwork,
			},
		},
		{
			name:          "первый файл не подтверждён Jira → под вопросом, остальные не загружаются",
			issueKey:      attachIssueKey,
			pathTemplates: []string{inOut("report.md"), inOut("scheme.PNG")},
			expect:        expectAttachments(reportAttached.failing(jira.ErrAttachUnconfirmed)),
			wantErr: &tools.AttachFilesError{
				IssueKey: attachIssueKey, Attached: []tools.AttachedFile{}, StoppedAt: "report.md", NotAttempted: []string{"scheme.PNG"}, Err: jira.ErrAttachUnconfirmed,
			},
		},
		{
			name:          "второй, последний файл не подтверждён Jira → первый в ошибке, непопробованных нет",
			issueKey:      attachIssueKey,
			pathTemplates: []string{inOut("report.md"), inOut("scheme.PNG")},
			expect:        expectAttachments(reportAttached, schemeAttached.failing(jira.ErrAttachUnconfirmed)),
			wantErr: &tools.AttachFilesError{
				IssueKey: attachIssueKey, Attached: []tools.AttachedFile{attachedReport}, StoppedAt: "scheme.PNG", NotAttempted: []string{}, Err: jira.ErrAttachUnconfirmed,
			},
		},
		{
			name:          "файл вырос после проверки → не отправляется",
			issueKey:      attachIssueKey,
			pathTemplates: []string{inOut("report.md"), inOut("notes.txt")},
			expect:        expectAttachments(reportAttached.whileUploading(growNotes)),
			wantErr:       &tools.AttachFilesError{IssueKey: attachIssueKey, Attached: []tools.AttachedFile{attachedReport}, StoppedAt: "notes.txt", NotAttempted: []string{}, Err: tools.ErrAttachmentTooLarge},
		},
		{
			name:          "файл подменён после проверки → не отправляется",
			issueKey:      attachIssueKey,
			pathTemplates: []string{inOut("report.md"), inOut("notes.txt")},
			expect:        expectAttachments(reportAttached.whileUploading(replaceNotes)),
			wantErr:       &tools.AttachFilesError{IssueKey: attachIssueKey, Attached: []tools.AttachedFile{attachedReport}, StoppedAt: "notes.txt", NotAttempted: []string{}, Err: tools.ErrAttachmentChanged},
		},
		{
			name:          "файл удалён после проверки → ErrAttachmentChanged",
			issueKey:      attachIssueKey,
			pathTemplates: []string{inOut("report.md"), inOut("notes.txt")},
			expect:        expectAttachments(reportAttached.whileUploading(removeNotes)),
			wantErr:       &tools.AttachFilesError{IssueKey: attachIssueKey, Attached: []tools.AttachedFile{attachedReport}, StoppedAt: "notes.txt", NotAttempted: []string{}, Err: tools.ErrAttachmentChanged},
		},
		{
			name:          "файл стал нечитаемым после проверки → ErrAttachmentPath",
			issueKey:      attachIssueKey,
			pathTemplates: []string{inOut("report.md"), inOut("notes.txt")},
			expect:        expectAttachments(reportAttached.whileUploading(lockNotes)),
			wantErr:       &tools.AttachFilesError{IssueKey: attachIssueKey, Attached: []tools.AttachedFile{attachedReport}, StoppedAt: "notes.txt", NotAttempted: []string{}, Err: tools.ErrAttachmentPath},
		},
		{
			name:          "файл подменён именованным каналом после проверки → не зависает и не отправляется",
			issueKey:      attachIssueKey,
			pathTemplates: []string{inOut("report.md"), inOut("notes.txt")},
			expect:        expectAttachments(reportAttached.whileUploading(replaceNotesWithFIFO)),
			wantErr:       &tools.AttachFilesError{IssueKey: attachIssueKey, Attached: []tools.AttachedFile{attachedReport}, StoppedAt: "notes.txt", NotAttempted: []string{}, Err: tools.ErrAttachmentChanged},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newAttachFixture(t)
			ctrl := gomock.NewController(t)
			attacher := mocks.NewMockAttacher(ctrl)
			if tt.expect != nil {
				tt.expect(attacher, fixture.root)
			}
			clients := mockClients(ctrl)
			clients.Attacher = attacher
			toolset := tools.New(clients, fixture.cfg, fixture.configPath)

			got, err := toolset.AttachFiles(t.Context(), tools.AttachFilesInput{IssueKey: tt.issueKey, Paths: fixture.paths(tt.pathTemplates)})

			require.Equal(t, tt.want, got)
			requireAttachError(t, tt.wantErr, err)
		})
	}
}

func TestAttachFilesHomeUnknown(t *testing.T) {
	fixture := newAttachFixture(t)
	t.Setenv("HOME", "")
	ctrl := gomock.NewController(t)
	toolset := tools.New(mockClients(ctrl), fixture.cfg, fixture.configPath)

	got, err := toolset.AttachFiles(t.Context(), tools.AttachFilesInput{IssueKey: attachIssueKey, Paths: []string{"~/out/report.md"}})

	require.Equal(t, tools.AttachFilesOutput{}, got)
	require.ErrorIs(t, err, tools.ErrAttachmentPath)
	require.ErrorIs(t, err, config.ErrHomeUnknown)
}

func TestAttachFilesRejectsPathThroughHome(t *testing.T) {
	tests := []struct {
		name              string
		home              func(t *testing.T, fixtureRoot string) string
		linkTarget        func(t *testing.T, fixtureRoot string) string
		fixtureRootInLink func(t *testing.T, fixtureRoot string) string
	}{
		{name: "симлинк на домашний", home: homeDir, linkTarget: homeDir, fixtureRootInLink: linkItself},
		{name: "симлинк на родителя домашнего", home: homeDir, linkTarget: homeParent, fixtureRootInLink: homeBaseName},
		{name: "симлинк на реальный путь домашнего в другом регистре", home: homeDir, linkTarget: upperCaseRealHome, fixtureRootInLink: linkItself},
		{name: "симлинк на корень", home: homeDir, linkTarget: rootDir, fixtureRootInLink: realHomeFromRoot},
		{name: "симлинк на корень при неизвестном HOME", home: unknownHome, linkTarget: rootDir, fixtureRootInLink: realHomeFromRoot},
		{name: "симлинк на /System/Volumes/Data", home: homeDir, linkTarget: dataVolumeRoot, fixtureRootInLink: realHomeFromRoot},
		{name: "симлинк на /System/Volumes", home: homeDir, linkTarget: systemVolumes, fixtureRootInLink: realHomeFromSystemVolumes},
		{name: "симлинк на /System", home: homeDir, linkTarget: systemDir, fixtureRootInLink: realHomeFromSystem},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newAttachFixture(t)
			linkTarget := tt.linkTarget(t, fixture.root)
			if _, err := os.Stat(linkTarget); err != nil {
				t.Skipf("%s does not exist on this file system: %v", linkTarget, err)
			}
			fixtureRootInLink := tt.fixtureRootInLink(t, fixture.root)
			requireSameDir(t, fixture.root, filepath.Join(linkTarget, fixtureRootInLink))
			t.Setenv("HOME", tt.home(t, fixture.root))
			homeLink := filepath.Join(t.TempDir(), "home-link")
			require.NoError(t, os.Symlink(linkTarget, homeLink))
			cfg := fixture.cfg
			cfg.Attachments = &config.Attachments{
				Dirs:       append([]string{homeLink}, fixture.cfg.Attachments.Dirs...),
				Extensions: fixture.cfg.Attachments.Extensions,
				MaxBytes:   fixture.cfg.Attachments.MaxBytes,
			}
			ctrl := gomock.NewController(t)
			toolset := tools.New(mockClients(ctrl), cfg, fixture.configPath)
			secretThroughLink := filepath.Join(homeLink, fixtureRootInLink, "outside", "secret.md")

			got, err := toolset.AttachFiles(t.Context(), tools.AttachFilesInput{IssueKey: attachIssueKey, Paths: []string{secretThroughLink}})

			require.Equal(t, tools.AttachFilesOutput{}, got)
			require.ErrorIs(t, err, tools.ErrAttachmentThroughHome)
		})
	}
}

func requireSameDir(t *testing.T, want, got string) {
	t.Helper()
	wantInfo, err := os.Stat(want)
	require.NoError(t, err)
	gotInfo, err := os.Stat(got)
	require.NoError(t, err)
	require.True(t, os.SameFile(wantInfo, gotInfo), "%s is not the same directory as %s", got, want)
}

func homeDir(_ *testing.T, fixtureRoot string) string {
	return fixtureRoot
}

func homeParent(_ *testing.T, fixtureRoot string) string {
	return filepath.Dir(fixtureRoot)
}

func unknownHome(*testing.T, string) string {
	return ""
}

func rootDir(*testing.T, string) string {
	return "/"
}

func dataVolumeRoot(*testing.T, string) string {
	return "/System/Volumes/Data"
}

func systemVolumes(*testing.T, string) string {
	return "/System/Volumes"
}

func systemDir(*testing.T, string) string {
	return "/System"
}

func upperCaseRealHome(t *testing.T, fixtureRoot string) string {
	return strings.ToUpper(realPath(t, fixtureRoot))
}

func linkItself(*testing.T, string) string {
	return "."
}

func homeBaseName(_ *testing.T, fixtureRoot string) string {
	return filepath.Base(fixtureRoot)
}

func realHomeFromRoot(t *testing.T, fixtureRoot string) string {
	return strings.TrimPrefix(realPath(t, fixtureRoot), "/")
}

func realHomeFromSystemVolumes(t *testing.T, fixtureRoot string) string {
	return filepath.Join("Data", realHomeFromRoot(t, fixtureRoot))
}

func realHomeFromSystem(t *testing.T, fixtureRoot string) string {
	return filepath.Join("Volumes", realHomeFromSystemVolumes(t, fixtureRoot))
}

func realPath(t *testing.T, path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return resolved
}

func requireAttachError(t *testing.T, wantErr, err error) {
	t.Helper()
	var wantAttachErr *tools.AttachFilesError
	if !errors.As(wantErr, &wantAttachErr) {
		require.ErrorIs(t, err, wantErr)
		return
	}
	var gotAttachErr *tools.AttachFilesError
	require.ErrorAs(t, err, &gotAttachErr)
	require.Equal(t, wantAttachErr.IssueKey, gotAttachErr.IssueKey)
	require.Equal(t, wantAttachErr.Attached, gotAttachErr.Attached)
	require.Equal(t, wantAttachErr.StoppedAt, gotAttachErr.StoppedAt)
	require.Equal(t, wantAttachErr.NotAttempted, gotAttachErr.NotAttempted)
	require.ErrorIs(t, err, wantAttachErr.Err)
}

func TestAttachFilesOverMCP(t *testing.T) {
	tests := []struct {
		name        string
		args        map[string]any
		expect      func(attacher *mocks.MockAttacher, root string)
		wantJSON    string
		wantIsError bool
	}{
		{
			name:     "успех → issue_key, url и приложенные файлы",
			args:     map[string]any{"issue_key": attachIssueKey, "paths": []any{"~/out/report.md"}},
			expect:   expectAttachments(reportAttached),
			wantJSON: `{"issue_key":"PROJ-7790","url":"https://jira.example.com/browse/PROJ-7790","attached":[{"file":"report.md","id":"10501"}]}`,
		},
		{name: "без paths → IsError", args: map[string]any{"issue_key": attachIssueKey}, wantIsError: true},
		{name: "issue_key не по шаблону → IsError", args: map[string]any{"issue_key": "10001", "paths": []any{"~/out/report.md"}}, wantIsError: true},
		{name: "пустой paths → IsError", args: map[string]any{"issue_key": attachIssueKey, "paths": []any{}}, wantIsError: true},
		{name: "paths строкой → IsError", args: map[string]any{"issue_key": attachIssueKey, "paths": "~/out/report.md"}, wantIsError: true},
		{name: "лишний параметр → IsError", args: map[string]any{"issue_key": attachIssueKey, "paths": []any{"~/out/report.md"}, "summary": "x"}, wantIsError: true},
		{name: "файл вне каталогов → IsError", args: map[string]any{"issue_key": attachIssueKey, "paths": []any{"~/outside/secret.md"}}, wantIsError: true},
		{
			name:        "ошибка клиента → IsError",
			args:        map[string]any{"issue_key": attachIssueKey, "paths": []any{"~/out/report.md"}},
			expect:      expectAttachments(reportAttached.failing(jira.ErrUnauthorized)),
			wantIsError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newAttachFixture(t)
			ctrl := gomock.NewController(t)
			attacher := mocks.NewMockAttacher(ctrl)
			if tt.expect != nil {
				tt.expect(attacher, fixture.root)
			}
			clients := mockClients(ctrl)
			clients.Attacher = attacher
			session := connectMCP(t, tools.New(clients, fixture.cfg, fixture.configPath))

			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tools.AttachFilesToolName, Arguments: tt.args})

			require.NoError(t, err)
			require.Equal(t, tt.wantIsError, result.IsError)
			if tt.wantIsError {
				require.NotEmpty(t, result.Content)
				return
			}
			structured, err := json.Marshal(result.StructuredContent)
			require.NoError(t, err)
			require.JSONEq(t, tt.wantJSON, string(structured))
		})
	}
}

func TestAttachFilesErrorText(t *testing.T) {
	unconfirmed := errors.Join(jira.ErrAttachUnconfirmed, errors.New("connection reset"))
	twoAttached := []tools.AttachedFile{{Name: "report.md", ID: "1"}, {Name: "scheme.png", ID: "2"}}
	tests := []struct {
		name string
		err  tools.AttachFilesError
		want string
	}{
		{
			name: "единственный файл не приложен",
			err:  tools.AttachFilesError{IssueKey: attachIssueKey, StoppedAt: "report.md", Err: jira.ErrUnauthorized},
			want: "tools: no files were attached to PROJ-7790 before report.md; report.md was not attached: " + jira.ErrUnauthorized.Error(),
		},
		{
			name: "часть приложена, после неприложенного есть файлы",
			err: tools.AttachFilesError{
				IssueKey: attachIssueKey, Attached: twoAttached, StoppedAt: "notes.txt", NotAttempted: []string{"a.md", "b.md"}, Err: errors.New("connection reset"),
			},
			want: "tools: attached to PROJ-7790: report.md (id 1), scheme.png (id 2); notes.txt was not attached: connection reset; not attempted after it: a.md, b.md. " +
				"Retrying would attach report.md, scheme.png again: Jira does not deduplicate attachments, so retry only the files that were not attached",
		},
		{
			name: "первый файл не подтверждён, после него есть файлы",
			err:  tools.AttachFilesError{IssueKey: attachIssueKey, StoppedAt: "report.md", NotAttempted: []string{"a.md"}, Err: unconfirmed},
			want: "tools: no files were attached to PROJ-7790 before report.md; report.md may have been attached, check the issue before retrying it: " +
				unconfirmed.Error() + "; not attempted after it: a.md",
		},
		{
			name: "часть приложена, следующий не подтверждён, после него есть файлы",
			err:  tools.AttachFilesError{IssueKey: attachIssueKey, Attached: twoAttached, StoppedAt: "notes.txt", NotAttempted: []string{"a.md"}, Err: unconfirmed},
			want: "tools: attached to PROJ-7790: report.md (id 1), scheme.png (id 2); notes.txt may have been attached, check the issue before retrying it: " +
				unconfirmed.Error() + "; not attempted after it: a.md. " +
				"Retrying would attach report.md, scheme.png again: Jira does not deduplicate attachments, so retry only the files after notes.txt, and notes.txt only if it is missing from the issue",
		},
		{
			name: "часть приложена, последний не подтверждён",
			err:  tools.AttachFilesError{IssueKey: attachIssueKey, Attached: twoAttached, StoppedAt: "notes.txt", Err: unconfirmed},
			want: "tools: attached to PROJ-7790: report.md (id 1), scheme.png (id 2); notes.txt may have been attached, check the issue before retrying it: " +
				unconfirmed.Error() + ". " +
				"Retrying would attach report.md, scheme.png again: Jira does not deduplicate attachments, so retry notes.txt only if it is missing from the issue",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.err.Error())
		})
	}
}
