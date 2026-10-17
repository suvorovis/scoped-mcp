package file

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scoped-mcp/pkg/secret"
)

const (
	dirMode           os.FileMode = 0o700
	groupOrOthersBits os.FileMode = 0o077
)

var ErrPermissions = errors.New("file: secret must not be accessible by group or others")

type directoryStore struct {
	dir string
}

func New(dir string) secret.Store {
	return directoryStore{dir: dir}
}

func (secretStore directoryStore) Get(name string) ([]byte, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	if err := requireExistingPrivate(secretStore.dir); err != nil {
		return nil, err
	}
	path := secretStore.secretPath(name)
	if err := requireExistingPrivate(path); err != nil {
		return nil, err
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read secret: %w", err)
	}
	return value, nil
}

func (secretStore directoryStore) Set(name string, value []byte) error {
	if err := validateName(name); err != nil {
		return err
	}
	if err := os.MkdirAll(secretStore.dir, dirMode); err != nil {
		return fmt.Errorf("create secrets dir: %w", err)
	}
	if err := requireExistingPrivate(secretStore.dir); err != nil {
		return err
	}
	return writeAtomically(secretStore.secretPath(name), value)
}

func (secretStore directoryStore) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	path := secretStore.secretPath(name)
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete secret: %w", mapNotExist(path, err))
	}
	return nil
}

func (secretStore directoryStore) secretPath(name string) string {
	return filepath.Join(secretStore.dir, name)
}

func writeAtomically(targetPath string, value []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(targetPath), "."+filepath.Base(targetPath)+".*")
	if err != nil {
		return fmt.Errorf("create temp secret: %w", err)
	}
	tempPath := temp.Name()
	_, writeErr := temp.Write(value)
	if writeErr == nil {
		writeErr = temp.Sync()
	}
	if err := errors.Join(writeErr, temp.Close()); err != nil {
		return errors.Join(fmt.Errorf("write secret: %w", err), os.Remove(tempPath))
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		return errors.Join(fmt.Errorf("rename secret: %w", err), os.Remove(tempPath))
	}
	return nil
}

func validateName(name string) error {
	if !isPlainFileName(name) {
		return fmt.Errorf("%w: %q", secret.ErrInvalidName, name)
	}
	return nil
}

func isPlainFileName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		name == filepath.Base(name) && !strings.ContainsRune(name, 0)
}

func requireExistingPrivate(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat secret path: %w", mapNotExist(path, err))
	}
	if perm := info.Mode().Perm(); perm&groupOrOthersBits != 0 {
		return fmt.Errorf("%w: %s has mode %04o", ErrPermissions, path, perm)
	}
	return nil
}

func mapNotExist(path string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", secret.ErrNotFound, path)
	}
	return err
}
