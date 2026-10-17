package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/env"
	"scoped-mcp/pkg/secret/file"
	"scoped-mcp/pkg/secret/keychain"
)

type Tools struct {
	GetPage          bool `yaml:"get_page,omitempty"`
	ListChildren     bool `yaml:"list_children,omitempty"`
	Search           bool `yaml:"search,omitempty"`
	ListComments     bool `yaml:"list_comments,omitempty"`
	ListAttachments  bool `yaml:"list_attachments,omitempty"`
	GetAttachment    bool `yaml:"get_attachment,omitempty"`
	CreatePage       bool `yaml:"create_page,omitempty"`
	UpdatePage       bool `yaml:"update_page,omitempty"`
	UploadAttachment bool `yaml:"upload_attachment,omitempty"`
}

func (t Tools) AnyRead() bool {
	return t.GetPage || t.ListChildren || t.Search || t.ListComments || t.ListAttachments || t.GetAttachment
}

func (t Tools) AnyWrite() bool {
	return t.CreatePage || t.UpdatePage || t.UploadAttachment
}

func (t Tools) UsesAttachments() bool {
	return t.GetAttachment || t.UploadAttachment
}

type Attachments struct {
	DownloadDir string   `yaml:"download_dir"`
	UploadDirs  []string `yaml:"upload_dirs"`
	Extensions  []string `yaml:"extensions"`
	MaxBytes    int64    `yaml:"max_bytes"`
}

type Search struct {
	MaxResults int `yaml:"max_results"`
}

type SecretsMode string

const (
	SecretsModeEnv      SecretsMode = "env"
	SecretsModeFile     SecretsMode = "file"
	SecretsModeKeychain SecretsMode = "keychain"
)

type Config struct {
	BaseURL      string       `yaml:"base_url"`
	SecretsMode  SecretsMode  `yaml:"secrets"`
	SecretsDir   string       `yaml:"secrets_dir"`
	KeychainPath string       `yaml:"keychain_path"`
	ReadRoots    []int64      `yaml:"read_roots"`
	WriteRoots   []int64      `yaml:"write_roots"`
	Tools        Tools        `yaml:"tools"`
	Attachments  *Attachments `yaml:"attachments,omitempty"`
	Search       Search       `yaml:"search"`
}

type TokenStore struct {
	Reader        secret.Reader
	WritableStore secret.Store
	Location      string
}

const (
	DefaultPath              = "~/.config/mcp-confluence/config.yaml"
	DefaultSearchMaxResults  = 25
	SearchMaxResultsLimit    = 100
	AttachmentsMaxBytesLimit = 20 << 20

	DefaultSecretsDir   = "~/.config/mcp-confluence/secrets"
	DefaultKeychainPath = "~/Library/Keychains/scoped-mcp.keychain-db"

	TokenSecretName = "token"
	TokenEnvVar     = "MCP_CONFLUENCE_TOKEN"

	envPrefix       = "MCP_CONFLUENCE_"
	keychainService = "mcp-confluence"
)

var (
	ErrDecode                     = errors.New("config: cannot decode YAML into config")
	ErrBaseURL                    = errors.New("config: base_url must be an absolute https URL without userinfo, query and fragment")
	ErrSecretsMode                = errors.New("config: secrets must be env, file or keychain")
	ErrTokenUnavailable           = errors.New("config: token is not available")
	ErrNoTools                    = errors.New("config: tools must enable at least one tool")
	ErrWriteRootsRequired         = errors.New("config: tools.create_page, tools.update_page and tools.upload_attachment require a non-empty write_roots")
	ErrReadZoneEmpty              = errors.New("config: read tools require at least one id in read_roots or write_roots")
	ErrRootID                     = errors.New("config: root ids must be positive")
	ErrDuplicateRoot              = errors.New("config: root id repeats within read_roots, within write_roots or across them")
	ErrAttachmentsSectionMismatch = errors.New("config: the attachments section must be set exactly when tools.get_attachment or tools.upload_attachment is enabled")
	ErrSearchMaxResults           = fmt.Errorf("config: search.max_results must be between 1 and %d", SearchMaxResultsLimit)
	ErrHomeUnknown                = errors.New("config: home directory is unknown, ~ cannot be expanded")
	ErrAttachmentsDownloadDir     = errors.New("config: attachments.download_dir must be absolute or start with ~/ and must not be the home directory, its parent or the root")
	ErrAttachmentsUploadDirs      = errors.New("config: attachments.upload_dirs must be non-empty, each absolute or starting with ~/, none giving access to the home directory")
	ErrAttachmentsExtensions      = errors.New("config: attachments.extensions must be non-empty, each like .md, without / and not repeated in any letter case")
	ErrAttachmentsMaxBytes        = fmt.Errorf("config: attachments.max_bytes must be between 1 and %d", AttachmentsMaxBytesLimit)
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
		return Config{}, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")
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
		Search:       Search{MaxResults: DefaultSearchMaxResults},
	}
}

func (c Config) validate() error {
	if err := validateBaseURL(c.BaseURL); err != nil {
		return err
	}
	if err := c.validateSecretsMode(); err != nil {
		return err
	}
	if err := c.validateTools(); err != nil {
		return err
	}
	if err := c.validateAttachmentsSection(); err != nil {
		return err
	}
	if err := c.validateAttachments(); err != nil {
		return err
	}
	if err := c.validateRootIDs(); err != nil {
		return err
	}
	if err := c.validateRootsCoverTools(); err != nil {
		return err
	}
	return c.validateSearch()
}

func validateBaseURL(baseURL string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("%w: base_url is not a parsable URL", ErrBaseURL)
	}
	if !isAbsoluteHTTPS(parsed) || hasUserinfo(parsed) || hasQueryOrFragment(parsed) {
		return fmt.Errorf("%w: %q", ErrBaseURL, parsed.Redacted())
	}
	return nil
}

func isAbsoluteHTTPS(u *url.URL) bool {
	return u.Scheme == "https" && u.Host != ""
}

func hasUserinfo(u *url.URL) bool {
	return u.User != nil
}

func hasQueryOrFragment(u *url.URL) bool {
	return u.RawQuery != "" || u.ForceQuery || u.Fragment != ""
}

func (c Config) validateSecretsMode() error {
	_, err := c.TokenStore()
	return err
}

func (c Config) validateTools() error {
	if !c.Tools.AnyRead() && !c.Tools.AnyWrite() {
		return ErrNoTools
	}
	return nil
}

func (c Config) validateAttachmentsSection() error {
	hasAttachmentsSection := c.Attachments != nil
	if c.Tools.UsesAttachments() != hasAttachmentsSection {
		return ErrAttachmentsSectionMismatch
	}
	return nil
}

func (c Config) validateAttachments() error {
	if c.Attachments == nil {
		return nil
	}
	return c.Attachments.validate()
}

func (a Attachments) validate() error {
	if !isAbsOrHomeRelative(a.DownloadDir) {
		return fmt.Errorf("%w: %q", ErrAttachmentsDownloadDir, a.DownloadDir)
	}
	if len(a.UploadDirs) == 0 {
		return ErrAttachmentsUploadDirs
	}
	for _, uploadDir := range a.UploadDirs {
		if !isAbsOrHomeRelative(uploadDir) {
			return fmt.Errorf("%w: %q", ErrAttachmentsUploadDirs, uploadDir)
		}
	}
	if err := validateExtensions(a.Extensions); err != nil {
		return err
	}
	if a.MaxBytes <= 0 || a.MaxBytes > AttachmentsMaxBytesLimit {
		return fmt.Errorf("%w: %d", ErrAttachmentsMaxBytes, a.MaxBytes)
	}
	return nil
}

func isAbsOrHomeRelative(dir string) bool {
	return filepath.IsAbs(dir) || isHomeRelative(dir)
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

func (c Config) validateRootIDs() error {
	owners := make(map[int64]string, len(c.ReadRoots)+len(c.WriteRoots))
	lists := []struct {
		name string
		ids  []int64
	}{{name: "read_roots", ids: c.ReadRoots}, {name: "write_roots", ids: c.WriteRoots}}
	for _, list := range lists {
		for _, id := range list.ids {
			if id <= 0 {
				return fmt.Errorf("%w: %s has %d", ErrRootID, list.name, id)
			}
			if owner, dup := owners[id]; dup {
				return fmt.Errorf("%w: %d in %s and %s", ErrDuplicateRoot, id, owner, list.name)
			}
			owners[id] = list.name
		}
	}
	return nil
}

func (c Config) validateRootsCoverTools() error {
	if c.Tools.AnyWrite() && len(c.WriteRoots) == 0 {
		return ErrWriteRootsRequired
	}
	if c.Tools.AnyRead() && len(c.ReadRoots)+len(c.WriteRoots) == 0 {
		return ErrReadZoneEmpty
	}
	return nil
}

func (c Config) validateSearch() error {
	if c.Search.MaxResults < 1 || c.Search.MaxResults > SearchMaxResultsLimit {
		return fmt.Errorf("%w: got %d", ErrSearchMaxResults, c.Search.MaxResults)
	}
	return nil
}

func (c Config) ReadZoneRoots() []string {
	return formatSortedIDs(slices.Concat(c.ReadRoots, c.WriteRoots))
}

func (c Config) WriteZoneRoots() []string {
	return formatSortedIDs(c.WriteRoots)
}

func formatSortedIDs(ids []int64) []string {
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	formatted := make([]string, 0, len(sorted))
	for _, id := range sorted {
		formatted = append(formatted, strconv.FormatInt(id, 10))
	}
	return formatted
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

func (c *Config) expandPaths() error {
	if c.Attachments != nil {
		if err := c.Attachments.expandDirsOutsideHome(); err != nil {
			return err
		}
	}
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
	return nil
}

func (a *Attachments) expandDirsOutsideHome() error {
	homeGuard := NewHomeGuard()
	downloadDir, err := expandDirOutsideHome(a.DownloadDir, homeGuard, ErrAttachmentsDownloadDir)
	if err != nil {
		return err
	}
	uploadDirs := make([]string, 0, len(a.UploadDirs))
	for _, uploadDir := range a.UploadDirs {
		expandedUploadDir, err := expandDirOutsideHome(uploadDir, homeGuard, ErrAttachmentsUploadDirs)
		if err != nil {
			return err
		}
		uploadDirs = append(uploadDirs, expandedUploadDir)
	}
	a.DownloadDir = downloadDir
	a.UploadDirs = uploadDirs
	return nil
}

func expandDirOutsideHome(dir string, homeGuard HomeGuard, fieldErr error) (string, error) {
	expanded, err := ExpandHome(dir)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %w", fieldErr, dir, err)
	}
	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("%w: %q expands to relative %q", fieldErr, dir, expanded)
	}
	cleaned := filepath.Clean(expanded)
	if homeGuard.IsHomeOrAboveHome(cleaned) {
		return "", fmt.Errorf("%w: %q gives access to the home directory", fieldErr, dir)
	}
	return cleaned, nil
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
	return g.mayBeHomeOrRoot(realDir) || g.containsAnyHome(realDir)
}

func (g HomeGuard) containsAnyHome(realDir string) bool {
	return slices.ContainsFunc(g.realHomes, func(realHome string) bool { return IsInsideDir(realHome, realDir) })
}

func (g HomeGuard) IsInsideDirWithoutCrossingHomeOrRoot(path, dir string) bool {
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

func (c Config) TokenStore() (TokenStore, error) {
	switch c.SecretsMode {
	case SecretsModeEnv:
		return TokenStore{Reader: env.New(envPrefix), Location: TokenEnvVar}, nil
	case SecretsModeFile:
		return writableTokenStore(file.New(c.SecretsDir), filepath.Join(c.SecretsDir, TokenSecretName)), nil
	case SecretsModeKeychain:
		if !keychain.Supported() {
			return TokenStore{}, fmt.Errorf("%w: %w", ErrSecretsMode, keychain.ErrUnsupported)
		}
		return writableTokenStore(keychain.New(c.KeychainPath, keychainService), c.KeychainPath), nil
	default:
		return TokenStore{}, fmt.Errorf("%w: got %q", ErrSecretsMode, c.SecretsMode)
	}
}

func writableTokenStore(secretStore secret.Store, location string) TokenStore {
	return TokenStore{Reader: secretStore, WritableStore: secretStore, Location: location}
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
