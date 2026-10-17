package main

import (
	"bytes"
	"context"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"scoped-mcp/mcp-jira/config"
	"scoped-mcp/pkg/secret/file"
)

func TestParseArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tests := []struct {
		name           string
		args           []string
		wantConfigPath string
		wantSubcommand string
		wantErr        error
	}{
		{name: "путь к конфигу по умолчанию раскрывается", args: []string{"serve"}, wantConfigPath: filepath.Join(home, ".config/mcp-jira/config.yaml"), wantSubcommand: "serve"},
		{name: "подкоманда token, путь к конфигу по умолчанию", args: []string{"token"}, wantConfigPath: filepath.Join(home, ".config/mcp-jira/config.yaml"), wantSubcommand: "token"},
		{name: "путь к конфигу от ~/ раскрывается", args: []string{"--config", "~/mcp/config.yaml", "serve"}, wantConfigPath: filepath.Join(home, "mcp/config.yaml"), wantSubcommand: "serve"},
		{name: "путь к конфигу из флага", args: []string{"--config", "/etc/mcp-jira.yaml", "serve"}, wantConfigPath: "/etc/mcp-jira.yaml", wantSubcommand: "serve"},
		{name: "без подкоманды", args: nil, wantErr: errUsage},
		{name: "две подкоманды", args: []string{"serve", "token"}, wantErr: errUsage},
		{name: "неизвестная подкоманда", args: []string{"login"}, wantErr: errUsage},
		{name: "--help", args: []string{"--help"}, wantErr: flag.ErrHelp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, subcommand, err := parseArgs(tt.args, &bytes.Buffer{})

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantConfigPath, configPath)
			require.Equal(t, tt.wantSubcommand, subcommand)
		})
	}
}

func TestParseArgsHomeUnknown(t *testing.T) {
	t.Setenv("HOME", "")

	_, _, err := parseArgs([]string{"--config", "~/mcp/config.yaml", "serve"}, &bytes.Buffer{})

	require.ErrorIs(t, err, config.ErrHomeUnknown)
}

func TestParseArgsHelpPrintsUsageAndFlags(t *testing.T) {
	var stderr bytes.Buffer

	_, _, err := parseArgs([]string{"--help"}, &stderr)

	require.ErrorIs(t, err, flag.ErrHelp)
	require.Contains(t, stderr.String(), "serve|token")
	require.Contains(t, stderr.String(), "-config")
	require.Contains(t, stderr.String(), "path to config.yaml")
}

func TestRunStopsBeforeLoadingConfig(t *testing.T) {
	nonexistentConfigPath := filepath.Join(t.TempDir(), "missing.yaml")
	tests := []struct {
		name    string
		args    []string
		wantErr error
	}{
		{name: "неизвестная подкоманда отклоняется до чтения конфига", args: []string{"--config", nonexistentConfigPath, "nope"}, wantErr: errUsage},
		{name: "--help не читает конфиг", args: []string{"--config", nonexistentConfigPath, "--help"}},
		{name: "-h не читает конфиг", args: []string{"--config", nonexistentConfigPath, "-h"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(t.Context(), tt.args, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

const unreadInput = "must-not-be-read\n"

func TestRunRejectsUnusableConfigOrToken(t *testing.T) {
	envModeConfigYAML := "base_url: https://jira.example.com\ntools: {add_comment: true}\nprojects: [{key: PROJ}]\n"
	tests := []struct {
		name          string
		subcommand    string
		prepareConfig func(t *testing.T, configDir string) (configYAML string)
		wantErr       error
	}{
		{name: "файла конфига нет", subcommand: "serve", wantErr: fs.ErrNotExist},
		{name: "конфиг не разбирается как YAML", subcommand: "serve", prepareConfig: literalConfig("base_url: ["), wantErr: config.ErrMalformed},
		{name: "base_url не https", subcommand: "serve", prepareConfig: literalConfig("base_url: http://jira.example.com\ntools: {create_issue: true}\nprojects: [{key: PROJ, issue_types: [{name: Task, id: \"3\"}]}]\n"), wantErr: config.ErrBaseURL},
		{name: "нет раздела tools", subcommand: "serve", prepareConfig: literalConfig("base_url: https://jira.example.com\nprojects: [{key: PROJ, issue_types: [{name: Task, id: \"3\"}]}]\n"), wantErr: config.ErrNoTools},
		{name: "удалённый ключ token_path", subcommand: "serve", prepareConfig: literalConfig("base_url: https://jira.example.com\ntoken_path: /t\ntools: {add_comment: true}\nprojects: [{key: PROJ}]\n"), wantErr: config.ErrMalformed},
		{name: "неизвестный режим secrets отклоняется при загрузке конфига", subcommand: "token", prepareConfig: literalConfig("base_url: https://jira.example.com\nsecrets: vault\ntools: {add_comment: true}\nprojects: [{key: PROJ}]\n"), wantErr: config.ErrSecretsMode},
		{name: "serve в режиме env без MCP_JIRA_TOKEN", subcommand: "serve", prepareConfig: literalConfig(envModeConfigYAML), wantErr: config.ErrTokenUnavailable},
		{name: "serve в режиме file без файла токена", subcommand: "serve", prepareConfig: fileModeConfig, wantErr: config.ErrTokenUnavailable},
		{name: "serve в режиме file, файл токена 0644 доступен остальным", subcommand: "serve", prepareConfig: fileModeConfigWithToken(0o644), wantErr: file.ErrPermissions},
		{name: "token в режиме env", subcommand: "token", prepareConfig: literalConfig(envModeConfigYAML), wantErr: errTokenModeEnv},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(config.TokenEnvVar, "")
			configDir := t.TempDir()
			configPath := filepath.Join(configDir, "config.yaml")
			if tt.prepareConfig != nil {
				require.NoError(t, os.WriteFile(configPath, []byte(tt.prepareConfig(t, configDir)), 0o600))
			}
			stdin := strings.NewReader(unreadInput)

			err := run(t.Context(), []string{"--config", configPath, tt.subcommand}, stdin, &bytes.Buffer{}, &bytes.Buffer{})

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, len(unreadInput), stdin.Len(), "stdin must stay unread")
		})
	}
}

func literalConfig(configYAML string) func(*testing.T, string) string {
	return func(*testing.T, string) string { return configYAML }
}

func fileModeConfig(_ *testing.T, configDir string) string {
	return "base_url: https://jira.example.com\nsecrets: file\nsecrets_dir: " + filepath.Join(configDir, "secrets") + "\ntools: {add_comment: true}\nprojects: [{key: PROJ}]\n"
}

func fileModeConfigWithToken(tokenMode os.FileMode) func(*testing.T, string) string {
	return func(t *testing.T, configDir string) string {
		secretsDir := filepath.Join(configDir, "secrets")
		tokenPath := filepath.Join(secretsDir, config.TokenSecretName)
		require.NoError(t, os.Mkdir(secretsDir, 0o700))
		require.NoError(t, os.WriteFile(tokenPath, []byte("jira-pat"), tokenMode))
		require.NoError(t, os.Chmod(tokenPath, tokenMode))
		return fileModeConfig(t, configDir)
	}
}

func TestRunTokenCanceledFails(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(fileModeConfig(t, configDir)), 0o600))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var stdout bytes.Buffer

	err := run(ctx, []string{"--config", configPath, "token"}, strings.NewReader("pat\n"), &stdout, &bytes.Buffer{})

	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, stdout.String())
	require.NoFileExists(t, filepath.Join(configDir, "secrets", config.TokenSecretName))
}
