package dalgo2fsingitdb

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/dal-go/dalgo/dal"
	dalrecord "github.com/dal-go/record"
	"github.com/ingitdb/ingitdb-go/ingitdb"
)

// aliasDef is two collections, a and b, whose records live in one folder:
// b's folder is a symlink to a's.
func aliasDef(root string) *ingitdb.Definition {
	col := func(id string) *ingitdb.CollectionDef {
		return &ingitdb.CollectionDef{ID: id, DirPath: filepath.Join(root, id),
			RecordFile: &ingitdb.RecordFileDef{Name: "{key}.yaml", Format: ingitdb.RecordFormatYAML, RecordType: ingitdb.SingleRecord},
			Columns:    map[string]*ingitdb.ColumnDef{"title": {Type: ingitdb.ColumnTypeString}}, ColumnsOrder: []string{"title"}}
	}
	return &ingitdb.Definition{Collections: map[string]*ingitdb.CollectionDef{"a": col("a"), "b": col("b")}}
}

// A delete through one path to a folder updates the listing read through
// another path to it: in one transaction, a stale listing of a never lets a
// Delete or Set of bob reach Bob, which b created after bob was deleted.
func TestCaseFold_SymlinkAliasOneTransaction(t *testing.T) {
	root := t.TempDir()
	withFoldFS(t, root)
	writeFiles(t, root, map[string]string{"a/$records/bob.yaml": "title: bob\n"})
	if err := os.Symlink(filepath.Join(root, "a"), filepath.Join(root, "b")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	db, err := NewLocalDBWithDef(root, aliasDef(root))
	if err != nil {
		t.Fatal(err)
	}
	bob := filepath.Join(root, "a", "$records", "Bob.yaml")
	for _, last := range []string{"delete", "set"} {
		t.Run(last, func(t *testing.T) {
			_ = os.Remove(bob)
			writeFiles(t, root, map[string]string{"a/$records/bob.yaml": "title: bob\n"})
			err := db.RunReadwriteTransaction(context.Background(), func(ctx context.Context, tx dal.ReadwriteTransaction) error {
				if err := tx.Get(ctx, dalrecord.NewRecordWithData(dalrecord.NewKeyWithID("a", "bob"), map[string]any{})); err != nil {
					return err
				}
				if err := tx.Delete(ctx, dalrecord.NewKeyWithID("b", "bob")); err != nil {
					return err
				}
				if err := tx.Insert(ctx, dalrecord.NewRecordWithData(dalrecord.NewKeyWithID("b", "Bob"), map[string]any{"title": "Bob"})); err != nil {
					return err
				}
				if last == "delete" {
					return tx.Delete(ctx, dalrecord.NewKeyWithID("a", "bob"))
				}
				return tx.Set(ctx, dalrecord.NewRecordWithData(dalrecord.NewKeyWithID("a", "bob"), map[string]any{"title": "bob"}))
			})
			if !errors.Is(err, ErrNameDiffersInCase) {
				t.Errorf("%s a/bob after b made Bob: %v", last, err)
			}
			if got := readFile(t, bob); got != "title: Bob\n" {
				t.Errorf("Bob.yaml: %q", got)
			}
		})
	}
}

// With no root, or a root that does not hold the collections, a parent
// record's folder on disk under another name is still found: bob's notes are
// never Bob's.
func TestCaseFold_AncestorWithoutRoot(t *testing.T) {
	for name, root := range map[string]func(t *testing.T) string{
		"no root":    func(*testing.T) string { return "" },
		"other root": func(t *testing.T) string { return t.TempDir() },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			withFoldFS(t, dir)
			n1 := "people/$records/Bob/notes/$records/n1.yaml"
			writeFiles(t, dir, map[string]string{"people/$records/Bob.yaml": "title: Bob\n", n1: "title: n1\n"})
			db, err := NewLocalDBWithDef(root(t), caseFoldDef(dir, ""))
			if err != nil {
				t.Fatal(err)
			}
			bob := dalrecord.NewKeyWithID("people", "bob")
			if err := deleteKey(db, dalrecord.NewKeyWithParentAndID(bob, "notes", "n1")); !errors.Is(err, ErrNameDiffersInCase) {
				t.Errorf("Delete bob/notes/n1: %v", err)
			}
			if err := db.RunReadwriteTransaction(context.Background(), func(ctx context.Context, tx dal.ReadwriteTransaction) error {
				return tx.Set(ctx, dalrecord.NewRecordWithData(dalrecord.NewKeyWithParentAndID(bob, "notes", "n1"), map[string]any{"title": "x"}))
			}); !errors.Is(err, ErrNameDiffersInCase) {
				t.Errorf("Set bob/notes/n1: %v", err)
			}
			if err := insertTitle(db, dalrecord.NewKeyWithParentAndID(bob, "notes", "n2"), "x"); !errors.Is(err, ErrNameDiffersInCase) {
				t.Errorf("Insert bob/notes/n2: %v", err)
			}
			if got := readFile(t, filepath.Join(dir, filepath.FromSlash(n1))); got != "title: n1\n" {
				t.Errorf("Bob's n1: %q", got)
			}
			if _, err := os.Stat(filepath.Join(dir, "people", "$records", "Bob", "notes", "$records", "n2.yaml")); err == nil {
				t.Error("n2 was written into Bob's notes")
			}
		})
	}
}

// statFails is a file system whose Stat always fails.
type statFails struct{ fileSystem }

func (statFails) Stat(name string) (fs.FileInfo, error) {
	return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrPermission}
}

// A folder that cannot be statted is still listed and found by its own path;
// it is only not matched to other paths that reach it.
func TestDirCache_StatFails(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"d/x": ""})
	saved := fsys
	fsys = statFails{osFS{}}
	t.Cleanup(func() { fsys = saved })
	cache := newDirCache()
	d := filepath.Join(dir, "d")
	if names, err := cache.list(d); err != nil || !names["x"] {
		t.Fatalf("list: %v %v", names, err)
	}
	cache.removed(filepath.Join(d, "x"))
	cache.wrote(filepath.Join(d, "y"))
	if got := cache.listings(d); len(got) != 1 || got[0]["x"] || !got[0]["y"] {
		t.Errorf("listings: %v", got)
	}
	cache.failed(filepath.Join(d, "y"))
	if len(cache.names) != 0 || len(cache.dirs) != 0 {
		t.Errorf("failed kept %v %v", cache.names, cache.dirs)
	}
}
