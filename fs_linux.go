//go:build linux

package dalgo2fsingitdb

import (
	"os"

	"golang.org/x/sys/unix"
)

// fsCasefoldFlag is FS_CASEFOLD_FL, the inode flag (chattr +F) of a folder
// whose names ext4, f2fs and tmpfs fold.
const fsCasefoldFlag = 0x40000000

// exactNameFileSystems are the file systems that hold every name as the
// bytes it is given, unless a folder has the casefold flag. XFS is not one:
// it can be made to fold ASCII case for the whole file system.
var exactNameFileSystems = map[int64]bool{
	unix.EXT4_SUPER_MAGIC:  true, // ext2, ext3 and ext4
	unix.BTRFS_SUPER_MAGIC: true,
	unix.F2FS_SUPER_MAGIC:  true,
	unix.TMPFS_MAGIC:       true,
}

// Test seams for the system calls ExactNames makes.
var (
	statfsType = func(dir string) (int64, error) {
		var st unix.Statfs_t
		err := unix.Statfs(dir, &st)
		return int64(st.Type), err
	}
	inodeFlags = func(dir string) (uint32, error) {
		f, err := os.Open(dir)
		if err != nil {
			return 0, err
		}
		defer func() { _ = f.Close() }()
		return unix.IoctlGetUint32(int(f.Fd()), unix.FS_IOC_GETFLAGS)
	}
)

// ExactNames reports whether dir is on a file system that holds names
// exactly and does not have the casefold flag. Any other file system, and
// any error, reports false: the folder is then listed.
func (osFS) ExactNames(dir string) bool {
	fsType, err := statfsType(dir)
	if err != nil || !exactNameFileSystems[fsType] {
		return false
	}
	flags, err := inodeFlags(dir)
	return err == nil && flags&fsCasefoldFlag == 0
}
