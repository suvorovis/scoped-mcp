package keychain

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"scoped-mcp/pkg/secret"
)

const (
	errSecSuccess               int32 = 0
	errSecUserCanceled          int32 = -128
	errSecAuthFailed            int32 = -25293
	errSecNoSuchKeychain        int32 = -25294
	errSecDuplicateItem         int32 = -25299
	errSecItemNotFound          int32 = -25300
	errSecInteractionNotAllowed int32 = -25308
)

var (
	ErrUnsupported           = errors.New("keychain: not supported on this platform")
	ErrNoKeychain            = errors.New("keychain: no such keychain")
	ErrAccessDenied          = errors.New("keychain: access denied")
	ErrInteractionNotAllowed = errors.New("keychain: user interaction is not allowed (no GUI session or keychain is locked)")
)

type StatusError struct {
	Code    int32
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("keychain: OSStatus %d (%s)", e.Code, e.Message)
}

func errorFromStatus(status int32, describe func(status int32) string) error {
	switch status {
	case errSecSuccess:
		return nil
	case errSecItemNotFound:
		return secret.ErrNotFound
	case errSecNoSuchKeychain:
		return ErrNoKeychain
	case errSecUserCanceled, errSecAuthFailed:
		return ErrAccessDenied
	case errSecInteractionNotAllowed:
		return ErrInteractionNotAllowed
	default:
		return &StatusError{Code: status, Message: describe(status)}
	}
}

func validateName(name string) error {
	if name == "" || strings.ContainsRune(name, 0) || !utf8.ValidString(name) {
		return fmt.Errorf("%w: %q", secret.ErrInvalidName, name)
	}
	return nil
}
