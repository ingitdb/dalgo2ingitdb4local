package dalgo2fsingitdb

import (
	"context"
	"testing"

	"github.com/dal-go/dalgo/dal"
	dalrecord "github.com/dal-go/record"
	"github.com/dal-go/record/update"
)

func TestReadwriteTx_Panics(t *testing.T) {
	t.Parallel()

	tx := readwriteTx{readonlyTx: readonlyTx{db: localDB{rootDirPath: "/tmp/root"}}}
	ctx := context.Background()
	var record dalrecord.Record
	var key *dalrecord.Key
	var records []dalrecord.Record
	var keys []*dalrecord.Key
	var updates []update.Update
	var preconditions []dal.Precondition
	var insertOptions []dal.InsertOption

	tests := []struct {
		name string
		fn   func()
	}{
		{
			name: "id",
			fn: func() {
				tx.ID()
			},
		},
		{
			name: "set_multi",
			fn: func() {
				err := tx.SetMulti(ctx, records)
				_ = err
			},
		},
		{
			name: "delete_multi",
			fn: func() {
				err := tx.DeleteMulti(ctx, keys)
				_ = err
			},
		},
		{
			name: "update",
			fn: func() {
				err := tx.Update(ctx, key, updates, preconditions...)
				_ = err
			},
		},
		{
			name: "update_record",
			fn: func() {
				err := tx.UpdateRecord(ctx, record, updates, preconditions...)
				_ = err
			},
		},
		{
			name: "update_multi",
			fn: func() {
				err := tx.UpdateMulti(ctx, keys, updates, preconditions...)
				_ = err
			},
		},
		{
			name: "insert_multi",
			fn: func() {
				err := tx.InsertMulti(ctx, records, insertOptions...)
				_ = err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expectPanic(t, tt.fn)
		})
	}
}
