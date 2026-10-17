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

	"scoped-mcp/mcp-gitlab/config"
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
		{name: "путь к конфигу по умолчанию раскрывается", args: []string{"serve"}, wantConfigPath: filepath.Join(home, ".config/mcp-gitlab/config.yaml"), wantSubcommand: "serve"},
		{name: "подкоманда token, путь к конфигу по умолчанию", args: []string{"token"}, wantConfigPath: filepath.Join(home, ".config/mcp-gitlab/config.yaml"), wantSubcommand: "token"},
		{name: "путь к конфигу от ~/ раскрывается", args: []string{"--config", "~/mcp/config.yaml", "serve"}, wantConfigPath: filepath.Join(home, "mcp/config.yaml"), wantSubcommand: "serve"},
		{name: "путь к конфигу из флага", args: []string{"--config", "/etc/mcp-gitlab.yaml", "serve"}, wantConfigPath: "/etc/mcp-gitlab.yaml", wantSubcommand: "serve"},
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
	envModeConfigYAML := "base_url: https://gitlab.example.com\ntools: {add_mr_note: true}\n"
	tests := []struct {
		name          string
		subcommand    string
		configContent func(configDir string) string
		wantErr       error
	}{
		{name: "файла конфига нет", subcommand: "serve", wantErr: fs.ErrNotExist},
		{name: "конфиг не разбирается как YAML", subcommand: "serve", configContent: literalConfig("base_url: ["), wantErr: config.ErrMalformed},
		{name: "base_url не https", subcommand: "serve", configContent: literalConfig("base_url: http://gitlab.example.com\ntools: {add_mr_note: true}\n"), wantErr: config.ErrBaseURL},
		{name: "base_url с путём", subcommand: "serve", configContent: literalConfig("base_url: https://gitlab.example.com/gitlab\ntools: {add_mr_note: true}\n"), wantErr: config.ErrBaseURL},
		{name: "нет раздела tools", subcommand: "serve", configContent: literalConfig("base_url: https://gitlab.example.com\n"), wantErr: config.ErrNoTools},
		{name: "неизвестный инструмент в tools", subcommand: "serve", configContent: literalConfig("base_url: https://gitlab.example.com\ntools: {publish_draft: true}\n"), wantErr: config.ErrMalformed},
		{name: "неизвестный режим secrets отклоняется при загрузке конфига", subcommand: "token", configContent: literalConfig("base_url: https://gitlab.example.com\nsecrets: vault\ntools: {add_mr_note: true}\n"), wantErr: config.ErrSecretsMode},
		{name: "serve в режиме env без MCP_GITLAB_TOKEN", subcommand: "serve", configContent: literalConfig(envModeConfigYAML), wantErr: config.ErrTokenUnavailable},
		{name: "serve в режиме file без файла токена", subcommand: "serve", configContent: fileModeConfig, wantErr: config.ErrTokenUnavailable},
		{name: "token в режиме env", subcommand: "token", configContent: literalConfig(envModeConfigYAML), wantErr: errTokenModeEnv},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(config.TokenEnvVar, "")
			configDir := t.TempDir()
			configPath := filepath.Join(configDir, "config.yaml")
			if tt.configContent != nil {
				require.NoError(t, os.WriteFile(configPath, []byte(tt.configContent(configDir)), 0o600))
			}
			stdin := strings.NewReader(unreadInput)

			err := run(t.Context(), []string{"--config", configPath, tt.subcommand}, stdin, &bytes.Buffer{}, &bytes.Buffer{})

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, len(unreadInput), stdin.Len(), "stdin must stay unread")
		})
	}
}

func literalConfig(content string) func(configDir string) string {
	return func(string) string { return content }
}

func fileModeConfig(configDir string) string {
	return "base_url: https://gitlab.example.com\nsecrets: file\nsecrets_dir: " + filepath.Join(configDir, "secrets") + "\ntools: {add_mr_note: true}\n"
}

func TestRunTokenCanceledFails(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(fileModeConfig(configDir)), 0o600))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var stdout bytes.Buffer

	err := run(ctx, []string{"--config", configPath, "token"}, strings.NewReader("pat\n"), &stdout, &bytes.Buffer{})

	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, stdout.String())
	require.NoFileExists(t, filepath.Join(configDir, "secrets", config.TokenSecretName))
}
