package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"golang.org/x/term"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/telegram"
	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/keychain"
)

var (
	errEmptyInput        = errors.New("login: empty input")
	errSignUpUnsupported = errors.New("login: account sign-up is not supported, register the number in a Telegram app first")
)

type credentialPrompter interface {
	apiID() (string, error)
	apiHash() (string, error)
}

type terminalAuthenticator struct {
	in         *bufio.Reader
	out        io.Writer
	readSecret func() ([]byte, error)
}

func (a terminalAuthenticator) Phone(context.Context) (string, error) {
	return a.prompt("Phone (international format): ", a.visibleLine)
}

func (a terminalAuthenticator) Code(context.Context, *tg.AuthSentCode) (string, error) {
	return a.prompt("Code: ", a.visibleLine)
}

func (a terminalAuthenticator) Password(context.Context) (string, error) {
	return a.prompt("2FA password: ", a.secretLine)
}

func (a terminalAuthenticator) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return errSignUpUnsupported
}

func (a terminalAuthenticator) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errSignUpUnsupported
}

func (a terminalAuthenticator) apiID() (string, error) {
	value, err := a.prompt("api_id: ", a.visibleLine)
	if err != nil {
		return "", err
	}
	if _, err = strconv.Atoi(value); err != nil {
		return "", config.ErrAPIIDInvalid
	}
	return value, nil
}

func (a terminalAuthenticator) apiHash() (string, error) {
	return a.prompt("api_hash: ", a.secretLine)
}

func (a terminalAuthenticator) prompt(label string, readLine func() (string, error)) (string, error) {
	if _, err := io.WriteString(a.out, label); err != nil {
		return "", fmt.Errorf("write prompt: %w", err)
	}
	line, err := readLine()
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return "", errEmptyInput
	}
	return value, nil
}

func (a terminalAuthenticator) visibleLine() (string, error) {
	line, err := a.in.ReadString('\n')
	if errors.Is(err, io.EOF) {
		return line, nil
	}
	return line, err
}

func (a terminalAuthenticator) secretLine() (string, error) {
	if a.readSecret == nil {
		return a.visibleLine()
	}
	line, readErr := a.readSecret()
	_, writeErr := io.WriteString(a.out, "\n")
	return string(line), errors.Join(readErr, writeErr)
}

func secretReaderIfTerminal(stdin io.Reader) func() ([]byte, error) {
	stdinFile, ok := stdin.(*os.File)
	if !ok {
		return nil
	}
	fd := int(stdinFile.Fd())
	if !term.IsTerminal(fd) {
		return nil
	}
	return func() ([]byte, error) { return term.ReadPassword(fd) }
}

func ensureKeychain(path string, keyStore secret.Store, createKeychain func(path string) error, stdout io.Writer) error {
	_, probeErr := keyStore.Get(config.SecretAPIID)
	keychainExists := !errors.Is(probeErr, keychain.ErrNoKeychain)
	if keychainExists {
		return nil
	}
	if _, err := fmt.Fprintf(stdout, "Creating keychain %s: choose its password in the macOS dialog\n", path); err != nil {
		return fmt.Errorf("write keychain notice: %w", err)
	}
	return createKeychain(path)
}

func promptMissingKeys(keyStore secret.Store, prompter credentialPrompter) error {
	keys := []struct {
		name string
		ask  func() (string, error)
	}{
		{name: config.SecretAPIID, ask: prompter.apiID},
		{name: config.SecretAPIHash, ask: prompter.apiHash},
	}
	for _, key := range keys {
		_, err := keyStore.Get(key.name)
		if err == nil {
			continue
		}
		if !errors.Is(err, secret.ErrNotFound) {
			return fmt.Errorf("read %s: %w", key.name, err)
		}
		value, err := key.ask()
		if err != nil {
			return fmt.Errorf("ask %s: %w", key.name, err)
		}
		if err = keyStore.Set(key.name, []byte(value)); err != nil {
			return fmt.Errorf("store %s: %w", key.name, err)
		}
	}
	return nil
}

func provisionKeys(cfg config.Config, stores config.SecretStores, prompter credentialPrompter, createKeychain func(path string) error, stdout io.Writer) error {
	switch cfg.SecretsMode {
	case config.SecretsModeFile:
		return promptMissingKeys(stores.KeyWriter, prompter)
	case config.SecretsModeKeychain:
		if err := ensureKeychain(cfg.KeychainPath, stores.KeyWriter, createKeychain, stdout); err != nil {
			return err
		}
		return promptMissingKeys(stores.KeyWriter, prompter)
	default:
		return nil
	}
}

func runLogin(ctx context.Context, cfg config.Config, stores config.SecretStores, stdin io.Reader, stdout io.Writer) error {
	authenticator := terminalAuthenticator{in: bufio.NewReader(stdin), out: stdout, readSecret: secretReaderIfTerminal(stdin)}
	if err := provisionKeys(cfg, stores, authenticator, keychain.Create, stdout); err != nil {
		return err
	}
	creds, err := config.LoadCredentials(stores.KeyReader)
	if err != nil {
		return err
	}
	tgClient := newTelegramClient(creds, newSessionStorage(stores))
	flow := auth.NewFlow(authenticator, auth.SendCodeOptions{})
	return tgClient.Run(ctx, func(ctx context.Context) error {
		if err := tgClient.Auth().IfNecessary(ctx, flow); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
		self, err := tgClient.Self(ctx)
		if err != nil {
			return fmt.Errorf("self: %w", err)
		}
		_, err = fmt.Fprintf(stdout, "Logged in as %s (id %d). Session saved to %s\n", telegram.UserName(self), self.ID, stores.SessionLocation)
		return err
	})
}
