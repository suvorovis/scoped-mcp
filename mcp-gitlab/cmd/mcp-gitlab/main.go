package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"scoped-mcp/mcp-gitlab/config"
)

const usage = "usage: mcp-gitlab [--config path] serve|token"

var (
	errUsage        = errors.New(usage)
	shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}
)

type stdio struct {
	in  io.Reader
	out io.Writer
}

type subcommandFunc func(ctx context.Context, cfg config.Config, tokenStore config.TokenStore, stdio stdio) error

var subcommands = map[string]subcommandFunc{
	"serve": runServe,
	"token": runToken,
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "mcp-gitlab:", err)
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

	ctx, stop := signal.NotifyContext(ctx, shutdownSignals...)
	defer stop()

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	runSubcommand := subcommands[subcommand]
	return runSubcommand(ctx, cfg, cfg.TokenStore(), stdio{in: stdin, out: stdout})
}

func parseArgs(args []string, stderr io.Writer) (string, string, error) {
	flags := flag.NewFlagSet("mcp-gitlab", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configFlag := flags.String("config", config.DefaultPath, "path to config.yaml")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, usage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return "", "", err
	}
	if flags.NArg() != 1 {
		return "", "", errUsage
	}
	subcommand := flags.Arg(0)
	if _, known := subcommands[subcommand]; !known {
		return "", "", fmt.Errorf("unknown subcommand %q: %w", subcommand, errUsage)
	}
	configPath, err := config.ExpandHome(*configFlag)
	if err != nil {
		return "", "", err
	}
	return configPath, subcommand, nil
}
