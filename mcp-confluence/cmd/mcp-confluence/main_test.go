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

	"scoped-mcp/mcp-confluence/config"
)

func TestParseArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	workDir, err := os.Getwd()
	require.NoError(t, err)
	tests := []struct {
		name           string
		args           []string
		wantConfigPath string
		wantSubcommand string
		wantErr        error
	}{
		{name: "путь к конфигу по умолчанию раскрывается", args: []string{"serve"}, wantConfigPath: filepath.Join(home, ".config/mcp-confluence/config.yaml"), wantSubcommand: "serve"},
		{name: "подкоманда token, путь к конфигу по умолчанию", args: []string{"token"}, wantConfigPath: filepath.Join(home, ".config/mcp-confluence/config.yaml"), wantSubcommand: "token"},
		{name: "путь к конфигу от ~/ раскрывается", args: []string{"--config", "~/mcp/config.yaml", "serve"}, wantConfigPath: filepath.Join(home, "mcp/config.yaml"), wantSubcommand: "serve"},
		{name: "путь к конфигу из флага", args: []string{"--config", "/etc/mcp-confluence.yaml", "serve"}, wantConfigPath: "/etc/mcp-confluence.yaml", wantSubcommand: "serve"},
		{name: "относительный путь к конфигу становится абсолютным", args: []string{"--config", "mcp/../config.yaml", "serve"}, wantConfigPath: filepath.Join(workDir, "config.yaml"), wantSubcommand: "serve"},
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

func TestRunRejectsUnusableConfigOrToken(t *testing.T) {
	validYAML := "base_url: https://confluence.example.com\nread_roots: [1]\ntools: {get_page: true}\n"
	tests := []struct {
		name          string
		subcommand    string
		configContent string
		wantErr       error
	}{
		{name: "файла конфига нет", subcommand: "serve", wantErr: fs.ErrNotExist},
		{name: "конфиг не разбирается как YAML", subcommand: "serve", configContent: "base_url: [", wantErr: config.ErrDecode},
		{name: "base_url не https", subcommand: "serve", configContent: "base_url: http://confluence.example.com\nread_roots: [1]\ntools: {get_page: true}\n", wantErr: config.ErrBaseURL},
		{name: "нет раздела tools", subcommand: "serve", configContent: "base_url: https://confluence.example.com\nread_roots: [1]\n", wantErr: config.ErrNoTools},
		{name: "serve в режиме env без MCP_CONFLUENCE_TOKEN", subcommand: "serve", configContent: validYAML, wantErr: config.ErrTokenUnavailable},
		{name: "token в режиме env", subcommand: "token", configContent: validYAML, wantErr: errTokenModeEnv},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(config.TokenEnvVar, "")
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			if tt.configContent != "" {
				require.NoError(t, os.WriteFile(configPath, []byte(tt.configContent), 0o600))
			}

			stdin := strings.NewReader("must-not-be-read\n")

			err := run(t.Context(), []string{"--config", configPath, tt.subcommand}, stdin, &bytes.Buffer{}, &bytes.Buffer{})

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, stdin.Size(), int64(stdin.Len()), "stdin must stay unread")
		})
	}
}

func TestRunTokenCanceledFails(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.yaml")
	configContent := "base_url: https://confluence.example.com\nsecrets: file\nsecrets_dir: " + filepath.Join(configDir, "secrets") + "\nread_roots: [1]\ntools: {get_page: true}\n"
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var stdout bytes.Buffer

	err := run(ctx, []string{"--config", configPath, "token"}, strings.NewReader("pat\n"), &stdout, &bytes.Buffer{})

	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, stdout.String())
	require.NoFileExists(t, filepath.Join(configDir, "secrets", config.TokenSecretName))
}
