package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/env"
	"scoped-mcp/pkg/secret/file"
	"scoped-mcp/pkg/secret/keychain"
)

type Kind string

const (
	KindText     Kind = "text"
	KindName     Kind = "name"
	KindNames    Kind = "names"
	KindOption   Kind = "option"
	KindOptions  Kind = "options"
	KindLabels   Kind = "labels"
	KindIssueKey Kind = "issue_key"
)

func (k Kind) IsList() bool {
	switch k {
	case KindNames, KindOptions, KindLabels:
		return true
	default:
		return false
	}
}

func Kinds() []Kind {
	return slices.Clone(knownKinds)
}

func (k Kind) known() bool {
	return slices.Contains(knownKinds, k)
}

type Field struct {
	Jira     string `yaml:"jira"`
	Kind     Kind   `yaml:"kind"`
	Required bool   `yaml:"required,omitempty"`
}

type IssueType struct {
	Name   string   `yaml:"name"`
	ID     string   `yaml:"id"`
	Fields []string `yaml:"fields"`
}

type Project struct {
	Key          string      `yaml:"key"`
	AssignToSelf bool        `yaml:"assign_to_self,omitempty"`
	IssueTypes   []IssueType `yaml:"issue_types"`
	UpdateFields []string    `yaml:"update_fields,omitempty"`
}

type Attachments struct {
	Dirs       []string `yaml:"dirs"`
	Extensions []string `yaml:"extensions"`
	MaxBytes   int64    `yaml:"max_bytes"`
}

type LinkDirection string

const (
	LinkOutward LinkDirection = "outward"
	LinkInward  LinkDirection = "inward"
)

type IssueLink struct {
	Relation  string        `yaml:"relation"`
	Type      string        `yaml:"type"`
	Direction LinkDirection `yaml:"direction"`
}

type Tools struct {
	CreateIssue bool `yaml:"create_issue,omitempty"`
	AttachFiles bool `yaml:"attach_files,omitempty"`
	AddComment  bool `yaml:"add_comment,omitempty"`
	LinkIssues  bool `yaml:"link_issues,omitempty"`
	UpdateIssue bool `yaml:"update_issue,omitempty"`
}

type SecretsMode string

const (
	SecretsModeEnv      SecretsMode = "env"
	SecretsModeFile     SecretsMode = "file"
	SecretsModeKeychain SecretsMode = "keychain"
)

type Config struct {
	BaseURL      string           `yaml:"base_url"`
	SecretsMode  SecretsMode      `yaml:"secrets"`
	SecretsDir   string           `yaml:"secrets_dir"`
	KeychainPath string           `yaml:"keychain_path"`
	Tools        Tools            `yaml:"tools"`
	Fields       map[string]Field `yaml:"fields"`
	Projects     []Project        `yaml:"projects"`
	Attachments  *Attachments     `yaml:"attachments,omitempty"`
	IssueLinks   []IssueLink      `yaml:"issue_links,omitempty"`
}

type TokenStore struct {
	SecretsMode SecretsMode
	TokenReader secret.Reader
	TokenWriter secret.Store
	Location    string
}

func (s TokenStore) Writable() bool {
	return s.TokenWriter != nil
}

func (s TokenStore) UsesKeychain() bool {
	return s.SecretsMode == SecretsModeKeychain
}

const (
	DefaultPath = "~/.config/mcp-jira/config.yaml"

	DefaultSecretsDir   = "~/.config/mcp-jira/secrets"
	DefaultKeychainPath = "~/Library/Keychains/scoped-mcp.keychain-db"

	TokenSecretName = "token"
	TokenEnvVar     = envPrefix + "TOKEN"

	envPrefix       = "MCP_JIRA_"
	keychainService = "mcp-jira"

	MaxAttachmentsMaxBytes = 20 << 20
)

const (
	projectKeyFragment  = `[A-Z][A-Z0-9_]+`
	issueNumberFragment = `-[1-9][0-9]*`
	IssueKeyExpr        = `^` + projectKeyFragment + issueNumberFragment + `$`
)

var (
	knownKinds              = []Kind{KindText, KindName, KindNames, KindOption, KindOptions, KindLabels, KindIssueKey}
	projectKeyPattern       = regexp.MustCompile(`^` + projectKeyFragment + `$`)
	issueKeyPattern         = regexp.MustCompile(`^(?P<project>` + projectKeyFragment + `)` + issueNumberFragment + `$`)
	reservedJiraFieldIDs    = []string{"project", "issuetype", "summary", "issuelinks", "assignee"}
	reservedConfigFieldKeys = []string{"project", "issue_type", "summary", "assignee", "issue_key"}
)

var (
	ErrMalformed          = errors.New("config: malformed YAML")
	ErrBaseURL            = errors.New("config: base_url must be an absolute https URL without userinfo, query and fragment")
	ErrNoProjects         = errors.New("config: projects list is empty")
	ErrProjectKey         = errors.New("config: invalid project key")
	ErrDuplicateProject   = errors.New("config: duplicate project key")
	ErrNoIssueTypes       = errors.New("config: tools.create_issue needs issue_types in at least one project")
	ErrIssueTypeName      = errors.New("config: issue type name is empty")
	ErrIssueTypeID        = errors.New("config: issue type id is empty")
	ErrDuplicateIssueType = errors.New("config: duplicate issue type name or id within a project")
	ErrUnknownField       = errors.New("config: issue type or update_fields refers to a field missing from fields")
	ErrDuplicateField     = errors.New("config: duplicate field within an issue type or update_fields")
	ErrFieldKind          = errors.New("config: unknown field kind")
	ErrFieldJira          = errors.New("config: field jira id is empty or reserved")
	ErrFieldKey           = errors.New("config: field key is reserved")
	ErrDuplicateJiraField = errors.New("config: two fields refer to the same Jira field")
	ErrSecretsMode        = errors.New("config: secrets must be env, file or keychain")
	ErrSecretsPath        = errors.New("config: secrets_dir and keychain_path must be absolute or start with ~/")
	ErrTokenUnavailable   = errors.New("config: token is not available")
	ErrHomeUnknown        = errors.New("config: home directory is unknown, ~ cannot be expanded")

	ErrNoTools             = errors.New("config: tools must enable at least one tool")
	ErrAttachmentsSection  = errors.New("config: tools.attach_files and the attachments section must be set together")
	ErrIssueLinksSection   = errors.New("config: tools.link_issues and a non-empty issue_links section must be set together")
	ErrUpdateIssueFields   = errors.New("config: tools.update_issue and a non-empty update_fields in at least one project must be set together")
	ErrFieldsSettings      = errors.New("config: fields are used only by tools.create_issue and tools.update_issue")
	ErrCreateIssueSettings = errors.New("config: issue_types and assign_to_self are used only by tools.create_issue")

	ErrAttachmentsDirs       = errors.New("config: attachments.dirs must be non-empty, each absolute or starting with ~/, none giving access to the home directory")
	ErrAttachmentsExtensions = errors.New("config: attachments.extensions must be non-empty, each like .md, without / and not repeated in any letter case")
	ErrAttachmentsMaxBytes   = fmt.Errorf("config: attachments.max_bytes must be positive and at most %d", MaxAttachmentsMaxBytes)

	ErrIssueLinkRelation  = errors.New("config: issue_links relation must be non-empty, without surrounding spaces and unique")
	ErrIssueLinkType      = errors.New("config: issue_links type must be non-empty")
	ErrIssueLinkDirection = errors.New("config: issue_links direction must be outward or inward")
)

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.expandPaths(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Parse(data []byte) (Config, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	cfg := defaults()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	cfg.normalize()
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaults() Config {
	return Config{
		SecretsMode:  SecretsModeEnv,
		SecretsDir:   DefaultSecretsDir,
		KeychainPath: DefaultKeychainPath,
	}
}

func (c *Config) normalize() {
	c.BaseURL = strings.TrimSuffix(c.BaseURL, "/")
}

func (c *Config) expandPaths() error {
	secretsDir, err := ExpandHome(c.SecretsDir)
	if err != nil {
		return err
	}
	keychainPath, err := ExpandHome(c.KeychainPath)
	if err != nil {
		return err
	}
	c.SecretsDir = secretsDir
	c.KeychainPath = keychainPath
	if !c.Tools.AttachFiles {
		return nil
	}
	return c.Attachments.expandAndCheckDirs()
}

func (a *Attachments) expandAndCheckDirs() error {
	homeGuard := NewHomeGuard()
	expandedDirs := make([]string, 0, len(a.Dirs))
	for _, dir := range a.Dirs {
		expanded, err := ExpandHome(dir)
		if err != nil {
			return fmt.Errorf("%w: %q: %w", ErrAttachmentsDirs, dir, err)
		}
		if !filepath.IsAbs(expanded) {
			return fmt.Errorf("%w: %q expands to relative %q", ErrAttachmentsDirs, dir, expanded)
		}
		if homeGuard.IsHomeOrAboveHome(expanded) {
			return fmt.Errorf("%w: %q gives access to the home directory", ErrAttachmentsDirs, dir)
		}
		expandedDirs = append(expandedDirs, expanded)
	}
	a.Dirs = expandedDirs
	return nil
}

func (c Config) validate() error {
	if err := validateBaseURL(c.BaseURL); err != nil {
		return err
	}
	if err := c.SecretsMode.validate(); err != nil {
		return err
	}
	if err := c.validateSecretsPaths(); err != nil {
		return err
	}
	if err := c.validateTools(); err != nil {
		return err
	}
	if err := validateFields(c.Fields); err != nil {
		return err
	}
	if err := validateProjects(c.Projects, c.Fields); err != nil {
		return err
	}
	if c.Tools.CreateIssue && !c.anyProjectHasIssueTypes() {
		return ErrNoIssueTypes
	}
	if c.Tools.AttachFiles {
		if err := c.Attachments.validate(); err != nil {
			return err
		}
	}
	if c.Tools.LinkIssues {
		if err := validateIssueLinks(c.IssueLinks); err != nil {
			return err
		}
	}
	return nil
}

func (c Config) validateTools() error {
	if !c.Tools.CreateIssue && !c.Tools.AttachFiles && !c.Tools.AddComment && !c.Tools.LinkIssues && !c.Tools.UpdateIssue {
		return ErrNoTools
	}
	hasAttachmentsSection := c.Attachments != nil
	if c.Tools.AttachFiles != hasAttachmentsSection {
		return ErrAttachmentsSection
	}
	hasIssueLinks := len(c.IssueLinks) > 0
	if c.Tools.LinkIssues != hasIssueLinks {
		return ErrIssueLinksSection
	}
	if c.Tools.UpdateIssue != c.anyProjectHasUpdateFields() {
		return ErrUpdateIssueFields
	}
	if !c.Tools.CreateIssue && !c.Tools.UpdateIssue && c.hasFields() {
		return ErrFieldsSettings
	}
	if !c.Tools.CreateIssue && c.hasCreateIssueSettings() {
		return ErrCreateIssueSettings
	}
	return nil
}

func (c Config) hasFields() bool {
	return len(c.Fields) > 0
}

func (c Config) anyProjectHasUpdateFields() bool {
	return slices.ContainsFunc(c.Projects, func(project Project) bool { return len(project.UpdateFields) > 0 })
}

func (c Config) anyProjectHasIssueTypes() bool {
	return slices.ContainsFunc(c.Projects, Project.HasIssueTypes)
}

func (c Config) hasCreateIssueSettings() bool {
	return c.anyProjectHasIssueTypes() || slices.ContainsFunc(c.Projects, func(project Project) bool { return project.AssignToSelf })
}

func (a Attachments) validate() error {
	if len(a.Dirs) == 0 {
		return ErrAttachmentsDirs
	}
	for _, dir := range a.Dirs {
		if !isHomeRelative(dir) && !filepath.IsAbs(dir) {
			return fmt.Errorf("%w: %q", ErrAttachmentsDirs, dir)
		}
	}
	if err := validateExtensions(a.Extensions); err != nil {
		return err
	}
	if a.MaxBytes <= 0 || a.MaxBytes > MaxAttachmentsMaxBytes {
		return fmt.Errorf("%w: %d", ErrAttachmentsMaxBytes, a.MaxBytes)
	}
	return nil
}

func validateIssueLinks(links []IssueLink) error {
	seenRelations := make(map[string]struct{}, len(links))
	for _, link := range links {
		_, dup := seenRelations[link.Relation]
		wellFormed := link.Relation != "" && strings.TrimSpace(link.Relation) == link.Relation
		if dup || !wellFormed {
			return fmt.Errorf("%w: %q", ErrIssueLinkRelation, link.Relation)
		}
		seenRelations[link.Relation] = struct{}{}
		if link.Type == "" {
			return fmt.Errorf("%w: relation %q", ErrIssueLinkType, link.Relation)
		}
		if link.Direction != LinkOutward && link.Direction != LinkInward {
			return fmt.Errorf("%w: relation %q has direction %q", ErrIssueLinkDirection, link.Relation, link.Direction)
		}
	}
	return nil
}

type HomeGuard struct {
	realHomes        []string
	homeAndRootInfos []os.FileInfo
}

func NewHomeGuard() HomeGuard {
	var guard HomeGuard
	if rootInfo, err := os.Stat(string(filepath.Separator)); err == nil {
		guard.homeAndRootInfos = append(guard.homeAndRootInfos, rootInfo)
	}
	for _, home := range absoluteHomeDirs() {
		realHome, err := filepath.EvalSymlinks(home)
		if err != nil {
			continue
		}
		homeInfo, err := os.Stat(realHome)
		if err != nil {
			continue
		}
		guard.realHomes = append(guard.realHomes, realHome)
		guard.homeAndRootInfos = append(guard.homeAndRootInfos, homeInfo)
	}
	return guard
}

func absoluteHomeDirs() []string {
	homes := []string{}
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		homes = append(homes, home)
	}
	if account, err := user.Current(); err == nil && filepath.IsAbs(account.HomeDir) {
		homes = append(homes, account.HomeDir)
	}
	return homes
}

func (g HomeGuard) IsHomeOrAboveHome(dir string) bool {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	return g.mayBeHomeOrRoot(realDir) || slices.ContainsFunc(g.realHomes, func(realHome string) bool { return IsInsideDir(realHome, realDir) })
}

func (g HomeGuard) IsInsideDirAvoidingHomeAndRoot(path, dir string) bool {
	for current := filepath.Dir(path); IsInsideDir(current, dir); current = filepath.Dir(current) {
		if g.mayBeHomeOrRoot(current) {
			return false
		}
		if current == dir {
			return true
		}
	}
	return false
}

func (g HomeGuard) mayBeHomeOrRoot(dir string) bool {
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return true
	}
	return slices.ContainsFunc(g.homeAndRootInfos, func(guarded os.FileInfo) bool { return os.SameFile(guarded, dirInfo) })
}

func IsInsideDir(path, dir string) bool {
	relative, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsLocal(relative)
}

func validateExtensions(extensions []string) error {
	if len(extensions) == 0 {
		return ErrAttachmentsExtensions
	}
	for i, extension := range extensions {
		if len(extension) < 2 || !strings.HasPrefix(extension, ".") || strings.Contains(extension, "/") {
			return fmt.Errorf("%w: %q", ErrAttachmentsExtensions, extension)
		}
		if containsFold(extensions[:i], extension) {
			return fmt.Errorf("%w: %q repeats", ErrAttachmentsExtensions, extension)
		}
	}
	return nil
}

func (a Attachments) AllowsExtension(fileName string) bool {
	return containsFold(a.Extensions, filepath.Ext(fileName))
}

func containsFold(list []string, target string) bool {
	return slices.ContainsFunc(list, func(item string) bool { return strings.EqualFold(item, target) })
}

func IssueKeyProject(issueKey string) (string, bool) {
	match := issueKeyPattern.FindStringSubmatch(issueKey)
	if match == nil {
		return "", false
	}
	return match[issueKeyPattern.SubexpIndex("project")], true
}

func validateBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBaseURL, err)
	}
	if !isAbsoluteHTTPS(parsed) || parsed.User != nil || hasQueryOrFragment(parsed) {
		return fmt.Errorf("%w: %q", ErrBaseURL, parsed.Redacted())
	}
	return nil
}

func isAbsoluteHTTPS(u *url.URL) bool {
	return u.Scheme == "https" && u.Host != ""
}

func hasQueryOrFragment(u *url.URL) bool {
	return u.RawQuery != "" || u.ForceQuery || u.Fragment != ""
}

func (c Config) validateSecretsPaths() error {
	for _, secretsPath := range []string{c.SecretsDir, c.KeychainPath} {
		if !isAbsOrUnderHome(secretsPath) {
			return fmt.Errorf("%w: %q", ErrSecretsPath, secretsPath)
		}
	}
	return nil
}

func isAbsOrUnderHome(path string) bool {
	return filepath.IsAbs(path) || strings.HasPrefix(path, "~/")
}

func (m SecretsMode) validate() error {
	switch m {
	case SecretsModeEnv, SecretsModeFile:
		return nil
	case SecretsModeKeychain:
		if !keychain.Supported() {
			return fmt.Errorf("%w: %w", ErrSecretsMode, keychain.ErrUnsupported)
		}
		return nil
	default:
		return fmt.Errorf("%w: got %q", ErrSecretsMode, m)
	}
}

func validateFields(fields map[string]Field) error {
	jiraOwners := make(map[string]string, len(fields))
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		field := fields[key]
		if slices.Contains(reservedConfigFieldKeys, key) {
			return fmt.Errorf("%w: %q, reserved: %v", ErrFieldKey, key, reservedConfigFieldKeys)
		}
		if field.Jira == "" || slices.Contains(reservedJiraFieldIDs, field.Jira) {
			return fmt.Errorf("%w: field %q has jira %q, reserved: %v", ErrFieldJira, key, field.Jira, reservedJiraFieldIDs)
		}
		if !field.Kind.known() {
			return fmt.Errorf("%w: field %q has kind %q, allowed: %v", ErrFieldKind, key, field.Kind, knownKinds)
		}
		if owner, dup := jiraOwners[field.Jira]; dup {
			return fmt.Errorf("%w: %q and %q both use %s", ErrDuplicateJiraField, owner, key, field.Jira)
		}
		jiraOwners[field.Jira] = key
	}
	return nil
}

func validateProjects(projects []Project, fields map[string]Field) error {
	if len(projects) == 0 {
		return ErrNoProjects
	}
	seen := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		if !projectKeyPattern.MatchString(project.Key) {
			return fmt.Errorf("%w: %q does not match %s", ErrProjectKey, project.Key, projectKeyPattern)
		}
		if _, dup := seen[project.Key]; dup {
			return fmt.Errorf("%w: %s", ErrDuplicateProject, project.Key)
		}
		seen[project.Key] = struct{}{}
		if err := validateProject(project, fields); err != nil {
			return err
		}
	}
	return nil
}

func validateProject(project Project, fields map[string]Field) error {
	if project.AssignToSelf && !project.HasIssueTypes() {
		return fmt.Errorf("%w: %s has assign_to_self without issue_types", ErrCreateIssueSettings, project.Key)
	}
	seenNames := make(map[string]struct{}, len(project.IssueTypes))
	seenIDs := make(map[string]struct{}, len(project.IssueTypes))
	for _, issueType := range project.IssueTypes {
		if issueType.Name == "" {
			return fmt.Errorf("%w: %s/%s", ErrIssueTypeName, project.Key, issueType.Name)
		}
		if issueType.ID == "" {
			return fmt.Errorf("%w: %s/%s", ErrIssueTypeID, project.Key, issueType.Name)
		}
		_, dupName := seenNames[issueType.Name]
		_, dupID := seenIDs[issueType.ID]
		if dupName || dupID {
			return fmt.Errorf("%w: %s/%s", ErrDuplicateIssueType, project.Key, issueType.Name)
		}
		seenNames[issueType.Name] = struct{}{}
		seenIDs[issueType.ID] = struct{}{}
		if err := validateFieldKeys(issueType.Fields, fields); err != nil {
			return fmt.Errorf("%s/%s: %w", project.Key, issueType.Name, err)
		}
	}
	if err := validateFieldKeys(project.UpdateFields, fields); err != nil {
		return fmt.Errorf("%s update_fields: %w", project.Key, err)
	}
	return nil
}

func validateFieldKeys(fieldKeys []string, fields map[string]Field) error {
	seen := make(map[string]struct{}, len(fieldKeys))
	for _, key := range fieldKeys {
		if _, known := fields[key]; !known {
			return fmt.Errorf("%w: %q", ErrUnknownField, key)
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateField, key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (c Config) FindProject(key string) (Project, bool) {
	for _, project := range c.Projects {
		if project.Key == key {
			return project, true
		}
	}
	return Project{}, false
}

func (c Config) FindIssueLink(relation string) (IssueLink, bool) {
	for _, link := range c.IssueLinks {
		if link.Relation == relation {
			return link, true
		}
	}
	return IssueLink{}, false
}

func (p Project) HasIssueTypes() bool {
	return len(p.IssueTypes) > 0
}

func (p Project) FindIssueType(name string) (IssueType, bool) {
	for _, issueType := range p.IssueTypes {
		if issueType.Name == name {
			return issueType, true
		}
	}
	return IssueType{}, false
}

func (c Config) TokenFilePath() string {
	return filepath.Join(c.SecretsDir, TokenSecretName)
}

func (c Config) TokenStore() TokenStore {
	switch c.SecretsMode {
	case SecretsModeEnv:
		return TokenStore{SecretsMode: c.SecretsMode, TokenReader: env.New(envPrefix), Location: TokenEnvVar}
	case SecretsModeFile:
		fileStore := file.New(c.SecretsDir)
		return TokenStore{SecretsMode: c.SecretsMode, TokenReader: fileStore, TokenWriter: fileStore, Location: c.TokenFilePath()}
	case SecretsModeKeychain:
		keychainStore := keychain.New(c.KeychainPath, keychainService)
		return TokenStore{SecretsMode: c.SecretsMode, TokenReader: keychainStore, TokenWriter: keychainStore, Location: c.KeychainPath}
	default:
		panic(fmt.Sprintf("config: TokenStore called with unvalidated secrets mode %q", c.SecretsMode))
	}
}

func LoadToken(tokenReader secret.Reader) (string, error) {
	value, err := tokenReader.Get(TokenSecretName)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrTokenUnavailable, err)
	}
	token := strings.TrimSpace(string(value))
	if token == "" {
		return "", fmt.Errorf("%w: %s is empty", ErrTokenUnavailable, TokenSecretName)
	}
	return token, nil
}

func ExpandHome(path string) (string, error) {
	if !isHomeRelative(path) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrHomeUnknown, err)
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}

func isHomeRelative(path string) bool {
	return path == "~" || strings.HasPrefix(path, "~/")
}
