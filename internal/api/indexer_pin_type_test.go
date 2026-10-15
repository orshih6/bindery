package api

import (
	"net/http"
	"strconv"
	"testing"
)

// An update body with an empty type means the default type (usenet), which is
// what the searcher already reads it as. For an indexer that is already usenet
// it must not count as a change of type, so a pinned client that was switched
// to the other protocol does not block the save, and nothing crashes.
func TestIndexerPin_EmptyTypeInAnUpdateIsNotATypeChange(t *testing.T) {
	f := newPinAPIFixture(t)
	usenetID := strconv.FormatInt(f.usenet.ID, 10)
	created := decodeIndexer(t, f.create(`{"name":"Usenet","url":"http://192.168.1.50:9117/api","type":"newznab","downloadClientId":`+usenetID+`}`))
	f.usenet.Type = "qbittorrent"
	if err := f.clients.Update(t.Context(), f.usenet); err != nil {
		t.Fatal(err)
	}
	rec := f.update(created.ID, `{"type":"","priority":3}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update with an empty type: %d %s", rec.Code, rec.Body.String())
	}
	if got := decodeIndexer(t, rec); got.DownloadClientID == nil || *got.DownloadClientID != f.usenet.ID || got.Priority != 3 {
		t.Errorf("the update changed the wrong things: %+v", got)
	}
}
