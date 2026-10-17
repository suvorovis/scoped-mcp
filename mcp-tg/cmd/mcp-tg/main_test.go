package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/telegram"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantConfigPath string
		wantSubcommand string
		wantErr        error
	}{
		{name: "путь к конфигу по умолчанию", args: []string{"serve"}, wantConfigPath: config.DefaultPath, wantSubcommand: "serve"},
		{name: "путь к конфигу из флага", args: []string{"--config", "/etc/mcp-tg.yaml", "dialogs"}, wantConfigPath: "/etc/mcp-tg.yaml", wantSubcommand: "dialogs"},
		{name: "login", args: []string{"login"}, wantConfigPath: config.DefaultPath, wantSubcommand: "login"},
		{name: "folders", args: []string{"folders"}, wantConfigPath: config.DefaultPath, wantSubcommand: "folders"},
		{name: "без подкоманды", args: nil, wantErr: errUsage},
		{name: "две подкоманды", args: []string{"login", "serve"}, wantErr: errUsage},
		{name: "неизвестная подкоманда", args: []string{"nope"}, wantErr: errUsage},
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

func TestParseArgsHelpPrintsSubcommandsAndFlags(t *testing.T) {
	var stderr bytes.Buffer

	_, _, err := parseArgs([]string{"--help"}, &stderr)

	require.ErrorIs(t, err, flag.ErrHelp)
	require.Contains(t, stderr.String(), "login|dialogs|folders|serve")
	require.Contains(t, stderr.String(), "-config")
	require.Contains(t, stderr.String(), "path to config.yaml")
}

func TestRunStopsBeforeLoadingConfig(t *testing.T) {
	t.Setenv("MCP_TG_API_ID", "")
	t.Setenv("MCP_TG_API_HASH", "")
	missingConfig := t.TempDir() + "/missing.yaml"
	tests := []struct {
		name    string
		args    []string
		wantErr error
	}{
		{name: "неизвестная подкоманда", args: []string{"--config", missingConfig, "nope"}, wantErr: errUsage},
		{name: "--help завершается без ошибки", args: []string{"--config", missingConfig, "--help"}},
		{name: "-h завершается без ошибки", args: []string{"--config", missingConfig, "-h"}},
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

func TestIgnoreCancellation(t *testing.T) {
	errOther := errors.New("other")
	tests := []struct {
		name    string
		err     error
		wantErr error
	}{
		{name: "без ошибки", err: nil, wantErr: nil},
		{name: "остановка сигналом", err: context.Canceled, wantErr: nil},
		{name: "обёрнутая остановка сигналом", err: fmt.Errorf("callback: %w", context.Canceled), wantErr: nil},
		{name: "отозванная сессия", err: telegram.ErrSessionRevoked, wantErr: telegram.ErrSessionRevoked},
		{name: "отозванная сессия вместе с отменой", err: errors.Join(telegram.ErrSessionRevoked, context.Canceled), wantErr: telegram.ErrSessionRevoked},
		{name: "обёрнутая отозванная сессия", err: fmt.Errorf("callback: %w", telegram.ErrSessionRevoked), wantErr: telegram.ErrSessionRevoked},
		{name: "прочая ошибка", err: errOther, wantErr: errOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ignoreCancellation(tt.err)

			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
