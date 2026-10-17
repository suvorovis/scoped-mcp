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

	"scoped-mcp/mcp-gitlab/config"
	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/keychain"
)

const tokenPromptLabel = "GitLab personal access token: "

var (
	errEmptyInput   = errors.New("token: empty input")
	errTokenModeEnv = errors.New("token: secrets mode is env, set " + config.TokenEnvVar + " in the environment instead")
)

type tokenPrompter struct {
	in               *bufio.Reader
	out              io.Writer
	enterHiddenInput func() (restoreInput func() error, err error)
}

type lineRead struct {
	line string
	err  error
}

func (p tokenPrompter) askToken(ctx context.Context) (string, error) {
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
	token := strings.TrimSpace(line)
	if token == "" {
		return "", errEmptyInput
	}
	return token, nil
}

func (p tokenPrompter) inTerminal() bool {
	return p.enterHiddenInput != nil
}

func (p tokenPrompter) inputLine(ctx context.Context) (string, error) {
	if p.inTerminal() {
		return p.hiddenLine(ctx)
	}
	return p.visibleLine(ctx)
}

func (p tokenPrompter) visibleLine(ctx context.Context) (string, error) {
	line, err := p.lineUnlessCanceled(ctx)
	inputInterrupted := errors.Is(err, context.Canceled)
	if inputInterrupted {
		return "", errors.Join(err, p.endPromptLine())
	}
	return line, err
}

func (p tokenPrompter) hiddenLine(ctx context.Context) (line string, err error) {
	restoreInput, err := p.enterHiddenInput()
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, restoreInput()) }()
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
		line, err := p.readLineAllowingEOF()
		lineReads <- lineRead{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case read := <-lineReads:
		return read.line, read.err
	}
}

func (p tokenPrompter) readLineAllowingEOF() (string, error) {
	line, err := p.in.ReadString('\n')
	if errors.Is(err, io.EOF) {
		return line, nil
	}
	return line, err
}

func terminalHiddenInput(stdin io.Reader) func() (restoreInput func() error, err error) {
	stdinFile, ok := stdin.(*os.File)
	if !ok {
		return nil
	}
	fd := int(stdinFile.Fd())
	if !term.IsTerminal(fd) {
		return nil
	}
	return func() (func() error, error) { return enterHiddenLineInput(fd) }
}

func createKeychainIfMissing(keychainPath string, keychainReader secret.Reader, createKeychain func(path string) error, out io.Writer) error {
	_, probeErr := keychainReader.Get(config.TokenSecretName)
	keychainMissing := errors.Is(probeErr, keychain.ErrNoKeychain)
	if !keychainMissing {
		return nil
	}
	if _, err := fmt.Fprintf(out, "Creating keychain %s: choose its password in the macOS dialog\n", keychainPath); err != nil {
		return fmt.Errorf("write keychain notice: %w", err)
	}
	return createKeychain(keychainPath)
}

func storeToken(ctx context.Context, tokenStore config.TokenStore, prompter tokenPrompter, createKeychain func(path string) error) error {
	if !tokenStore.Writable() {
		return errTokenModeEnv
	}
	if tokenStore.UsesKeychain() {
		if err := createKeychainIfMissing(tokenStore.Location, tokenStore.TokenReader, createKeychain, prompter.out); err != nil {
			return err
		}
	}
	token, err := prompter.askToken(ctx)
	if err != nil {
		return err
	}
	if err := tokenStore.TokenWriter.Set(config.TokenSecretName, []byte(token)); err != nil {
		return fmt.Errorf("store %s: %w", config.TokenSecretName, err)
	}
	_, err = fmt.Fprintf(prompter.out, "Token saved to %s\n", tokenStore.Location)
	return err
}

func runToken(ctx context.Context, _ config.Config, tokenStore config.TokenStore, stdio stdio) error {
	prompter := tokenPrompter{in: bufio.NewReader(stdio.in), out: stdio.out, enterHiddenInput: terminalHiddenInput(stdio.in)}
	return storeToken(ctx, tokenStore, prompter, keychain.Create)
}
