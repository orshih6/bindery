package indexer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vavallee/bindery/internal/models"
)

// fragmentPosts are per-chapter and per-part postings that the junk filter
// must drop (#3268, part 3). Grabbing one yields a few minutes of a book.
var fragmentPosts = []string{
	"Terry Goodkind - SOT06 Faith of the Fallen Ch63 of 71.mp3",
	"Terry Goodkind - SOT06 Faith of the Fallen Ch63.mp3",
	"Terry Goodkind - Faith of the Fallen Chapter 12 MP3",
	"Terry Goodkind - Faith of the Fallen Ch. 5 m4b",
	"Terry Goodkind - Faith of the Fallen Ch 5",
	"Author - Some Book - 12 of 40 (MP3)",
	"Author - Some Story 07 of 24",
	"Author - Some Book Part 03 of 12 m4b",
	"Author - Some Book (Part 3 of 12) FLAC",
	"Author - Some Book Chapter 1 of 30 ogg",
}

// fragmentKeepers are releases that look a little like fragments and are not.
var fragmentKeepers = []string{
	"Author - Some Book Part 1 of 2 MP3",
	"Author - Some Book (Part 2 of 2) M4B",
	"Author - Some Book Part 1 of 9 MP3",
	"Author - Catch 22 of Them MP3",
	"Author - 1 of a Kind MP3",
	"Author - Series Book 3 of 12 EPUB",
	"Author - Some Book (Series Book 3 of 10) (retail) (epub)",
	"Author - Some Book Book 3 of 12",
	"Author - Some Book Vol 2 of 10 MP3",
	"Author - Some Book Chapter 12 epub",
	"Author - Some Book 3 of 12 (retail) (epub)",
	"Author - Some Book Part 4 of 12 PDF",
	"Author - Catch22 MP3",
	"Author - Catch 22 (Unabridged) MP3",
	"Author - Some Book - Chapter One MP3",
	"Author - The Chronicles of Narnia (Unabridged) MP3",
	"Author - Sapiens 2014 MP3",
	"Author - Some Book 64 kbps MP3",
	"Author - Some Book (Unabridged) 12 hours MP3",
	"Terry Goodkind - Faith of the Fallen (Unabridged) M4B",
}

func TestUsenetJunkDropsPerChapterFragments(t *testing.T) {
	out := filterUsenetJunk(toResults(append(append([]string{}, fragmentPosts...), fragmentKeepers...)...))
	for _, j := range fragmentPosts {
		if contains(out, j) {
			t.Errorf("fragment slipped through: %q", j)
		}
	}
	for _, k := range fragmentKeepers {
		if !contains(out, k) {
			t.Errorf("keeper was dropped: %q", k)
		}
	}
}

func TestUsenetJunkDebugReasons(t *testing.T) {
	const old = "looks like a raw per-article posting (RAR/PAR2/SFV/yEnc/[N/M])"
	in := toResults(
		"Terry Goodkind - SOT06 Faith of the Fallen Ch63 of 71.mp3",
		"Post [1/5] - \"chunk\" yEnc",
		"Terry Goodkind - Faith of the Fallen (Unabridged) M4B",
	)
	kept, dropped := filterUsenetJunkDebug(in)
	if len(kept) != 1 || len(dropped) != 2 {
		t.Fatalf("kept=%d dropped=%d, want 1 and 2", len(kept), len(dropped))
	}
	byTitle := map[string]string{}
	for _, d := range dropped {
		if d.Stage != "usenet-junk" {
			t.Errorf("stage = %q, want usenet-junk", d.Stage)
		}
		byTitle[d.Title] = d.Reason
	}
	if got := byTitle["Terry Goodkind - SOT06 Faith of the Fallen Ch63 of 71.mp3"]; got != reasonChapterFragment {
		t.Errorf("fragment reason = %q, want %q", got, reasonChapterFragment)
	}
	if got := byTitle["Post [1/5] - \"chunk\" yEnc"]; got != old {
		t.Errorf("existing junk reason changed: %q", got)
	}
}

// TestUsenetJunkFragmentReachesEveryEntrypoint runs the production entry points
// against a fake indexer serving the Goodkind fragment next to the whole book.
func TestUsenetJunkFragmentReachesEveryEntrypoint(t *testing.T) {
	right := "Terry Goodkind - Faith of the Fallen (Unabridged) M4B"
	wrong := "Terry Goodkind - SOT06 Faith of the Fallen Ch63 of 71.mp3"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(feedOf(wrong, right)))
	}))
	defer srv.Close()
	idxs := []models.Indexer{{ID: 1, Name: "test", URL: srv.URL, Enabled: true, Categories: []int{3030}}}
	crit := MatchCriteria{Title: "Faith of the Fallen", Author: "Terry Goodkind", MediaType: models.MediaTypeAudiobook}
	ctx := context.Background()

	plain := newTestSearcher().SearchBook(ctx, idxs, crit)
	debugged, dbg := newTestSearcher().SearchBookWithDebug(ctx, idxs, crit)
	for name, got := range map[string][]string{
		"SearchBook":          resultTitles(plain),
		"SearchBookWithDebug": resultTitles(debugged),
	} {
		if len(got) != 1 || got[0] != right {
			t.Errorf("%s kept %v, want only %q", name, got, right)
		}
	}
	if dbg.Pipeline.RawCount != 2 || dbg.Pipeline.AfterUsenetJunk != 1 {
		t.Errorf("pipeline raw=%d afterJunk=%d, want 2 and 1", dbg.Pipeline.RawCount, dbg.Pipeline.AfterUsenetJunk)
	}
}
