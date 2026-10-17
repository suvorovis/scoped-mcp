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

	"github.com/gotd/td/session"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/telegram"
)

var errUsage = errors.New("usage: mcp-tg [--config path] login|dialogs|folders|serve")

type subcommandFunc func(ctx context.Context, cfg config.Config, stores config.SecretStores, stdin io.Reader, stdout io.Writer) error

type credentialedSubcommandFunc func(ctx context.Context, creds config.Credentials, clientSession session.Storage, stdout io.Writer) error

var subcommands = map[string]subcommandFunc{
	"login":   runLogin,
	"dialogs": withCredentials(runDialogs),
	"folders": withCredentials(runFolders),
	"serve":   serveSubcommand,
}

func withCredentials(runWithCredentials credentialedSubcommandFunc) subcommandFunc {
	return func(ctx context.Context, _ config.Config, stores config.SecretStores, _ io.Reader, stdout io.Writer) error {
		creds, clientSession, err := loadCredentialsAndSession(stores)
		if err != nil {
			return err
		}
		return runWithCredentials(ctx, creds, clientSession, stdout)
	}
}

func serveSubcommand(ctx context.Context, cfg config.Config, stores config.SecretStores, _ io.Reader, _ io.Writer) error {
	creds, clientSession, err := loadCredentialsAndSession(stores)
	if err != nil {
		return err
	}
	return ignoreCancellation(runServe(ctx, cfg, creds, clientSession))
}

func loadCredentialsAndSession(stores config.SecretStores) (config.Credentials, session.Storage, error) {
	creds, err := config.LoadCredentials(stores.KeyReader)
	if err != nil {
		return config.Credentials{}, nil, err
	}
	return creds, newSessionStorage(stores), nil
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-tg:", err)
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
	stores, err := cfg.SecretStores()
	if err != nil {
		return err
	}
	return subcommands[subcommand](ctx, cfg, stores, stdin, stdout)
}

func parseArgs(args []string, stderr io.Writer) (configPath, subcommand string, err error) {
	flags := flag.NewFlagSet("mcp-tg", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configFlag := flags.String("config", config.DefaultPath, "path to config.yaml")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, errUsage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return "", "", err
	}
	if flags.NArg() != 1 {
		return "", "", errUsage
	}
	subcommand = flags.Arg(0)
	if _, known := subcommands[subcommand]; !known {
		return "", "", fmt.Errorf("unknown subcommand %q: %w", subcommand, errUsage)
	}
	return *configFlag, subcommand, nil
}

func ignoreCancellation(err error) error {
	if errors.Is(err, telegram.ErrSessionRevoked) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
