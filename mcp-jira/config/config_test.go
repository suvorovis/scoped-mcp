package config

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/keychain"
	"scoped-mcp/pkg/secret/mocks"
)

func validConfig() Config {
	return Config{
		BaseURL:      "https://jira.example.com",
		SecretsMode:  SecretsModeEnv,
		SecretsDir:   DefaultSecretsDir,
		KeychainPath: DefaultKeychainPath,
		Tools:        Tools{CreateIssue: true},
		Fields: map[string]Field{
			"description": {Jira: "description", Kind: KindText},
			"priority":    {Jira: "priority", Kind: KindName, Required: true},
			"labels":      {Jira: "labels", Kind: KindLabels},
			"epic_name":   {Jira: "customfield_10203", Kind: KindText, Required: true},
			"epic_link":   {Jira: "customfield_10201", Kind: KindIssueKey},
		},
		Projects: []Project{{Key: "PROJ", IssueTypes: []IssueType{
			{Name: "Epic", ID: "10000", Fields: []string{"description", "priority", "labels", "epic_name"}},
			{Name: "История", ID: "11203", Fields: []string{"description", "priority", "epic_link"}},
		}}},
	}
}

func withAttachFiles(mutate func(attachments *Attachments)) func(cfg *Config) {
	return func(cfg *Config) {
		attachments := Attachments{Dirs: []string{"/home/bot/out"}, Extensions: []string{".md", ".png"}, MaxBytes: 10 << 20}
		mutate(&attachments)
		cfg.Tools.AttachFiles = true
		cfg.Attachments = &attachments
	}
}

func withAddComment() func(cfg *Config) {
	return func(cfg *Config) {
		cfg.Tools.AddComment = true
	}
}

func issueLinks() []IssueLink {
	return []IssueLink{
		{Relation: "blocks", Type: "Blocks", Direction: LinkOutward},
		{Relation: "is blocked by", Type: "Blocks", Direction: LinkInward},
		{Relation: "relates to", Type: "Relates", Direction: LinkOutward},
	}
}

func withLinkIssues(mutate func(links *[]IssueLink)) func(cfg *Config) {
	return func(cfg *Config) {
		links := issueLinks()
		mutate(&links)
		cfg.Tools.LinkIssues = true
		cfg.IssueLinks = links
	}
}

func withUpdateIssue(mutate func(cfg *Config)) func(cfg *Config) {
	return func(cfg *Config) {
		cfg.Tools.UpdateIssue = true
		cfg.Projects[0].UpdateFields = []string{"epic_link"}
		mutate(cfg)
	}
}

func withOnlyTool(enableTool func(cfg *Config), mutate func(cfg *Config)) func(cfg *Config) {
	return func(cfg *Config) {
		cfg.Tools.CreateIssue = false
		cfg.Fields = nil
		cfg.Projects = []Project{{Key: "PROJ"}, {Key: "OTHER"}}
		enableTool(cfg)
		mutate(cfg)
	}
}

func withAttachFilesOnly(mutate func(cfg *Config)) func(cfg *Config) {
	return withOnlyTool(withAttachFiles(func(*Attachments) {}), mutate)
}

func withAddCommentOnly(mutate func(cfg *Config)) func(cfg *Config) {
	return withOnlyTool(withAddComment(), mutate)
}

func withLinkIssuesOnly(mutate func(cfg *Config)) func(cfg *Config) {
	return withOnlyTool(withLinkIssues(func(*[]IssueLink) {}), mutate)
}

func withUpdateIssueOnly(mutate func(cfg *Config)) func(cfg *Config) {
	return withOnlyTool(withUpdateIssue(func(cfg *Config) {
		cfg.Fields = map[string]Field{"epic_link": {Jira: "customfield_10201", Kind: KindIssueKey}}
	}), mutate)
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(cfg *Config)
		wantErr error
	}{
		{name: "валидный конфиг", mutate: func(*Config) {}},
		{name: "второй проект с тем же именем типа допустим", mutate: func(cfg *Config) {
			cfg.Projects = append(cfg.Projects, Project{Key: "OTHER", IssueTypes: []IssueType{{Name: "Epic", ID: "1"}}})
		}},
		{name: "тип без полей допустим", mutate: func(cfg *Config) { cfg.Projects[0].IssueTypes[0].Fields = nil }},
		{name: "режим secrets file", mutate: func(cfg *Config) { cfg.SecretsMode = SecretsModeFile }},

		{name: "base_url http", mutate: func(cfg *Config) { cfg.BaseURL = "http://jira.example.com" }, wantErr: ErrBaseURL},
		{name: "base_url без схемы", mutate: func(cfg *Config) { cfg.BaseURL = "jira.example.com" }, wantErr: ErrBaseURL},
		{name: "base_url с query", mutate: func(cfg *Config) { cfg.BaseURL = "https://jira.example.com/?x=1" }, wantErr: ErrBaseURL},
		{name: "base_url с fragment", mutate: func(cfg *Config) { cfg.BaseURL = "https://jira.example.com/#top" }, wantErr: ErrBaseURL},
		{name: "base_url пустой", mutate: func(cfg *Config) { cfg.BaseURL = "" }, wantErr: ErrBaseURL},
		{name: "base_url с пользователем", mutate: func(cfg *Config) { cfg.BaseURL = "https://bot@jira.example.com" }, wantErr: ErrBaseURL},
		{name: "base_url с пользователем и паролем", mutate: func(cfg *Config) { cfg.BaseURL = "https://bot:secret@jira.example.com" }, wantErr: ErrBaseURL},

		{name: "неизвестный режим secrets", mutate: func(cfg *Config) { cfg.SecretsMode = "vault" }, wantErr: ErrSecretsMode},
		{name: "пустой режим secrets", mutate: func(cfg *Config) { cfg.SecretsMode = "" }, wantErr: ErrSecretsMode},

		{name: "secrets_dir абсолютный допустим", mutate: func(cfg *Config) { cfg.SecretsDir = "/var/mcp-jira/secrets" }},
		{name: "keychain_path абсолютный допустим", mutate: func(cfg *Config) { cfg.KeychainPath = "/var/scoped-mcp.keychain-db" }},
		{name: "secrets_dir относительный", mutate: func(cfg *Config) { cfg.SecretsDir = "secrets" }, wantErr: ErrSecretsPath},
		{name: "secrets_dir от текущего каталога", mutate: func(cfg *Config) { cfg.SecretsDir = "./secrets" }, wantErr: ErrSecretsPath},
		{name: "secrets_dir от ~user не раскрывается", mutate: func(cfg *Config) { cfg.SecretsDir = "~bot/secrets" }, wantErr: ErrSecretsPath},
		{name: "secrets_dir ~ отклоняется", mutate: func(cfg *Config) { cfg.SecretsDir = "~" }, wantErr: ErrSecretsPath},
		{name: "secrets_dir пустой", mutate: func(cfg *Config) { cfg.SecretsDir = "" }, wantErr: ErrSecretsPath},
		{name: "keychain_path относительный", mutate: func(cfg *Config) { cfg.KeychainPath = "scoped-mcp.keychain-db" }, wantErr: ErrSecretsPath},
		{name: "keychain_path от ~user не раскрывается", mutate: func(cfg *Config) { cfg.KeychainPath = "~bot/scoped-mcp.keychain-db" }, wantErr: ErrSecretsPath},
		{name: "keychain_path пустой", mutate: func(cfg *Config) { cfg.KeychainPath = "" }, wantErr: ErrSecretsPath},

		{name: "projects пуст", mutate: func(cfg *Config) { cfg.Projects = nil }, wantErr: ErrNoProjects},
		{name: "ключ проекта строчными", mutate: func(cfg *Config) { cfg.Projects[0].Key = "proj" }, wantErr: ErrProjectKey},
		{name: "ключ проекта с дефисом", mutate: func(cfg *Config) { cfg.Projects[0].Key = "PR-OJ" }, wantErr: ErrProjectKey},
		{name: "ключ проекта из одной буквы", mutate: func(cfg *Config) { cfg.Projects[0].Key = "M" }, wantErr: ErrProjectKey},
		{name: "повтор ключа проекта", mutate: func(cfg *Config) {
			cfg.Projects = append(cfg.Projects, Project{Key: "PROJ", IssueTypes: []IssueType{{Name: "Task", ID: "1"}}})
		}, wantErr: ErrDuplicateProject},
		{name: "проект без типов", mutate: func(cfg *Config) { cfg.Projects[0].IssueTypes = nil }, wantErr: ErrNoIssueTypes},
		{name: "тип без имени", mutate: func(cfg *Config) { cfg.Projects[0].IssueTypes[0].Name = "" }, wantErr: ErrIssueTypeName},
		{name: "тип без id", mutate: func(cfg *Config) { cfg.Projects[0].IssueTypes[0].ID = "" }, wantErr: ErrIssueTypeID},
		{name: "повтор имени типа в проекте", mutate: func(cfg *Config) { cfg.Projects[0].IssueTypes[1].Name = "Epic" }, wantErr: ErrDuplicateIssueType},
		{name: "повтор id типа в проекте", mutate: func(cfg *Config) { cfg.Projects[0].IssueTypes[1].ID = "10000" }, wantErr: ErrDuplicateIssueType},

		{name: "поле типа не описано в fields", mutate: func(cfg *Config) {
			cfg.Projects[0].IssueTypes[0].Fields = []string{"priority", "sprint"}
		}, wantErr: ErrUnknownField},
		{name: "повтор поля в типе", mutate: func(cfg *Config) {
			cfg.Projects[0].IssueTypes[0].Fields = []string{"priority", "priority"}
		}, wantErr: ErrDuplicateField},
		{name: "неизвестный kind", mutate: func(cfg *Config) { cfg.Fields["labels"] = Field{Jira: "labels", Kind: "list"} }, wantErr: ErrFieldKind},
		{name: "jira пуст", mutate: func(cfg *Config) { cfg.Fields["labels"] = Field{Kind: KindLabels} }, wantErr: ErrFieldJira},
		{name: "jira = issuelinks", mutate: func(cfg *Config) { cfg.Fields["links"] = Field{Jira: "issuelinks", Kind: KindNames} }, wantErr: ErrFieldJira},
		{name: "jira = summary", mutate: func(cfg *Config) { cfg.Fields["title"] = Field{Jira: "summary", Kind: KindText} }, wantErr: ErrFieldJira},
		{name: "jira = project", mutate: func(cfg *Config) { cfg.Fields["proj"] = Field{Jira: "project", Kind: KindName} }, wantErr: ErrFieldJira},
		{name: "jira = issuetype", mutate: func(cfg *Config) { cfg.Fields["issuetype_field"] = Field{Jira: "issuetype", Kind: KindName} }, wantErr: ErrFieldJira},
		{name: "jira = assignee", mutate: func(cfg *Config) { cfg.Fields["owner"] = Field{Jira: "assignee", Kind: KindName} }, wantErr: ErrFieldJira},
		{name: "ключ поля project", mutate: func(cfg *Config) { cfg.Fields["project"] = Field{Jira: "customfield_1", Kind: KindText} }, wantErr: ErrFieldKey},
		{name: "ключ поля issue_type", mutate: func(cfg *Config) { cfg.Fields["issue_type"] = Field{Jira: "customfield_1", Kind: KindText} }, wantErr: ErrFieldKey},
		{name: "ключ поля summary", mutate: func(cfg *Config) { cfg.Fields["summary"] = Field{Jira: "customfield_1", Kind: KindText} }, wantErr: ErrFieldKey},
		{name: "ключ поля assignee", mutate: func(cfg *Config) { cfg.Fields["assignee"] = Field{Jira: "customfield_1", Kind: KindName} }, wantErr: ErrFieldKey},
		{name: "ключ поля issue_key", mutate: func(cfg *Config) { cfg.Fields["issue_key"] = Field{Jira: "customfield_1", Kind: KindIssueKey} }, wantErr: ErrFieldKey},
		{name: "два ключа на одно поле Jira", mutate: func(cfg *Config) {
			cfg.Fields["prio"] = Field{Jira: "priority", Kind: KindName}
		}, wantErr: ErrDuplicateJiraField},

		{name: "раздел attachments", mutate: withAttachFiles(func(*Attachments) {})},
		{name: "каталог attachments от ~/", mutate: withAttachFiles(func(a *Attachments) { a.Dirs = []string{"~/agent-out"} })},
		{name: "max_bytes ровно 20 MiB", mutate: withAttachFiles(func(a *Attachments) { a.MaxBytes = 20 << 20 })},
		{name: "расширение в верхнем регистре допустимо", mutate: withAttachFiles(func(a *Attachments) { a.Extensions = []string{".MD", ".png"} })},
		{name: "dirs пуст", mutate: withAttachFiles(func(a *Attachments) { a.Dirs = nil }), wantErr: ErrAttachmentsDirs},
		{name: "каталог — пустая строка", mutate: withAttachFiles(func(a *Attachments) { a.Dirs = []string{""} }), wantErr: ErrAttachmentsDirs},
		{name: "каталог относительный", mutate: withAttachFiles(func(a *Attachments) { a.Dirs = []string{"out"} }), wantErr: ErrAttachmentsDirs},
		{name: "каталог от ~user не раскрывается", mutate: withAttachFiles(func(a *Attachments) { a.Dirs = []string{"~bot/out"} }), wantErr: ErrAttachmentsDirs},
		{name: "второй каталог относительный", mutate: withAttachFiles(func(a *Attachments) { a.Dirs = []string{"/a/out", "./out"} }), wantErr: ErrAttachmentsDirs},
		{name: "extensions пуст", mutate: withAttachFiles(func(a *Attachments) { a.Extensions = nil }), wantErr: ErrAttachmentsExtensions},
		{name: "расширение без точки", mutate: withAttachFiles(func(a *Attachments) { a.Extensions = []string{"md"} }), wantErr: ErrAttachmentsExtensions},
		{name: "расширение из одной точки", mutate: withAttachFiles(func(a *Attachments) { a.Extensions = []string{"."} }), wantErr: ErrAttachmentsExtensions},
		{name: "расширение пустое", mutate: withAttachFiles(func(a *Attachments) { a.Extensions = []string{""} }), wantErr: ErrAttachmentsExtensions},
		{name: "расширение со слэшем", mutate: withAttachFiles(func(a *Attachments) { a.Extensions = []string{".tar/gz"} }), wantErr: ErrAttachmentsExtensions},
		{name: "расширение повторяется в другом регистре", mutate: withAttachFiles(func(a *Attachments) { a.Extensions = []string{".md", ".MD"} }), wantErr: ErrAttachmentsExtensions},
		{name: "max_bytes ноль", mutate: withAttachFiles(func(a *Attachments) { a.MaxBytes = 0 }), wantErr: ErrAttachmentsMaxBytes},
		{name: "max_bytes отрицательный", mutate: withAttachFiles(func(a *Attachments) { a.MaxBytes = -1 }), wantErr: ErrAttachmentsMaxBytes},
		{name: "max_bytes больше 20 MiB", mutate: withAttachFiles(func(a *Attachments) { a.MaxBytes = 20<<20 + 1 }), wantErr: ErrAttachmentsMaxBytes},

		{name: "только attach_files, проекты из одного key", mutate: withAttachFilesOnly(func(*Config) {})},
		{name: "ни один инструмент не включён", mutate: func(cfg *Config) { cfg.Tools = Tools{} }, wantErr: ErrNoTools},
		{name: "attach_files без раздела attachments", mutate: func(cfg *Config) { cfg.Tools.AttachFiles = true }, wantErr: ErrAttachmentsSection},
		{name: "раздел attachments без attach_files", mutate: func(cfg *Config) {
			withAttachFiles(func(*Attachments) {})(cfg)
			cfg.Tools.AttachFiles = false
		}, wantErr: ErrAttachmentsSection},
		{name: "без create_issue и update_issue непустые fields", mutate: withAttachFilesOnly(func(cfg *Config) {
			cfg.Fields = map[string]Field{"labels": {Jira: "labels", Kind: KindLabels}}
		}), wantErr: ErrFieldsSettings},
		{name: "без create_issue issue_types у проекта", mutate: withAttachFilesOnly(func(cfg *Config) {
			cfg.Projects = []Project{{Key: "PROJ"}, {Key: "OTHER", IssueTypes: []IssueType{{Name: "Task", ID: "3"}}}}
		}), wantErr: ErrCreateIssueSettings},
		{name: "без create_issue assign_to_self у проекта", mutate: withAttachFilesOnly(func(cfg *Config) {
			cfg.Projects = []Project{{Key: "PROJ"}, {Key: "OTHER", AssignToSelf: true}}
		}), wantErr: ErrCreateIssueSettings},
		{name: "только attach_files, projects пуст", mutate: withAttachFilesOnly(func(cfg *Config) { cfg.Projects = nil }), wantErr: ErrNoProjects},
		{name: "add_comment вместе с create_issue", mutate: withAddComment()},
		{name: "только add_comment, проекты из одного key", mutate: withAddCommentOnly(func(*Config) {})},
		{name: "только add_comment, projects пуст", mutate: withAddCommentOnly(func(cfg *Config) { cfg.Projects = nil }), wantErr: ErrNoProjects},
		{name: "только add_comment, issue_types у проекта", mutate: withAddCommentOnly(func(cfg *Config) {
			cfg.Projects = []Project{{Key: "PROJ", IssueTypes: []IssueType{{Name: "Task", ID: "3"}}}}
		}), wantErr: ErrCreateIssueSettings},
		{name: "только add_comment и раздел attachments", mutate: withAddCommentOnly(func(cfg *Config) {
			cfg.Attachments = &Attachments{Dirs: []string{"/home/bot/out"}, Extensions: []string{".md"}, MaxBytes: 1024}
		}), wantErr: ErrAttachmentsSection},
		{name: "link_issues вместе с create_issue", mutate: withLinkIssues(func(*[]IssueLink) {})},
		{name: "только link_issues, проекты из одного key", mutate: withLinkIssuesOnly(func(*Config) {})},
		{name: "один type и direction у двух relation допустим", mutate: withLinkIssues(func(links *[]IssueLink) { (*links)[2].Type = "Blocks" })},
		{name: "только link_issues, projects пуст", mutate: withLinkIssuesOnly(func(cfg *Config) { cfg.Projects = nil }), wantErr: ErrNoProjects},
		{name: "только link_issues, issue_types у проекта", mutate: withLinkIssuesOnly(func(cfg *Config) {
			cfg.Projects = []Project{{Key: "PROJ", IssueTypes: []IssueType{{Name: "Task", ID: "3"}}}}
		}), wantErr: ErrCreateIssueSettings},
		{name: "link_issues без раздела issue_links", mutate: func(cfg *Config) { cfg.Tools.LinkIssues = true }, wantErr: ErrIssueLinksSection},
		{name: "link_issues с пустым issue_links", mutate: withLinkIssues(func(links *[]IssueLink) { *links = []IssueLink{} }), wantErr: ErrIssueLinksSection},
		{name: "раздел issue_links без link_issues", mutate: func(cfg *Config) { cfg.IssueLinks = issueLinks() }, wantErr: ErrIssueLinksSection},
		{name: "relation пуст", mutate: withLinkIssues(func(links *[]IssueLink) { (*links)[0].Relation = "" }), wantErr: ErrIssueLinkRelation},
		{name: "relation с пробелом по краю", mutate: withLinkIssues(func(links *[]IssueLink) { (*links)[0].Relation = "blocks " }), wantErr: ErrIssueLinkRelation},
		{name: "relation повторяется", mutate: withLinkIssues(func(links *[]IssueLink) { (*links)[2].Relation = "blocks" }), wantErr: ErrIssueLinkRelation},
		{name: "relation повторяется в другом регистре допустим", mutate: withLinkIssues(func(links *[]IssueLink) { (*links)[2].Relation = "Blocks" })},
		{name: "type пуст", mutate: withLinkIssues(func(links *[]IssueLink) { (*links)[1].Type = "" }), wantErr: ErrIssueLinkType},
		{name: "direction пуст", mutate: withLinkIssues(func(links *[]IssueLink) { (*links)[0].Direction = "" }), wantErr: ErrIssueLinkDirection},
		{name: "direction не outward и не inward", mutate: withLinkIssues(func(links *[]IssueLink) { (*links)[0].Direction = "both" }), wantErr: ErrIssueLinkDirection},
		{name: "direction в другом регистре", mutate: withLinkIssues(func(links *[]IssueLink) { (*links)[0].Direction = "Outward" }), wantErr: ErrIssueLinkDirection},
		{name: "update_issue вместе с create_issue", mutate: withUpdateIssue(func(*Config) {})},
		{name: "update_fields с полем вне типов задач проекта", mutate: withUpdateIssue(func(cfg *Config) { cfg.Projects[0].UpdateFields = []string{"labels"} })},
		{name: "только update_issue, update_fields у первого проекта", mutate: withUpdateIssueOnly(func(*Config) {})},
		{name: "только update_issue, update_fields у второго проекта", mutate: withUpdateIssueOnly(func(cfg *Config) {
			cfg.Projects[0].UpdateFields = nil
			cfg.Projects[1].UpdateFields = []string{"epic_link"}
		})},
		{name: "только update_issue, required у поля не мешает", mutate: withUpdateIssueOnly(func(cfg *Config) {
			cfg.Fields["epic_link"] = Field{Jira: "customfield_10201", Kind: KindIssueKey, Required: true}
		})},
		{name: "update_issue без update_fields", mutate: withUpdateIssue(func(cfg *Config) { cfg.Projects[0].UpdateFields = nil }), wantErr: ErrUpdateIssueFields},
		{name: "update_issue с пустым update_fields", mutate: withUpdateIssue(func(cfg *Config) { cfg.Projects[0].UpdateFields = []string{} }), wantErr: ErrUpdateIssueFields},
		{name: "update_fields без update_issue", mutate: func(cfg *Config) { cfg.Projects[0].UpdateFields = []string{"epic_link"} }, wantErr: ErrUpdateIssueFields},
		{name: "update_fields не описан в fields", mutate: withUpdateIssue(func(cfg *Config) {
			cfg.Projects[0].UpdateFields = []string{"epic_link", "sprint"}
		}), wantErr: ErrUnknownField},
		{name: "повтор в update_fields", mutate: withUpdateIssue(func(cfg *Config) {
			cfg.Projects[0].UpdateFields = []string{"epic_link", "epic_link"}
		}), wantErr: ErrDuplicateField},
		{name: "только update_issue, fields пуст", mutate: withUpdateIssueOnly(func(cfg *Config) { cfg.Fields = nil }), wantErr: ErrUnknownField},
		{name: "только update_issue, issue_types у проекта", mutate: withUpdateIssueOnly(func(cfg *Config) {
			cfg.Projects[1].IssueTypes = []IssueType{{Name: "Task", ID: "3"}}
		}), wantErr: ErrCreateIssueSettings},
		{name: "только update_issue, assign_to_self у проекта", mutate: withUpdateIssueOnly(func(cfg *Config) { cfg.Projects[1].AssignToSelf = true }), wantErr: ErrCreateIssueSettings},
		{name: "только update_issue, projects пуст", mutate: withUpdateIssueOnly(func(cfg *Config) { cfg.Projects = nil }), wantErr: ErrUpdateIssueFields},
		{name: "attach_files и link_issues с пустым relation", mutate: func(cfg *Config) {
			withAttachFiles(func(*Attachments) {})(cfg)
			withLinkIssues(func(links *[]IssueLink) { (*links)[0].Relation = "" })(cfg)
		}, wantErr: ErrIssueLinkRelation},
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
base_url: https://jira.example.com
secrets: keychain
keychain_path: ~/Library/Keychains/scoped-mcp.keychain-db

tools:
  create_issue: true
  attach_files: true
  add_comment: true
  link_issues: true

fields:
  description: {jira: description,       kind: text}
  priority:    {jira: priority,          kind: name, required: true}
  labels:      {jira: labels,            kind: labels}
  components:  {jira: components,        kind: names}
  epic_name:   {jira: customfield_10203, kind: text, required: true}
  epic_link:   {jira: customfield_10201, kind: issue_key}

projects:
  - key: PROJ
    assign_to_self: true
    issue_types:
      - name: Epic
        id: "10000"
        fields: [description, priority, labels, components, epic_name]
      - name: История
        id: "11203"
        fields: [description, priority, labels, components, epic_link]

attachments:
  dirs: [~/agent-out, /var/reports]
  extensions: [.md, .drawio, .png, .svg, .txt, .json, .log]
  max_bytes: 10485760

issue_links:
  - {relation: blocks,        type: Blocks,  direction: outward}
  - {relation: is blocked by, type: Blocks,  direction: inward}
  - {relation: relates to,    type: Relates, direction: outward}
`

func TestParse(t *testing.T) {
	fullConfig := Config{
		BaseURL:      "https://jira.example.com",
		SecretsMode:  SecretsModeKeychain,
		SecretsDir:   DefaultSecretsDir,
		KeychainPath: "~/Library/Keychains/scoped-mcp.keychain-db",
		Tools:        Tools{CreateIssue: true, AttachFiles: true, AddComment: true, LinkIssues: true},
		Fields: map[string]Field{
			"description": {Jira: "description", Kind: KindText},
			"priority":    {Jira: "priority", Kind: KindName, Required: true},
			"labels":      {Jira: "labels", Kind: KindLabels},
			"components":  {Jira: "components", Kind: KindNames},
			"epic_name":   {Jira: "customfield_10203", Kind: KindText, Required: true},
			"epic_link":   {Jira: "customfield_10201", Kind: KindIssueKey},
		},
		Projects: []Project{{Key: "PROJ", AssignToSelf: true, IssueTypes: []IssueType{
			{Name: "Epic", ID: "10000", Fields: []string{"description", "priority", "labels", "components", "epic_name"}},
			{Name: "История", ID: "11203", Fields: []string{"description", "priority", "labels", "components", "epic_link"}},
		}}},
		Attachments: &Attachments{
			Dirs:       []string{"~/agent-out", "/var/reports"},
			Extensions: []string{".md", ".drawio", ".png", ".svg", ".txt", ".json", ".log"},
			MaxBytes:   10485760,
		},
		IssueLinks: issueLinks(),
	}
	tests := []struct {
		name    string
		yaml    string
		want    Config
		wantErr error
	}{
		{name: "все виды полей, два типа задач, assign_to_self, attachments и issue_links", yaml: fullYAML, want: fullConfig},
		{
			name: "base_url с завершающим слэшем нормализуется",
			yaml: `
base_url: https://jira.example.com/
tools: {create_issue: true}
projects:
  - key: PROJ
    issue_types: [{name: Task, id: "3"}]
`,
			want: Config{BaseURL: "https://jira.example.com", SecretsMode: SecretsModeEnv, SecretsDir: DefaultSecretsDir, KeychainPath: DefaultKeychainPath, Tools: Tools{CreateIssue: true}, Projects: []Project{{Key: "PROJ", IssueTypes: []IssueType{{Name: "Task", ID: "3"}}}}},
		},
		{
			name: "только attach_files: проект из одного key",
			yaml: `
base_url: https://jira.example.com
tools: {attach_files: true}
projects: [{key: PROJ}]
attachments: {dirs: [/var/reports], extensions: [.md], max_bytes: 1024}
`,
			want: Config{
				BaseURL:      "https://jira.example.com",
				SecretsMode:  SecretsModeEnv,
				SecretsDir:   DefaultSecretsDir,
				KeychainPath: DefaultKeychainPath,
				Tools:        Tools{AttachFiles: true},
				Projects:     []Project{{Key: "PROJ"}},
				Attachments:  &Attachments{Dirs: []string{"/var/reports"}, Extensions: []string{".md"}, MaxBytes: 1024},
			},
		},
		{
			name: "только add_comment: проекты из одного key",
			yaml: `
base_url: https://jira.example.com
tools: {add_comment: true}
projects: [{key: PROJ}, {key: OTHER}]
`,
			want: Config{
				BaseURL:      "https://jira.example.com",
				SecretsMode:  SecretsModeEnv,
				SecretsDir:   DefaultSecretsDir,
				KeychainPath: DefaultKeychainPath,
				Tools:        Tools{AddComment: true},
				Projects:     []Project{{Key: "PROJ"}, {Key: "OTHER"}},
			},
		},
		{
			name: "только link_issues: проекты из одного key",
			yaml: `
base_url: https://jira.example.com
tools: {link_issues: true}
projects: [{key: PROJ}, {key: OTHER}]
issue_links: [{relation: blocks, type: Blocks, direction: outward}]
`,
			want: Config{
				BaseURL:      "https://jira.example.com",
				SecretsMode:  SecretsModeEnv,
				SecretsDir:   DefaultSecretsDir,
				KeychainPath: DefaultKeychainPath,
				Tools:        Tools{LinkIssues: true},
				Projects:     []Project{{Key: "PROJ"}, {Key: "OTHER"}},
				IssueLinks:   []IssueLink{{Relation: "blocks", Type: "Blocks", Direction: LinkOutward}},
			},
		},
		{
			name: "режим file со своим secrets_dir",
			yaml: `
base_url: https://jira.example.com
secrets: file
secrets_dir: /tmp/secrets
tools: {add_comment: true}
projects: [{key: PROJ}]
`,
			want: Config{
				BaseURL:      "https://jira.example.com",
				SecretsMode:  SecretsModeFile,
				SecretsDir:   "/tmp/secrets",
				KeychainPath: DefaultKeychainPath,
				Tools:        Tools{AddComment: true},
				Projects:     []Project{{Key: "PROJ"}},
			},
		},
		{
			name: "только update_issue: epic_link в update_fields проекта",
			yaml: `
base_url: https://jira.example.com
tools: {update_issue: true}
fields:
  epic_link: {jira: customfield_10201, kind: issue_key}
projects:
  - key: PROJ
    update_fields: [epic_link]
  - key: OTHER
`,
			want: Config{
				BaseURL:      "https://jira.example.com",
				SecretsMode:  SecretsModeEnv,
				SecretsDir:   DefaultSecretsDir,
				KeychainPath: DefaultKeychainPath,
				Tools:        Tools{UpdateIssue: true},
				Fields:       map[string]Field{"epic_link": {Jira: "customfield_10201", Kind: KindIssueKey}},
				Projects:     []Project{{Key: "PROJ", UpdateFields: []string{"epic_link"}}, {Key: "OTHER"}},
			},
		},
		{name: "неизвестный режим secrets", yaml: "base_url: https://jira.example.com\nsecrets: vault\ntools: {add_comment: true}\nprojects: [{key: PROJ}]\n", wantErr: ErrSecretsMode},
		{name: "пустой режим secrets", yaml: "base_url: https://jira.example.com\nsecrets: \"\"\ntools: {add_comment: true}\nprojects: [{key: PROJ}]\n", wantErr: ErrSecretsMode},
		{name: "удалённый ключ token_path отклоняется", yaml: "base_url: https://jira.example.com\ntoken_path: /t\ntools: {add_comment: true}\nprojects: [{key: PROJ}]\n", wantErr: ErrMalformed},
		{
			name: "неизвестный ключ в issue_links отклоняется",
			yaml: `
base_url: https://jira.example.com
tools: {link_issues: true}
projects: [{key: PROJ}]
issue_links: [{relation: blocks, type: Blocks, direction: outward, comment: true}]
`,
			wantErr: ErrMalformed,
		},
		{
			name: "без раздела tools",
			yaml: `
base_url: https://jira.example.com
projects:
  - key: PROJ
    issue_types: [{name: Task, id: "3"}]
`,
			wantErr: ErrNoTools,
		},
		{
			name: "неизвестный ключ в tools отклоняется",
			yaml: `
base_url: https://jira.example.com
tools: {create_issue: true, search: true}
projects:
  - key: PROJ
    issue_types: [{name: Task, id: "3"}]
`,
			wantErr: ErrMalformed,
		},
		{name: "невалидный YAML", yaml: "base_url: [", wantErr: ErrMalformed},
		{
			name: "опечатка в ключе base_uri отклоняется",
			yaml: `
base_uri: https://jira.example.com
projects:
  - key: PROJ
    issue_types: [{name: Task, id: "3"}]
`,
			wantErr: ErrMalformed,
		},
		{
			name: "ошибка валидации пробрасывается",
			yaml: `
base_url: https://jira.example.com
tools: {create_issue: true}
projects: []
`,
			wantErr: ErrNoProjects,
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

func TestFindProjectAndIssueType(t *testing.T) {
	cfg := validConfig()
	tests := []struct {
		name               string
		projectKey         string
		issueTypeName      string
		wantProjectFound   bool
		wantIssueTypeFound bool
	}{
		{name: "проект и тип найдены", projectKey: "PROJ", issueTypeName: "История", wantProjectFound: true, wantIssueTypeFound: true},
		{name: "проект найден, тип нет", projectKey: "PROJ", issueTypeName: "Bug", wantProjectFound: true},
		{name: "ключ проекта в другом регистре не найден", projectKey: "proj"},
		{name: "проект отсутствует в конфиге", projectKey: "OTHER"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project, foundProject := cfg.FindProject(tt.projectKey)
			require.Equal(t, tt.wantProjectFound, foundProject)
			if !foundProject {
				return
			}
			require.Equal(t, tt.projectKey, project.Key)

			issueType, foundIssueType := project.FindIssueType(tt.issueTypeName)
			require.Equal(t, tt.wantIssueTypeFound, foundIssueType)
			if !foundIssueType {
				return
			}
			require.Equal(t, tt.issueTypeName, issueType.Name)
		})
	}
}

func TestFindIssueLink(t *testing.T) {
	cfg := Config{IssueLinks: issueLinks()}
	tests := []struct {
		name     string
		relation string
		want     IssueLink
		wantOK   bool
	}{
		{name: "outward relation найден", relation: "blocks", want: IssueLink{Relation: "blocks", Type: "Blocks", Direction: LinkOutward}, wantOK: true},
		{name: "inward relation найден", relation: "is blocked by", want: IssueLink{Relation: "is blocked by", Type: "Blocks", Direction: LinkInward}, wantOK: true},
		{name: "relation в другом регистре не найден", relation: "Blocks"},
		{name: "relation с пробелом по краю не найден", relation: " blocks"},
		{name: "relation отсутствует в конфиге", relation: "duplicates"},
		{name: "пустой relation не найден", relation: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := cfg.FindIssueLink(tt.relation)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestKindIsList(t *testing.T) {
	tests := []struct {
		kind Kind
		want bool
	}{
		{kind: KindText},
		{kind: KindName},
		{kind: KindOption},
		{kind: KindIssueKey},
		{kind: KindNames, want: true},
		{kind: KindOptions, want: true},
		{kind: KindLabels, want: true},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			require.Equal(t, tt.want, tt.kind.IsList())
		})
	}
}

func TestKinds(t *testing.T) {
	allKinds := []Kind{KindText, KindName, KindNames, KindOption, KindOptions, KindLabels, KindIssueKey}

	returned := Kinds()
	require.Equal(t, allKinds, returned)

	returned[0] = KindLabels
	require.Equal(t, allKinds, Kinds())
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
			name:         "env: только чтение из MCP_JIRA_TOKEN",
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
		{name: "токен как есть", stored: []byte("jira-pat-secret"), want: "jira-pat-secret"},
		{name: "пробелы и перевод строки обрезаются", stored: []byte("  jira-pat-secret\r\n"), want: "jira-pat-secret"},
		{name: "пустой токен", stored: []byte(" \n"), wantErr: ErrTokenUnavailable},
		{name: "нет секрета", stored: []byte("jira-pat-secret"), storeErr: secret.ErrNotFound, wantErr: secret.ErrNotFound},
		{name: "ошибка хранилища сохраняется в цепочке ошибок", stored: []byte("jira-pat-secret"), storeErr: errRead, wantErr: errRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenReader := mocks.NewMockReader(gomock.NewController(t))
			tokenReader.EXPECT().Get(TokenSecretName).Return(tt.stored, tt.storeErr)

			got, err := LoadToken(tokenReader)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, ErrTokenUnavailable)
				require.ErrorIs(t, err, tt.wantErr)
				require.NotContains(t, err.Error(), "jira-pat-secret")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
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
		{name: "путь от ~/ раскрывается в домашний каталог", in: "~/.config/mcp-jira/token", want: filepath.Join(home, ".config/mcp-jira/token")},
		{name: "только тильда", in: "~", want: home},
		{name: "абсолютный путь", in: "/var/token", want: "/var/token"},
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
	path := filepath.Join(home, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(fullYAML), 0o600))

	got, err := Load(path)

	require.NoError(t, err)
	require.Equal(t, SecretsModeKeychain, got.SecretsMode)
	require.Equal(t, filepath.Join(home, ".config/mcp-jira/secrets"), got.SecretsDir)
	require.Equal(t, filepath.Join(home, "Library/Keychains/scoped-mcp.keychain-db"), got.KeychainPath)
	require.Equal(t, "https://jira.example.com", got.BaseURL)
	require.Len(t, got.Projects, 1)
	require.Equal(t, []string{filepath.Join(home, "agent-out"), "/var/reports"}, got.Attachments.Dirs)
}

func TestLoadRejectsSecretsPathsWithoutHome(t *testing.T) {
	tests := []struct {
		name             string
		absolutePathLine string
	}{
		{name: "secrets_dir по умолчанию от ~/", absolutePathLine: "keychain_path: /var/scoped-mcp.keychain-db\n"},
		{name: "keychain_path по умолчанию от ~/", absolutePathLine: "secrets_dir: /var/mcp-jira/secrets\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", "")
			path := filepath.Join(t.TempDir(), "config.yaml")
			configYAML := "base_url: https://jira.example.com\n" + tt.absolutePathLine + "tools: {add_comment: true}\nprojects: [{key: PROJ}]\n"
			require.NoError(t, os.WriteFile(path, []byte(configYAML), 0o600))

			_, err := Load(path)

			require.ErrorIs(t, err, ErrHomeUnknown)
		})
	}
}

func TestLoadRejectsAttachmentDirs(t *testing.T) {
	absoluteHome := t.TempDir()
	linkToHome := filepath.Join(t.TempDir(), "home-link")
	require.NoError(t, os.Symlink(absoluteHome, linkToHome))
	linkToHomeParent := filepath.Join(t.TempDir(), "home-parent-link")
	require.NoError(t, os.Symlink(filepath.Dir(absoluteHome), linkToHomeParent))
	accountHome := existingAccountHome()
	tests := []struct {
		name string
		home string
		dir  string
	}{
		{name: "каталог от ~/ при относительном HOME", home: "relative-home", dir: "~/out"},
		{name: "каталог от ~/ без HOME", home: "", dir: "~/out"},
		{name: "каталог — домашний по абсолютному пути", home: absoluteHome, dir: absoluteHome},
		{name: "каталог — домашний по абсолютному пути со слэшем", home: absoluteHome, dir: absoluteHome + "/"},
		{name: "каталог — домашний ~", home: absoluteHome, dir: "~"},
		{name: "каталог — домашний ~/", home: absoluteHome, dir: "~/"},
		{name: "каталог над домашним ~/..", home: absoluteHome, dir: "~/.."},
		{name: "каталог над домашним ~/../..", home: absoluteHome, dir: "~/../.."},
		{name: "родитель абсолютного HOME", home: absoluteHome, dir: filepath.Dir(absoluteHome)},
		{name: "корень", home: absoluteHome, dir: "/"},
		{name: "корень через ..", home: absoluteHome, dir: "/usr/.."},
		{name: "симлинк на домашний", home: absoluteHome, dir: linkToHome},
		{name: "симлинк на родителя домашнего", home: absoluteHome, dir: linkToHomeParent},
		{name: "домашний из учётной записи при пустом HOME", home: "", dir: accountHome},
		{name: "домашний из учётной записи при относительном HOME", home: "relative-home", dir: accountHome},
		{name: "домашний из учётной записи при другом абсолютном HOME", home: absoluteHome, dir: accountHome},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.dir == "" {
				t.Skip("the account has no existing home directory")
			}
			t.Setenv("HOME", tt.home)
			path := filepath.Join(t.TempDir(), "config.yaml")
			configYAML := `
base_url: https://jira.example.com
secrets_dir: /var/mcp-jira/secrets
keychain_path: /var/scoped-mcp.keychain-db
tools: {create_issue: true, attach_files: true}
projects:
  - key: PROJ
    issue_types: [{name: Task, id: "3"}]
attachments:
  dirs: ["` + tt.dir + `"]
  extensions: [.md]
  max_bytes: 1024
`
			require.NoError(t, os.WriteFile(path, []byte(configYAML), 0o600))

			_, err := Load(path)

			require.ErrorIs(t, err, ErrAttachmentsDirs)
		})
	}
}

func TestHomeGuardIsInsideDirAvoidingHomeAndRoot(t *testing.T) {
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
			require.Equal(t, tt.want, NewHomeGuard().IsInsideDirAvoidingHomeAndRoot(tt.path, tt.dir))
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

func TestIssueKeyProjectAndExpr(t *testing.T) {
	tests := []struct {
		key         string
		wantProject string
		wantOK      bool
	}{
		{key: "PROJ-7790", wantProject: "PROJ", wantOK: true},
		{key: "A1_B-1", wantProject: "A1_B", wantOK: true},
		{key: "proj-1"},
		{key: "PROJ-0"},
		{key: "PROJ-01"},
		{key: "PROJ"},
		{key: "M-1"},
		{key: "PROJ-1/attachments"},
		{key: "10001"},
		{key: ""},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			project, ok := IssueKeyProject(tt.key)

			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantProject, project)
			require.Equal(t, tt.wantOK, regexp.MustCompile(IssueKeyExpr).MatchString(tt.key))
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
		{fileName: "report.md.txt"},
		{fileName: "md"},
		{fileName: "report."},
		{fileName: "report"},
		{fileName: ".md", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.fileName, func(t *testing.T) {
			require.Equal(t, tt.want, attachments.AllowsExtension(tt.fileName))
		})
	}
}
