package dalgo2fsingitdb

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/dal-go/dalgo/dal"
	dalrecord "github.com/dal-go/record"
	"github.com/dal-go/record/update"
	"github.com/ingitdb/dalgo2ingitdb"
	"github.com/ingitdb/ingitdb-go/ingitdb"
)

var _ dal.ReadwriteTransaction = (*readwriteTx)(nil)

type readwriteTx struct {
	readonlyTx
}

func (r readwriteTx) ID() string {
	//TODO implement me
	panic("implement me")
}

func (r readwriteTx) Set(ctx context.Context, record dalrecord.Record) error {
	_ = ctx
	colDef, recordKey, err := r.resolveCollection(record.Key())
	if err != nil {
		return err
	}
	record.SetError(nil)
	data, err := dalrecord.DataToMap(record.Data())
	if err != nil {
		return err
	}
	if err := dalgo2ingitdb.ValidateWrite(r.db.def, "Set", colDef.ID, colDef, recordKey, data); err != nil {
		return err
	}
	path := resolveRecordPath(colDef, recordKey)
	switch colDef.RecordFile.RecordType {
	case ingitdb.MapOfRecords:
		allRecords, _, err := readMapOfRecordsFile(path, colDef.RecordFile.Format)
		if err != nil {
			return err
		}
		if allRecords == nil {
			allRecords = make(map[string]map[string]any)
		}
		allRecords[recordKey] = ingitdb.ApplyLocaleToWrite(data, colDef.Columns)
		return writeMapOfRecordsFile(path, colDef, allRecords)
	default:
		if colDef.RecordFile.Format == ingitdb.RecordFormatMarkdown {
			return writeMarkdownRecord(path, colDef, data)
		}
		return writeRecordToFile(path, colDef.RecordFile.Format, data)
	}
}

func (r readwriteTx) SetMulti(ctx context.Context, records []dalrecord.Record) error {
	//TODO implement me
	panic("implement me")
}

func (r readwriteTx) Delete(ctx context.Context, key *dalrecord.Key) error {
	_ = ctx
	colDef, recordKey, err := r.resolveCollection(key)
	if err != nil {
		return err
	}
	if err := dalgo2ingitdb.ValidateDelete(r.db.def, colDef.ID, recordKey); err != nil {
		return err
	}
	path := resolveRecordPath(colDef, recordKey)
	switch colDef.RecordFile.RecordType {
	case ingitdb.MapOfRecords:
		allRecords, found, err := readMapOfRecordsFile(path, colDef.RecordFile.Format)
		if err != nil {
			return err
		}
		if !found {
			return dalrecord.ErrRecordNotFound
		}
		if _, exists := allRecords[recordKey]; !exists {
			return dalrecord.ErrRecordNotFound
		}
		delete(allRecords, recordKey)
		return writeMapOfRecordsFile(path, colDef, allRecords)
	default:
		return deleteRecordFile(path)
	}
}

func (r readwriteTx) DeleteMulti(ctx context.Context, keys []*dalrecord.Key) error {
	//TODO implement me
	panic("implement me")
}

func (r readwriteTx) Update(ctx context.Context, key *dalrecord.Key, updates []update.Update, preconditions ...dal.Precondition) error {
	//TODO implement me
	panic("implement me")
}

func (r readwriteTx) UpdateRecord(ctx context.Context, record dalrecord.Record, updates []update.Update, preconditions ...dal.Precondition) error {
	//TODO implement me
	panic("implement me")
}

func (r readwriteTx) UpdateMulti(ctx context.Context, keys []*dalrecord.Key, updates []update.Update, preconditions ...dal.Precondition) error {
	//TODO implement me
	panic("implement me")
}

func (r readwriteTx) Insert(ctx context.Context, record dalrecord.Record, opts ...dal.InsertOption) error {
	_, _ = ctx, opts
	colDef, recordKey, err := r.resolveCollection(record.Key())
	if err != nil {
		return err
	}
	record.SetError(nil)
	data, err := dalrecord.DataToMap(record.Data())
	if err != nil {
		return err
	}
	if err := dalgo2ingitdb.ValidateWrite(r.db.def, "Insert", colDef.ID, colDef, recordKey, data); err != nil {
		return err
	}
	path := resolveRecordPath(colDef, recordKey)
	switch colDef.RecordFile.RecordType {
	case ingitdb.MapOfRecords:
		allRecords, _, err := readMapOfRecordsFile(path, colDef.RecordFile.Format)
		if err != nil {
			return err
		}
		if allRecords == nil {
			allRecords = make(map[string]map[string]any)
		}
		if _, exists := allRecords[recordKey]; exists {
			return fmt.Errorf("record already exists: %s in %s", recordKey, path)
		}
		allRecords[recordKey] = ingitdb.ApplyLocaleToWrite(data, colDef.Columns)
		return writeMapOfRecordsFile(path, colDef, allRecords)
	default:
		_, statErr := os.Stat(path)
		if statErr == nil {
			return fmt.Errorf("record already exists: %s", path)
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("failed to check file %s: %w", path, statErr)
		}
		if colDef.RecordFile.Format == ingitdb.RecordFormatMarkdown {
			return writeMarkdownRecord(path, colDef, data)
		}
		return writeRecordToFile(path, colDef.RecordFile.Format, data)
	}
}

func (r readwriteTx) InsertMulti(ctx context.Context, records []dalrecord.Record, opts ...dal.InsertOption) error {
	//TODO implement me
	panic("implement me")
}
