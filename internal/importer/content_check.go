package importer

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vavallee/bindery/internal/db"
	"github.com/vavallee/bindery/internal/indexer"
	"github.com/vavallee/bindery/internal/metadata"
	"github.com/vavallee/bindery/internal/models"
	"github.com/vavallee/bindery/internal/notifier"
)

// Post-download content check (#3015, #3246).
//
// The grab time filters judge a release by its NAME, and the import path before
// this looked at the file's format (#1782), a multi-book pack's name (#2276) and
// an EPUB's declared language (#2998), but never at whether the file is the book
// that was grabbed for. A release named for the wanted book can hold another one,
// and then the wrong book sits in the library as imported and is never searched
// for again.
//
// So the importer now compares what the files say about themselves with the
// book, using only metadata already inside them: an EPUB's dc:title and
// dc:creator, an audiobook's album and track tags. It never reads text or
// pages and never writes anything into a file.
//
// Each media type has its own setting, off, warn or block:
//
//   - off: the check does not run, and no file is read for it.
//   - warn (the default): the import proceeds and the bookImported event and
//     webhook carry a contentCheck object saying what was found. A check that
//     is wrong in warn mode costs a note in History, not a refused download.
//   - block: the same path the language and format checks take. The release is
//     blocklisted, the download is blocked with a message naming what was found
//     and what was wanted, and the book stays Wanted. Match to book and manual
//     import skip the check, and Retry import runs it again.
//
// Only a download tied to a book is checked, only on the download import path
// (tryImportInternal), and never for a library scan, a manual import or Match
// to book: a person who picks the files has already decided.

// The setting keys. The api package declares the same literals as
// SettingImportEbookContentCheck and SettingImportAudiobookContentCheck; the
// importer reads them as strings to avoid an import cycle, so keep them in sync.
const settingImportEbookContentCheck = "import.ebook.content_check"

// Values of the content check settings.
const (
	contentCheckOff   = "off"
	contentCheckWarn  = "warn"
	contentCheckBlock = "block"
)

// contentCheckMode reads a content check setting. Anything other than "off" or
// "block", including a value nobody set, is warn: the setting is validated when
// saved, so an odd value here is a stale row, and the safe reading of it is the
// one that never refuses a download.
func (s *Scanner) contentCheckMode(ctx context.Context, key string) string {
	if s.settings == nil {
		return contentCheckWarn
	}
	setting, err := s.settings.Get(ctx, key)
	if err != nil || setting == nil {
		return contentCheckWarn
	}
	switch v := strings.ToLower(strings.TrimSpace(setting.Value)); v {
	case contentCheckOff, contentCheckBlock:
		return v
	}
	return contentCheckWarn
}

// WithAuthorAliases wires the author alias repo the ebook content check uses to
// accept a pen name or house name as the author ("Franklin W. Dixon" for Leslie
// McFarlane). Without it the check compares the author's own name only.
func (s *Scanner) WithAuthorAliases(aliases *db.AuthorAliasRepo) *Scanner {
	s.authorAliases = aliases
	return s
}

// contentCheckResult is what a content check found. The zero value means the
// check did not run or found nothing wrong.
type contentCheckResult struct {
	mode string
	// flagged is true when the files look like another book.
	flagged bool
	// data is the contentCheck object attached to the bookImported event and
	// webhook in warn mode, and to the failure event and webhook in block mode.
	data map[string]any
	// reason is the message a blocked download shows.
	reason string
}

// blocks reports whether the result refuses the download.
func (r contentCheckResult) blocks() bool { return r.flagged && r.mode == contentCheckBlock }

// compilationWordRe matches the words a compilation names itself with that the
// catalogue's bundle detection does not treat as a bundle on its own: "The
// Complete Sherlock Holmes", "Collected Stories", "An Anthology", "Best of Poe".
var compilationWordRe = regexp.MustCompile(`(?i)\b(?:complete|collected|anthology|anthologies|collection|collections|omnibus|treasury|best of|short stories)\b`)

// bookIsCompilation reports whether a wanted book is a box set, omnibus,
// anthology or pack. Its files legitimately carry the titles of other works, so
// there is nothing to compare. Skipping on a word too many only costs a missed
// mismatch, which is the safe direction.
func bookIsCompilation(title string) bool {
	return metadata.IsBundleTitle(title) || indexer.MultiBookPackMarker(title) != "" || compilationWordRe.MatchString(title)
}

// splitCreators breaks a dc:creator string that credits several people into one
// entry per person where it can tell, and always keeps the whole string as an
// entry too, so a "Last, First" name stays readable.
func splitCreators(creator string) []string {
	creator = strings.TrimSpace(creator)
	if creator == "" {
		return nil
	}
	out := []string{creator}
	repl := strings.NewReplacer(";", "\n", " and ", "\n", " & ", "\n", " | ", "\n", " / ", "\n")
	for _, part := range strings.Split(repl.Replace(creator), "\n") {
		if p := strings.TrimSpace(part); p != "" && p != creator {
			out = append(out, p)
		}
	}
	return out
}

// ebookTitleMatches reports whether an EPUB's dc:title is compatible with the
// book's title (see contentTitleSupports). An empty title on either side is
// compatible: a file with no title has not said it is another book.
func ebookTitleMatches(bookTitle, fileTitle string) bool {
	if strings.TrimSpace(fileTitle) == "" || strings.TrimSpace(bookTitle) == "" {
		return true
	}
	return contentTitleSupports(bookTitle, fileTitle, true)
}

// ebookAuthorMatches reports whether an EPUB's dc:creator names one of the
// names the book's author goes by. It uses the importer's author matcher in both
// directions, so "Tolkien, J. R. R." meets "J.R.R. Tolkien" and a credit of
// "James Patterson and Maxine Paetro" meets "James Patterson". An empty creator,
// or a book with no author on record, is compatible: nothing was said.
func ebookAuthorMatches(names []string, creator string) bool {
	creators := splitCreators(creator)
	if len(creators) == 0 || len(names) == 0 {
		return true
	}
	for _, c := range creators {
		for _, n := range names {
			if strings.TrimSpace(n) == "" {
				continue
			}
			if authorMatch(n, c) || authorMatch(c, n) {
				return true
			}
		}
	}
	return false
}

// ebookDefiniteMismatch reports whether an EPUB is plausibly another book: its
// title does not match the book's AND its author does not match any name the
// book's author goes by. Requiring both keeps false positives low, since a real
// book with odd metadata usually still gets one of the two right. Missing
// metadata never counts.
func ebookDefiniteMismatch(bookTitle string, names []string, fileTitle, fileCreator string) bool {
	return !ebookTitleMatches(bookTitle, fileTitle) && !ebookAuthorMatches(names, fileCreator)
}

// authorNamesFor returns the names the book's author is known by: the name on
// record plus every alias.
func (s *Scanner) authorNamesFor(ctx context.Context, author *models.Author) []string {
	if author == nil {
		return nil
	}
	names := []string{author.Name}
	if s.authorAliases == nil {
		return names
	}
	aliases, err := s.authorAliases.ListByAuthor(ctx, author.ID)
	if err != nil {
		slog.Warn("content check: could not load author aliases", "authorID", author.ID, "error", err)
		return names
	}
	for _, a := range aliases {
		names = append(names, a.Name)
	}
	return names
}

// withContentCheck returns a bookImported history payload with the contentCheck
// object added when there is one. With none it carries exactly the input, so an
// import nobody flagged writes the same row as before.
func withContentCheck(data map[string]string, contentCheck map[string]any) map[string]any {
	out := make(map[string]any, len(data)+1)
	for k, v := range data {
		out[k] = v
	}
	if contentCheck != nil {
		out["contentCheck"] = contentCheck
	}
	return out
}

// withContentCheckPayload adds the contentCheck object to a webhook payload.
func withContentCheckPayload(payload map[string]interface{}, contentCheck map[string]any) map[string]interface{} {
	if contentCheck != nil {
		payload["contentCheck"] = contentCheck
	}
	return payload
}

// safeFileText cleans text that came out of a downloaded file or a metadata
// provider before it goes into a message, a History row or a webhook: control
// and invisible characters are removed, the length is capped, and chat markup
// (@everyone, <!channel>, [text](url), bare URLs) is neutralised, since the
// failure message is sent to chat webhooks as is. It changes nothing a person
// reads in an ordinary title.
func safeFileText(s string) string { return notifier.SafeText(s, 200) }

// describeEpub renders what a file says about itself for a message:
// `"Mary and Mr Eliot" by Mary Trevelyan`.
func describeEpub(title, author string) string {
	title, author = strings.TrimSpace(title), strings.TrimSpace(author)
	switch {
	case title != "" && author != "":
		return fmt.Sprintf("%q by %s", title, author)
	case title != "":
		return fmt.Sprintf("%q", title)
	case author != "":
		return "a book by " + author
	}
	return "no title or author"
}

// checkEbookContent compares the title and author inside each EPUB of a
// download with the book it was grabbed for (#3015).
//
// A release is flagged only when every EPUB it holds that could be read is a
// definite mismatch: one EPUB that agrees, or that carries no metadata to
// disagree with, keeps the release, since a stray extra file should not condemn
// the real one. Only EPUB is read; every other format is unchanged.
//
// A wanted box set or omnibus is not compared, and the check does nothing when
// the setting is off.
func (s *Scanner) checkEbookContent(ctx context.Context, book *models.Book, author *models.Author, files []string) contentCheckResult {
	mode := s.contentCheckMode(ctx, settingImportEbookContentCheck)
	if mode == contentCheckOff || book == nil || bookIsCompilation(book.Title) {
		return contentCheckResult{}
	}
	var epubs []string
	for _, f := range files {
		if IsEpubFile(f) {
			epubs = append(epubs, f)
		}
	}
	if len(epubs) == 0 {
		return contentCheckResult{}
	}
	if s.onContentCheck != nil {
		s.onContentCheck("ebook")
	}
	names := s.authorNamesFor(ctx, author)

	type found struct{ file, title, author string }
	var read int
	var mismatches []found
	for _, f := range epubs {
		meta, err := ReadEpubMetadata(f)
		if err != nil {
			continue
		}
		read++
		if ebookDefiniteMismatch(book.Title, names, meta.Title, meta.Author) {
			mismatches = append(mismatches, found{safeFileText(filepath.Base(f)), safeFileText(meta.Title), safeFileText(meta.Author)})
		}
	}
	if read == 0 || len(mismatches) < read {
		return contentCheckResult{mode: mode}
	}

	bookAuthor := ""
	if author != nil {
		bookAuthor = safeFileText(author.Name)
	}
	bookTitle := safeFileText(book.Title)
	listed := make([]map[string]string, 0, len(mismatches))
	for _, m := range mismatches {
		listed = append(listed, map[string]string{"file": m.file, "title": m.title, "author": m.author})
	}
	first := mismatches[0]
	extra := ""
	if len(mismatches) > 1 {
		extra = fmt.Sprintf(" (and %d more file(s) like it)", len(mismatches)-1)
	}
	return contentCheckResult{
		mode:    mode,
		flagged: true,
		data: map[string]any{
			"kind":       "ebook",
			"bookTitle":  bookTitle,
			"bookAuthor": bookAuthor,
			"mismatches": listed,
		},
		reason: fmt.Sprintf("the file says it is %s%s, but the book is %q by %s. "+
			"Not imported, and the release was blocklisted so the next search picks another. "+
			"To keep this file anyway, use Match to book in the Queue or manual import; "+
			"to stop this check, set import.ebook.content_check to warn or off",
			describeEpub(first.title, first.author), extra, bookTitle, bookAuthor),
	}
}
