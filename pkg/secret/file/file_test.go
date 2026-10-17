package file_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"scoped-mcp/pkg/secret"
	"scoped-mcp/pkg/secret/file"
)

const (
	secretName    = "session"
	preparedValue = "v0"
)

func writeSecretFile(t *testing.T, dir string, fileMode os.FileMode) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, secretName), []byte(preparedValue), 0o600))
	require.NoError(t, os.Chmod(filepath.Join(dir, secretName), fileMode))
}

func TestStore(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, dir string)
		run     func(secretStore secret.Store) ([]byte, error)
		want    []byte
		wantErr error
	}{
		{
			name: "запись и чтение",
			run: func(secretStore secret.Store) ([]byte, error) {
				if err := secretStore.Set(secretName, []byte("v1")); err != nil {
					return nil, err
				}
				return secretStore.Get(secretName)
			},
			want: []byte("v1"),
		},
		{
			name: "перезапись",
			run: func(secretStore secret.Store) ([]byte, error) {
				if err := secretStore.Set(secretName, []byte("v1")); err != nil {
					return nil, err
				}
				if err := secretStore.Set(secretName, []byte("v2")); err != nil {
					return nil, err
				}
				return secretStore.Get(secretName)
			},
			want: []byte("v2"),
		},
		{
			name: "чтение после удаления — не найден",
			run: func(secretStore secret.Store) ([]byte, error) {
				if err := secretStore.Set(secretName, []byte("v1")); err != nil {
					return nil, err
				}
				if err := secretStore.Delete(secretName); err != nil {
					return nil, err
				}
				return secretStore.Get(secretName)
			},
			wantErr: secret.ErrNotFound,
		},
		{
			name: "удаление отсутствующего",
			run: func(secretStore secret.Store) ([]byte, error) {
				return nil, secretStore.Delete(secretName)
			},
			wantErr: secret.ErrNotFound,
		},
		{
			name:    "чтение отсутствующего файла — не найден",
			run:     func(secretStore secret.Store) ([]byte, error) { return secretStore.Get(secretName) },
			wantErr: secret.ErrNotFound,
		},
		{
			name:    "чтение без каталога — не найден",
			prepare: func(t *testing.T, dir string) { require.NoError(t, os.Remove(dir)) },
			run:     func(secretStore secret.Store) ([]byte, error) { return secretStore.Get(secretName) },
			wantErr: secret.ErrNotFound,
		},
		{
			name:    "файл 0644 отклоняется",
			prepare: func(t *testing.T, dir string) { writeSecretFile(t, dir, 0o644) },
			run:     func(secretStore secret.Store) ([]byte, error) { return secretStore.Get(secretName) },
			wantErr: file.ErrPermissions,
		},
		{
			name:    "файл 0660 отклоняется",
			prepare: func(t *testing.T, dir string) { writeSecretFile(t, dir, 0o660) },
			run:     func(secretStore secret.Store) ([]byte, error) { return secretStore.Get(secretName) },
			wantErr: file.ErrPermissions,
		},
		{
			name:    "файл 0400 читается",
			prepare: func(t *testing.T, dir string) { writeSecretFile(t, dir, 0o400) },
			run:     func(secretStore secret.Store) ([]byte, error) { return secretStore.Get(secretName) },
			want:    []byte(preparedValue),
		},
		{
			name: "чтение из каталога 0755 отклоняется",
			prepare: func(t *testing.T, dir string) {
				writeSecretFile(t, dir, 0o600)
				require.NoError(t, os.Chmod(dir, 0o755))
			},
			run:     func(secretStore secret.Store) ([]byte, error) { return secretStore.Get(secretName) },
			wantErr: file.ErrPermissions,
		},
		{
			name:    "запись в каталог 0755 отклоняется",
			prepare: func(t *testing.T, dir string) { require.NoError(t, os.Chmod(dir, 0o755)) },
			run: func(secretStore secret.Store) ([]byte, error) {
				return nil, secretStore.Set(secretName, []byte("v1"))
			},
			wantErr: file.ErrPermissions,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "secrets")
			require.NoError(t, os.Mkdir(dir, 0o700))
			if tt.prepare != nil {
				tt.prepare(t, dir)
			}

			got, err := tt.run(file.New(dir))

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestStoreRejectsInvalidNames(t *testing.T) {
	tests := []struct {
		name        string
		invalidName string
	}{
		{name: "родительский каталог", invalidName: "../x"},
		{name: "вложенный путь", invalidName: "a/b"},
		{name: "абсолютный путь", invalidName: "/etc/passwd"},
		{name: "точка", invalidName: "."},
		{name: "две точки", invalidName: ".."},
		{name: "пустое", invalidName: ""},
		{name: "нулевой байт", invalidName: "a\x00b"},
		{name: "слеш в конце", invalidName: "session/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretStore := file.New(t.TempDir())

			_, err := secretStore.Get(tt.invalidName)
			require.ErrorIs(t, err, secret.ErrInvalidName)
			require.ErrorIs(t, secretStore.Set(tt.invalidName, []byte("v")), secret.ErrInvalidName)
			require.ErrorIs(t, secretStore.Delete(tt.invalidName), secret.ErrInvalidName)
		})
	}
}

func TestSetCreatesOnlyPrivateDirAndFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing", "secrets")

	require.NoError(t, file.New(dir).Set(secretName, []byte("v1")))

	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
	fileInfo, err := os.Stat(filepath.Join(dir, secretName))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestSetRemovesTempFileWhenRenameFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	targetDir := filepath.Join(dir, secretName)
	require.NoError(t, os.MkdirAll(filepath.Join(targetDir, "occupied"), 0o700))

	err := file.New(dir).Set(secretName, []byte("v1"))

	require.ErrorIs(t, err, os.ErrExist)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, secretName, entries[0].Name())
}
