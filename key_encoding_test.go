package dalgo2fsingitdb

// specscore: feature/record-key-encoding (ingitdb-go)

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dal-go/dalgo/dal"
	dalrecord "github.com/dal-go/record"
	"github.com/ingitdb/ingitdb-go/ingitdb"
)

func tildeSpacesNotes(root string) *ingitdb.Definition {
	rf := func() *ingitdb.RecordFileDef {
		return &ingitdb.RecordFileDef{Name: "{key}.yaml", Format: ingitdb.RecordFormatYAML, RecordType: ingitdb.SingleRecord, KeyEncoding: ingitdb.KeyEncodingTildeV1}
	}
	cols := map[string]*ingitdb.ColumnDef{"title": {Type: ingitdb.ColumnTypeString}}
	notes := &ingitdb.CollectionDef{ID: "notes", DirPath: "ignored", RecordFile: rf(), Columns: cols, ColumnsOrder: []string{"title"}}
	spaces := &ingitdb.CollectionDef{ID: "spaces", DirPath: filepath.Join(root, "spaces"), RecordFile: rf(), Columns: cols, ColumnsOrder: []string{"title"},
		SubCollections: map[string]*ingitdb.CollectionDef{"notes": notes}}
	return &ingitdb.Definition{Collections: map[string]*ingitdb.CollectionDef{"spaces": spaces}}
}

func setTitle(ctx context.Context, db dal.DB, key *dalrecord.Key, title string) error {
	return db.RunReadwriteTransaction(ctx, func(ctx context.Context, tx dal.ReadwriteTransaction) error {
		return tx.Set(ctx, dalrecord.NewRecordWithData(key, map[string]any{"title": title}))
	})
}

func get(db dal.DB, rec dalrecord.Record) error {
	return db.RunReadonlyTransaction(context.Background(), func(ctx context.Context, tx dal.ReadTransaction) error {
		return tx.Get(ctx, rec)
	})
}

func queryKeys(t *testing.T, db dal.DB, ref dal.CollectionRef) ([]string, error) {
	t.Helper()
	q := dal.NewQueryBuilder(dal.From(ref)).SelectIntoRecord(func() dalrecord.Record {
		return dalrecord.NewRecordWithData(dalrecord.NewKeyWithID(ref.Name(), ""), map[string]any{})
	})
	var keys []string
	err := db.RunReadonlyTransaction(context.Background(), func(ctx context.Context, tx dal.ReadTransaction) error {
		reader, err := tx.ExecuteQueryToRecordsReader(ctx, q)
		if err != nil {
			return err
		}
		defer func() { _ = reader.Close() }()
		for {
			rec, nextErr := reader.Next()
			if nextErr != nil {
				return nil
			}
			keys = append(keys, rec.Key().ID.(string))
		}
	})
	return keys, err
}

// Verifies record-key-encoding#ac:readers-use-decoded-keys for the local
// driver: keys are written under their tilde-v1 names, at every level, and
// read back (Get, query, collection-group parents) as the real ids.
func TestKeyEncoding_TildeV1RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	def := tildeSpacesNotes(root)
	db, err := NewLocalDBWithDef(root, def)
	if err != nil {
		t.Fatal(err)
	}
	space := dalrecord.NewKeyWithID("spaces", "a/b")
	note := dalrecord.NewKeyWithParentAndID(space, "notes", "..")
	for key, title := range map[*dalrecord.Key]string{space: "space", dalrecord.NewKeyWithID("spaces", "CON"): "con", note: "note"} {
		if err := setTitle(ctx, db, key, title); err != nil {
			t.Fatalf("Set %v: %v", key, err)
		}
	}
	for _, p := range []string{"spaces/$records/a~2Fb.yaml", "spaces/$records/~43ON.yaml", "spaces/$records/a~2Fb/notes/$records/~2E~2E.yaml"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Errorf("expected %s: %v", p, err)
		}
	}
	data := map[string]any{}
	if err := get(db, dalrecord.NewRecordWithData(note, data)); err != nil || data["title"] != "note" {
		t.Errorf("Get note: %v, %v", data, err)
	}
	if keys, err := queryKeys(t, db, dal.NewRootCollectionRef("spaces", "")); err != nil || strings.Join(keys, " ") != "a/b CON" {
		t.Errorf("query spaces: %q, %v", keys, err)
	}
	if keys, err := queryKeys(t, db, dal.NewCollectionRef("notes", "", space)); err != nil || strings.Join(keys, " ") != ".." {
		t.Errorf("query notes: %q, %v", keys, err)
	}
	parents, err := SubCollectionParents(root, def, "spaces/notes")
	if err != nil || len(parents) != 1 || parents[0].ID != "a/b" {
		t.Errorf("parents: %v, %v", parents, err)
	}
	if err := db.RunReadwriteTransaction(ctx, func(ctx context.Context, tx dal.ReadwriteTransaction) error {
		return tx.Delete(ctx, note)
	}); err != nil {
		t.Errorf("Delete: %v", err)
	}
}

// Verifies record-key-encoding#ac:invalid-names-and-collisions-reported and
// #ac:over-long-key-fails for the local driver.
func TestKeyEncoding_TildeV1Refusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	def := tildeSpacesNotes(root)
	db, err := NewLocalDBWithDef(root, def)
	if err != nil {
		t.Fatal(err)
	}
	if err := setTitle(ctx, db, dalrecord.NewKeyWithID("spaces", "ab"), "ab"); err != nil {
		t.Fatal(err)
	}
	for _, write := range []func(tx dal.ReadwriteTransaction) error{
		func(tx dal.ReadwriteTransaction) error {
			return tx.Set(ctx, dalrecord.NewRecordWithData(dalrecord.NewKeyWithID("spaces", "AB"), map[string]any{"title": "x"}))
		},
		func(tx dal.ReadwriteTransaction) error {
			return tx.Insert(ctx, dalrecord.NewRecordWithData(dalrecord.NewKeyWithID("spaces", "Ab"), map[string]any{"title": "x"}))
		},
	} {
		err := db.RunReadwriteTransaction(ctx, func(_ context.Context, tx dal.ReadwriteTransaction) error { return write(tx) })
		if !errors.Is(err, ErrNameDiffersInCase) || !strings.Contains(err.Error(), "ab.yaml exists;") {
			t.Errorf("case-only difference: %v", err)
		}
	}
	long := strings.Repeat("é", 43)
	if err := setTitle(ctx, db, dalrecord.NewKeyWithID("spaces", long), "x"); !errors.Is(err, ingitdb.ErrKeyNotStorable) {
		t.Errorf("over-long key Set: %v", err)
	}
	if err := db.RunReadwriteTransaction(ctx, func(ctx context.Context, tx dal.ReadwriteTransaction) error {
		return tx.Insert(ctx, dalrecord.NewRecordWithData(dalrecord.NewKeyWithID("spaces", long), map[string]any{"title": "x"}))
	}); !errors.Is(err, ingitdb.ErrKeyNotStorable) {
		t.Errorf("over-long key Insert: %v", err)
	}
	if err := get(db, dalrecord.NewRecordWithData(dalrecord.NewKeyWithID("spaces", long), map[string]any{})); !errors.Is(err, ingitdb.ErrKeyNotStorable) {
		t.Errorf("over-long key Get: %v", err)
	}
	if err := db.RunReadwriteTransaction(ctx, func(ctx context.Context, tx dal.ReadwriteTransaction) error {
		return tx.Delete(ctx, dalrecord.NewKeyWithID("spaces", long))
	}); !errors.Is(err, ingitdb.ErrKeyNotStorable) {
		t.Errorf("over-long key Delete: %v", err)
	}
	if err := setTitle(ctx, db, dalrecord.NewKeyWithParentAndID(dalrecord.NewKeyWithID("spaces", strings.Repeat("/", 90)), "notes", "n"), "x"); !errors.Is(err, ingitdb.ErrKeyNotStorable) {
		t.Errorf("over-long parent: %v", err)
	}
	if err := setTitle(ctx, db, dalrecord.NewKeyWithParentAndID(dalrecord.NewKeyWithParentAndID(dalrecord.NewKeyWithID("spaces", strings.Repeat("/", 90)), "notes", "n"), "notes", "n"), "x"); err == nil {
		t.Error("over-long ancestor accepted")
	}

	// A parent directory that differs only in case is refused too.
	if err := setTitle(ctx, db, dalrecord.NewKeyWithParentAndID(dalrecord.NewKeyWithID("spaces", "Pq"), "notes", "n"), "x"); err != nil {
		t.Fatal(err)
	}
	if err := setTitle(ctx, db, dalrecord.NewKeyWithParentAndID(dalrecord.NewKeyWithID("spaces", "pq"), "notes", "m"), "x"); !errors.Is(err, ErrNameDiffersInCase) || !strings.Contains(err.Error(), filepath.Join("$records", "Pq")+" exists") {
		t.Errorf("parent directory differing in case: %v", err)
	}
	// A hidden file is not a record: a query skips it.
	writeFiles(t, root, map[string]string{"spaces/$records/._ab.yaml": "title: n\n"})
	if keys, err := queryKeys(t, db, dal.NewRootCollectionRef("spaces", "")); err != nil || strings.Join(keys, " ") != "ab" {
		t.Errorf("hidden file: %q, %v", keys, err)
	}

	// A name the encoding never writes fails a query and a group read.
	writeFiles(t, root, map[string]string{"spaces/$records/x~zz/notes/$records/n.yaml": "title: n\n"})
	if _, err := SubCollectionParents(root, def, "spaces/notes"); !errors.Is(err, ingitdb.ErrInvalidKeyName) {
		t.Errorf("group read: %v", err)
	}
	writeFiles(t, root, map[string]string{"spaces/$records/a b.yaml": "title: n\n"})
	if _, err := queryKeys(t, db, dal.NewRootCollectionRef("spaces", "")); !errors.Is(err, ingitdb.ErrInvalidKeyName) {
		t.Errorf("query: %v", err)
	}
}

// A collection without a key encoding keeps keys as they are, and a parent
// collection without a record file names instances by the id.
func TestKeyEncoding_NoneUnchanged(t *testing.T) {
	t.Parallel()
	if name, err := parentDirName(&ingitdb.CollectionDef{}, "a b"); err != nil || name != "a b" {
		t.Errorf("no record file: %q, %v", name, err)
	}
	col := &ingitdb.CollectionDef{DirPath: "/db/c", RecordFile: &ingitdb.RecordFileDef{Name: "{key}.yaml"}}
	if p, err := keyedRecordPath(col, "a b"); err != nil || p != filepath.Join("/db/c", "$records", "a b.yaml") {
		t.Errorf("none: %q, %v", p, err)
	}
	if err := checkNoCaseCollision("/db", col, "a", "/db/c/$records/a.yaml"); err != nil {
		t.Errorf("none: %v", err)
	}
}

// A foreign key holds the real id of a record in a collection with a key
// encoding; the write check finds the record under its encoded name.
func TestKeyEncoding_ForeignKeyToEncodedCollection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	def := tildeSpacesNotes(root)
	def.Collections["refs"] = &ingitdb.CollectionDef{ID: "refs", DirPath: filepath.Join(root, "refs"),
		RecordFile:   &ingitdb.RecordFileDef{Name: "{key}.yaml", Format: ingitdb.RecordFormatYAML, RecordType: ingitdb.SingleRecord},
		Columns:      map[string]*ingitdb.ColumnDef{"space": {Type: ingitdb.ColumnTypeString, ForeignKey: "spaces"}},
		ColumnsOrder: []string{"space"}}
	db, err := NewLocalDBWithDef(root, def)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a/b", "CON"} {
		if err := setTitle(ctx, db, dalrecord.NewKeyWithID("spaces", id), id); err != nil {
			t.Fatal(err)
		}
		if err := db.RunReadwriteTransaction(ctx, func(ctx context.Context, tx dal.ReadwriteTransaction) error {
			return tx.Set(ctx, dalrecord.NewRecordWithData(dalrecord.NewKeyWithID("refs", "r"), map[string]any{"space": id}))
		}); err != nil {
			t.Errorf("foreign key %q: %v", id, err)
		}
	}
	if err := db.RunReadwriteTransaction(ctx, func(ctx context.Context, tx dal.ReadwriteTransaction) error {
		return tx.Set(ctx, dalrecord.NewRecordWithData(dalrecord.NewKeyWithID("refs", "r"), map[string]any{"space": "a~2Fb"}))
	}); err == nil {
		t.Error("the encoded name is not the id: want a missing foreign key")
	}
}

// A mislaid subcollection is reported with the parent's real id, and the
// directory names it found and expects.
func TestKeyEncoding_MislaidNamesTheRealID(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	db, err := NewLocalDBWithDef(root, tildeSpacesNotes(root))
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, map[string]string{"spaces/a~2Fb/notes/$records/n.yaml": "title: n\n"})
	_, err = queryKeys(t, db, dal.NewCollectionRef("notes", "", dalrecord.NewKeyWithID("spaces", "a/b")))
	if !errors.Is(err, ErrMislaidSubCollection) || !strings.Contains(err.Error(), `parent record "a/b"`) || !strings.Contains(err.Error(), filepath.Join("$records", "a~2Fb", "notes")) {
		t.Errorf("got %v", err)
	}
}

// Without a database root only the record file's own directory is checked.
func TestCheckNoCaseCollision_NoRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"Ab.yaml": ""})
	col := &ingitdb.CollectionDef{RecordFile: &ingitdb.RecordFileDef{Name: "{key}.yaml", KeyEncoding: ingitdb.KeyEncodingTildeV1}}
	if err := checkNoCaseCollision("", col, "ab", filepath.Join(dir, "ab.yaml")); !errors.Is(err, ErrNameDiffersInCase) {
		t.Errorf("got %v", err)
	}
}
