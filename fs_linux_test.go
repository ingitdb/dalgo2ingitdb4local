//go:build linux

package dalgo2fsingitdb

import (
	"context"
	"errors"
	"testing"

	dalrecord "github.com/dal-go/record"
	"golang.org/x/sys/unix"
)

// withProbes stands in the system calls of osFS.ExactNames until the test
// ends.
func withProbes(t *testing.T, fsType func(string) (int64, error), flags func(string) (uint32, error)) {
	t.Helper()
	savedType, savedFlags := statfsType, inodeFlags
	statfsType, inodeFlags = fsType, flags
	t.Cleanup(func() { statfsType, inodeFlags = savedType, savedFlags })
}

// A folder holds names exactly only on a known file system, without the
// casefold flag, and when both questions are answered.
func TestOSFS_ExactNames(t *testing.T) {
	errProbe := errors.New("probe fails")
	ext4 := func(string) (int64, error) { return unix.EXT4_SUPER_MAGIC, nil }
	noFlags := func(string) (uint32, error) { return 0, nil }
	for name, c := range map[string]struct {
		fsType func(string) (int64, error)
		flags  func(string) (uint32, error)
		want   bool
	}{
		"ext4":          {ext4, noFlags, true},
		"statfs fails":  {func(string) (int64, error) { return 0, errProbe }, noFlags, false},
		"xfs":           {func(string) (int64, error) { return unix.XFS_SUPER_MAGIC, nil }, noFlags, false},
		"flags fail":    {ext4, func(string) (uint32, error) { return 0, errProbe }, false},
		"casefold flag": {ext4, func(string) (uint32, error) { return fsCasefoldFlag, nil }, false},
	} {
		t.Run(name, func(t *testing.T) {
			withProbes(t, c.fsType, c.flags)
			if got := (osFS{}).ExactNames(t.TempDir()); got != c.want {
				t.Errorf("ExactNames: %v, want %v", got, c.want)
			}
		})
	}
}

// The real probes answer for a real folder, and a folder that cannot be
// opened has no flags.
func TestOSFS_ExactNamesProbes(t *testing.T) {
	dir := t.TempDir()
	if _, err := statfsType(dir); err != nil {
		t.Errorf("statfs: %v", err)
	}
	if _, err := inodeFlags(dir + "/missing"); err == nil {
		t.Error("flags of a missing folder: no error")
	}
	// tmpfs and the root file system of a CI runner answer FS_IOC_GETFLAGS;
	// a file system that does not just makes ExactNames false.
	_, _ = inodeFlags(dir)
}

// On a Linux folder that holds names exactly, one write in a transaction
// lists no folder.
func TestCaseFold_RealExactNamesNeverList(t *testing.T) {
	root, counts := t.TempDir(), map[string]int{}
	if !(osFS{}).ExactNames(root) {
		t.Skip("the temporary folder is not on a file system known to hold names exactly")
	}
	saved := fsys
	fsys = countReadDir{osFS{}, counts}
	t.Cleanup(func() { fsys = saved })
	db, err := NewLocalDBWithDef(root, caseFoldDef(root, ""))
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, map[string]string{"people/$records/ann.yaml": "title: ann\n"})
	for _, id := range []string{"bob", "ann"} {
		if err := setTitle(context.Background(), db, dalrecord.NewKeyWithID("people", id), id); err != nil {
			t.Errorf("Set %s: %v", id, err)
		}
	}
	if len(counts) != 0 {
		t.Errorf("folders listed: %v", counts)
	}
}
