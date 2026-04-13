package safefile

import (
	"io"
	"os"
)

// ReadFile reads a file within rootDir using os.Root to prevent directory traversal.
// The name must be a relative path within rootDir.
func ReadFile(rootDir, name string) ([]byte, error) {
	r, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()

	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	return io.ReadAll(f)
}

// OpenFile opens a file within rootDir with given flags using os.Root.
// The returned *os.File is independent and safe to use after this function returns.
func OpenFile(rootDir, name string, flag int, perm os.FileMode) (*os.File, error) {
	r, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()

	return r.OpenFile(name, flag, perm)
}

// Stat returns FileInfo for a file scoped within rootDir using os.Root.
func Stat(rootDir, name string) (os.FileInfo, error) {
	r, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()

	return r.Stat(name)
}

// WriteFile writes data to a file within rootDir using os.Root to prevent
// directory traversal. The file is created or truncated with the given permissions.
func WriteFile(rootDir, name string, data []byte, perm os.FileMode) error {
	f, err := OpenFile(rootDir, name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
