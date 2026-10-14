package importer

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vavallee/bindery/internal/indexer"
	"github.com/vavallee/bindery/internal/indexer/newznab"
	"github.com/vavallee/bindery/internal/models"
)

// Audiobook half of the post-download content check (#3246). See content_check.go
// for the settings, the three modes and what is skipped.
//
// An audiobook download is imported whole under the grabbed book, so a release
// named for the right book that also holds the tracks of other works lands them
// all there. The files say which work they belong to in their tags, so the
// check reads them.

// settingImportAudiobookContentCheck is the audiobook setting; the api package
// declares the same literal as SettingImportAudiobookContentCheck.
const settingImportAudiobookContentCheck = "import.audiobook.content_check"

// maxContentCheckTagReads bounds how many files one check reads tags from. A
// folder of more files than this is sampled evenly across the sorted file list,
// first and last included, so the cost of one import stays flat however large
// the download is. Real audiobooks run to a few hundred files at most (the
// largest in the reports behind #3246 had 288); a download with more than this
// is a pack, and a sample of it is still decisive, because the flag rules look
// at proportions and at distinct titles, not exact counts.
const maxContentCheckTagReads = 500

// maxContentCheckTitles caps how many other works the contentCheck object and a
// block message list.
const maxContentCheckTitles = 10

// audioWorkCount is one other work found in a download and how many files named it.
type audioWorkCount struct {
	title string
	count int
}

// audiobookContentResult is what the tags of a download say about the book.
type audiobookContentResult struct {
	// tracksRead is the number of files whose tags were read.
	tracksRead int
	// matches counts the files whose work title supports the wanted book.
	matches int
	// conflicts counts the files that carry another volume of the wanted
	// title's own series (rule b).
	conflicts int
	// mismatches lists every other work found, most files first.
	mismatches []audioWorkCount
	// flagged is true when the download looks like it holds other works.
	flagged bool
}

// workTitleKey is the grouping key of a work title: folded, any trailing
// "(Unabridged)" or "[Dramatized]" dropped, leading article removed.
func workTitleKey(title string) string {
	t := title
	for i := 0; i < 3; i++ {
		next := indexer.StripBracketSuffixes(newznab.NormalizeQueryTitle(t))
		if next == t {
			break
		}
		t = next
	}
	if v := titleFoldVariants(t); len(v) > 0 {
		return v[0]
	}
	return ""
}

// usableWorkTitle reports whether a tag value names a work: it is not empty, not
// a generic title such as "Chapter 12" or "Prologue", and not promotional
// material such as an excerpt or a preview.
func usableWorkTitle(title string) bool {
	title = strings.TrimSpace(title)
	return title != "" && !genericTrackTitle(title) && !promoTrackTitle(title) && workTitleKey(title) != ""
}

// evaluateAudiobookContent decides whether the tags of a download say it holds
// works other than wanted (#3246).
//
// The work title of each file is its album tag, which audiobook rips set to the
// book. Only when a file has no album is its track title used, and then only
// when at least two files share it, so unique chapter names are never counted
// as other works. A file with no usable title (nothing tagged, a generic title,
// a promo title) counts for nothing in either direction.
//
// A file supports the wanted book when its work title equals the wanted title
// or one of its variants (see contentTitleForms), or contains it as whole words.
// A file carrying another volume of the wanted title's own series ("NYPD Red 4"
// for "NYPD Red 3") is a conflict and does not support it.
//
// The download is flagged when either:
//
//	(a) at least two files do not support the book, and at least one file does
//	    or at least two distinct other titles appear; or
//	(b) at least two files carry a conflicting volume.
//
// Rule (a) is what lets a download whose every file carries one other title
// pass: a translated or retitled edition of the book looks exactly like that,
// and flagging it would refuse a good download. A wanted box set or omnibus is
// never flagged, since its files legitimately name other works.
func evaluateAudiobookContent(wanted string, tags []AudioTags) audiobookContentResult {
	res := audiobookContentResult{tracksRead: len(tags)}
	if strings.TrimSpace(wanted) == "" || bookIsCompilation(wanted) {
		return res
	}

	// Tags repeat: a folder of 300 files carries a handful of distinct titles,
	// so each distinct string is judged once. keyOf returns the grouping key of
	// a usable work title and "" for anything else.
	keys := make(map[string]string)
	keyOf := func(title string) string {
		if k, ok := keys[title]; ok {
			return k
		}
		k := ""
		// A title in a script the wanted title is not written in (a Russian
		// or Japanese edition of a Latin catalogued book) is the same book in
		// another language, so it names no other work.
		if usableWorkTitle(title) && !nonLatinAgainstLatin(wanted, title) {
			k = workTitleKey(title)
		}
		keys[title] = k
		return k
	}

	works := make([]string, len(tags))
	trackKeys := make([]string, len(tags))
	trackCounts := make(map[string]int)
	for i, t := range tags {
		if album := strings.TrimSpace(t.Album); album != "" {
			// An album tag decides the file's work. A generic or promo album
			// leaves the file without a title rather than falling back to the
			// track title, which names a chapter, not a book.
			if keyOf(album) != "" {
				works[i] = album
			}
			continue
		}
		if title := strings.TrimSpace(t.Title); keyOf(title) != "" {
			k := keyOf(title)
			trackKeys[i] = k
			trackCounts[k]++
		}
	}
	for i, t := range tags {
		if works[i] == "" && trackKeys[i] != "" && trackCounts[trackKeys[i]] >= 2 {
			works[i] = strings.TrimSpace(t.Title)
		}
	}

	type verdict struct{ supports, conflict bool }
	verdicts := make(map[string]verdict)
	type group struct {
		title string
		count int
	}
	others := make(map[string]*group)
	otherFiles := 0
	for _, w := range works {
		if w == "" {
			continue
		}
		k := keyOf(w)
		v, seen := verdicts[k]
		if !seen {
			v.conflict = otherVolumeOfSameWork(wanted, w)
			v.supports = !v.conflict && contentTitleSupports(wanted, w, false)
			verdicts[k] = v
		}
		switch {
		case v.supports:
			res.matches++
		default:
			otherFiles++
			if v.conflict {
				res.conflicts++
			}
			g := others[k]
			if g == nil {
				g = &group{title: w}
				others[k] = g
			}
			g.count++
		}
	}
	for _, g := range others {
		res.mismatches = append(res.mismatches, audioWorkCount{title: g.title, count: g.count})
	}
	sort.Slice(res.mismatches, func(i, j int) bool {
		if res.mismatches[i].count != res.mismatches[j].count {
			return res.mismatches[i].count > res.mismatches[j].count
		}
		return res.mismatches[i].title < res.mismatches[j].title
	})

	ruleA := otherFiles >= 2 && (res.matches >= 1 || len(others) >= 2)
	ruleB := res.conflicts >= 2
	res.flagged = ruleA || ruleB
	return res
}

// data builds the contentCheck object for an audiobook: how many tracks were
// read, how many support the book, and the other works found, capped at
// maxContentCheckTitles. The text comes from files and a metadata provider, so
// it is cleaned the way every other outbound string here is (safeFileText).
func (r audiobookContentResult) data(wantedTitle, wantedAuthor string, sampled bool) map[string]any {
	listed := make([]map[string]any, 0, maxContentCheckTitles)
	for i, m := range r.mismatches {
		if i >= maxContentCheckTitles {
			break
		}
		listed = append(listed, map[string]any{"title": safeFileText(m.title), "count": m.count})
	}
	d := map[string]any{
		"kind":       "audiobook",
		"bookTitle":  safeFileText(wantedTitle),
		"bookAuthor": safeFileText(wantedAuthor),
		"tracksRead": r.tracksRead,
		"matches":    r.matches,
		"mismatches": listed,
	}
	if more := len(r.mismatches) - len(listed); more > 0 {
		d["moreTitles"] = more
	}
	if sampled {
		d["sampled"] = true
	}
	return d
}

// found renders the other works for a block message: "Corralled (9), Coming
// Home to Myself (8)", at most five, with a count of the rest.
func (r audiobookContentResult) found() string {
	const shown = 5
	parts := make([]string, 0, shown+1)
	for i, m := range r.mismatches {
		if i >= shown {
			parts = append(parts, fmt.Sprintf("and %d more", len(r.mismatches)-shown))
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%d)", safeFileText(m.title), m.count))
	}
	return strings.Join(parts, ", ")
}

// audioTags reads one file's tags for the content check, through ReadAudioTags
// unless a test replaced it.
func (s *Scanner) audioTags(path string) (AudioTags, error) {
	if s.readTags != nil {
		return s.readTags(path)
	}
	return ReadAudioTags(path)
}

// sampleEvenly picks n of the files, spread across the list with the first and
// last always included. n must be at least 2 and below len(files).
func sampleEvenly(files []string, n int) []string {
	out := make([]string, 0, n)
	last := len(files) - 1
	for i := 0; i < n; i++ {
		out = append(out, files[i*last/(n-1)])
	}
	return out
}

// checkAudiobookContent reads the tags of the audio files in a download, once,
// and decides whether they hold works other than the book (#3246).
//
// The file list is the one the importer already walked, and tags are read only
// through ReadAudioTags (itself size capped, #2989), so the check opens no path
// of its own. It reads at most maxContentCheckTagReads files, sequentially, and
// stops reading when the import's context ends. Each read is a bounded header
// read, not a decode of the audio.
//
// It does nothing when the setting is off, when the book is a box set or
// omnibus, or when the download holds no audio file.
func (s *Scanner) checkAudiobookContent(ctx context.Context, book *models.Book, author *models.Author, files []string) contentCheckResult {
	mode := s.contentCheckMode(ctx, settingImportAudiobookContentCheck)
	if mode == contentCheckOff || book == nil || bookIsCompilation(book.Title) {
		return contentCheckResult{}
	}
	audio := make([]string, 0, len(files))
	for _, f := range files {
		if IsAudioTagFile(f) {
			audio = append(audio, f)
		}
	}
	if len(audio) == 0 {
		return contentCheckResult{}
	}
	if s.onContentCheck != nil {
		s.onContentCheck("audiobook")
	}
	sampled := false
	if len(audio) > maxContentCheckTagReads {
		audio = sampleEvenly(audio, maxContentCheckTagReads)
		sampled = true
	}
	tags := make([]AudioTags, 0, len(audio))
	for _, f := range audio {
		if ctx.Err() != nil {
			break
		}
		t, err := s.audioTags(f)
		if err != nil {
			continue
		}
		tags = append(tags, t)
	}
	res := evaluateAudiobookContent(book.Title, tags)
	if !res.flagged {
		return contentCheckResult{mode: mode}
	}
	bookAuthor := ""
	if author != nil {
		bookAuthor = author.Name
	}
	return contentCheckResult{
		mode:    mode,
		flagged: true,
		data:    res.data(book.Title, bookAuthor, sampled),
		reason: fmt.Sprintf("the files contain %s, but the book is %q (%d of %d tracks match). "+
			"Not imported, and the release was blocklisted so the next search picks another. "+
			"To keep this folder anyway, use Match to book in the Queue or manual import; "+
			"to stop this check, set import.audiobook.content_check to warn or off",
			res.found(), safeFileText(book.Title), res.matches, res.tracksRead),
	}
}
