package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
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

type ChatType string

const (
	ChatTypeUser    ChatType = "user"
	ChatTypeChat    ChatType = "chat"
	ChatTypeChannel ChatType = "channel"
)

type SecretsMode string

const (
	SecretsModeEnv      SecretsMode = "env"
	SecretsModeFile     SecretsMode = "file"
	SecretsModeKeychain SecretsMode = "keychain"
)

type Chat struct {
	ID         int64    `yaml:"id"`
	Type       ChatType `yaml:"type"`
	AccessHash int64    `yaml:"access_hash,omitempty"`
	Title      string   `yaml:"title,omitempty"`
}

type ChatKey struct {
	Type ChatType
	ID   int64
}

func (c Chat) Key() ChatKey {
	return ChatKey{Type: c.Type, ID: c.ID}
}

type Folder struct {
	ID    int    `yaml:"id"`
	Title string `yaml:"title,omitempty"`
}

type Config struct {
	SecretsMode  SecretsMode `yaml:"secrets"`
	SessionPath  string      `yaml:"session_path"`
	SecretsDir   string      `yaml:"secrets_dir"`
	KeychainPath string      `yaml:"keychain_path"`
	Chats        []Chat      `yaml:"chats"`
	Folders      []Folder    `yaml:"folders"`
}

type Credentials struct {
	APIID   int
	APIHash string
}

type SecretStores struct {
	KeyReader       secret.Reader
	KeyWriter       secret.Store
	SessionStore    secret.Store
	SessionName     string
	SessionLocation string
}

const (
	DefaultPath         = "~/.config/mcp-tg/config.yaml"
	DefaultSecretsDir   = "~/.config/mcp-tg/secrets"
	DefaultKeychainPath = "~/Library/Keychains/scoped-mcp.keychain-db"

	SecretAPIID   = "api_id"
	SecretAPIHash = "api_hash"
	SecretSession = "session"

	envPrefix       = "MCP_TG_"
	keychainService = "mcp-tg"
)

var (
	ErrSessionPathMissing = errors.New("config: session_path is empty")
	ErrSecretsMode        = errors.New("config: secrets must be env, file or keychain")
	ErrEmptyAllowlist     = errors.New("config: both chats and folders are empty, add at least one chat or folder")
	ErrChatIDInvalid      = errors.New("config: chat id must be positive, as printed by mcp-tg dialogs (not the Bot API -100… form)")
	ErrChatType           = errors.New("config: chat type must be user, chat or channel")
	ErrDuplicateChat      = errors.New("config: duplicate chat")
	ErrFolderIDInvalid    = errors.New("config: folder id must be positive, as printed by mcp-tg folders")
	ErrDuplicateFolder    = errors.New("config: duplicate folder id")
	ErrAccessHashMissing  = errors.New("config: access_hash is required for user and channel")
	ErrMalformed          = errors.New("config: malformed YAML")
	ErrAPIIDUnavailable   = errors.New("config: api_id is not available")
	ErrAPIIDInvalid       = errors.New("config: api_id must be an integer")
	ErrAPIHashUnavailable = errors.New("config: api_hash is not available")
)

func Load(path string) (Config, error) {
	expanded, err := ExpandHome(path)
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(expanded)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return Config{}, err
	}
	return cfg.withExpandedPaths()
}

func (c Config) withExpandedPaths() (Config, error) {
	var err error
	c.SessionPath, err = ExpandHome(c.SessionPath)
	if err != nil {
		return Config{}, err
	}
	c.SecretsDir, err = ExpandHome(c.SecretsDir)
	if err != nil {
		return Config{}, err
	}
	c.KeychainPath, err = ExpandHome(c.KeychainPath)
	if err != nil {
		return Config{}, err
	}
	return c, nil
}

func Parse(data []byte) (Config, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	cfg := Config{SecretsMode: SecretsModeEnv, SecretsDir: DefaultSecretsDir, KeychainPath: DefaultKeychainPath}
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if err := validateSecretsMode(cfg); err != nil {
		return Config{}, err
	}
	if err := validateChats(cfg.Chats); err != nil {
		return Config{}, err
	}
	if err := validateFolders(cfg.Folders); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validateSecretsMode(cfg Config) error {
	switch cfg.SecretsMode {
	case SecretsModeEnv:
		if cfg.SessionPath == "" {
			return ErrSessionPathMissing
		}
		return nil
	case SecretsModeFile:
		return nil
	case SecretsModeKeychain:
		if !keychain.Supported() {
			return keychain.ErrUnsupported
		}
		return nil
	default:
		return fmt.Errorf("%w: got %q", ErrSecretsMode, cfg.SecretsMode)
	}
}

func validateChats(chats []Chat) error {
	seen := make(map[ChatKey]struct{}, len(chats))
	for _, chat := range chats {
		if err := chat.validate(); err != nil {
			return err
		}
		key := chat.Key()
		if _, dup := seen[key]; dup {
			return fmt.Errorf("%w: %s %d", ErrDuplicateChat, chat.Type, chat.ID)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (c Chat) validate() error {
	if c.ID <= 0 {
		return fmt.Errorf("%w: got %d", ErrChatIDInvalid, c.ID)
	}
	switch c.Type {
	case ChatTypeChat:
		return nil
	case ChatTypeUser, ChatTypeChannel:
		if c.AccessHash == 0 {
			return fmt.Errorf("%w: chat %d", ErrAccessHashMissing, c.ID)
		}
		return nil
	default:
		return fmt.Errorf("%w: chat %d has type %q", ErrChatType, c.ID, c.Type)
	}
}

func validateFolders(folders []Folder) error {
	seen := make(map[int]struct{}, len(folders))
	for _, folder := range folders {
		if folder.ID <= 0 {
			return fmt.Errorf("%w: got %d", ErrFolderIDInvalid, folder.ID)
		}
		if _, dup := seen[folder.ID]; dup {
			return fmt.Errorf("%w: %d", ErrDuplicateFolder, folder.ID)
		}
		seen[folder.ID] = struct{}{}
	}
	return nil
}

func (c Config) RequireAllowlist() error {
	if len(c.Chats) == 0 && len(c.Folders) == 0 {
		return ErrEmptyAllowlist
	}
	return nil
}

func MergeChats(explicit, fromFolders []Chat) []Chat {
	merged := make([]Chat, 0, len(explicit)+len(fromFolders))
	seen := make(map[ChatKey]struct{}, len(explicit)+len(fromFolders))
	for _, chat := range slices.Concat(explicit, fromFolders) {
		key := chat.Key()
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, chat)
	}
	return merged
}

func (c Config) SecretStores() (SecretStores, error) {
	if err := validateSecretsMode(c); err != nil {
		return SecretStores{}, err
	}
	switch c.SecretsMode {
	case SecretsModeFile:
		return sharedSecretStores(file.New(c.SecretsDir), filepath.Join(c.SecretsDir, SecretSession)), nil
	case SecretsModeKeychain:
		return sharedSecretStores(keychain.New(c.KeychainPath, keychainService), c.KeychainPath), nil
	default:
		return envSecretStores(c.SessionPath), nil
	}
}

func envSecretStores(sessionPath string) SecretStores {
	return SecretStores{
		KeyReader:       env.New(envPrefix),
		SessionStore:    file.New(filepath.Dir(sessionPath)),
		SessionName:     filepath.Base(sessionPath),
		SessionLocation: sessionPath,
	}
}

func sharedSecretStores(secretStore secret.Store, sessionLocation string) SecretStores {
	return SecretStores{
		KeyReader:       secretStore,
		KeyWriter:       secretStore,
		SessionStore:    secretStore,
		SessionName:     SecretSession,
		SessionLocation: sessionLocation,
	}
}

func ExpandHome(path string) (string, error) {
	isHomeRelative := path == "~" || strings.HasPrefix(path, "~/")
	if !isHomeRelative {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}

func LoadCredentials(keyReader secret.Reader) (Credentials, error) {
	apiIDText, err := readTrimmed(keyReader, SecretAPIID)
	if err != nil {
		return Credentials{}, fmt.Errorf("%w: %w", ErrAPIIDUnavailable, err)
	}
	apiID, err := strconv.Atoi(apiIDText)
	if err != nil {
		return Credentials{}, ErrAPIIDInvalid
	}
	apiHash, err := readTrimmed(keyReader, SecretAPIHash)
	if err != nil {
		return Credentials{}, fmt.Errorf("%w: %w", ErrAPIHashUnavailable, err)
	}
	return Credentials{APIID: apiID, APIHash: apiHash}, nil
}

func readTrimmed(keyReader secret.Reader, name string) (string, error) {
	value, err := keyReader.Get(name)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" {
		return "", fmt.Errorf("%w: %s is empty", secret.ErrNotFound, name)
	}
	return trimmed, nil
}
