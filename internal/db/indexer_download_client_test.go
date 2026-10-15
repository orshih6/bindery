package db

import (
	"context"
	"errors"
	"testing"

	"github.com/vavallee/bindery/internal/models"
)

// TestMigrate109_ExistingIndexersStayUnpinned: the column is added to a table
// that already has rows, and every one of them reads back with no pin.
func TestMigrate109_ExistingIndexersStayUnpinned(t *testing.T) {
	database, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()

	// Roll the column back out, insert a row the way a pre 109 install had it,
	// then add the column again exactly as the migration does.
	if _, err := database.ExecContext(ctx, `ALTER TABLE indexers DROP COLUMN download_client_id`); err != nil {
		t.Fatalf("drop column: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO indexers (name, type, url, api_key, categories, priority, enabled, supports_search, created_at, updated_at)
		VALUES ('old', 'newznab', 'http://x', 'k', '[7020]', 25, 1, 1, datetime('now'), datetime('now'))`); err != nil {
		t.Fatalf("insert old row: %v", err)
	}
	if _, err := database.ExecContext(ctx, `ALTER TABLE indexers ADD COLUMN download_client_id INTEGER REFERENCES download_clients(id) ON DELETE RESTRICT`); err != nil {
		t.Fatalf("re-add column: %v", err)
	}

	got, err := NewIndexerRepo(database).List(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("list: %v (%d rows)", err, len(got))
	}
	if got[0].DownloadClientID != nil {
		t.Errorf("an existing indexer came back pinned to %d", *got[0].DownloadClientID)
	}
}

// TestIndexerRepo_PinRoundTripAndForeignKey: the pin persists through
// Create, Update, GetByID and List, and the database itself refuses to delete
// a client an indexer is pinned to.
func TestIndexerRepo_PinRoundTripAndForeignKey(t *testing.T) {
	database, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	clients := NewDownloadClientRepo(database)
	indexers := NewIndexerRepo(database)

	c := &models.DownloadClient{Name: "qbit", Type: "qbittorrent", Host: "h", Port: 1, Enabled: true}
	if err := clients.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	idx := &models.Indexer{Name: "t", Type: "torznab", URL: "http://t", Enabled: true, Categories: []int{7020}, DownloadClientID: &c.ID}
	if err := indexers.Create(ctx, idx); err != nil {
		t.Fatal(err)
	}
	got, err := indexers.GetByID(ctx, idx.ID)
	if err != nil || got == nil || got.DownloadClientID == nil || *got.DownloadClientID != c.ID {
		t.Fatalf("GetByID pin = %+v, %v", got, err)
	}

	err = clients.Delete(ctx, c.ID)
	var pinned *ClientPinnedError
	if !errors.As(err, &pinned) {
		t.Fatalf("Delete of a pinned client = %v, want a *ClientPinnedError", err)
	}
	if len(pinned.Indexers) != 1 || pinned.Indexers[0] != "t" {
		t.Errorf("pinned indexers = %v, want [t]", pinned.Indexers)
	}
	if again, _ := clients.GetByID(ctx, c.ID); again == nil {
		t.Fatal("the client was deleted")
	}

	got.DownloadClientID = nil
	if err := indexers.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if err := clients.Delete(ctx, c.ID); err != nil {
		t.Fatalf("Delete after unpinning: %v", err)
	}
}
