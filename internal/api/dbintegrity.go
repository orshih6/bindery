package api

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/vavallee/bindery/internal/db"
)

// dbIntegrityTimeout bounds one drift scan. The scan is a full pass over every
// table with a foreign key, so it gets its own deadline rather than waiting on
// whatever the HTTP server allows.
const dbIntegrityTimeout = 60 * time.Second

// DBIntegrityHandler serves the read only foreign key drift report (#3356,
// part 1 of #1973). It never writes: the cleanup is `bindery db-repair`.
//
// The scan runs on its own read only connection opened from the database file
// (db.ForeignKeyDriftReportAt), not on the shared pool. The pool has ONE
// connection, so a scan on it would stall every other request for the whole
// scan, including /health and the auth middleware's settings reads. That also
// defeated the single flight guard: a second POST sat in the auth middleware
// until the first scan finished and then ran a second full scan.
type DBIntegrityHandler struct {
	// scan is set by the constructor; tests swap it to hold a scan open or to
	// make it slow.
	scan    func(ctx context.Context) ([]db.ForeignKeyDrift, error)
	timeout time.Duration
	// running is taken with TryLock, never Lock: a second click while a scan
	// is in flight is refused with 409 rather than queued behind it. Because
	// the scan no longer holds the app's connection, the second request now
	// reaches this guard while the first is still running.
	running sync.Mutex
	now     func() time.Time
	hook    func()
}

// NewDBIntegrityHandler builds the handler. dbPath is the database file; with
// an empty path (tests and in-memory databases, which cannot be reopened from
// a path) the scan falls back to the shared connection, accepting that it
// then blocks the pool for its duration.
func NewDBIntegrityHandler(database *sql.DB, dbPath string) *DBIntegrityHandler {
	h := &DBIntegrityHandler{timeout: dbIntegrityTimeout, now: time.Now}
	h.scan = func(ctx context.Context) ([]db.ForeignKeyDrift, error) {
		if dbPath != "" {
			return db.ForeignKeyDriftReportAt(ctx, dbPath, h.hook)
		}
		return db.ForeignKeyDriftReport(ctx, database, h.hook)
	}
	return h
}

// SetScanHook registers a function run while the scan's statement is open.
// For tests that need to hold a scan in flight.
func (h *DBIntegrityHandler) SetScanHook(f func()) { h.hook = f }

type dbIntegrityRelationship struct {
	ChildTable  string `json:"childTable"`
	ParentTable string `json:"parentTable"`
	// OnDelete is the declared ON DELETE action (CASCADE, SET NULL, ...):
	// what a cleanup would do to these rows. Empty when unknown.
	OnDelete   string `json:"onDelete"`
	Violations int    `json:"violations"`
}

type dbIntegrityResponse struct {
	Relationships []dbIntegrityRelationship `json:"relationships"`
	Total         int                       `json:"total"`
	CheckedAt     time.Time                 `json:"checkedAt"`
}

// Check handles POST /api/v1/system/db-integrity.
//
// It is a POST although it changes nothing: the scan is expensive, and a GET
// can be fired by link prefetchers, crawlers and browser prefetch. A POST also
// has to carry the CSRF header and token, so only the UI or a deliberate API
// call starts one.
func (h *DBIntegrityHandler) Check(w http.ResponseWriter, r *http.Request) {
	if !h.running.TryLock() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a database check is already running"})
		return
	}
	defer h.running.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	found, err := h.scan(ctx)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && r.Context().Err() == nil {
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "the database check took too long and was stopped; try again when Bindery is idle"})
			return
		}
		if r.Context().Err() != nil {
			// The client went away; nobody is left to read an answer.
			slog.Info("database drift check cancelled by the client")
			return
		}
		writeServerError(w, r, err)
		return
	}

	resp := dbIntegrityResponse{Relationships: make([]dbIntegrityRelationship, 0, len(found)), CheckedAt: h.now().UTC()}
	for _, d := range found {
		resp.Relationships = append(resp.Relationships, dbIntegrityRelationship{
			ChildTable: d.ChildTable, ParentTable: d.ParentTable, OnDelete: d.OnDelete, Violations: d.Violations,
		})
		resp.Total += d.Violations
	}
	writeJSON(w, http.StatusOK, resp)
}
