package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vavallee/bindery/internal/auth"
	"github.com/vavallee/bindery/internal/config"
	"github.com/vavallee/bindery/internal/db"
	"github.com/vavallee/bindery/internal/downloader"
	"github.com/vavallee/bindery/internal/models"
	"github.com/vavallee/bindery/internal/telemetry"
)

type blockedFixture struct {
	h        *SetupStateHandler
	indexers *db.IndexerRepo
	clients  *db.DownloadClientRepo
	settings *db.SettingsRepo
	health   *downloader.HealthStore
	cfg      *config.Config
	lib, dl  string
}

// newBlockedFixture builds a fully set up, previously imported instance with
// healthy folders and one healthy client.
func newBlockedFixture(t *testing.T) *blockedFixture {
	t.Helper()
	h, indexers, clients, settings := newSetupStateFixture(t)
	f := &blockedFixture{h: h, indexers: indexers, clients: clients, settings: settings}
	f.lib, f.dl = t.TempDir(), t.TempDir()
	f.cfg = &config.Config{LibraryDir: f.lib, DownloadDir: f.dl}
	f.health = downloader.NewHealthStore()
	h.WithBlockedChecks(f.cfg, f.health)

	ctx := context.Background()
	if err := indexers.Create(ctx, &models.Indexer{Name: "ix", URL: "http://x/api", Type: "newznab", Enabled: true}); err != nil {
		t.Fatalf("indexer: %v", err)
	}
	c := &models.DownloadClient{Name: "sab", Type: "sabnzbd", Host: "h", Port: 8080, Enabled: true}
	if err := clients.Create(ctx, c); err != nil {
		t.Fatalf("client: %v", err)
	}
	f.health.Set(c.ID, models.DownloadClientHealth{Status: downloader.HealthOK})
	now := time.Now().UTC().Format(time.RFC3339)
	for _, k := range []string{telemetry.SettingFirstAuthorAt, telemetry.SettingFirstImportAt} {
		if err := settings.Set(ctx, k, now); err != nil {
			t.Fatalf("marker: %v", err)
		}
	}
	return f
}

func (f *blockedFixture) get(t *testing.T, role string) (SetupState, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/setup-state", nil)
	if role != "" {
		req = req.WithContext(auth.WithUserRole(req.Context(), role))
	}
	w := httptest.NewRecorder()
	f.h.Get(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var s SetupState
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return s, w.Body.String()
}

func TestSetupStateBlocked_HealthyInstanceIsNotBlocked(t *testing.T) {
	f := newBlockedFixture(t)
	s, _ := f.get(t, "admin")
	if s.Blocked || s.BlockedArea != "" || s.BlockedReason != "" {
		t.Errorf("healthy instance reported blocked: %+v", s)
	}
	if !s.Complete {
		t.Error("fixture should be complete")
	}
}

func TestSetupStateBlocked_MissingLibraryDirAfterComplete(t *testing.T) {
	f := newBlockedFixture(t)
	f.cfg.LibraryDir = filepath.Join(f.lib, "gone")
	s, body := f.get(t, "admin")
	if !s.Blocked || s.BlockedArea != "storage" {
		t.Fatalf("want blocked storage, got %+v", s)
	}
	// The reason names the area and the fix, never a path.
	if strings.Contains(body, f.lib) || strings.Contains(body, f.dl) {
		t.Errorf("response leaks a folder path: %s", body)
	}
}

func TestSetupStateBlocked_UnwritableDownloadDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	f := newBlockedFixture(t)
	if err := os.Chmod(f.dl, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.dl, 0o700) })
	if s, _ := f.get(t, "admin"); !s.Blocked || s.BlockedArea != "storage" {
		t.Fatalf("want blocked storage, got %+v", s)
	}
}

// The folder probe touches the disk, so it is cached between requests and
// picked up again once the TTL passes.
func TestSetupStateBlocked_StorageProbeIsCached(t *testing.T) {
	f := newBlockedFixture(t)
	clock := time.Now()
	f.h.now = func() time.Time { return clock }
	if s, _ := f.get(t, "admin"); s.Blocked {
		t.Fatalf("unexpectedly blocked: %+v", s)
	}
	f.cfg.LibraryDir = filepath.Join(f.lib, "gone")
	if s, _ := f.get(t, "admin"); s.Blocked {
		t.Error("probe re-ran inside the TTL")
	}
	clock = clock.Add(storageProbeTTL + time.Second)
	if s, _ := f.get(t, "admin"); !s.Blocked {
		t.Error("probe did not re-run after the TTL")
	}
}

func TestSetupStateBlocked_NoEnabledIndexer(t *testing.T) {
	f := newBlockedFixture(t)
	list, _ := f.indexers.List(context.Background())
	list[0].Enabled = false
	if err := f.indexers.Update(context.Background(), &list[0]); err != nil {
		t.Fatalf("update: %v", err)
	}
	s, _ := f.get(t, "admin")
	if !s.Blocked || s.BlockedArea != "indexer" {
		t.Fatalf("want blocked indexer, got %+v", s)
	}
}

func TestSetupStateBlocked_NoEnabledClient(t *testing.T) {
	f := newBlockedFixture(t)
	list, _ := f.clients.List(context.Background())
	list[0].Enabled = false
	if err := f.clients.Update(context.Background(), &list[0]); err != nil {
		t.Fatalf("update: %v", err)
	}
	s, _ := f.get(t, "admin")
	if !s.Blocked || s.BlockedArea != "download-client" {
		t.Fatalf("want blocked download-client, got %+v", s)
	}
}

func TestSetupStateBlocked_AllClientsFailingPathCheck(t *testing.T) {
	f := newBlockedFixture(t)
	list, _ := f.clients.List(context.Background())
	f.health.Set(list[0].ID, models.DownloadClientHealth{Status: downloader.HealthError, Message: "cannot read /downloads/secret"})
	s, body := f.get(t, "admin")
	if !s.Blocked || s.BlockedArea != "download-client" {
		t.Fatalf("want blocked download-client, got %+v", s)
	}
	if strings.Contains(body, "/downloads/secret") {
		t.Errorf("client health message leaked: %s", body)
	}

	// A second, healthy client keeps the pipeline moving.
	c2 := &models.DownloadClient{Name: "nzbget", Type: "nzbget", Host: "h2", Port: 6789, Enabled: true}
	if err := f.clients.Create(context.Background(), c2); err != nil {
		t.Fatal(err)
	}
	f.health.Set(c2.ID, models.DownloadClientHealth{Status: downloader.HealthOK})
	if s, _ := f.get(t, "admin"); s.Blocked {
		t.Errorf("one healthy client should be enough: %+v", s)
	}
}

// "We could not check" and "still checking" are not failures.
func TestSetupStateBlocked_UnknownAndCheckingAreNotFailures(t *testing.T) {
	f := newBlockedFixture(t)
	list, _ := f.clients.List(context.Background())
	for _, st := range []string{downloader.HealthUnknown, downloader.HealthChecking} {
		f.health.Set(list[0].ID, models.DownloadClientHealth{Status: st})
		if s, _ := f.get(t, "admin"); s.Blocked {
			t.Errorf("status %q reported blocked", st)
		}
	}
}

// Advisories (blocklist paused, missing unpackers) are not a path failure.
func TestSetupStateBlocked_AdvisoryAloneIsNotBlocked(t *testing.T) {
	f := newBlockedFixture(t)
	list, _ := f.clients.List(context.Background())
	f.health.SetAdvisory(list[0].ID, downloader.AdvisoryUnpackers, models.DownloadClientHealth{Status: downloader.HealthError, Message: "no unrar"})
	if s, _ := f.get(t, "admin"); s.Blocked {
		t.Errorf("advisory reported blocked: %+v", s)
	}
}

// A catalogue-only install that never imported has no pipeline to break.
func TestSetupStateBlocked_NeverImportedIsNotBlocked(t *testing.T) {
	f := newBlockedFixture(t)
	if err := f.settings.Set(context.Background(), telemetry.SettingFirstImportAt, ""); err != nil {
		t.Fatal(err)
	}
	f.cfg.LibraryDir = filepath.Join(f.lib, "gone")
	if s, _ := f.get(t, "admin"); s.Blocked {
		t.Errorf("install that never imported reported blocked: %+v", s)
	}
}

func TestSetupStateBlocked_NonAdminNeverSeesIt(t *testing.T) {
	f := newBlockedFixture(t)
	f.cfg.LibraryDir = filepath.Join(f.lib, "gone")
	for _, role := range []string{"user", ""} {
		s, body := f.get(t, role)
		if s.Blocked || s.BlockedArea != "" || s.BlockedReason != "" {
			t.Errorf("role %q saw blocked state: %s", role, body)
		}
	}
}
