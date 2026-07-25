package dalgo2fsingitdb

import (
	"context"
	"fmt"
	"maps"

	"github.com/dal-go/dalgo/dal"
	"github.com/dal-go/dalgo/recordset"
	dalrecord "github.com/dal-go/record"
	"github.com/ingitdb/dalgo2ingitdb"
	"github.com/ingitdb/ingitdb-go/ingitdb"
)

var _ dal.ReadTransaction = (*readonlyTx)(nil)

type readonlyTx struct {
	db localDB
}

func (r readonlyTx) Options() dal.TransactionOptions {
	//TODO implement me
	panic("implement me")
}

func (r readonlyTx) Get(ctx context.Context, record dalrecord.Record) error {
	_ = ctx
	colDef, recordKey, err := r.resolveCollection(record.Key())
	if err != nil {
		return err
	}
	path := resolveRecordPath(colDef, recordKey)
	switch colDef.RecordFile.RecordType {
	case ingitdb.SingleRecord:
		var (
			data  map[string]any
			found bool
			err   error
		)
		if colDef.RecordFile.Format == ingitdb.RecordFormatMarkdown {
			data, found, err = readMarkdownRecord(path, colDef)
		} else {
			data, found, err = readRecordFromFile(path, colDef.RecordFile.Format)
		}
		if err != nil {
			return err
		}
		if !found {
			record.SetError(dalrecord.ErrRecordNotFound)
			return nil
		}
		record.SetError(nil)
		target := record.Data().(map[string]any)
		maps.Copy(target, data)
	case ingitdb.MapOfRecords:
		allRecords, found, err := readMapOfRecordsFile(path, colDef.RecordFile.Format)
		if err != nil {
			return err
		}
		if !found {
			record.SetError(dalrecord.ErrRecordNotFound)
			return nil
		}
		recordData, exists := allRecords[recordKey]
		if !exists {
			record.SetError(dalrecord.ErrRecordNotFound)
			return nil
		}
		record.SetError(nil)
		target := record.Data().(map[string]any)
		maps.Copy(target, ingitdb.ApplyLocaleToRead(recordData, colDef.Columns))
	default:
		return fmt.Errorf("not yet implemented for record type %q", colDef.RecordFile.RecordType)
	}
	return nil
}

// resolveCollection looks up the collection definition for a key and renders
// the record key as a string. resolveScopedCollection walks the key's parent
// chain so nested records are physically scoped under their parent record
// (spaces/family/contacts/...); for a top-level key (no parent) it is a plain
// flat lookup, unchanged.
func (r readonlyTx) resolveCollection(key *dalrecord.Key) (*ingitdb.CollectionDef, string, error) {
	if r.db.def == nil {
		return nil, "", fmt.Errorf("definition is required: use NewLocalDBWithDef")
	}
	if key == nil {
		return nil, "", fmt.Errorf("key is nil")
	}
	colDef, err := resolveScopedCollection(r.db.def, key.Collection(), key.Parent())
	if err != nil {
		return nil, "", err
	}
	if colDef.RecordFile == nil {
		return nil, "", fmt.Errorf("collection %q has no record_file definition", key.Collection())
	}
	recordKey := fmt.Sprintf("%v", key.ID)
	return colDef, recordKey, nil
}

func (r readonlyTx) Exists(ctx context.Context, key *dalrecord.Key) (bool, error) {
	//TODO implement me
	panic("implement me")
}

func (r readonlyTx) GetMulti(ctx context.Context, records []dalrecord.Record) error {
	//TODO implement me
	panic("implement me")
}

func (r readonlyTx) ExecuteQueryToRecordsReader(ctx context.Context, query dal.Query) (dal.RecordsReader, error) {
	return executeQueryToRecordsReader(ctx, r, query)
}

// ExecuteQueryToRecordsetReader executes a structured query against a single
// collection and returns a recordset-shaped reader. Stored columns carry each
// record's value; computed (formula) columns are registered via
// recordset.NewComputedColumn bound to a Starlark-backed evaluator, so computed
// values resolve lazily when a consumer reads them rather than being baked in.
func (r readonlyTx) ExecuteQueryToRecordsetReader(_ context.Context, query dal.Query, _ ...recordset.Option) (dal.RecordsetReader, error) {
	colDef, err := collectionFromQuery(r.db.def, query)
	if err != nil {
		return nil, err
	}
	stored, err := readAllStoredRecords(colDef)
	if err != nil {
		return nil, err
	}
	return dalgo2ingitdb.NewRecordsetReader(dalgo2ingitdb.BuildRecordset(colDef, stored)), nil
}
