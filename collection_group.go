package dalgo2fsingitdb

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	dalrecord "github.com/dal-go/record"
	"github.com/ingitdb/ingitdb-go/ingitdb"
)

// ErrParentOutsideDatabase reports a parent directory that resolves outside
// the database through a symbolic link; a collection-group read does not
// follow it.
var ErrParentOutsideDatabase = errors.New("parent directory resolves outside the database through a symbolic link")

// SubCollectionParents returns the parent record key of every instance of the
// subcollection at schemaPath that holds data on disk, for a collection-group
// read (every `days` under every `ext` under every `spaces`). root is the
// database root. schemaPath is the collection ids from the root down, without
// record keys, the way `ingitdb validate` names a subcollection:
// "spaces/ext/days".
//
// Instances are found where the definition puts them (resolveScopedCollection:
// <parent dir>/<parent records_dir>/<parentKey>/<subcollection>), with the
// rule `ingitdb validate` uses, ingitdb.ScanParentDirs, so a group read never
// returns a record validation did not consider: a parent with no record file
// of its own still counts, as Firestore allows; hidden entries, inGitDB's own
// directories and names matched by the parent's exclude_regex do not; other
// `$`-prefixed names do. A parent reached through a symbolic link that leads
// outside the database fails the read with ErrParentOutsideDatabase, and data
// at <parent dir>/<parentKey>/<subcollection> while the parent's records_dir
// is not "." fails it with ErrMislaidSubCollection, rather than returning
// fewer instances than exist.
//
// Each returned key's collection-and-id chain is the parent chain to pass to
// dal.NewCollectionRef(<leaf>, "", parent). Keys come in tree order: by
// parent key at each level, outermost first. A schemaPath of one root
// collection id has one instance, the root itself, returned as a nil key.
func SubCollectionParents(root string, def *ingitdb.Definition, schemaPath string) ([]*dalrecord.Key, error) {
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
		base := recordsBasePath(colDef)
		var next []instance
		for _, inst := range current {
			if base != "" {
				if err := mislaidInstances(root, inst.dir, colDef, subID, base); err != nil {
					return nil, err
				}
			}
			parentsDir := filepath.Join(inst.dir, base)
			keys, outside := ingitdb.ScanParentDirs(root, parentsDir, colDef.RecordFile).KeysWith(subID)
			if len(outside) > 0 {
				return nil, fmt.Errorf("%w: %s (subcollection %s); it is not read",
					ErrParentOutsideDatabase, filepath.Join(parentsDir, outside[0], subID), schemaPath)
			}
			for _, name := range keys {
				key, err := name, error(nil)
				if colDef.RecordFile != nil {
					key, err = colDef.RecordFile.DecodeKey(name)
				}
				if err != nil {
					return nil, fmt.Errorf("parent directory %s (subcollection %s): %w", filepath.Join(parentsDir, name), schemaPath, err)
				}
				next = append(next, instance{
					dir:    filepath.Join(parentsDir, name, subID),
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
	return parents, nil
}

// mislaidInstances fails with ErrMislaidSubCollection when an instance of
// subID holds files at <dir>/<key>/<subID>, where the definition does not put
// it because the parent keeps its records in base.
func mislaidInstances(root, dir string, colDef *ingitdb.CollectionDef, subID, base string) error {
	keys, _ := ingitdb.ScanParentDirs(root, dir, colDef.RecordFile).KeysWith(subID)
	for _, key := range keys {
		found := filepath.Join(dir, key, subID)
		if files := countFiles(found); files > 0 {
			return fmt.Errorf("%w: subcollection %s/%s of parent record %q: %s holds %d file(s), but the definition puts them at %s (%s keeps its records in record_file.records_dir %q). Move them there, or set record_file.records_dir: \".\" on %s and move its own records out of its records directory",
				ErrMislaidSubCollection, colDef.ID, subID, key, found, files, filepath.Join(dir, base, key, subID), colDef.ID, base, colDef.ID)
		}
	}
	return nil
}
