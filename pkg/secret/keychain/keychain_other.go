//go:build !darwin

package keychain

import "scoped-mcp/pkg/secret"

type unsupportedStore struct{}

func New(string, string) secret.Store {
	return unsupportedStore{}
}

func Create(string) error {
	return ErrUnsupported
}

func Supported() bool {
	return false
}

func (unsupportedStore) Get(string) ([]byte, error) {
	return nil, ErrUnsupported
}

func (unsupportedStore) Set(string, []byte) error {
	return ErrUnsupported
}

func (unsupportedStore) Delete(string) error {
	return ErrUnsupported
}
