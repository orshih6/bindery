package indexer

import (
	"regexp"
	"strconv"
	"strings"
)

// Per-chapter fragments (#3268, part 3).
//
// Some usenet posts carry one chapter or one slice of an audiobook, named with
// a textual counter rather than the "[NN/MM]" bracket usenetJunkRe knows:
// "Terry Goodkind - SOT06 Faith of the Fallen Ch63 of 71.mp3" (43 MB). Grabbing
// one yields a few minutes of a book that Bindery then tries to import as the
// whole thing.
//
// A title is a fragment when it carries either of two counters:
//
//   - a chapter marker, "Ch", "Ch." or "Chapter", directly followed by a number
//     ("Ch63", "Ch. 5", "Chapter 12"); or
//   - "NN of MM" with MM of 10 or more ("Ch63 of 71", "12 of 40", "Part 3 of
//     12"), unless the word before NN is one that numbers a book in a series
//     ("Book 3 of 12", "Vol 2 of 10").
//
// and it is dropped only when it also names an audio container (mp3, m4a, m4b,
// flac, ogg, aac, wav, wma, opus) or names no format at all. A title that names
// an ebook format is left alone: "Series Book 3 of 12 EPUB" and "Chapter 12
// epub" are books.
//
// What this deliberately does not drop:
//
//   - "Part 1 of 2", "Part 2 of 2" and the like. MM under 10 is the shape of
//     one recording split into a few parts, the normal form of a long
//     audiobook, and the multi-book pack code (pack.go) already makes the same
//     call for "part".
//   - "Catch 22 of Them", "1 of a Kind": MM must be digits, and "Catch22"
//     does not hold a chapter marker because "ch" must start a word.
//
// Known limits: a title that really contains "NN of MM" with MM 10 or more and
// no ebook format ("12 of 56 Stories") is dropped, and a fragment posted as
// "Part 3 of 8" is kept because MM is under 10.

// reasonChapterFragment is the FilterDebug reason for a per-chapter fragment.
const reasonChapterFragment = "looks like one chapter or part of a larger post (Ch NN, NN of MM)"

var (
	chapterMarkerRe = regexp.MustCompile(`(?i)\b(?:ch|chapter)\.?\s*\d{1,3}\b`)
	nOfMRe          = regexp.MustCompile(`(?i)\b(\d{1,3})\s*of\s*(\d{2,3})\b`)
	audioFormatRe   = regexp.MustCompile(`(?i)\b(?:mp3|m4a|m4b|flac|ogg|aac|wav|wma|opus)\b`)
	ebookFormatRe   = regexp.MustCompile(`(?i)\b(?:epub3?|kepub|azw3?|mobi|pdf|djvu|cbr|cbz|fb2|lit|prc|rtf|txt|doc|docx|kfx|ebook|html?)\b`)
	// precedingWordRe captures the last word before an index.
	precedingWordRe = regexp.MustCompile(`(?i)([a-z]+)\.?\s*#?\s*$`)
)

// seriesCountWords are the words that, directly before "NN of MM", say NN is a
// book's place in a series and not a chapter.
var seriesCountWords = map[string]bool{
	"book": true, "books": true, "bk": true, "vol": true, "volume": true,
	"series": true, "no": true, "number": true, "novel": true,
	"tome": true, "band": true, "season": true, "episode": true, "issue": true,
	"disc": true, "cd": true,
}

// isChapterFragment reports whether title is a per-chapter or per-slice post,
// as described above.
func isChapterFragment(title string) bool {
	// An underscore is a word character to a regexp but a space to a release
	// name ("Author_Title_Ch63_of_71_mp3").
	title = strings.ReplaceAll(title, "_", " ")
	counter := chapterMarkerRe.MatchString(title)
	if !counter {
		for _, m := range nOfMRe.FindAllStringSubmatchIndex(title, -1) {
			total, _ := strconv.Atoi(title[m[4]:m[5]])
			if total < 10 {
				continue
			}
			before := strings.TrimRight(title[:m[0]], " ")
			if strings.HasSuffix(before, "#") {
				continue
			}
			if w := precedingWordRe.FindStringSubmatch(title[:m[0]]); w != nil && seriesCountWords[strings.ToLower(w[1])] {
				continue
			}
			counter = true
			break
		}
	}
	if !counter {
		return false
	}
	return audioFormatRe.MatchString(title) || !ebookFormatRe.MatchString(title)
}

// usenetJunkReason returns why title is dropped by the usenet junk filter, or
// "" when it is kept. filterUsenetJunk and filterUsenetJunkDebug share it so the
// pipeline and the debug view cannot disagree.
func usenetJunkReason(title string) string {
	if usenetJunkRe.MatchString(title) {
		return reasonRawPosting
	}
	if isChapterFragment(title) {
		return reasonChapterFragment
	}
	return ""
}

// reasonRawPosting is the FilterDebug reason for a raw per-article posting.
const reasonRawPosting = "looks like a raw per-article posting (RAR/PAR2/SFV/yEnc/[N/M])"
