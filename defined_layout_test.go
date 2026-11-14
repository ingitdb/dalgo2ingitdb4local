package dalgo2fsingitdb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dalrecord "github.com/dal-go/record"
)

// The definition puts the contacts of space "family" in spaces' records
// directory, next to its record file: spaces/$records/family/contacts/ under
// the default records_dir, spaces/family/contacts/ with records_dir ".".
func TestDefinedLayout_InstanceDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	key := dalrecord.NewKeyWithParentAndID(dalrecord.NewKeyWithID("spaces", "family"), "contacts", "c1")
	def := spacesWithContactsSubDef(root)
	got, err := resolveScopedCollection(def, key.Collection(), key.Parent())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "spaces", "$records", "family", "contacts"); got.DirPath != want {
		t.Errorf("default records_dir: DirPath = %q, want %q", got.DirPath, want)
	}
	dot := "."
	def.Collections["spaces"].RecordFile.RecordsDir = &dot
	got, err = resolveScopedCollection(def, key.Collection(), key.Parent())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "spaces", "family", "contacts"); got.DirPath != want {
		t.Errorf("records_dir .: DirPath = %q, want %q", got.DirPath, want)
	}
	// A parent with no record file has no records directory.
	def.Collections["spaces"].RecordFile = nil
	if got, err = resolveScopedCollection(def, key.Collection(), key.Parent()); err != nil || got.DirPath != filepath.Join(root, "spaces", "family", "contacts") {
		t.Errorf("no record file: %v, %v", got, err)
	}
}

// Files at spaces/<key>/contacts/ while spaces keeps its records in $records
// (where earlier versions wrote them) make that instance fail with the details.
func TestDefinedLayout_MislaidInstanceIsAnError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mislaid := filepath.Join(root, "spaces", "family", "contacts", "$records")
	if err := os.MkdirAll(mislaid, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"c1.yaml", ".hidden"} {
		if err := os.WriteFile(filepath.Join(mislaid, name), []byte("name: A\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	def := spacesWithContactsSubDef(root)
	key := dalrecord.NewKeyWithParentAndID(dalrecord.NewKeyWithID("spaces", "family"), "contacts", "c1")
	_, err := resolveScopedCollection(def, key.Collection(), key.Parent())
	if !errors.Is(err, ErrMislaidSubCollection) {
		t.Fatalf("expected ErrMislaidSubCollection, got %v", err)
	}
	for _, want := range []string{
		`subcollection spaces/contacts of parent record "family"`,
		filepath.Join(root, "spaces", "family", "contacts") + " holds 1 file(s)",
		"but the definition puts them at " + filepath.Join(root, "spaces", "$records", "family", "contacts"),
		`record_file.records_dir "$records"`, `records_dir: "."`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	other := dalrecord.NewKeyWithParentAndID(dalrecord.NewKeyWithID("spaces", "work"), "contacts", "c1")
	if _, err := resolveScopedCollection(def, other.Collection(), other.Parent()); err != nil {
		t.Errorf("work: %v", err)
	}
}
