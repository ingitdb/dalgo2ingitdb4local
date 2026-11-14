package dalgo2fsingitdb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dalrecord "github.com/dal-go/record"
	"github.com/ingitdb/ingitdb-go/ingitdb"
)

// spacesContactsNotes is spaces > contacts > notes, every level with a {key}
// record name, so every level keeps its records in $records unless
// spacesRecordsDir says otherwise.
func spacesContactsNotes(root string, spacesRecordsDir *string) *ingitdb.Definition {
	rf := func(dir *string) *ingitdb.RecordFileDef {
		return &ingitdb.RecordFileDef{Name: "{key}.yaml", Format: ingitdb.RecordFormatYAML, RecordType: ingitdb.SingleRecord, RecordsDir: dir}
	}
	notes := &ingitdb.CollectionDef{ID: "notes", RecordFile: rf(nil)}
	contacts := &ingitdb.CollectionDef{ID: "contacts", RecordFile: rf(nil), SubCollections: map[string]*ingitdb.CollectionDef{"notes": notes}}
	spaces := &ingitdb.CollectionDef{ID: "spaces", DirPath: filepath.Join(root, "spaces"), RecordFile: rf(spacesRecordsDir),
		SubCollections: map[string]*ingitdb.CollectionDef{"contacts": contacts, "items": notes}}
	return &ingitdb.Definition{Collections: map[string]*ingitdb.CollectionDef{"spaces": spaces}}
}

func writeFileAt(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contactKey(space, contact string) *dalrecord.Key {
	return dalrecord.NewKeyWithParentAndID(dalrecord.NewKeyWithID("spaces", space), "contacts", contact)
}

// Mislaid data one level below a correctly placed ancestor is reported.
func TestDefinedLayout_MislaidAtDepthTwo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFileAt(t, filepath.Join(root, "spaces", "$records", "family", "contacts", "$records", "c1.yaml"))
	writeFileAt(t, filepath.Join(root, "spaces", "$records", "family", "contacts", "c1", "notes", "$records", "n1.yaml"))
	_, err := resolveScopedCollection(spacesContactsNotes(root, nil), "notes", contactKey("family", "c1"))
	if !errors.Is(err, ErrMislaidSubCollection) {
		t.Fatalf("expected ErrMislaidSubCollection, got %v", err)
	}
	for _, want := range []string{
		"subcollection contacts/notes", `parent record "c1"`,
		filepath.Join("contacts", "c1", "notes") + " holds 1 file(s)",
		filepath.Join("contacts", "$records", "c1", "notes") + " (contacts keeps",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// The layout this adapter wrote before, at every level, is reported at the
// first level that differs, also when only the deeper instance has files;
// the defined layout resolves.
func TestDefinedLayout_MislaidAncestorOfDeeperInstance(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFileAt(t, filepath.Join(root, "spaces", "family", "contacts", "c1", "notes", "n1.yaml"))
	_, err := resolveScopedCollection(spacesContactsNotes(root, nil), "notes", contactKey("family", "c1"))
	if !errors.Is(err, ErrMislaidSubCollection) || !strings.Contains(err.Error(), "subcollection spaces/contacts") {
		t.Fatalf("expected the spaces/contacts instance to be reported, got %v", err)
	}
	ok := t.TempDir()
	col, err := resolveScopedCollection(spacesContactsNotes(ok, nil), "notes", contactKey("family", "c1"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(ok, "spaces", "$records", "family", "contacts", "$records", "c1", "notes"); col.DirPath != want {
		t.Errorf("DirPath = %s, want %s", col.DirPath, want)
	}
}

// With records_dir data/items, a parent whose key is "data" does not take
// the parent's own records directory for its mislaid subcollection.
func TestDefinedLayout_NestedRecordsDirNotMislaid(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	nested := "data/items"
	writeFileAt(t, filepath.Join(root, "spaces", "data", "items", "s1.yaml"))
	col, err := resolveScopedCollection(spacesContactsNotes(root, &nested), "items", dalrecord.NewKeyWithID("spaces", "data"))
	if err != nil {
		t.Fatalf("false positive: %v", err)
	}
	if want := filepath.Join(root, "spaces", "data", "items", "data", "items"); col.DirPath != want {
		t.Errorf("DirPath = %s, want %s", col.DirPath, want)
	}
}

// A parent ID that climbs out of the collection directory is refused before
// anything is read there, with the default records directory and with ".".
func TestDefinedLayout_ParentKeyEscapes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFileAt(t, filepath.Join(root, "contacts", "secret.yaml")) // outside spaces/
	dot := "."
	for name, dir := range map[string]*string{"default": nil, "dot": &dot} {
		_, err := resolveScopedCollection(spacesContactsNotes(root, dir), "contacts", dalrecord.NewKeyWithID("spaces", ".."))
		if !errors.Is(err, ErrParentKeyEscapes) {
			t.Errorf("%s: expected ErrParentKeyEscapes, got %v", name, err)
		}
	}
	if !within("/a", "/a") || !within("/a", "/a/b") || within("/a", "/b") || within("/a", "/a/../..x") {
		t.Error("within misjudges a path")
	}
}
