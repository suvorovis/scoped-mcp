package secret

//go:generate go tool mockgen -destination=mocks/mock_secret.go -package=mocks scoped-mcp/pkg/secret Reader,Store

import "errors"

type Reader interface {
	Get(name string) ([]byte, error)
}

type Store interface {
	Reader
	Set(name string, value []byte) error
	Delete(name string) error
}

var (
	ErrNotFound    = errors.New("secret: not found")
	ErrInvalidName = errors.New("secret: invalid name")
)
