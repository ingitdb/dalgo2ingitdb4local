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

// caseVariant checks only path's own name when path is not below root, and
// nothing for root itself; the error names the other record only when its
// name is one the collection's encoding writes.
func TestCaseVariant_Edges(t *testing.T) {
	root := t.TempDir()
	withFoldFS(t, root)
	writeFiles(t, root, map[string]string{"Up/x.yaml": "", "people/$records/a~2fb.yaml": "title: x\n"})
	if m, err := caseVariant(nil, "", filepath.Join(root, "up", "x.yaml")); err != nil || m != nil {
		t.Errorf("no root, folder differs in case: %+v %v", m, err)
	}
	if m, err := caseVariant(nil, "", filepath.Join(root, "Up", "X.yaml")); err != nil || m == nil || m.onDisk != filepath.Join(root, "Up", "x.yaml") {
		t.Errorf("no root, file differs in case: %+v %v", m, err)
	}
	if m, err := caseVariant(nil, root, root); err != nil || m != nil {
		t.Errorf("root itself: %+v %v", m, err)
	}
	// A name that is on disk as it is given is never another name.
	writeFiles(t, root, map[string]string{"42/x": ""})
	if m, err := caseVariant(nil, root, filepath.Join(root, "42", "x")); err != nil || m != nil {
		t.Errorf("no letters: %+v %v", m, err)
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
	if got := shown("people/caf\u00e9.yaml") + " " + shown("people/Bob.yaml"); got != `"people/caf\u00e9.yaml" people/Bob.yaml` {
		t.Errorf("shown: %s", got)
	}
}

// A listing read before another process renamed a file shows neither name:
// the error then says the file system treats the names as the same, and
// still refuses.
func TestCaseVariant_StaleListing(t *testing.T) {
	root := t.TempDir()
	withFoldFS(t, root)
	writeFiles(t, root, map[string]string{"d/other": ""})
	cache := newDirCache()
	if _, err := cache.list(filepath.Join(root, "d")); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, map[string]string{"d/Bob.yaml": ""})
	m, err := caseVariant(cache, root, filepath.Join(root, "d", "bob.yaml"))
	if err != nil || m == nil || m.onDisk != "" || m.shownOnDisk() != "another name this file system treats as the same" {
		t.Errorf("stale listing: %+v %v", m, err)
	}
	col := &ingitdb.CollectionDef{DirPath: filepath.Join(root, "d"), RecordFile: &ingitdb.RecordFileDef{Name: "{key}.yaml"}}
	if got := otherRecord(col, keyNamesOf(&ingitdb.Definition{}, col, nil), m); got != "" {
		t.Errorf("otherRecord of an unknown name: %q", got)
	}
}

// A nil dirCache lists every time and records nothing, written or removed; absPath makes a
// relative path absolute.
func TestDirCache_Nil(t *testing.T) {
	var cache *dirCache
	cache.wrote("x")
	cache.removed("x")
	dir := t.TempDir()
	if names, err := cache.list(dir); err != nil || len(names) != 0 {
		t.Errorf("nil list: %v %v", names, err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := absPath("x"); got != filepath.Join(wd, "x") {
		t.Errorf("absPath: %q", got)
	}
}
