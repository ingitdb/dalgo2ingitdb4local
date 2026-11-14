package dalgo2fsingitdb

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dalrecord "github.com/dal-go/record"
	"github.com/ingitdb/ingitdb-go/ingitdb"
)

// SubCollectionParents returns the parent record key of every instance of the
// subcollection at schemaPath that holds data on disk, for a collection-group
// read (every `days` under every `ext` under every `spaces`). schemaPath is the
// collection ids from the root down, without record keys, the way
// `ingitdb validate` names a subcollection: "spaces/ext/days".
//
// An instance is found from its directory in this driver's layout
// (resolveScopedCollection: <parent dir>/<parentKey>/<subcollection>), so a
// parent with no record file of its own still counts, as Firestore allows.
// Hidden and `$`-prefixed entries (`$records`, `$views`; no record key starts
// with `$`) are never parents. A symlinked parent directory counts, as it
// does when the instance is read by path; exclude_regex applies to record
// files, not to parent directories, as in the readers. Each returned key's collection-and-id chain is the parent chain to
// pass to dal.NewCollectionRef(<leaf>, "", parent). Keys are sorted by path.
// A schemaPath of one root collection id has one instance, the root itself,
// returned as a nil key.
func SubCollectionParents(def *ingitdb.Definition, schemaPath string) ([]*dalrecord.Key, error) {
	if def == nil {
		return nil, fmt.Errorf("definition is required: use NewLocalDBWithDef")
	}
	segments := strings.Split(schemaPath, "/")
	colDef, ok := def.Collections[segments[0]]
	if !ok {
		return nil, fmt.Errorf("collection %q not found in definition", schemaPath)
	}
	type instance struct {
		dir    string
		parent *dalrecord.Key
	}
	current := []instance{{dir: colDef.DirPath}}
	collection := segments[0]
	for _, subID := range segments[1:] {
		sub, declared := colDef.SubCollections[subID]
		if !declared {
			return nil, fmt.Errorf("collection %q not found in definition", schemaPath)
		}
		var next []instance
		for _, inst := range current {
			for _, key := range parentKeysWithDir(inst.dir, subID) {
				next = append(next, instance{
					dir:    filepath.Join(inst.dir, key, subID),
					parent: dalrecord.NewKeyWithParentAndID(inst.parent, collection, key),
				})
			}
		}
		current, colDef, collection = next, sub, subID
	}
	parents := make([]*dalrecord.Key, 0, len(current))
	for _, inst := range current {
		parents = append(parents, inst.parent)
	}
	sort.Slice(parents, func(i, j int) bool { return parents[i].String() < parents[j].String() })
	return parents, nil
}

// parentKeysWithDir returns the names of the entries of dir that hold a
// directory named subID (following symlinks), skipping hidden and
// `$`-prefixed names. An unreadable or missing dir yields none.
func parentKeysWithDir(dir, subID string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var keys []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") {
			continue
		}
		if info, statErr := os.Stat(filepath.Join(dir, name, subID)); statErr != nil || !info.IsDir() {
			continue
		}
		keys = append(keys, name)
	}
	return keys
}
