package dalgo2fsingitdb

import (
	"context"
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
	spacesRF.ExcludeRegex = "^skip-"
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

// Every instance of spaces/ext/days is found, including instances below
// parents with no record file (s2 has none, nor does any ext), and nothing
// else is: hidden, `$`-prefixed and excluded entries, a plain file, a parent
// whose subcollection entry is a file, and a parent without that subcollection.
// Each returned parent reads its own records through the driver.
func TestSubCollectionParents_FindsEveryInstance(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"spaces/$records/s1.yaml":                             "title: S1\n",
		"spaces/s1/ext/$records/calendarius.yaml":             "title: C\n",
		"spaces/s1/ext/calendarius/days/$records/d1.yaml":     "title: s1 c d1\n",
		"spaces/s1/ext/commitius/days/$records/d1.yaml":       "title: s1 m d1\n",
		"spaces/s2/ext/calendarius/days/$records/d2.yaml":     "title: s2 c d2\n",
		"spaces/s3/ext/calendarius/other/$records/x.yaml":     "title: no days\n",
		"spaces/s4/ext/calendarius/days":                      "a file, not a directory",
		"spaces/skip-me/ext/calendarius/days/$records/x.yaml": "title: excluded\n",
		"spaces/.hidden/ext/calendarius/days/$records/x.yaml": "title: hidden\n",
		"spaces/$views/ext/calendarius/days/$records/x.yaml":  "title: views\n",
		"spaces/file.yaml":                                    "title: plain file\n",
	})
	def := collectionGroupDef(root)
	parents, err := SubCollectionParents(def, "spaces/ext/days")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range parents {
		got = append(got, p.String())
	}
	want := "spaces/s1/ext/calendarius spaces/s1/ext/commitius spaces/s2/ext/calendarius"
	if strings.Join(got, " ") != want {
		t.Fatalf("parents = %v, want %s", got, want)
	}

	db, err := NewLocalDBWithDef(root, def)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"d1", "d1", "d2"}
	wantTitles := []string{"s1 c d1", "s1 m d1", "s2 c d2"}
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

func TestSubCollectionParents_RootAndErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	def := collectionGroupDef(root)
	parents, err := SubCollectionParents(def, "spaces")
	if err != nil || len(parents) != 1 || parents[0] != nil {
		t.Fatalf("a root collection is its only instance (nil parent), got %v, %v", parents, err)
	}
	parents, err = SubCollectionParents(def, "spaces/ext/days")
	if err != nil || len(parents) != 0 {
		t.Fatalf("missing directories give no instances, got %v, %v", parents, err)
	}
	for _, path := range []string{"nope", "spaces/nope", "spaces/ext/nope", "spaces//days"} {
		if _, err := SubCollectionParents(def, path); err == nil || !strings.Contains(err.Error(), "not found in definition") {
			t.Errorf("%s: expected not found, got %v", path, err)
		}
	}
	if _, err := SubCollectionParents(nil, "spaces"); err == nil {
		t.Error("nil definition must fail")
	}
}
