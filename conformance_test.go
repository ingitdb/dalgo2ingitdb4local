package dalgo2fsingitdb

import (
	"testing"
)

// TestConformance documents why the shared dalgotest.RunConformance suite
// (github.com/dal-go/dalgo/dalgotest) is not wired up against this adapter,
// rather than silently omitting it.
//
// This is not a live-infrastructure gap — localDB needs no credentials or
// external service, only a temp directory, so nothing stops the suite from
// running here. The blocker is that several methods the suite exercises are
// not implemented at all yet, and panic rather than return an error:
//
//   - localDB.Exists, localDB.GetMulti, localDB.Schema (db_local.go) —
//     "//TODO implement me" / panic("implement me")
//   - readonlyTx.Exists, readonlyTx.GetMulti, readonlyTx.Options
//     (tx_readonly.go) — same
//   - readwriteTx.SetMulti, DeleteMulti, Update, UpdateRecord, UpdateMulti,
//     InsertMulti (tx_readwrite.go) — same
//
// dalgotest.RunConformance calls dal.TxWithMessage(...) when opening its
// transaction (which the framework's write pipeline resolves via
// tx.Options()), exercises UpdateRecord and the Multi methods directly, and
// checks persistence via db.Exists — every one of those would panic here
// rather than fail a single subtest, which risks taking down the whole test
// binary instead of reporting one conformance failure. Running the suite for
// real is therefore unsafe until those methods have real implementations (or
// at least return dal.ErrNotImplementedYet instead of panicking) — a
// pre-existing gap in this adapter's completeness, not something introduced
// by, or in scope for, the dal.DB-sealing migration.
//
// Set and Insert (the two write paths that are implemented) were updated to
// convert record.Data() via record.DataToMap instead of a raw
// map[string]any type assertion, matching the dalgo2ingitdb /
// dalgo2ingitdb4github siblings, so at least those two accept the suite's
// typed record fixtures whenever this gap is closed enough to attempt wiring
// again.
func TestConformance(t *testing.T) {
	t.Skip("dalgotest.RunConformance cannot run safely against this adapter yet: " +
		"Exists/GetMulti/Options/SetMulti/DeleteMulti/Update/UpdateRecord/UpdateMulti/InsertMulti " +
		"are all panic(\"implement me\") stubs (see the comment on this test), and the suite calls into " +
		"several of them directly — not a live-infrastructure gap, a pre-existing adapter-completeness one")
}
