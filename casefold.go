package dalgo2fsingitdb

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dal-go/dalgo/dal"
	dalrecord "github.com/dal-go/record"
	"github.com/ingitdb/ingitdb-go/ingitdb"
)

// A file system that folds names (macOS APFS and Windows NTFS by default)
// keeps one file for Bob.yaml and bob.yaml, under the name it was created
// with. APFS also keeps one file for a name in Unicode NFC and in NFD (café
// written either way). On such a file system, a Set of bob writes into
// Bob.yaml, a Delete of bob deletes Bob.yaml, and a Get of bob reads Bob's
// data. The driver never deletes, writes over or reads a record because the
// file system folds names, in every key encoding:
//
//   - A Set or Insert fails with ErrNameDiffersInCase, naming both, when its
//     file is on disk under another name the file system treats as the same.
//     The same applies to a folder on its path, a parent record's included.
//   - A Delete removes only an exact match. When the only match is under
//     another name, it fails with an error that is both
//     dalrecord.ErrRecordNotFound and ErrNameDiffersInCase, and deletes
//     nothing.
//   - A Get of such a key finds no record: the record's error is that same
//     error. A query below a parent record whose folder is under another name
//     lists none, as a query lists the names on disk.
//   - A folder of the definition's own layout (a collection folder, its
//     records folder, a subcollection's name) that is on disk under another
//     name fails every operation with ErrNameDiffersInCase. The definition
//     and the disk disagree, and an empty answer would hide that.
//
// Whether a folder folds names is found out on the folder itself, whatever
// rule it folds them by: every existing entry on a record's path is looked
// up in its folder's listing, and an entry the file system finds under a
// name the listing does not show is on disk under another name. A name the
// file system does not find needs no listing: if another name stood for it,
// the file system would find that. Nor does a folder the file system
// reports to hold names exactly (fileSystem.ExactNames; on Linux, ext4,
// btrfs, f2fs or tmpfs without the casefold flag). The operating system's
// name decides nothing: case-sensitive APFS volumes, which still treat NFC
// and NFD names as one, and case-sensitive folders on Windows, exist.
//
// The listings are read once per transaction (dirCache) and updated by the
// transaction's own writes and deletes. A bulk write of N records into one
// folder lists that folder once, not N times. The driver is not safe for
// concurrent use, and that holds across processes too: a name another
// process deletes and re-creates under another name during a transaction is
// still taken, for the rest of the transaction, to be the name it listed.

// dirCache holds the names in each folder a transaction has listed, keyed by
// the folder's absolute path, and whether each folder it asked holds names
// exactly. A nil dirCache lists and asks every time.
type dirCache struct {
	names map[string]map[string]bool
	exact map[string]bool
}

func newDirCache() *dirCache {
	return &dirCache{names: make(map[string]map[string]bool), exact: make(map[string]bool)}
}

// exactNames reports whether dir holds every name exactly as it is given
// (fileSystem.ExactNames): then no other name can find an entry in it, and
// it need not be listed.
func (c *dirCache) exactNames(dir string) bool {
	if c == nil {
		return fsys.ExactNames(dir)
	}
	exact, ok := c.exact[dir]
	if !ok {
		exact = fsys.ExactNames(dir)
		c.exact[dir] = exact
	}
	return exact
}

// list returns the names in dir.
func (c *dirCache) list(dir string) (map[string]bool, error) {
	if c != nil {
		if names, ok := c.names[dir]; ok {
			return names, nil
		}
	}
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s for names that differ only in case or normalisation: %w", dir, err)
	}
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	if c != nil {
		c.names[dir] = names
	}
	return names, nil
}

// wrote records that path, and every folder on it, now exist under the names
// path gives them. A write is made only where no other name stands for path.
func (c *dirCache) wrote(path string) {
	if c == nil {
		return
	}
	for p := absPath(path); filepath.Dir(p) != p; p = filepath.Dir(p) {
		if names, ok := c.names[filepath.Dir(p)]; ok {
			names[filepath.Base(p)] = true
		}
	}
}

// failed records that a write of path failed. It may have made some of the
// folders on path, or the file, before it failed, so the listing of every
// folder on path is read again when it is next needed.
func (c *dirCache) failed(path string) {
	if c == nil {
		return
	}
	for p := absPath(path); filepath.Dir(p) != p; p = filepath.Dir(p) {
		delete(c.names, filepath.Dir(p))
	}
}

// removed records that path, which a transaction deleted, is no longer in its
// folder.
func (c *dirCache) removed(path string) {
	if c == nil {
		return
	}
	p := absPath(path)
	delete(c.names[filepath.Dir(p)], filepath.Base(p))
}

// absPath is p made absolute, so a relative and an absolute path compare.
func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return p
}

// sameName describes the entry on a record's path that is on disk under
// another name the file system treats as the same.
type sameName struct {
	given  string // the path up to that entry, as the driver names it
	onDisk string // the entry as it is on disk, or "" when the listing does not show it
	rest   string // the record's path below given
}

// shownOnDisk is onDisk, or a description when the listing does not show it.
func (m *sameName) shownOnDisk() string {
	if m.onDisk == "" {
		return "another name this file system treats as the same"
	}
	return shown(m.onDisk)
}

// caseVariant reports the first entry on path, below root, that exists on
// disk under another name the file system treats as the same, or nil. With
// path not below root, only path's own name is checked. A missing entry ends
// the check: there is nothing on disk to mistake path for.
func caseVariant(cache *dirCache, root, path string) (*sameName, error) {
	path = absPath(path)
	dir, rel := filepath.Dir(path), filepath.Base(path)
	if root != "" && within(absPath(root), path) {
		dir = absPath(root)
		rel, _ = filepath.Rel(dir, path) // within proved it relative
	}
	if rel == "." {
		return nil, nil
	}
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		given := filepath.Join(dir, name)
		info, statErr := fsys.Lstat(given)
		if statErr != nil {
			return nil, nil // nothing on disk, under this name or any other
		}
		if cache.exactNames(dir) {
			dir = given
			continue
		}
		names, err := cache.list(dir)
		if err != nil {
			return nil, err
		}
		if !names[name] {
			return &sameName{given: given, onDisk: sameFileIn(dir, names, info), rest: path[len(given):]}, nil
		}
		dir = given
	}
	return nil, nil
}

// sameFileIn is the entry of dir, among names, that is the file info
// describes, or "" when none is.
func sameFileIn(dir string, names map[string]bool, info os.FileInfo) string {
	for name := range names {
		if other, err := fsys.Lstat(filepath.Join(dir, name)); err == nil && os.SameFile(info, other) {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// shown is s as an error message shows it: quoted in ASCII when it has a
// non-ASCII rune, so that two names in different Unicode normalisation
// forms read differently.
func shown(s string) string {
	if isASCII(s) {
		return s
	}
	return strconv.QuoteToASCII(s)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// keyNames is the folders and file of a record's path that are named by a
// key: each parent record's folder, and everything in the collection's
// records folder. Every other folder belongs to the definition's layout.
type keyNames struct {
	recordsDir string
	parentDirs map[string]bool
}

// keyNamesOf is the keyNames of colDef's records below parent. A collection
// in one file (no {key} in its name) has no record named by its key.
func keyNamesOf(def *ingitdb.Definition, colDef *ingitdb.CollectionDef, parent *dalrecord.Key) keyNames {
	k := keyNames{parentDirs: map[string]bool{}}
	if strings.Contains(colDef.RecordFile.Name, "{key}") {
		k.recordsDir = absPath(filepath.Join(colDef.DirPath, recordsBasePath(colDef)))
	}
	for p := parent; p != nil; p = p.Parent() {
		// The record's own resolution succeeded, so each ancestor resolves.
		col, _ := resolveScopedCollection(def, p.Collection(), p.Parent())
		name, _ := parentDirName(col, fmt.Sprintf("%v", p.ID))
		k.parentDirs[absPath(filepath.Join(col.DirPath, recordsBasePath(col), name))] = true
	}
	return k
}

// isKeyName reports whether path, a folder or file on a record's path, is
// named by a key.
func (k keyNames) isKeyName(path string) bool {
	return k.parentDirs[path] || k.recordsDir != "" && strings.HasPrefix(path, k.recordsDir+string(filepath.Separator))
}

// layoutError is the error of any operation whose path goes through m, a
// folder of the definition's layout that is on disk under another name.
func layoutError(colDef *ingitdb.CollectionDef, m *sameName) error {
	return fmt.Errorf("%w: collection %s: the definition puts its records below %s, but this file system holds that folder as %s, a name it treats as the same (it differs in letter case, Unicode normalisation or another way the file system ignores): rename the folder or fix the definition so that the two agree",
		ErrNameDiffersInCase, colDef.ID, shown(m.given), m.shownOnDisk())
}

// otherRecord names the record whose file or folder m is, when it is one in
// colDef's records folder.
func otherRecord(colDef *ingitdb.CollectionDef, k keyNames, m *sameName) string {
	full := m.onDisk + m.rest
	if m.onDisk == "" || k.recordsDir == "" || !strings.HasPrefix(full, k.recordsDir+string(filepath.Separator)) {
		return ""
	}
	key, err := colDef.RecordFile.DecodeKey(buildKeyExtractor(colDef.RecordFile.Name)(full[len(k.recordsDir)+1:]))
	if err != nil || key == "" {
		return ""
	}
	return fmt.Sprintf(" (the file of record %+q)", key)
}

// recordCaseVariant is the name on recordKey's path, at path, that is on
// disk under another name, with the keyNames of the path; nil when there is
// none. A layout folder under another name is an error.
func (r readonlyTx) recordCaseVariant(colDef *ingitdb.CollectionDef, parent *dalrecord.Key, path string) (*sameName, keyNames, error) {
	m, err := caseVariant(r.names, r.db.rootDirPath, path)
	if err != nil || m == nil {
		return nil, keyNames{}, err
	}
	k := keyNamesOf(r.db.def, colDef, parent)
	if !k.isKeyName(m.given) {
		return nil, k, layoutError(colDef, m)
	}
	return m, k, nil
}

// refuseCaseVariant fails a Set or Insert of recordKey at path when path, or
// a folder on it below the root, is on disk under another name.
func (r readwriteTx) refuseCaseVariant(colDef *ingitdb.CollectionDef, key *dalrecord.Key, recordKey, path string) error {
	m, k, err := r.recordCaseVariant(colDef, key.Parent(), path)
	if err != nil || m == nil {
		return err
	}
	return fmt.Errorf("%w: record %+q of collection %s would be written to %s, but this file system treats that name and another one as the same, and holds %s as %s%s: writing would overwrite it. Use a key whose name differs in more than letter case, Unicode normalisation or another way the file system ignores, or a file system that tells such names apart. To rename a record to such a key, delete the old record first (an exact key deletes it), then set the new one: the old record's folder of subcollections stays where it is, and records below the new key are refused until that folder is renamed",
		ErrNameDiffersInCase, recordKey, colDef.ID, shown(path), shown(m.given), m.shownOnDisk(), otherRecord(colDef, k, m))
}

// caseVariantNotFound is the error of a Get or Delete of recordKey at path
// when path, or a folder on it below the root, is on disk under another name.
// The record is not found, and nothing is read or deleted.
func (r readonlyTx) caseVariantNotFound(colDef *ingitdb.CollectionDef, parent *dalrecord.Key, recordKey, path string) error {
	m, k, err := r.recordCaseVariant(colDef, parent, path)
	if err != nil || m == nil {
		return err
	}
	return fmt.Errorf("%w: record %+q of collection %s: %s is on disk as %s%s, a name the file system treats as the same (%w), and is not that record",
		dalrecord.ErrRecordNotFound, recordKey, colDef.ID, shown(m.given), m.shownOnDisk(), otherRecord(colDef, k, m), ErrNameDiffersInCase)
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

// queryParent is the parent record of the collection a query reads, which
// collectionFromQuery has validated.
func queryParent(query dal.Query) *dalrecord.Key {
	sq, _ := query.(dal.StructuredQuery)
	ref, _ := sq.From().Base().(dal.CollectionRef)
	return ref.Parent()
}

// queryReadsCaseVariant reports whether a query of colDef reads below a
// parent record whose folder is on disk under another name: a Get finds no
// record there, so the query lists none. A layout folder under another name
// is an error.
func (r readonlyTx) queryReadsCaseVariant(colDef *ingitdb.CollectionDef, query dal.Query) (bool, error) {
	m, _, err := r.recordCaseVariant(colDef, queryParent(query), queryTarget(colDef))
	return m != nil, err
}
