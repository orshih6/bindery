package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/vavallee/bindery/internal/models"
)

func deleteBookWithFiles(t *testing.T, h *BookHandler, id int64) {
	t.Helper()
	req := withURLParam(httptest.NewRequest(http.MethodDelete, "/api/v1/book/"+strconv.FormatInt(id, 10)+"?deleteFiles=true", nil), "id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", rec.Code)
	}
}

// TestBookDelete_KeepsFilesUnderAnotherBooksTrackedFolder is #3336 part B.
// A box set registered as a whole folder contains a subfolder that another
// live book tracks as its own folder. Deleting the box set with its files must
// remove only what nobody else owns: the nested folder's files are not an
// exact book_files path, but they sit under a folder another book tracks.
func TestBookDelete_KeepsFilesUnderAnotherBooksTrackedFolder(t *testing.T) {
	h, books, _, author, ctx := bookFixture(t)
	root := t.TempDir()
	boxDir := filepath.Join(root, "Harry Potter 1-4")
	ownDisc := mustWrite(t, filepath.Join(boxDir, "Book 1", "01.mp3"))
	nestedDir := filepath.Join(boxDir, "Book 2")
	nestedTrack := mustWrite(t, filepath.Join(nestedDir, "01.mp3"))

	box := &models.Book{ForeignID: "B-BOX", AuthorID: author.ID, Title: "Box", SortTitle: "box", Status: "wanted", Genres: []string{}, MetadataProvider: "openlibrary", Monitored: true}
	child := &models.Book{ForeignID: "B-CHILD", AuthorID: author.ID, Title: "Child", SortTitle: "child", Status: "wanted", Genres: []string{}, MetadataProvider: "openlibrary", Monitored: true}
	for _, b := range []*models.Book{box, child} {
		if err := books.Create(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	if err := books.AddBookFile(ctx, box.ID, models.MediaTypeAudiobook, boxDir); err != nil {
		t.Fatal(err)
	}
	if err := books.AddBookFile(ctx, child.ID, models.MediaTypeAudiobook, nestedDir); err != nil {
		t.Fatal(err)
	}

	deleteBookWithFiles(t, h, box.ID)

	if !exists(nestedTrack) {
		t.Errorf("a file under another live book's tracked folder must survive: %s", nestedTrack)
	}
	if exists(ownDisc) {
		t.Errorf("the deleted book's own file should be removed: %s", ownDisc)
	}
}

// TestBookDelete_RefusesFileInsideAnotherBooksTrackedFolder: deleting a book
// whose own tracked file lies inside a folder another live book tracks must
// leave the file, because the folder's owner covers it.
func TestBookDelete_RefusesFileInsideAnotherBooksTrackedFolder(t *testing.T) {
	h, books, _, author, ctx := bookFixture(t)
	root := t.TempDir()
	boxDir := filepath.Join(root, "Harry Potter 1-4")
	track := mustWrite(t, filepath.Join(boxDir, "Book 2", "01.mp3"))

	box := &models.Book{ForeignID: "B-BOX2", AuthorID: author.ID, Title: "Box", SortTitle: "box", Status: "wanted", Genres: []string{}, MetadataProvider: "openlibrary", Monitored: true}
	child := &models.Book{ForeignID: "B-CHILD2", AuthorID: author.ID, Title: "Child", SortTitle: "child", Status: "wanted", Genres: []string{}, MetadataProvider: "openlibrary", Monitored: true}
	for _, b := range []*models.Book{box, child} {
		if err := books.Create(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	if err := books.AddBookFile(ctx, box.ID, models.MediaTypeAudiobook, boxDir); err != nil {
		t.Fatal(err)
	}
	if err := books.AddBookFile(ctx, child.ID, models.MediaTypeAudiobook, track); err != nil {
		t.Fatal(err)
	}

	deleteBookWithFiles(t, h, child.ID)

	if !exists(track) {
		t.Errorf("a file inside another live book's tracked folder must survive: %s", track)
	}
}
