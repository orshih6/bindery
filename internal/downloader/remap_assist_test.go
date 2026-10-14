package downloader

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/vavallee/bindery/internal/models"
)

func qbitCategoryServer(t *testing.T, savePath string) *httptest.Server {
	t.Helper()
	quoted, err := json.Marshal(savePath)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/categories":
			_, _ = w.Write([]byte(`{"books":{"name":"books","savePath":` + string(quoted) + `}}`))
		case "/api/v2/app/defaultSavePath":
			_, _ = w.Write([]byte(""))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// The raw client path is exposed so the UI can seed a remap (#2031).
func TestPathVisibility_RemotePathPopulated(t *testing.T) {
	visible := t.TempDir()
	srv := qbitCategoryServer(t, "/remote/downloads")
	defer srv.Close()
	host, port := serverHostPort(t, srv.URL)

	t.Run("not visible", func(t *testing.T) {
		client := &models.DownloadClient{Type: "qbittorrent", Host: host, Port: port, Username: "u", Password: "p", Category: "books"}
		got := CheckCompletedPathVisibility(context.Background(), client, "/d", "", "")
		if got.Status != PathNotVisible {
			t.Fatalf("status = %q", got.Status)
		}
		if got.RemotePath != "/remote/downloads" {
			t.Fatalf("RemotePath = %q, want /remote/downloads", got.RemotePath)
		}
	})
	t.Run("visible keeps the client path, not the remapped one", func(t *testing.T) {
		client := &models.DownloadClient{Type: "qbittorrent", Host: host, Port: port, Username: "u", Password: "p", Category: "books", PathRemap: "/remote/downloads:" + visible}
		got := CheckCompletedPathVisibility(context.Background(), client, "/d", "", "")
		if got.Status != PathVisible || got.RemotePath != "/remote/downloads" || got.Path != filepath.Clean(visible) {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestPathVisibility_RemotePathWindows(t *testing.T) {
	srv := qbitCategoryServer(t, `S:\Downloads\bindery`)
	defer srv.Close()
	host, port := serverHostPort(t, srv.URL)
	client := &models.DownloadClient{Type: "qbittorrent", Host: host, Port: port, Username: "u", Password: "p", Category: "books"}
	got := CheckCompletedPathVisibility(context.Background(), client, "/d", "", "")
	// The qBittorrent adapter hands the path over with forward slashes; both
	// separator styles are the same Windows path to pathmap.
	if got.RemotePath != "S:/Downloads/bindery" {
		t.Fatalf("RemotePath = %q", got.RemotePath)
	}
	cands := RemapCandidates("qbittorrent", got.RemotePath, []RemapRoot{{Path: "/data/downloads", Source: RemapSourceDownloadDir}})
	if len(cands) != 1 || cands[0].After != "/data/downloads/Example Book" {
		t.Fatalf("candidates from the reported path = %+v", cands)
	}
}

func TestSupportsRemapAssistant(t *testing.T) {
	for typ, want := range map[string]bool{
		"qbittorrent": true, "nzbget": true, "rtorrent": true,
		"sabnzbd": false, "transmission": false, "deluge": false, "": false,
	} {
		if got := SupportsRemapAssistant(typ); got != want {
			t.Errorf("SupportsRemapAssistant(%q) = %v, want %v", typ, got, want)
		}
	}
}

func TestRemapCandidates(t *testing.T) {
	roots := []RemapRoot{
		{Path: "/data/downloads", Source: RemapSourceDownloadDir},
		{Path: "/data/downloads/", Source: RemapSourceLibrary}, // duplicate once cleaned
		{Path: "", Source: RemapSourceDownloadDir},
		{Path: "/books", Source: RemapSourceLibrary},
	}

	t.Run("posix", func(t *testing.T) {
		got := RemapCandidates("qbittorrent", "/downloads/books", roots)
		if len(got) != 2 {
			t.Fatalf("got %+v, want 2 candidates", got)
		}
		if got[0].Path != "/data/downloads" || got[0].Source != RemapSourceDownloadDir {
			t.Errorf("first = %+v", got[0])
		}
		if got[0].Before != "/downloads/books/Example Book" || got[0].After != "/data/downloads/Example Book" {
			t.Errorf("preview = %q -> %q", got[0].Before, got[0].After)
		}
	})
	t.Run("windows drive", func(t *testing.T) {
		got := RemapCandidates("rtorrent", `S:\Downloads\books`, roots[:1])
		if len(got) != 1 || got[0].Before != `S:\Downloads\books\Example Book` || got[0].After != "/data/downloads/Example Book" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("windows share", func(t *testing.T) {
		got := RemapCandidates("nzbget", `\\nas\books\dl`, roots[:1])
		if len(got) != 1 || got[0].Before != `\\nas\books\dl\Example Book` || got[0].After != "/data/downloads/Example Book" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("unsupported type offers nothing", func(t *testing.T) {
		for _, typ := range []string{"sabnzbd", "transmission", "deluge", ""} {
			if got := RemapCandidates(typ, "/downloads", roots); got != nil {
				t.Errorf("%q: got %+v, want none", typ, got)
			}
		}
	})
	t.Run("no remote path offers nothing", func(t *testing.T) {
		if got := RemapCandidates("qbittorrent", "  ", roots); got != nil {
			t.Errorf("got %+v", got)
		}
	})
}
