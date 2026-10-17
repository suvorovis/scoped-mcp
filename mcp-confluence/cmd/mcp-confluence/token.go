package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"scoped-mcp/mcp-confluence/config"
	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/keychain"
)

const tokenPromptLabel = "Confluence personal access token: "

var (
	errEmptyInput   = errors.New("token: empty input")
	errTokenModeEnv = errors.New("token: secrets mode is env, set " + config.TokenEnvVar + " in the environment instead")
)

type tokenPrompter struct {
	in          *bufio.Reader
	out         io.Writer
	disableEcho func() (restoreEcho func() error, err error)
}

type lineRead struct {
	line string
	err  error
}

func (p tokenPrompter) token(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	if _, err := io.WriteString(p.out, tokenPromptLabel); err != nil {
		return "", fmt.Errorf("write prompt: %w", err)
	}
	line, err := p.inputLine(ctx)
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return "", errEmptyInput
	}
	return value, nil
}

func (p tokenPrompter) inputLine(ctx context.Context) (string, error) {
	if p.disableEcho == nil {
		return p.visibleLine(ctx)
	}
	return p.hiddenLine(ctx)
}

func (p tokenPrompter) visibleLine(ctx context.Context) (string, error) {
	line, err := p.lineUnlessCanceled(ctx)
	if ctx.Err() != nil {
		return "", errors.Join(ctx.Err(), p.endPromptLine())
	}
	return line, err
}

func (p tokenPrompter) hiddenLine(ctx context.Context) (line string, err error) {
	restoreEcho, err := p.disableEcho()
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, restoreEcho()) }()
	line, err = p.lineUnlessCanceled(ctx)
	return line, errors.Join(err, p.endPromptLine())
}

func (p tokenPrompter) endPromptLine() error {
	_, err := io.WriteString(p.out, "\n")
	return err
}

func (p tokenPrompter) lineUnlessCanceled(ctx context.Context) (string, error) {
	lineReads := make(chan lineRead, 1)
	go func() {
		line, err := p.stdinLine()
		lineReads <- lineRead{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case read := <-lineReads:
		return read.line, read.err
	}
}

func (p tokenPrompter) stdinLine() (string, error) {
	line, err := p.in.ReadString('\n')
	if errors.Is(err, io.EOF) {
		return line, nil
	}
	return line, err
}

func disableEchoIfTerminal(stdin io.Reader) func() (restoreEcho func() error, err error) {
	stdinFile, ok := stdin.(*os.File)
	if !ok {
		return nil
	}
	fd := int(stdinFile.Fd())
	if !term.IsTerminal(fd) {
		return nil
	}
	return func() (func() error, error) { return disableTerminalEcho(fd) }
}

func ensureKeychain(path string, writableStore secret.Store, createKeychain func(path string) error, stdout io.Writer) error {
	_, probeErr := writableStore.Get(config.TokenSecretName)
	keychainExists := !errors.Is(probeErr, keychain.ErrNoKeychain)
	if keychainExists {
		return nil
	}
	if _, err := fmt.Fprintf(stdout, "Creating keychain %s: choose its password in the macOS dialog\n", path); err != nil {
		return fmt.Errorf("write keychain notice: %w", err)
	}
	return createKeychain(path)
}

func storeToken(ctx context.Context, cfg config.Config, tokenStore config.TokenStore, prompter tokenPrompter, createKeychain func(path string) error, stdout io.Writer) error {
	if cfg.SecretsMode == config.SecretsModeEnv {
		return errTokenModeEnv
	}
	if cfg.SecretsMode == config.SecretsModeKeychain {
		if err := ensureKeychain(cfg.KeychainPath, tokenStore.WritableStore, createKeychain, stdout); err != nil {
			return err
		}
	}
	value, err := prompter.token(ctx)
	if err != nil {
		return err
	}
	if err := tokenStore.WritableStore.Set(config.TokenSecretName, []byte(value)); err != nil {
		return fmt.Errorf("store %s: %w", config.TokenSecretName, err)
	}
	_, err = fmt.Fprintf(stdout, "Token saved to %s\n", tokenStore.Location)
	return err
}

func runToken(ctx context.Context, cfg config.Config, _ string, tokenStore config.TokenStore, streams stdio) error {
	prompter := tokenPrompter{in: bufio.NewReader(streams.in), out: streams.out, disableEcho: disableEchoIfTerminal(streams.in)}
	return storeToken(ctx, cfg, tokenStore, prompter, keychain.Create, streams.out)
}
