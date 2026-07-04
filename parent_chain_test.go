package dalgo2fsingitdb

// parent_chain_test.go is the regression suite for the per-parent ("per-space")
// scoping fix. Before the fix the adapter mapped a dal.Key to an on-disk path
// using only the leaf collection + record id, dropping the parent chain, so two
// keys sharing a leaf (contacts/c1) under different parents (spaces/family vs
// spaces/work) collided on one file and clobbered each other. After the fix each
// nested record is scoped under its parent record's directory
// (spaces/<spaceID>/contacts/...), matching the dalgo2ingitdb and
// dalgo2ingitdb4github siblings.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dal-go/dalgo/dal"

	"github.com/ingitdb/ingitdb-go/ingitdb"
)

// spacesWithContactsSubDef returns a definition with a top-level "spaces"
// collection and a "contacts" subcollection nested under it, rooted at root.
func spacesWithContactsSubDef(root string) *ingitdb.Definition {
	contacts := &ingitdb.CollectionDef{
		ID:      "contacts",
		DirPath: filepath.Join(root, "spaces", "contacts"), // schema-declared; scoping overrides per parent record
		RecordFile: &ingitdb.RecordFileDef{
			Name:       "{key}.yaml",
			Format:     ingitdb.RecordFormatYAML,
			RecordType: ingitdb.SingleRecord,
		},
		Columns:      map[string]*ingitdb.ColumnDef{"name": {Type: ingitdb.ColumnTypeString}},
		ColumnsOrder: []string{"name"},
	}
	spaces := &ingitdb.CollectionDef{
		ID:      "spaces",
		DirPath: filepath.Join(root, "spaces"),
		RecordFile: &ingitdb.RecordFileDef{
			Name:       "{key}.yaml",
			Format:     ingitdb.RecordFormatYAML,
			RecordType: ingitdb.SingleRecord,
		},
		Columns:        map[string]*ingitdb.ColumnDef{"name": {Type: ingitdb.ColumnTypeString}},
		ColumnsOrder:   []string{"name"},
		SubCollections: map[string]*ingitdb.CollectionDef{"contacts": contacts},
	}
	return &ingitdb.Definition{
		Collections: map[string]*ingitdb.CollectionDef{"spaces": spaces},
	}
}

// TestResolveScopedCollection_DistinctPathsForSameLeaf proves the core fix at
// the path-mapping layer: two keys sharing a leaf collection + record id but
// differing in their parent chain resolve to DISTINCT on-disk paths. A top-level
// key still resolves to the flat schema-declared path (unchanged behaviour).
func TestResolveScopedCollection_DistinctPathsForSameLeaf(t *testing.T) {
	t.Parallel()
	root := "/repo"
	def := spacesWithContactsSubDef(root)

	familyKey := dal.NewKeyWithParentAndID(dal.NewKeyWithID("spaces", "family"), "contacts", "c1")
	workKey := dal.NewKeyWithParentAndID(dal.NewKeyWithID("spaces", "work"), "contacts", "c1")

	familyDef, err := resolveScopedCollection(def, familyKey.Collection(), familyKey.Parent())
	if err != nil {
		t.Fatalf("resolveScopedCollection(family): %v", err)
	}
	workDef, err := resolveScopedCollection(def, workKey.Collection(), workKey.Parent())
	if err != nil {
		t.Fatalf("resolveScopedCollection(work): %v", err)
	}

	familyPath := resolveRecordPath(familyDef, "c1")
	workPath := resolveRecordPath(workDef, "c1")

	wantFamily := filepath.Join(root, "spaces", "family", "contacts", "$records", "c1.yaml")
	wantWork := filepath.Join(root, "spaces", "work", "contacts", "$records", "c1.yaml")
	if familyPath != wantFamily {
		t.Errorf("family path = %q, want %q", familyPath, wantFamily)
	}
	if workPath != wantWork {
		t.Errorf("work path = %q, want %q", workPath, wantWork)
	}
	if familyPath == workPath {
		t.Fatalf("regression: same-leaf keys under different parents collide on %q", familyPath)
	}

	// Unknown subcollection under a known parent → clear error.
	if _, err := resolveScopedCollection(def, "widgets", dal.NewKeyWithID("spaces", "family")); err == nil ||
		!strings.Contains(err.Error(), "not found in definition") {
		t.Errorf("unknown subcollection: got %v, want 'not found in definition'", err)
	}
}

// TestResolveScopedCollection_DeepNesting exercises the intermediate-ancestor
// loop: a grandchild collection (spaces/{s}/projects/{p}/tasks/{t}) interleaves
// every parent-record id and subcollection name into the on-disk path.
func TestResolveScopedCollection_DeepNesting(t *testing.T) {
	t.Parallel()
	root := "/repo"
	rf := &ingitdb.RecordFileDef{Name: "{key}.yaml", Format: ingitdb.RecordFormatYAML, RecordType: ingitdb.SingleRecord}
	tasks := &ingitdb.CollectionDef{ID: "tasks", DirPath: "ignored", RecordFile: rf}
	projects := &ingitdb.CollectionDef{ID: "projects", DirPath: "ignored", RecordFile: rf,
		SubCollections: map[string]*ingitdb.CollectionDef{"tasks": tasks}}
	spaces := &ingitdb.CollectionDef{ID: "spaces", DirPath: filepath.Join(root, "spaces"), RecordFile: rf,
		SubCollections: map[string]*ingitdb.CollectionDef{"projects": projects}}
	def := &ingitdb.Definition{Collections: map[string]*ingitdb.CollectionDef{"spaces": spaces}}

	// key: spaces/s1/projects/p1/tasks/t1
	parent := dal.NewKeyWithParentAndID(dal.NewKeyWithParentAndID(dal.NewKeyWithID("spaces", "s1"), "projects", "p1"), "tasks", "t1").Parent()
	colDef, err := resolveScopedCollection(def, "tasks", parent)
	if err != nil {
		t.Fatalf("resolveScopedCollection: %v", err)
	}
	want := filepath.Join(root, "spaces", "s1", "projects", "p1", "tasks", "$records", "t1.yaml")
	if got := resolveRecordPath(colDef, "t1"); got != want {
		t.Errorf("deep nested path = %q, want %q", got, want)
	}

	// Unknown intermediate subcollection under a known parent → error.
	badParent := dal.NewKeyWithParentAndID(dal.NewKeyWithID("spaces", "s1"), "unknown", "x")
	if _, err := resolveScopedCollection(def, "tasks", badParent); err == nil ||
		!strings.Contains(err.Error(), "not found in definition") {
		t.Errorf("unknown intermediate: got %v, want 'not found in definition'", err)
	}
}

// TestLocalDB_NestedKeys_ScopedByParentRecord is the end-to-end filesystem
// round-trip: write a contact "c1" under spaces/family and a different contact
// "c1" under spaces/work, then read both back. Before the fix the second write
// clobbered the first; after it, each is physically scoped under its parent
// space (spaces/<spaceID>/contacts/$records/c1.yaml) and round-trips
// independently.
func TestLocalDB_NestedKeys_ScopedByParentRecord(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	db := openTestDB(t, root, spacesWithContactsSubDef(root))
	ctx := context.Background()

	familyContact := dal.NewKeyWithParentAndID(dal.NewKeyWithID("spaces", "family"), "contacts", "c1")
	workContact := dal.NewKeyWithParentAndID(dal.NewKeyWithID("spaces", "work"), "contacts", "c1")

	if err := db.RunReadwriteTransaction(ctx, func(ctx context.Context, tx dal.ReadwriteTransaction) error {
		if setErr := tx.Set(ctx, dal.NewRecordWithData(familyContact, map[string]any{"name": "Alice"})); setErr != nil {
			return setErr
		}
		return tx.Set(ctx, dal.NewRecordWithData(workContact, map[string]any{"name": "Bob"}))
	}); err != nil {
		t.Fatalf("write nested contacts: %v", err)
	}

	// Physical layout: nested under the parent space, no collision.
	familyPath := filepath.Join(root, "spaces", "family", "contacts", "$records", "c1.yaml")
	workPath := filepath.Join(root, "spaces", "work", "contacts", "$records", "c1.yaml")
	if _, err := os.Stat(familyPath); err != nil {
		t.Errorf("expected family contact file at %s: %v", familyPath, err)
	}
	if _, err := os.Stat(workPath); err != nil {
		t.Errorf("expected work contact file at %s: %v", workPath, err)
	}

	// Read each back; confirm they did NOT clobber each other.
	famRec := dal.NewRecordWithData(familyContact, map[string]any{})
	workRec := dal.NewRecordWithData(workContact, map[string]any{})
	if err := db.RunReadonlyTransaction(ctx, func(ctx context.Context, tx dal.ReadTransaction) error {
		if getErr := tx.Get(ctx, famRec); getErr != nil {
			return getErr
		}
		return tx.Get(ctx, workRec)
	}); err != nil {
		t.Fatalf("read nested contacts: %v", err)
	}
	if got := famRec.Data().(map[string]any)["name"]; got != "Alice" {
		t.Errorf("family contact name: got %v, want Alice (records collided across parents?)", got)
	}
	if got := workRec.Data().(map[string]any)["name"]; got != "Bob" {
		t.Errorf("work contact name: got %v, want Bob", got)
	}
}
