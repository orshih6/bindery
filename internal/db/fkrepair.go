package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// DefaultRepairBatchSize is how many violating rows one repair transaction
// resolves before it commits and hands the connection back to the pool.
const DefaultRepairBatchSize = 500

// RepairOptions tunes RepairForeignKeyDrift.
type RepairOptions struct {
	// Path is the database file. With a real path the list of violating rows is
	// STREAMED from a short lived read only connection while the writes run on
	// the shared pool in small batches. Empty (or an in-memory or file: URI
	// database, which cannot be reopened) loads the list through the pool first.
	Path string
	// BatchSize is rows per transaction; zero means DefaultRepairBatchSize.
	BatchSize int
	// AfterBatch, when set, runs after each committed batch with the 1 based
	// batch number. Tests use it to cancel mid repair.
	AfterBatch func(batch int)
}

// IsReopenablePath reports whether path names a database file a second
// connection can open: not empty, not ":memory:" and not a "file:" URI.
func IsReopenablePath(path string) bool {
	return path != "" && path != ":memory:" && !strings.HasPrefix(path, "file:")
}

// RepairForeignKeyDrift is the running app's counterpart of
// RepairForeignKeyViolations. It resolves each violating row exactly the same
// way (both use the same per row repairer: CASCADE removes the row, SET NULL
// clears the reference, anything else is reported and left alone) but is built
// not to stall a live instance.
//
// The pool has ONE connection, so a repair that held it for its whole run would
// freeze /health, sign in and the UI, exactly as a scan on the pool did. So:
//
//   - finding the violating rows (the expensive full scan) runs on its own read
//     only connection and is streamed, never held in memory whole;
//   - writes happen in transactions of BatchSize rows. Between batches the
//     connection goes back to the pool and any request waiting for it is served
//     before the next batch starts, so the app is blocked for one batch at a
//     time (milliseconds), not for the repair;
//   - ctx is checked between batches and bounds every statement, so a cancel or
//     deadline stops the repair at a batch boundary.
//
// Atomicity: each batch is all or nothing, the whole repair is not. A repair
// that stops part way (cancelled, timed out, an error) leaves every committed
// batch in place and is safe to run again: it is idempotent, and each row is
// re-verified as still orphaned before it is touched. The caller takes the
// backup that makes the whole operation recoverable.
//
// Remaining is left zero: the caller runs a fresh scan to report what is left.
func RepairForeignKeyDrift(ctx context.Context, database *sql.DB, opts RepairOptions) (RepairReport, error) {
	size := opts.BatchSize
	if size <= 0 {
		size = DefaultRepairBatchSize
	}
	r := newRepairer()

	next, closeSrc, err := violationSource(ctx, database, opts.Path)
	if err != nil {
		return r.report, err
	}
	defer closeSrc()

	batch := make([]ForeignKeyViolation, 0, size)
	for n := 1; ; n++ {
		batch = batch[:0]
		for len(batch) < size {
			v, ok, err := next()
			if err != nil {
				return r.report, err
			}
			if !ok {
				break
			}
			batch = append(batch, v)
		}
		if len(batch) == 0 {
			return r.report, nil
		}
		if err := ctx.Err(); err != nil {
			return r.report, err
		}
		if err := r.applyBatch(ctx, database, batch); err != nil {
			return r.report, err
		}
		if opts.AfterBatch != nil {
			opts.AfterBatch(n)
		}
	}
}

// applyBatch resolves one batch in a single transaction. On error the batch is
// rolled back and the report goes back to what it was, so the counts only ever
// describe committed work.
func (r *repairer) applyBatch(ctx context.Context, database *sql.DB, batch []ForeignKeyViolation) error {
	saved := r.report
	savedNotes := len(r.report.SkipNotes)
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin repair batch: %w", err)
	}
	for _, v := range batch {
		if err := r.apply(ctx, tx, v); err != nil {
			_ = tx.Rollback()
			r.report = saved
			r.report.SkipNotes = r.report.SkipNotes[:savedNotes]
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		r.report = saved
		r.report.SkipNotes = r.report.SkipNotes[:savedNotes]
		return fmt.Errorf("commit repair batch: %w", err)
	}
	return nil
}

// violationSource returns an iterator over every violating row and a closer.
func violationSource(ctx context.Context, database *sql.DB, path string) (func() (ForeignKeyViolation, bool, error), func(), error) {
	if !IsReopenablePath(path) {
		all, err := ForeignKeyViolations(ctx, database)
		if err != nil {
			return nil, nil, err
		}
		i := 0
		return func() (ForeignKeyViolation, bool, error) {
			if i >= len(all) {
				return ForeignKeyViolation{}, false, nil
			}
			i++
			return all[i-1], true, nil
		}, func() {}, nil
	}

	ro, err := sql.Open("sqlite", SQLiteFileURI(path, "mode=ro&_pragma=busy_timeout(5000)"))
	if err != nil {
		return nil, nil, fmt.Errorf("open %q read-only: %w", path, err)
	}
	ro.SetMaxOpenConns(1)
	rows, err := ro.QueryContext(ctx, `SELECT "table", "rowid", "parent", "fkid" FROM pragma_foreign_key_check`) //nolint:rowserrcheck // rows.Err() is checked where the iterator drains it, below
	if err != nil {
		_ = ro.Close()
		return nil, nil, fmt.Errorf("foreign_key_check: %w", err)
	}
	closeSrc := func() {
		_ = rows.Close()
		_ = ro.Close()
	}
	return func() (ForeignKeyViolation, bool, error) {
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return ForeignKeyViolation{}, false, fmt.Errorf("iterate foreign_key_check: %w", err)
			}
			return ForeignKeyViolation{}, false, nil
		}
		var v ForeignKeyViolation
		if err := rows.Scan(&v.Table, &v.RowID, &v.Parent, &v.FKID); err != nil {
			return ForeignKeyViolation{}, false, fmt.Errorf("scan foreign_key_check row: %w", err)
		}
		return v, true, nil
	}, closeSrc, nil
}
