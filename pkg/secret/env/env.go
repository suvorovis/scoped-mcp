package env

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"scoped-mcp/pkg/secret"
)

var namePattern = regexp.MustCompile(`^[a-z0-9_]+$`)

type envReader struct {
	prefix string
}

func New(prefix string) secret.Reader {
	return envReader{prefix: prefix}
}

func (secretReader envReader) Get(name string) ([]byte, error) {
	if !namePattern.MatchString(name) {
		return nil, fmt.Errorf("%w: %q", secret.ErrInvalidName, name)
	}
	variable := secretReader.prefix + strings.ToUpper(name)
	value := os.Getenv(variable)
	if value == "" {
		return nil, fmt.Errorf("%w: %s is not set", secret.ErrNotFound, variable)
	}
	return []byte(value), nil
}
