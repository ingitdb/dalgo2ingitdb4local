package dalgo2fsingitdb

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dal-go/dalgo/dal"
	dalrecord "github.com/dal-go/record"
	"github.com/ingitdb/ingitdb-go/ingitdb"
)

// foldFS is a case-insensitive, case-preserving file system below root, made
// on top of the real one: a name below root that differs only in letter case
// from an existing entry names that entry, as on macOS APFS and Windows NTFS,
// and a new name keeps the case it is created with. A test that stands it in
// (withFoldFS) shows on a case-sensitive file system (Linux CI) what a
// case-insensitive one does.
type foldFS struct{ root string }

// resolve is the real path of the entry path names on this file system.
func (f foldFS) resolve(path string) string {
	rel, err := filepath.Rel(f.root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return path
	}
	dir := f.root
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.EqualFold(e.Name(), name) {
				name = e.Name()
				break
			}
		}
		dir = filepath.Join(dir, name)
	}
	return dir
}

func (f foldFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(f.resolve(name)) }
func (f foldFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(f.resolve(name), data, perm)
}
func (f foldFS) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(f.resolve(path), perm)
}
func (f foldFS) Remove(name string) error                   { return os.Remove(f.resolve(name)) }
func (f foldFS) Stat(name string) (fs.FileInfo, error)      { return os.Stat(f.resolve(name)) }
func (f foldFS) Lstat(name string) (fs.FileInfo, error)     { return os.Lstat(f.resolve(name)) }
func (f foldFS) ReadDir(name string) ([]fs.DirEntry, error) { return os.ReadDir(f.resolve(name)) }

// Glob matches, like filepath.Glob on a case-insensitive file system, the
// names read from a folder case-sensitively, and returns the folders above the
// first pattern character as they are given.
func (f foldFS) Glob(pattern string) ([]string, error) {
	i := strings.IndexAny(pattern, "*?[")
	if i < 0 {
		if _, err := f.Lstat(pattern); err != nil {
			return nil, nil
		}
		return []string{pattern}, nil
	}
	prefix := filepath.Dir(pattern[:i+1])
	real := f.resolve(prefix)
	matches, err := filepath.Glob(real + pattern[len(prefix):])
	for k := range matches {
		matches[k] = prefix + matches[k][len(real):]
	}
	return matches, err
}

// withFoldFS stands foldFS in for the operating system's file system below
// root until the test ends. A test that calls it must not call t.Parallel: the
// seam is package state, and a test that is not parallel never runs beside
// one that is.
func withFoldFS(t *testing.T, root string, wrap ...func(fileSystem) fileSystem) {
	t.Helper()
	var f fileSystem = foldFS{root: root}
	for _, w := range wrap {
		f = w(f)
	}
	saved := fsys
	fsys = f
	t.Cleanup(func() { fsys = saved })
}

// foldsCase reports whether dir is on a case-insensitive file system.
func foldsCase(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "CaseProbe")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(probe) }()
	a, errA := os.Lstat(probe)
	b, errB := os.Lstat(filepath.Join(dir, "caseprobe"))
	return errA == nil && errB == nil && os.SameFile(a, b)
}

// caseFoldDef is people/{key}.yaml with notes below each person, and the
// map-of-records file tags/tags.yaml.
func caseFoldDef(root string, encoding string) *ingitdb.Definition {
	rf := func() *ingitdb.RecordFileDef {
		return &ingitdb.RecordFileDef{Name: "{key}.yaml", Format: ingitdb.RecordFormatYAML, RecordType: ingitdb.SingleRecord, KeyEncoding: encoding}
	}
	cols := map[string]*ingitdb.ColumnDef{"title": {Type: ingitdb.ColumnTypeString}}
	notes := &ingitdb.CollectionDef{ID: "notes", DirPath: "ignored", RecordFile: rf(), Columns: cols, ColumnsOrder: []string{"title"}}
	people := &ingitdb.CollectionDef{ID: "people", DirPath: filepath.Join(root, "people"), RecordFile: rf(), Columns: cols, ColumnsOrder: []string{"title"},
		SubCollections: map[string]*ingitdb.CollectionDef{"notes": notes}}
	tags := &ingitdb.CollectionDef{ID: "tags", DirPath: filepath.Join(root, "tags"), Columns: cols,
		RecordFile: &ingitdb.RecordFileDef{Name: "tags.yaml", Format: ingitdb.RecordFormatYAML, RecordType: ingitdb.MapOfRecords}}
	return &ingitdb.Definition{Collections: map[string]*ingitdb.CollectionDef{"people": people, "tags": tags}}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

func deleteKey(db dal.DB, key *dalrecord.Key) error {
	return db.RunReadwriteTransaction(context.Background(), func(ctx context.Context, tx dal.ReadwriteTransaction) error {
		return tx.Delete(ctx, key)
	})
}

func insertTitle(db dal.DB, key *dalrecord.Key, title string) error {
	return db.RunReadwriteTransaction(context.Background(), func(ctx context.Context, tx dal.ReadwriteTransaction) error {
		return tx.Insert(ctx, dalrecord.NewRecordWithData(key, map[string]any{"title": title}))
	})
}

// getTitle reads key: whether it exists, its title, and the error of Get.
func getTitle(db dal.DB, key *dalrecord.Key) (bool, any, error) {
	data := map[string]any{}
	rec := dalrecord.NewRecordWithData(key, data)
	if err := get(db, rec); err != nil {
		return false, nil, err
	}
	return rec.Exists(), data["title"], nil
}

func sortedKeys(t *testing.T, db dal.DB, ref dal.CollectionRef) string {
	t.Helper()
	keys, err := queryKeys(t, db, ref)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}

// recordsetRows is the number of rows a recordset query of ref reads.
func recordsetRows(db dal.DB, ref dal.CollectionRef) (int, error) {
	q := dal.NewQueryBuilder(dal.From(ref)).SelectIntoRecord(func() dalrecord.Record {
		return dalrecord.NewRecordWithData(dalrecord.NewKeyWithID(ref.Name(), ""), map[string]any{})
	})
	rows := 0
	err := db.RunReadonlyTransaction(context.Background(), func(ctx context.Context, tx dal.ReadTransaction) error {
		reader, err := tx.ExecuteQueryToRecordsetReader(ctx, q)
		if err != nil {
			return err
		}
		defer func() { _ = reader.Close() }()
		for {
			if _, _, err := reader.Next(); err != nil {
				return nil
			}
			rows++
		}
	})
	return rows, err
}

// checkCaseFold is the rule, on a file system that folds case below root: no
// Set, Insert or Delete deletes or writes over a live record because the file
// system folds case, and Get, Query and the recordset query agree that a
// record is only where its own name is.
func checkCaseFold(t *testing.T, root string, encoding string) {
	t.Helper()
	db, err := NewLocalDBWithDef(root, caseFoldDef(root, encoding))
	if err != nil {
		t.Fatal(err)
	}
	people := dal.NewRootCollectionRef("people", "")
	bobFile := filepath.Join(root, "people", "$records", "Bob.yaml")
	bobNote := filepath.Join(root, "people", "$records", "Bob", "notes", "$records", "n1.yaml")
	tagsFile := filepath.Join(root, "tags", "Tags.yaml")
	writeFiles(t, root, map[string]string{
		"people/$records/Bob.yaml":                   "title: Bob\n",
		"people/$records/Bob/notes/$records/n1.yaml": "title: Bob's note\n",
		"tags/Tags.yaml":                             "red:\n  title: Red\n",
	})
	Bob := dalrecord.NewKeyWithID("people", "Bob")
	bob := dalrecord.NewKeyWithID("people", "bob")
	bobsNote := dalrecord.NewKeyWithParentAndID(bob, "notes", "n1")
	red := dalrecord.NewKeyWithID("tags", "red")

	// Set and Insert of a key that differs only in case are refused, naming
	// both keys, and leave the live record as it was.
	err = setTitle(context.Background(), db, bob, "bob")
	if !errors.Is(err, ErrNameDiffersInCase) || !strings.Contains(err.Error(), `"bob"`) || !strings.Contains(err.Error(), "Bob") {
		t.Errorf("Set bob over Bob: %v", err)
	}
	if err := insertTitle(db, bob, "bob"); !errors.Is(err, ErrNameDiffersInCase) {
		t.Errorf("Insert bob over Bob: %v", err)
	}
	if got := readFile(t, bobFile); got != "title: Bob\n" {
		t.Errorf("Bob.yaml after Set/Insert of bob: %q", got)
	}
	// A record below a parent folder that differs only in case is refused too.
	if err := setTitle(context.Background(), db, bobsNote, "bob's note"); !errors.Is(err, ErrNameDiffersInCase) {
		t.Errorf("Set bob/notes/n1 over Bob/notes/n1: %v", err)
	}
	if err := setTitle(context.Background(), db, red, "red"); !errors.Is(err, ErrNameDiffersInCase) {
		t.Errorf("Set tags/red into Tags.yaml: %v", err)
	}
	if got := readFile(t, bobNote); got != "title: Bob's note\n" {
		t.Errorf("Bob's note after Set of bob's: %q", got)
	}

	// Delete removes only an exact-case match.
	for _, key := range []*dalrecord.Key{bob, bobsNote, red} {
		if err := deleteKey(db, key); !dalrecord.IsNotFound(err) || !errors.Is(err, ErrNameDiffersInCase) {
			t.Errorf("Delete %v: %v", key, err)
		}
	}
	if got := readFile(t, bobFile) + readFile(t, bobNote) + readFile(t, tagsFile); got != "title: Bob\ntitle: Bob's note\nred:\n  title: Red\n" {
		t.Errorf("records after Delete of case variants: %q", got)
	}

	// Get finds a record only under its own name, as Query lists it.
	if exists, title, err := getTitle(db, Bob); err != nil || !exists || title != "Bob" {
		t.Errorf("Get Bob: %v %v %v", exists, title, err)
	}
	for _, key := range []*dalrecord.Key{bob, bobsNote, red} {
		if exists, title, err := getTitle(db, key); err != nil || exists || title != nil {
			t.Errorf("Get %v: exists=%v title=%v err=%v", key, exists, title, err)
		}
	}
	if got := sortedKeys(t, db, people); got != "Bob" {
		t.Errorf("query people: %q", got)
	}
	if got := sortedKeys(t, db, dal.NewCollectionRef("notes", "", bob)); got != "" {
		t.Errorf("query notes of bob: %q", got)
	}
	if got := sortedKeys(t, db, dal.NewCollectionRef("notes", "", Bob)); got != "n1" {
		t.Errorf("query notes of Bob: %q", got)
	}
	if got := sortedKeys(t, db, dal.NewRootCollectionRef("tags", "")); got != "" {
		t.Errorf("query tags (the file is Tags.yaml): %q", got)
	}
	if rows, err := recordsetRows(db, dal.NewCollectionRef("notes", "", bob)); err != nil || rows != 0 {
		t.Errorf("recordset of notes of bob: %d, %v", rows, err)
	}
	if rows, err := recordsetRows(db, dal.NewCollectionRef("notes", "", Bob)); err != nil || rows != 1 {
		t.Errorf("recordset of notes of Bob: %d, %v", rows, err)
	}

	// The exact case works as before, and so do names with no letters.
	if err := setTitle(context.Background(), db, Bob, "Bob 2"); err != nil {
		t.Errorf("Set Bob: %v", err)
	}
	if err := insertTitle(db, dalrecord.NewKeyWithID("people", "42"), "42"); err != nil {
		t.Errorf("Insert 42: %v", err)
	}
	if got := sortedKeys(t, db, people); got != "42 Bob" {
		t.Errorf("query people after Set: %q", got)
	}
	if err := deleteKey(db, Bob); err != nil {
		t.Errorf("Delete Bob: %v", err)
	}
	if _, err := os.Lstat(bobFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Bob.yaml after Delete Bob: %v", err)
	}
	// With Bob gone, bob is a new record.
	if err := setTitle(context.Background(), db, bob, "bob"); err != nil {
		t.Errorf("Set bob after Bob is gone: %v", err)
	}
	if got := sortedKeys(t, db, people); got != "42 bob" {
		t.Errorf("query people after Set bob: %q", got)
	}
}

// Verifies the case-folding rule on a simulated case-insensitive,
// case-preserving file system, so it holds on case-sensitive CI runners too,
// in every key encoding.
func TestCaseFold_Simulated(t *testing.T) {
	for _, encoding := range []string{"", ingitdb.KeyEncodingTildeV1} {
		t.Run("encoding="+encoding, func(t *testing.T) {
			root := t.TempDir()
			withFoldFS(t, root)
			checkCaseFold(t, root, encoding)
		})
	}
}

// Verifies the case-folding rule on the real file system, where it folds case
// (macOS APFS, Windows NTFS by default); elsewhere it is skipped.
func TestCaseFold_RealFileSystem(t *testing.T) {
	for _, encoding := range []string{"", ingitdb.KeyEncodingTildeV1} {
		t.Run("encoding="+encoding, func(t *testing.T) {
			root := t.TempDir()
			if !foldsCase(t, root) {
				t.Skip("the temporary folder is on a case-sensitive file system")
			}
			checkCaseFold(t, root, encoding)
		})
	}
}

// readDirFails is a file system whose folders cannot be listed.
type readDirFails struct{ fileSystem }

var errReadDir = errors.New("read dir fails")

func (readDirFails) ReadDir(string) ([]fs.DirEntry, error) { return nil, errReadDir }

// A folder that folds case but cannot be listed fails every operation that
// needs to know the names in it, and nothing is read, written or deleted.
func TestCaseFold_ReadDirFails(t *testing.T) {
	root := t.TempDir()
	withFoldFS(t, root, func(f fileSystem) fileSystem { return readDirFails{f} })
	writeFiles(t, root, map[string]string{"people/$records/Bob.yaml": "title: Bob\n"})
	db, err := NewLocalDBWithDef(root, caseFoldDef(root, ""))
	if err != nil {
		t.Fatal(err)
	}
	bob := dalrecord.NewKeyWithID("people", "bob")
	if err := setTitle(context.Background(), db, bob, "bob"); !errors.Is(err, errReadDir) {
		t.Errorf("Set: %v", err)
	}
	if err := insertTitle(db, bob, "bob"); !errors.Is(err, errReadDir) {
		t.Errorf("Insert: %v", err)
	}
	if err := deleteKey(db, bob); !errors.Is(err, errReadDir) {
		t.Errorf("Delete: %v", err)
	}
	if _, _, err := getTitle(db, bob); !errors.Is(err, errReadDir) {
		t.Errorf("Get: %v", err)
	}
	if _, err := queryKeys(t, db, dal.NewRootCollectionRef("people", "")); !errors.Is(err, errReadDir) {
		t.Errorf("query: %v", err)
	}
	if _, err := recordsetRows(db, dal.NewRootCollectionRef("people", "")); !errors.Is(err, errReadDir) {
		t.Errorf("recordset: %v", err)
	}
	if got := readFile(t, filepath.Join(root, "people", "$records", "Bob.yaml")); got != "title: Bob\n" {
		t.Errorf("Bob.yaml: %q", got)
	}
}

// caseVariant checks only path's own name when path is not below root, and
// nothing for root itself; the error names the other record only when its
// name is one the collection's encoding writes.
func TestCaseVariant_Edges(t *testing.T) {
	root := t.TempDir()
	withFoldFS(t, root)
	writeFiles(t, root, map[string]string{"Up/x.yaml": "", "people/$records/a~2fb.yaml": "title: x\n"})
	if _, onDisk, found, err := caseVariant("", filepath.Join(root, "up", "x.yaml")); err != nil || found || onDisk != "" {
		t.Errorf("no root, folder differs in case: %q %v %v", onDisk, found, err)
	}
	if _, onDisk, found, err := caseVariant("", filepath.Join(root, "Up", "X.yaml")); err != nil || !found || onDisk != filepath.Join(root, "Up", "x.yaml") {
		t.Errorf("no root, file differs in case: %q %v %v", onDisk, found, err)
	}
	if _, _, found, err := caseVariant(root, root); err != nil || found {
		t.Errorf("root itself: %v %v", found, err)
	}
	// A name with no letter that has a case is never another name.
	writeFiles(t, root, map[string]string{"42/x": ""})
	if _, _, found, err := caseVariant(root, filepath.Join(root, "42", "x")); err != nil || found {
		t.Errorf("no letters: %v %v", found, err)
	}
	// A one-record collection with no {key} in its name is read by its file.
	one := &ingitdb.CollectionDef{DirPath: filepath.Join(root, "c"), RecordFile: &ingitdb.RecordFileDef{Name: "data.yaml", RecordType: ingitdb.SingleRecord}}
	if got := queryTarget(one); got != filepath.Join(root, "c", "data.yaml") {
		t.Errorf("queryTarget without {key}: %q", got)
	}
	db, err := NewLocalDBWithDef(root, caseFoldDef(root, ingitdb.KeyEncodingTildeV1))
	if err != nil {
		t.Fatal(err)
	}
	// a~2Fb is the tilde-v1 name of a/b; a~2fb is no name the encoding writes.
	if err := deleteKey(db, dalrecord.NewKeyWithID("people", "a/b")); !errors.Is(err, ErrNameDiffersInCase) || strings.Contains(err.Error(), "the file of record") {
		t.Errorf("Delete a/b: %v", err)
	}
	if exists, _, err := getTitle(db, dalrecord.NewKeyWithID("people", "a/b")); err != nil || exists {
		t.Errorf("Get a/b: %v %v", exists, err)
	}
	if got := swapCase("Bob-42é"); got != "bOB-42É" {
		t.Errorf("swapCase: %q", got)
	}
}

// osFS lists a folder; the driver lists one only where it folds case, which a
// case-sensitive CI runner never has.
func TestOSFS_ReadDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.yaml": ""})
	if entries, err := (osFS{}).ReadDir(dir); err != nil || len(entries) != 1 || entries[0].Name() != "a.yaml" {
		t.Errorf("ReadDir: %v, %v", entries, err)
	}
}
