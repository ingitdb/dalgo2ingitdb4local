package dalgo2fsingitdb

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	dalrecord "github.com/dal-go/record"
	"github.com/ingitdb/ingitdb-go/ingitdb"
)

// A case-insensitive, case-preserving file system (macOS APFS and Windows
// NTFS by default) keeps one file for Bob.yaml and bob.yaml, under the name it
// was created with: a Set of bob writes into Bob.yaml, a Delete of bob deletes
// Bob.yaml, and a Get of bob reads Bob's data. The driver never deletes,
// writes over or reads a record because the file system folds case, in every
// key encoding:
//
//   - A Set or Insert whose file (or a folder on its path, a parent record's
//     included) is on disk under a name that differs only in case fails with
//     ErrNameDiffersInCase, naming both.
//   - A Delete removes only an exact-case match: when the only match differs in
//     case it fails with an error that is both dalrecord.ErrRecordNotFound and
//     ErrNameDiffersInCase, and deletes nothing.
//   - A Get of such a key finds no record (the record's error is that same
//     error), and a query of a collection whose folder or file is on disk under
//     a name that differs only in case lists none, as a query lists the names
//     on disk.
//
// Whether a folder folds case is found out on the folder itself (caseVariant):
// the name in the other case is the same file (os.SameFile) or not. The
// operating system's name decides nothing: a case-sensitive APFS volume, and a
// case-sensitive folder on Windows, exist.

// caseVariant reports the first entry on path, below root, that exists on
// disk under a name differing from path's only in letter case, in a folder
// that folds case: given is path up to that entry, and onDisk the entry as it
// is on disk. With path not below root, only path's own name is checked. A
// missing entry ends the check: there is nothing on disk to mistake path for.
func caseVariant(root, path string) (given, onDisk string, found bool, err error) {
	dir, rel := filepath.Dir(path), filepath.Base(path)
	if root != "" && within(root, path) {
		dir = root
		rel, _ = filepath.Rel(root, path) // within proved it relative
	}
	if rel == "." {
		return "", "", false, nil
	}
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		given = filepath.Join(dir, name)
		info, statErr := fsys.Lstat(given)
		if statErr != nil {
			return "", "", false, nil
		}
		if folds(dir, name, info) {
			entries, readErr := fsys.ReadDir(dir)
			if readErr != nil {
				return "", "", false, fmt.Errorf("read %s for names that differ only in case: %w", dir, readErr)
			}
			exact, other := false, ""
			for _, entry := range entries {
				exact = exact || entry.Name() == name
				if entry.Name() != name && strings.EqualFold(entry.Name(), name) {
					other = entry.Name()
				}
			}
			if !exact && other != "" {
				return given, filepath.Join(dir, other), true, nil
			}
		}
		dir = given
	}
	return "", "", false, nil
}

// folds reports whether dir folds the case of name, the existing entry
// described by info: name in the other case is the same file.
func folds(dir, name string, info os.FileInfo) bool {
	variant := swapCase(name)
	if variant == name {
		return false // no letter with a case: nothing to fold
	}
	other, err := fsys.Lstat(filepath.Join(dir, variant))
	return err == nil && os.SameFile(info, other)
}

// swapCase turns every upper-case letter of s to lower case and every other
// letter to upper case.
func swapCase(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsUpper(r) {
			return unicode.ToLower(r)
		}
		return unicode.ToUpper(r)
	}, s)
}

// otherRecord names the record whose file or folder onDisk (given, on disk)
// is, when it is one in colDef's records folder: path is the file of the
// record being written, given a prefix of it.
func otherRecord(colDef *ingitdb.CollectionDef, path, given, onDisk string) string {
	base := filepath.Join(colDef.DirPath, colDef.RecordFile.RecordsBasePath()) + string(filepath.Separator)
	full := onDisk + path[len(given):]
	if !strings.Contains(colDef.RecordFile.Name, "{key}") || !strings.HasPrefix(full, base) {
		return ""
	}
	key, err := colDef.RecordFile.DecodeKey(buildKeyExtractor(colDef.RecordFile.Name)(full[len(base):]))
	if err != nil || key == "" {
		return ""
	}
	return fmt.Sprintf(" (the file of record %q)", key)
}

// refuseCaseVariant fails a Set or Insert of recordKey at path when path, or a
// folder on it below root, is on disk under a name that differs only in case.
func refuseCaseVariant(root string, colDef *ingitdb.CollectionDef, recordKey, path string) error {
	given, onDisk, found, err := caseVariant(root, path)
	if err != nil || !found {
		return err
	}
	return fmt.Errorf("%w: record %q of collection %s would be written to %s, but this file system does not tell letter case apart and holds %s as %s%s: writing would overwrite it; use a key that differs in more than letter case, or a case-sensitive file system",
		ErrNameDiffersInCase, recordKey, colDef.ID, path, given, onDisk, otherRecord(colDef, path, given, onDisk))
}

// caseVariantNotFound is the error of a Get or Delete of recordKey at path
// when path, or a folder on it below root, is on disk under a name that
// differs only in case: the record is not found, and nothing is read or
// deleted.
func caseVariantNotFound(root string, colDef *ingitdb.CollectionDef, recordKey, path string) error {
	given, onDisk, found, err := caseVariant(root, path)
	if err != nil || !found {
		return err
	}
	return fmt.Errorf("%w: record %q of collection %s: %s is on disk as %s%s, a name that differs only in letter case (%w), and is not that record",
		dalrecord.ErrRecordNotFound, recordKey, colDef.ID, given, onDisk, otherRecord(colDef, path, given, onDisk), ErrNameDiffersInCase)
}

// queryTarget is the folder (or, for a collection in one file, the file) a
// query of colDef reads by its given name: below it the names are read from
// disk and matched exactly.
func queryTarget(colDef *ingitdb.CollectionDef) string {
	if colDef.RecordFile.RecordType != ingitdb.SingleRecord {
		return resolveRecordPath(colDef, "")
	}
	pattern := resolveRecordPath(colDef, "*")
	if i := strings.Index(pattern, "*"); i >= 0 {
		return filepath.Dir(pattern[:i+1])
	}
	return pattern
}

// queryReadsCaseVariant reports whether the folder or file a query of colDef
// reads is on disk under a name that differs only in case: a Get finds no
// record there, so the query lists none.
func queryReadsCaseVariant(root string, colDef *ingitdb.CollectionDef) (bool, error) {
	_, _, found, err := caseVariant(root, queryTarget(colDef))
	return found, err
}
