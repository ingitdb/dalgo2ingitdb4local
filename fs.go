package dalgo2fsingitdb

import (
	"io/fs"
	"os"
	"path/filepath"
)

// fileSystem is every file operation the driver makes on record files and
// their folders. It is a test seam: a test stands a case-insensitive,
// case-preserving file system (macOS APFS, Windows NTFS) in on a
// case-sensitive one, so the case-folding rules (casefold.go) are tested on
// Linux too.
type fileSystem interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Remove(name string) error
	Stat(name string) (fs.FileInfo, error)
	Lstat(name string) (fs.FileInfo, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	Glob(pattern string) ([]string, error)
}

// osFS is the file system of the operating system.
type osFS struct{}

func (osFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }
func (osFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (osFS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }
func (osFS) Remove(name string) error                     { return os.Remove(name) }
func (osFS) Stat(name string) (fs.FileInfo, error)        { return os.Stat(name) }
func (osFS) Lstat(name string) (fs.FileInfo, error)       { return os.Lstat(name) }
func (osFS) ReadDir(name string) ([]fs.DirEntry, error)   { return os.ReadDir(name) }
func (osFS) Glob(pattern string) ([]string, error)        { return filepath.Glob(pattern) }

// fsys is the file system records are read from and written to.
var fsys fileSystem = osFS{}
