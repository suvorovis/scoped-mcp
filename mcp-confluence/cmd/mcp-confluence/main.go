package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"scoped-mcp/mcp-confluence/config"
)

const usage = "usage: mcp-confluence [--config path] serve|token"

var errUsage = errors.New(usage)

type stdio struct {
	in     io.Reader
	out    io.Writer
	errOut io.Writer
}

type subcommandFunc func(ctx context.Context, cfg config.Config, configPath string, tokenStore config.TokenStore, streams stdio) error

var subcommands = map[string]subcommandFunc{
	"serve": runServe,
	"token": runToken,
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "mcp-confluence:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	configPath, subcommand, err := parseArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	tokenStore, err := cfg.TokenStore()
	if err != nil {
		return err
	}
	runSubcommand := subcommands[subcommand]
	return runSubcommand(ctx, cfg, configPath, tokenStore, stdio{in: stdin, out: stdout, errOut: stderr})
}

func parseArgs(args []string, stderr io.Writer) (configPath, subcommand string, err error) {
	flags := flag.NewFlagSet("mcp-confluence", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configFlag := flags.String("config", config.DefaultPath, "path to config.yaml")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, usage)
		flags.PrintDefaults()
	}
	if err = flags.Parse(args); err != nil {
		return "", "", err
	}
	if flags.NArg() != 1 {
		return "", "", errUsage
	}
	subcommand = flags.Arg(0)
	if _, known := subcommands[subcommand]; !known {
		return "", "", fmt.Errorf("unknown subcommand %q: %w", subcommand, errUsage)
	}
	configPath, err = absoluteConfigPath(*configFlag)
	if err != nil {
		return "", "", err
	}
	return configPath, subcommand, nil
}

func absoluteConfigPath(configFlag string) (string, error) {
	expanded, err := config.ExpandHome(configFlag)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	return absolute, nil
}
