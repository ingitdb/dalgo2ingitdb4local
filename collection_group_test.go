package dalgo2fsingitdb

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

func collectionGroupDef(root string) *ingitdb.Definition {
	rf := func() *ingitdb.RecordFileDef {
		return &ingitdb.RecordFileDef{Name: "{key}.yaml", Format: ingitdb.RecordFormatYAML, RecordType: ingitdb.SingleRecord}
	}
	cols := map[string]*ingitdb.ColumnDef{"title": {Type: ingitdb.ColumnTypeString}}
	days := &ingitdb.CollectionDef{ID: "days", DirPath: "ignored", RecordFile: rf(), Columns: cols, ColumnsOrder: []string{"title"}}
	ext := &ingitdb.CollectionDef{ID: "ext", DirPath: "ignored", RecordFile: rf(), Columns: cols, ColumnsOrder: []string{"title"},
		SubCollections: map[string]*ingitdb.CollectionDef{"days": days}}
	spacesRF := rf()
	spacesRF.ExcludeRegex = "^old"
	spaces := &ingitdb.CollectionDef{ID: "spaces", DirPath: filepath.Join(root, "spaces"), RecordFile: spacesRF, Columns: cols, ColumnsOrder: []string{"title"},
		SubCollections: map[string]*ingitdb.CollectionDef{"ext": ext}}
	return &ingitdb.Definition{Collections: map[string]*ingitdb.CollectionDef{"spaces": spaces}}
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Every instance of spaces/ext/days is found where the definition puts it
// (each parent's records directory, $records by default), with the rule
// validate uses: parents with no record file (s2 has none, nor does any ext),
// a Firestore-style "$x" id, and a symlinked parent inside the database count.
// Hidden entries, inGitDB's own $views, names matched by exclude_regex, a
// plain file, a parent whose subcollection entry is a file, and a parent
// without that subcollection do not. Keys come in tree order ("s1" before
// "s1-b", although "spaces/s1-b" sorts before "spaces/s1/" as text).
// Each returned parent reads its own records through the driver.
func TestSubCollectionParents_FindsEveryInstance(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	r := "spaces/$records/"
	writeFiles(t, root, map[string]string{
		r + "s1.yaml":                                                  "title: S1\n",
		r + "s1/ext/$records/calendarius.yaml":                         "title: C\n",
		r + "s1/ext/$records/calendarius/days/$records/d1.yaml":        "title: s1 c d1\n",
		r + "s1/ext/$records/commitius/days/$records/d1.yaml":          "title: s1 m d1\n",
		r + "s1-b/ext/$records/calendarius/days/$records/d7.yaml":      "title: s1-b c d7\n",
		r + "s2/ext/$records/calendarius/days/$records/d2.yaml":        "title: s2 c d2\n",
		r + "$x/ext/$records/calendarius/days/$records/d8.yaml":        "title: $x c d8\n",
		r + "s3/ext/$records/calendarius/other/$records/x.yaml":        "title: no days\n",
		r + "s4/ext/$records/calendarius/days":                         "a file, not a directory",
		r + "old-s5/ext/$records/calendarius/days/$records/d5.yaml":    "title: excluded\n",
		"linked-target/ext/$records/calendarius/days/$records/d6.yaml": "title: s6 c d6\n",
		r + ".hidden/ext/$records/calendarius/days/$records/x.yaml":    "title: hidden\n",
		r + "$views/ext/$records/calendarius/days/$records/x.yaml":     "title: views\n",
		r + "file.yaml": "title: plain file\n",
	})
	if err := os.Symlink(filepath.Join(root, "linked-target"), filepath.Join(root, "spaces", "$records", "s6")); err != nil {
		t.Fatal(err)
	}
	def := collectionGroupDef(root)
	parents, err := SubCollectionParents(root, def, "spaces/ext/days")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range parents {
		got = append(got, p.String())
	}
	want := "spaces/%24x/ext/calendarius spaces/s1/ext/calendarius spaces/s1/ext/commitius spaces/s1-b/ext/calendarius spaces/s2/ext/calendarius spaces/s6/ext/calendarius"
	if strings.Join(got, " ") != want {
		t.Fatalf("parents = %v, want %s", got, want)
	}

	db, err := NewLocalDBWithDef(root, def)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"d8", "d1", "d1", "d7", "d2", "d6"}
	wantTitles := []string{"$x c d8", "s1 c d1", "s1 m d1", "s1-b c d7", "s2 c d2", "s6 c d6"}
	err = db.RunReadonlyTransaction(context.Background(), func(ctx context.Context, tx dal.ReadTransaction) error {
		for i, parent := range parents {
			data := map[string]any{}
			rec := dalrecord.NewRecordWithData(dalrecord.NewKeyWithParentAndID(parent, "days", ids[i]), data)
			if getErr := tx.Get(ctx, rec); getErr != nil {
				return getErr
			}
			if data["title"] != wantTitles[i] {
				t.Errorf("%s: title = %v, want %s", rec.Key(), data["title"], wantTitles[i])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A parent symlinked to a directory outside the database fails the read
// instead of being followed or silently dropped.
func TestSubCollectionParents_OutsideLinkFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	writeFiles(t, outside, map[string]string{"ext/$records/calendarius/days/$records/d.yaml": "title: X\n"})
	if err := os.MkdirAll(filepath.Join(root, "spaces", "$records"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "spaces", "$records", "evil")); err != nil {
		t.Fatal(err)
	}
	_, err := SubCollectionParents(root, collectionGroupDef(root), "spaces/ext/days")
	if !errors.Is(err, ErrParentOutsideDatabase) || !strings.Contains(err.Error(), filepath.Join("spaces", "$records", "evil", "ext")) {
		t.Fatalf("expected ErrParentOutsideDatabase naming the link, got %v", err)
	}
}

// Data where the definition does not put it fails the group read with the
// details, at the first level and below a defined instance.
func TestSubCollectionParents_MislaidFails(t *testing.T) {
	t.Parallel()
	for name, file := range map[string]string{
		"top":    "spaces/s9/ext/$records/calendarius/days/$records/d.yaml",
		"nested": "spaces/$records/s1/ext/calendarius/days/$records/d.yaml",
	} {
		root := t.TempDir()
		writeFiles(t, root, map[string]string{file: "title: X\n"})
		_, err := SubCollectionParents(root, collectionGroupDef(root), "spaces/ext/days")
		if !errors.Is(err, ErrMislaidSubCollection) || !strings.Contains(err.Error(), "holds 1 file(s), but the definition puts them at") {
			t.Errorf("%s: expected ErrMislaidSubCollection, got %v", name, err)
		}
	}
	// With records_dir "." there is one layout and nothing is mislaid.
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"spaces/s9/ext/$records/calendarius/days/$records/d.yaml": "title: X\n"})
	def := collectionGroupDef(root)
	dot := "."
	def.Collections["spaces"].RecordFile.RecordsDir = &dot
	parents, err := SubCollectionParents(root, def, "spaces/ext/days")
	if err != nil || len(parents) != 1 || parents[0].String() != "spaces/s9/ext/calendarius" {
		t.Fatalf("records_dir .: %v, %v", parents, err)
	}
	// With records_dir data/ext the parent's own records directory, whose
	// first segment "data" holds "ext", is not taken for mislaid data.
	root = t.TempDir()
	writeFiles(t, root, map[string]string{
		"spaces/data/ext/s1.yaml":                                "title: S1\n",
		"spaces/data/ext/s1/ext/$records/c/days/$records/d.yaml": "title: X\n",
	})
	def = collectionGroupDef(root)
	nested := "data/ext"
	def.Collections["spaces"].RecordFile.RecordsDir = &nested
	parents, err = SubCollectionParents(root, def, "spaces/ext/days")
	if err != nil || len(parents) != 1 || parents[0].String() != "spaces/s1/ext/c" {
		t.Fatalf("records_dir data/ext: %v, %v", parents, err)
	}
}

func TestSubCollectionParents_RootAndErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	def := collectionGroupDef(root)
	parents, err := SubCollectionParents(root, def, "spaces")
	if err != nil || len(parents) != 1 || parents[0] != nil {
		t.Fatalf("a root collection is its only instance (nil parent), got %v, %v", parents, err)
	}
	parents, err = SubCollectionParents(root, def, "spaces/ext/days")
	if err != nil || len(parents) != 0 {
		t.Fatalf("missing directories give no instances, got %v, %v", parents, err)
	}
	for _, path := range []string{"nope", "spaces/nope", "spaces/ext/nope", "spaces//days"} {
		if _, err := SubCollectionParents(root, def, path); err == nil || !strings.Contains(err.Error(), "not found in definition") {
			t.Errorf("%s: expected not found, got %v", path, err)
		}
	}
	if _, err := SubCollectionParents(root, nil, "spaces"); err == nil {
		t.Error("nil definition must fail")
	}
}
