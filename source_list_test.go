package dalgo2fsingitdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dal-go/dalgo/dal"
	"github.com/dal-go/record"
	"github.com/ingitdb/ingitdb-go/ingitdb"
)

func importedListDef(dir string, format ingitdb.RecordFormat) *ingitdb.CollectionDef {
	file := "records.jsonl"
	if format == ingitdb.RecordFormatCSV {
		file = "records.csv"
	}
	col := &ingitdb.CollectionDef{
		ID: "sample", DirPath: dir,
		RecordFile: &ingitdb.RecordFileDef{Name: file, Format: format, RecordType: ingitdb.ListOfRecords},
		Columns: map[string]*ingitdb.ColumnDef{
			"part": {Type: ingitdb.ColumnTypeString}, "wide": {Type: ingitdb.ColumnTypeInt},
			"blob": {Type: ingitdb.ColumnTypeString}, "amount": {Type: ingitdb.ColumnTypeString},
		},
		ColumnsOrder: []string{"part", "wide", "blob", "amount"},
		PrimaryKey:   []string{"part", "wide"},
		SourceSchema: &ingitdb.SourceSchemaDef{KeyMode: "source-primary-key", Fields: []ingitdb.SourceFieldDef{
			{Name: "part", Type: "string"}, {Name: "wide", Type: "int64"},
			{Name: "blob", Type: "bytes"}, {Name: "amount", Type: "decimal"},
		}},
	}
	if format == ingitdb.RecordFormatCSV {
		col.RecordFile.CSVCellEncoding = "json-v1"
		col.ColumnsOrder = append([]string{"$ID"}, col.ColumnsOrder...)
	}
	return col
}

func TestImportedListQueryAndPointPreserveTransport(t *testing.T) {
	t.Parallel()
	for _, format := range []ingitdb.RecordFormat{ingitdb.RecordFormatJSONL, ingitdb.RecordFormatCSV} {
		t.Run(string(format), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			col := importedListDef(dir, format)
			rows := []map[string]any{{"$ID": "pk-composite", "part": "A", "wide": int64(9007199254740993), "blob": "AAEC", "amount": "1.20"}}
			var content []byte
			var err error
			if format == ingitdb.RecordFormatCSV {
				content, err = ingitdb.EncodeRecordContentForCollection(rows, col)
			} else {
				content, err = ingitdb.EncodeListOfRecordsContent(rows, format, col.ColumnsOrder)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, col.RecordFile.Name), content, 0o644); err != nil {
				t.Fatal(err)
			}
			stored, err := readAllListStored(col)
			if err != nil {
				t.Fatal(err)
			}
			if len(stored) != 1 || stored[0].Key != "pk-composite" || stored[0].Stored["wide"] != int64(9007199254740993) || stored[0].Stored["amount"] != "1.20" || string(stored[0].Stored["blob"].([]byte)) != "\x00\x01\x02" {
				t.Fatalf("transport changed: %#v", stored)
			}
			if _, exists := stored[0].Stored["$ID"]; exists {
				t.Fatal("transport ID leaked into source columns")
			}
			def := &ingitdb.Definition{Collections: map[string]*ingitdb.CollectionDef{"sample": col}}
			query := dal.NewQueryBuilder(dal.From(dal.NewRootCollectionRef("sample", ""))).SelectIntoRecord(func() record.Record {
				return record.NewRecordWithData(record.NewKeyWithID("sample", ""), map[string]any{})
			})
			reader, err := (readonlyTx{db: localDB{def: def}}).ExecuteQueryToRecordsetReader(context.Background(), query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reader.Close() }()
			row, rs, err := reader.Next()
			if err != nil {
				t.Fatal(err)
			}
			wide, err := row.GetValueByName("wide", rs)
			if err != nil || wide != int64(9007199254740993) {
				t.Fatalf("recordset wide = %v, %v", wide, err)
			}
			point := record.NewRecordWithData(record.NewKeyWithID("sample", "pk-composite"), map[string]any{})
			if err := (readonlyTx{db: localDB{def: def}}).Get(context.Background(), point); err != nil {
				t.Fatal(err)
			}
			if !point.Exists() || point.Data().(map[string]any)["wide"] != int64(9007199254740993) {
				t.Fatalf("point changed: %#v", point.Data())
			}
			missing := record.NewRecordWithData(record.NewKeyWithID("sample", "missing"), map[string]any{})
			if err := (readonlyTx{db: localDB{def: def}}).Get(context.Background(), missing); err != nil || missing.Exists() {
				t.Fatalf("missing point = %v, %v", missing.Exists(), err)
			}
		})
	}
}

func TestImportedListRejectsInvalidTransport(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, content, want string }{
		{"missing-id", `{"part":"A"}` + "\n", "transport ID"},
		{"nonstring-id", `{"$ID":42,"part":"A"}` + "\n", "transport ID"},
		{"duplicate-id", `{"$ID":"same","part":"A","blob":null}` + "\n" + `{"$ID":"same","part":"B","blob":null}` + "\n", "duplicate"},
		{"invalid-blob", `{"$ID":"x","blob":"!"}` + "\n", "decode source bytes"},
		{"wrong-blob-type", `{"$ID":"x","blob":42}` + "\n", "transport type"},
		{"missing-blob", `{"$ID":"x","part":"A"}` + "\n", "missing source bytes field"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			col := importedListDef(dir, ingitdb.RecordFormatJSONL)
			if err := os.WriteFile(filepath.Join(dir, col.RecordFile.Name), []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := readAllListStored(col)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if tc.name == "wrong-blob-type" || tc.name == "missing-blob" {
				def := &ingitdb.Definition{Collections: map[string]*ingitdb.CollectionDef{"sample": col}}
				point := record.NewRecordWithData(record.NewKeyWithID("sample", "x"), map[string]any{})
				if getErr := (readonlyTx{db: localDB{def: def}}).Get(context.Background(), point); getErr == nil || !strings.Contains(getErr.Error(), tc.want) {
					t.Fatalf("point error = %v, want %q", getErr, tc.want)
				}
				if point.Error() == nil {
					t.Fatal("point record did not retain the read error")
				}
			}
		})
	}
	col := importedListDef(t.TempDir(), ingitdb.RecordFormatJSONL)
	if records, err := readAllListStored(col); err != nil || len(records) != 0 {
		t.Fatalf("missing file = %#v, %v", records, err)
	}
	if err := os.Mkdir(filepath.Join(col.DirPath, col.RecordFile.Name), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := readAllListStored(col); err == nil || !errors.Is(err, os.ErrInvalid) && !strings.Contains(err.Error(), "read list records") {
		t.Fatalf("directory read error = %v", err)
	}
}

func TestImportedMapDecodesSourceBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	col := &ingitdb.CollectionDef{
		ID: "sample", DirPath: dir,
		RecordFile:   &ingitdb.RecordFileDef{Name: "records.json", Format: ingitdb.RecordFormatJSON, RecordType: ingitdb.MapOfRecords},
		Columns:      map[string]*ingitdb.ColumnDef{"blob": {Type: ingitdb.ColumnTypeString}, "wide": {Type: ingitdb.ColumnTypeInt}},
		ColumnsOrder: []string{"blob", "wide"},
		SourceSchema: &ingitdb.SourceSchemaDef{KeyMode: "export-ordinal", Fields: []ingitdb.SourceFieldDef{
			{Name: "blob", Type: "bytes"}, {Name: "wide", Type: "int64"},
		}},
	}
	content, err := ingitdb.EncodeMapOfRecordsContent(map[string]map[string]any{
		"row-000000000001": {"blob": "AAEC", "wide": int64(9007199254740993)},
	}, ingitdb.RecordFormatJSON, "sample", col.ColumnsOrder)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "records.json"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	stored, err := readAllMapStored(col)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Stored["wide"] != int64(9007199254740993) || string(stored[0].Stored["blob"].([]byte)) != "\x00\x01\x02" {
		t.Fatalf("map transport changed: %#v", stored)
	}
	def := &ingitdb.Definition{Collections: map[string]*ingitdb.CollectionDef{"sample": col}}
	point := record.NewRecordWithData(record.NewKeyWithID("sample", "row-000000000001"), map[string]any{})
	if err := (readonlyTx{db: localDB{def: def}}).Get(context.Background(), point); err != nil {
		t.Fatal(err)
	}
	if string(point.Data().(map[string]any)["blob"].([]byte)) != "\x00\x01\x02" {
		t.Fatalf("map point bytes: %#v", point.Data())
	}
	if err := os.WriteFile(filepath.Join(dir, "records.json"), []byte(`{"row-000000000001":{"blob":42,"wide":9007199254740993}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := record.NewRecordWithData(record.NewKeyWithID("sample", "row-000000000001"), map[string]any{})
	if err := (readonlyTx{db: localDB{def: def}}).Get(context.Background(), broken); err == nil || broken.Error() == nil {
		t.Fatalf("corrupt map point state: Get=%v, Record.Error=%v", err, broken.Error())
	}
}

func TestImportedCSVRejectsInvalidTransportIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		rows []map[string]any
		want string
	}{
		{"missing", []map[string]any{{"part": "A", "blob": nil}}, "transport ID"},
		{"nonstring", []map[string]any{{"$ID": int64(42), "part": "A", "blob": nil}}, "transport ID"},
		{"duplicate", []map[string]any{{"$ID": "same", "part": "A", "blob": nil}, {"$ID": "same", "part": "B", "blob": nil}}, "duplicate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			col := importedListDef(dir, ingitdb.RecordFormatCSV)
			content, err := ingitdb.EncodeRecordContentForCollection(tc.rows, col)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, col.RecordFile.Name), content, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := readAllListStored(col); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CSV ID error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSourceBytesSparseOrdinaryAndNullableImported(t *testing.T) {
	t.Parallel()
	col := &ingitdb.CollectionDef{SourceSchema: &ingitdb.SourceSchemaDef{
		Fields: []ingitdb.SourceFieldDef{{Name: "blob", Type: "bytes", Nullable: true}},
	}}
	if err := decodeSourceTransport(col, map[string]any{}); err != nil {
		t.Fatalf("ordinary sparse record: %v", err)
	}
	col.SourceSchema.KeyMode = "export-ordinal"
	if err := decodeSourceTransport(col, map[string]any{"blob": nil}); err != nil {
		t.Fatalf("imported nullable null: %v", err)
	}
}
