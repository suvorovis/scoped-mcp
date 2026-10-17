package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/env"
	"scoped-mcp/pkg/secret/file"
	"scoped-mcp/pkg/secret/keychain"
)

type Tools struct {
	AddMRNote         bool `yaml:"add_mr_note,omitempty"`
	AddMRLineComment  bool `yaml:"add_mr_line_comment,omitempty"`
	ReplyToDiscussion bool `yaml:"reply_to_discussion,omitempty"`
	AddDraftNote      bool `yaml:"add_draft_note,omitempty"`
}

func (t Tools) AnyEnabled() bool {
	return t.AddMRNote || t.AddMRLineComment || t.ReplyToDiscussion || t.AddDraftNote
}

func (t Tools) NeedsDiffPositions() bool {
	return t.AddMRLineComment || t.AddDraftNote
}

type SecretsMode string

const (
	SecretsModeEnv      SecretsMode = "env"
	SecretsModeFile     SecretsMode = "file"
	SecretsModeKeychain SecretsMode = "keychain"
)

type Config struct {
	BaseURL      string      `yaml:"base_url"`
	SecretsMode  SecretsMode `yaml:"secrets"`
	SecretsDir   string      `yaml:"secrets_dir"`
	KeychainPath string      `yaml:"keychain_path"`
	Tools        Tools       `yaml:"tools"`
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
	DefaultPath = "~/.config/mcp-gitlab/config.yaml"

	DefaultSecretsDir   = "~/.config/mcp-gitlab/secrets"
	DefaultKeychainPath = "~/Library/Keychains/scoped-mcp.keychain-db"

	TokenSecretName = "token"
	TokenEnvVar     = envPrefix + "TOKEN"

	envPrefix       = "MCP_GITLAB_"
	keychainService = "mcp-gitlab"
)

var (
	ErrMalformed        = errors.New("config: malformed YAML")
	ErrBaseURL          = errors.New("config: base_url must be an absolute https URL without userinfo, path, query and fragment")
	ErrSecretsMode      = errors.New("config: secrets must be env, file or keychain")
	ErrSecretsPath      = errors.New("config: secrets_dir and keychain_path must be absolute or start with ~/")
	ErrTokenUnavailable = errors.New("config: token is not available")
	ErrNoTools          = errors.New("config: tools must enable at least one tool")
	ErrHomeUnknown      = errors.New("config: home directory is unknown, ~ cannot be expanded")
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
	}
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
	if !c.Tools.AnyEnabled() {
		return ErrNoTools
	}
	return nil
}

func validateBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: not a parsable URL", ErrBaseURL)
	}
	if !isHTTPSOrigin(parsed) {
		return fmt.Errorf("%w: %q", ErrBaseURL, parsed.Redacted())
	}
	return nil
}

func isHTTPSOrigin(u *url.URL) bool {
	return u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Opaque == "" &&
		u.Path == "" && u.RawPath == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
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

func (c Config) TokenStore() TokenStore {
	switch c.SecretsMode {
	case SecretsModeEnv:
		return TokenStore{SecretsMode: c.SecretsMode, TokenReader: env.New(envPrefix), Location: TokenEnvVar}
	case SecretsModeFile:
		fileStore := file.New(c.SecretsDir)
		return TokenStore{SecretsMode: c.SecretsMode, TokenReader: fileStore, TokenWriter: fileStore, Location: filepath.Join(c.SecretsDir, TokenSecretName)}
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
