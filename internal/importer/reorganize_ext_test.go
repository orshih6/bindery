package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vavallee/bindery/internal/models"
)

func writeBytesAt(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func setTemplate(t *testing.T, env reorgEnv, ctx context.Context, tmpl string) {
	t.Helper()
	if err := env.s.settings.Set(ctx, "naming.bookTemplate", tmpl); err != nil {
		t.Fatal(err)
	}
}

// The #3217 repro: a template typo strips the extension, and fixing the
// template afterwards must bring it back, not propose "Title.".
func TestReorganize_RecoversExtensionFromContent(t *testing.T) {
	env, libraryDir, _, ctx := reorgFixture(t)
	book := env.seed(t, ctx, "Jane Doe", "My Book")
	lost := filepath.Join(libraryDir, "Jane Doe", "My Bookepub")
	writeBytesAt(t, lost, epubBytes())
	if err := env.books.AddBookFile(ctx, book.ID, models.MediaTypeEbook, lost); err != nil {
		t.Fatal(err)
	}
	setTemplate(t, env, ctx, "{Author}/{Title}.{ext}")

	moves, err := env.s.PreviewReorganizeBook(ctx, book.ID)
	if err != nil || len(moves) != 1 {
		t.Fatalf("preview: %v %v", moves, err)
	}
	want := filepath.Join(libraryDir, "Jane Doe", "My Book.epub")
	if moves[0].Status != ReorgStatusMove || moves[0].Proposed != want {
		t.Fatalf("got %q %q, want move to %q", moves[0].Status, moves[0].Proposed, want)
	}
	res := env.s.ApplyReorganize(ctx, []int64{moves[0].FileID})
	if len(res) != 1 || res[0].Status != ReorgStatusMoved {
		t.Fatalf("apply = %+v", res)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("repaired file missing: %v", err)
	}
}

// azw3 and cbz cannot be told from mobi or a bare zip by content, so the
// extension comes from the bookRenamed history of the rename that lost it.
func TestReorganize_RecoversExtensionFromHistory(t *testing.T) {
	env, libraryDir, _, ctx := reorgFixture(t)
	book := env.seed(t, ctx, "Jane Doe", "My Book")
	orig := filepath.Join(libraryDir, "old", "My Book.azw3")
	lost := filepath.Join(libraryDir, "Jane Doe", "My Bookazw3")
	writeFileAt(t, lost) // content is not conclusive
	if err := env.books.AddBookFile(ctx, book.ID, models.MediaTypeEbook, lost); err != nil {
		t.Fatal(err)
	}
	setTemplate(t, env, ctx, "{Author}/{Title}.{ext}")
	env.s.createHistoryEvent(ctx, models.HistoryEventBookRenamed, book.Title, &book.ID, map[string]string{
		"from": orig, "to": lost, "format": "ebook",
	})

	moves, err := env.s.PreviewReorganizeBook(ctx, book.ID)
	if err != nil || len(moves) != 1 {
		t.Fatalf("preview: %v %v", moves, err)
	}
	want := filepath.Join(libraryDir, "Jane Doe", "My Book.azw3")
	if moves[0].Status != ReorgStatusMove || moves[0].Proposed != want {
		t.Fatalf("got %q %q (%s), want move to %q", moves[0].Status, moves[0].Proposed, moves[0].Message, want)
	}
}

// With nothing to recover the extension from, no move is proposed or applied.
func TestReorganize_RefusesWhenExtensionCannotBeRecovered(t *testing.T) {
	env, libraryDir, _, ctx := reorgFixture(t)
	book := env.seed(t, ctx, "Jane Doe", "My Book")
	lost := filepath.Join(libraryDir, "Jane Doe", "My Bookepub")
	writeFileAt(t, lost)
	if err := env.books.AddBookFile(ctx, book.ID, models.MediaTypeEbook, lost); err != nil {
		t.Fatal(err)
	}
	setTemplate(t, env, ctx, "{Author}/{Title}.{ext}")

	moves, _ := env.s.PreviewReorganizeBook(ctx, book.ID)
	if len(moves) != 1 || moves[0].Status != ReorgStatusError || moves[0].Proposed != "" {
		t.Fatalf("preview = %+v, want one error with no proposal", moves)
	}
	res := env.s.ApplyReorganize(ctx, []int64{moves[0].FileID})
	if res[0].Status != ReorgStatusError {
		t.Fatalf("apply = %+v, want error", res[0])
	}
	if _, err := os.Stat(lost); err != nil {
		t.Fatalf("file must stay put: %v", err)
	}
}

// The typo itself: {Title}{ext} must not be applied again.
func TestReorganize_RefusesTemplateWithoutDotBeforeExt(t *testing.T) {
	env, libraryDir, _, ctx := reorgFixture(t)
	book := env.seed(t, ctx, "Jane Doe", "My Book")
	old := filepath.Join(libraryDir, "misc", "x.epub")
	writeFileAt(t, old)
	if err := env.books.AddBookFile(ctx, book.ID, models.MediaTypeEbook, old); err != nil {
		t.Fatal(err)
	}
	setTemplate(t, env, ctx, "{Author}/{Title}{ext}")

	moves, _ := env.s.PreviewReorganizeBook(ctx, book.ID)
	if len(moves) != 1 || moves[0].Status != ReorgStatusError {
		t.Fatalf("preview = %+v, want error", moves)
	}
	if res := env.s.ApplyReorganize(ctx, []int64{moves[0].FileID}); res[0].Status != ReorgStatusError {
		t.Fatalf("apply = %+v, want error", res[0])
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("file must stay put: %v", err)
	}
}

func TestDestPath_ExtensionGuard(t *testing.T) {
	author := &models.Author{Name: "Jane Doe"}
	book := &models.Book{Title: "Dr. Book"}
	dir := t.TempDir()
	epub := filepath.Join(dir, "a")
	writeBytesAt(t, epub, epubBytes())

	// Content supplies the extension for an extensionless source.
	got, err := NewRenamer("{Title}.{ext}").DestPath("/lib", author, book, "", "", epub)
	if err != nil || got != "/lib/Dr. Book.epub" {
		t.Fatalf("got %q %v", got, err)
	}
	// A dotted title does not hide a missing dot before {ext}.
	if _, err := NewRenamer("{Title}{ext}").DestPath("/lib", author, book, "", "", "x.epub"); !errors.Is(err, ErrEbookNoExtension) {
		t.Fatalf("err = %v, want ErrEbookNoExtension", err)
	}
	// No extension anywhere.
	if _, err := NewRenamer("{Title}.{ext}").DestPath("/lib", author, book, "", "", filepath.Join(dir, "none")); !errors.Is(err, ErrEbookNoExtension) {
		t.Fatalf("err = %v, want ErrEbookNoExtension", err)
	}
}

func TestTemplateExtChecks(t *testing.T) {
	for tmpl, want := range map[string]bool{
		"{Title}.{ext}": false, "{Title}{ext}": true, "{A}/{Title} {ext}": true,
		"{Title}{.ext}": false, "{Title}": false,
	} {
		if got := EbookTemplateExtNeedsDot(tmpl); got != want {
			t.Errorf("EbookTemplateExtNeedsDot(%q) = %v, want %v", tmpl, got, want)
		}
	}
	for tmpl, want := range map[string]bool{
		"{Title}.{ext}": true, "{Title}{.ext}": true, "{Title}": false, "{Author}/{Title} ({Year})": false,
	} {
		if got := AudiobookTemplateUsesExt(tmpl); got != want {
			t.Errorf("AudiobookTemplateUsesExt(%q) = %v, want %v", tmpl, got, want)
		}
	}
}
