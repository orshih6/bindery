package downloader

import (
	"path/filepath"
	"strings"

	"github.com/vavallee/bindery/internal/pathmap"
)

// Where a remap candidate came from, for the UI to label.
const (
	RemapSourceDownloadDir = "downloadDir"
	RemapSourceLibrary     = "library"
)

// remapPreviewName is the stand in file name the before and after preview
// shows under the folder, so the join reads as a path to a real item.
const remapPreviewName = "Example Book"

// RemapRoot is one folder on Bindery's own filesystem that a client's
// completed folder could be the same storage as.
type RemapRoot struct {
	Path   string
	Source string
}

// RemapCandidate is one offered right hand side for a path remap, with the
// join previewed. It is information only: it carries no remap string, so
// nothing here can be replayed as a write. The UI composes the remap and
// saves it through the normal download client update route.
type RemapCandidate struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	// Before is a sample path as the client names it, After the same path as
	// Bindery would read it under this candidate.
	Before string `json:"before"`
	After  string `json:"after"`
}

// SupportsRemapAssistant reports whether the remap assistant may be offered
// for a client type: the types CheckCompletedPathVisibility can introspect and
// that report a static completed folder. For every other type there is no
// remote path to seed from, so the assistant does not appear rather than guess.
func SupportsRemapAssistant(clientType string) bool {
	switch clientType {
	case "qbittorrent", "nzbget", "rtorrent":
		return true
	default:
		return false
	}
}

// RemapCandidates previews mapping remotePath onto each root. The preview goes
// through pathmap, the same parser the importer uses, so a Windows drive or
// share on the client side previews exactly as it will resolve. Roots that are
// empty or repeat an earlier one once cleaned are dropped. An unsupported type
// or a blank remote path yields nil.
func RemapCandidates(clientType, remotePath string, roots []RemapRoot) []RemapCandidate {
	remotePath = strings.TrimSpace(remotePath)
	if !SupportsRemapAssistant(clientType) || remotePath == "" {
		return nil
	}
	remote := pathmap.CleanClientPath(remotePath)
	before := pathmap.ClientPathJoin(remote, remapPreviewName)
	var out []RemapCandidate
	seen := map[string]bool{}
	for _, root := range roots {
		if strings.TrimSpace(root.Path) == "" {
			continue
		}
		local := filepath.Clean(strings.TrimSpace(root.Path))
		if seen[local] {
			continue
		}
		seen[local] = true
		after, matched := pathmap.Parse(remote + ":" + local).ApplyMatched(before)
		if !matched {
			continue
		}
		out = append(out, RemapCandidate{Path: local, Source: root.Source, Before: before, After: after})
	}
	return out
}
