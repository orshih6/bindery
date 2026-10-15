package indexer

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/vavallee/bindery/internal/indexer/newznab"
	"github.com/vavallee/bindery/internal/models"
)

// Trailing title rules (#3268).
//
// The title identity gate (titleIdentityWords, ContainsPhrase) looks only at
// what sits BETWEEN the wanted title's words. It never reads what FOLLOWS
// them, so "Leigh Bardugo - King of Scars 02 - Rule of Wolves" was approved
// for "King of Scars": the title is there, whole, and the release is the next
// book in the series.
//
// These rules read the words after the wanted title and reject on POSITIVE
// evidence only. A false rejection hides a release the user wanted, so every
// signal below needs the release to say something specific, and anything
// unclear is kept.
//
// Signal A, a series number after the title. The release has a number N of 2
// or more directly after the title ("King of Scars 02", "[Rotten School 12]",
// "Series Book 3"), optionally behind a marker word (book, vol, volume, no,
// number, episode, tome, band), and then:
//
//   - when the wanted book's series position is known, N must equal it (or sit
//     inside a stored range such as "1-3"); a different N rejects, with or
//     without words after it;
//   - when the position is not known, N rejects only if a dash follows it and
//     the first significant word after the dash is not release furniture (a
//     format, a year, a bitrate, a language tag, a credit such as "narrated
//     by", the author's own name). "Title 02 - Rule of Wolves" rejects;
//     "Title 02 (Unabridged)", "Title 02" and "Title 02 retail epub" do not.
//
// N of 1 never rejects: "Title 01" is the first book, the usual reading of the
// wanted title. "N of M" counters ("Part 2 of 2", "Book 2 of the Stormlight
// Archive"), four digit years, decimals, bitrates ("64 kbps", "128 MP3"), disc
// counts ("2 CD") and running times ("12 hours") are not series numbers.
//
// Signal B, another of the author's titles after the title. When the caller
// supplies the titles of the author's other books in the library, a release
// whose remainder after the wanted title contains one of them as whole words
// is that other book. A known title BEFORE the wanted one is a series prefix
// ("Belgariad 2 - Queen of Sorcery") and is not read. Only titles of two or
// more significant words count, so a one word title ("Legion", "Wolves")
// cannot match the ordinary words of every release. Titles that contain the
// wanted title ("Dune Messiah" for "Dune") and titles that are themselves box
// sets are skipped, and the whole signal is off when the wanted title is
// itself a set (a set release lists its members).
//
// Both signals need to know where the wanted title ends in the release. They
// find it with the same phrase match the identity gate uses; a release that
// matched only through the unordered keyword fallback has no end to read from,
// and is left alone.

// FilterDebug reasons for the trailing title rules.
const (
	reasonAnotherTitle = "release names another title after this one"
	reasonLaterNumber  = "release names a different series number after this title"
)

// bookContext is what the library knows about the wanted book beyond its
// title and author. The zero value means nothing is known, and every rule that
// needs a fact stays silent without it.
type bookContext struct {
	// seriesPosition is the book's position in its primary series, as stored
	// ("3", "03", "1-3", "2.5", ""). Empty when it has none.
	seriesPosition string
	// otherTitles are the titles of the author's other books in the library.
	otherTitles []string
}

// context is the book context carried by the search criteria.
func (c MatchCriteria) context() bookContext {
	return bookContext{seriesPosition: c.SeriesPosition, otherTitles: c.AuthorTitles}
}

// relTok is one NormalizeRelease token of a release name together with the raw
// text that separated it from the token before it.
type relTok struct {
	text string
	sep  string
}

func isTokenRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '\'' || r == '’'
}

// tokenizeRelease splits raw into the tokens NormalizeRelease(raw) would
// produce, keeping the raw separator in front of each so a dash can be told
// from a space. NormalizeRelease collapses every separator, which is right for
// matching and wrong for reading "02 - Rule of Wolves". ok is false when the
// two tokenisations disagree, in which case the caller must not read anything
// from the result.
func tokenizeRelease(raw string) ([]relTok, bool) {
	runes := []rune(norm.NFC.String(raw))
	var toks []relTok
	last := 0
	for i := 0; i < len(runes); {
		if !isTokenRune(runes[i]) {
			i++
			continue
		}
		j := i
		for j < len(runes) && isTokenRune(runes[j]) {
			j++
		}
		sep := string(runes[last:i])
		for k, w := range strings.Fields(NormalizeRelease(string(runes[i:j]))) {
			if k > 0 {
				sep = " "
			}
			toks = append(toks, relTok{text: w, sep: sep})
		}
		last, i = j, j
	}
	want := strings.Fields(NormalizeRelease(raw))
	if len(want) != len(toks) {
		return nil, false
	}
	for i := range want {
		if want[i] != toks[i].text {
			return nil, false
		}
	}
	return toks, true
}

// dashRunes are the hyphen, en dash and em dash code points, built from rune
// values so the source carries none of them literally.
var dashRunes = string([]rune{'-', 0x2013, 0x2014})

// dashBefore reports whether the separator in front of tok is a dash used as
// punctuation: " - ", "]-", "._-_", or "02-Title" (a number running into a
// word). A bare hyphen between two letters is a compound word and is not.
func dashBefore(prev, tok relTok) bool {
	if !strings.ContainsAny(tok.sep, dashRunes) {
		return false
	}
	if strings.Trim(tok.sep, dashRunes) != "" {
		return true
	}
	if prev.text == "" || tok.text == "" {
		return false
	}
	last := rune(prev.text[len(prev.text)-1])
	first := []rune(tok.text)[0]
	return unicode.IsDigit(last) && unicode.IsLetter(first)
}

// seriesNumberMarkers are the words that may sit between a title and its
// series number: "Title Book 2", "Title Vol. 2". "part" is left out on
// purpose: "Title Part 2" is the second half of one recording as often as it is
// a sequel.
var seriesNumberMarkers = map[string]bool{
	"book": true, "bk": true, "vol": true, "volume": true, "no": true,
	"number": true, "episode": true, "tome": true, "band": true,
}

// numberUnits are the words after a number that make it a count of something
// other than a series position.
var numberUnits = map[string]bool{
	"kbps": true, "kbit": true, "khz": true, "cd": true, "cds": true,
	"disc": true, "discs": true, "dvd": true, "hour": true, "hours": true,
	"hr": true, "hrs": true, "min": true, "mins": true, "minutes": true,
	"mb": true, "gb": true, "k": true,
}

// bitrateNumbers are the two digit bitrates that may be written bare in front
// of a format ("64 MP3").
var bitrateNumbers = map[int]bool{32: true, 40: true, 48: true, 56: true, 64: true, 80: true, 96: true}

// setTitleRe finds a title that names a whole set rather than one book.
var setTitleRe = regexp.MustCompile(`(?i)\b(?:trilogy|tetralogy|quartet|quintet|saga|collection|omnibus|anthology|complete|boxed?\s*set|boxset|books?\s+\d+)\b`)

// isSetTitle reports whether title names a set of books.
func isSetTitle(title string) bool {
	return MultiBookPackMarker(title) != "" || setTitleRe.MatchString(title)
}

type otherTitle struct {
	ident []string
	words []string // ASCII identity words, for a cheap pre-check
}

// trailingRules holds the per search state of the trailing title rules.
type trailingRules struct {
	posLo, posHi int
	posKnown     bool
	others       []otherTitle
	authorSets   [][]string
}

// newTrailingRules prepares the rules for one wanted title. wanted are the
// identity word lists of every reading of the wanted title, used to keep a
// library title that contains the wanted one out of signal B.
func newTrailingRules(title string, bc bookContext, authorSets [][]string, wanted ...[]string) *trailingRules {
	tr := &trailingRules{authorSets: authorSets}
	tr.posLo, tr.posHi, tr.posKnown = parsePosition(bc.seriesPosition)
	if isSetTitle(title) {
		return tr
	}
	seen := map[string]bool{}
	for _, raw := range bc.otherTitles {
		cleaned := newznab.StripFormatQualifiers(newznab.NormalizeQueryTitle(raw))
		if isSetTitle(cleaned) {
			continue
		}
		for _, v := range []string{cleaned, primaryTitle(cleaned), newznab.SeriesPositionTitle(cleaned)} {
			ident := titleIdentityKeywords(v)
			if len(ident) < 2 {
				continue
			}
			key := strings.Join(ident, " ")
			if seen[key] {
				continue
			}
			seen[key] = true
			normTitle := NormalizeRelease(v)
			contained := false
			for _, w := range wanted {
				if len(w) > 0 && ContainsPhrase(normTitle, w) {
					contained = true
					break
				}
			}
			if contained {
				continue
			}
			ot := otherTitle{ident: ident}
			for _, w := range ident {
				if isASCII(w) && !isAllDigits(w) && !newznab.IsVolumeMarker(w) {
					ot.words = append(ot.words, w)
				}
			}
			tr.others = append(tr.others, ot)
		}
	}
	return tr
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

var (
	positionIntRe   = regexp.MustCompile(`^(\d{1,3})(?:\.0+)?$`)
	positionRangeRe = regexp.MustCompile(`^(\d{1,3})\s*[-\x{2013}\x{2014}]\s*(\d{1,3})$`)
)

// parsePosition reads a stored series position. Only a whole number or a
// whole number range is understood; "2.5", "Prequel" and "" are unknown, and
// the rules then judge the release name alone.
func parsePosition(s string) (lo, hi int, ok bool) {
	s = strings.TrimSpace(s)
	if m := positionIntRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, n, true
	}
	if m := positionRangeRe.FindStringSubmatch(s); m != nil {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		if a <= b {
			return a, b, true
		}
	}
	return 0, 0, false
}

// check applies both signals to one release for one reading of the wanted
// title. n is NormalizeRelease(raw); idents are the identity word lists of the
// reading (strict, then the apostrophe elided one). It returns the drop reason,
// or "" when the release is not read as another book.
func (tr *trailingRules) check(raw, n string, idents ...[]string) string {
	end := -1
	for _, ident := range idents {
		if len(ident) == 0 {
			continue
		}
		if loc := phraseRegex(ident).FindStringIndex(n); loc != nil {
			end = loc[1]
			break
		}
	}
	if end < 0 {
		return ""
	}
	toks, ok := tokenizeRelease(raw)
	if !ok {
		return ""
	}
	k := len(strings.Fields(n[:end]))
	if k >= len(toks) {
		return ""
	}
	rest := toks[k:]
	if reason := tr.seriesNumberAfter(toks, k); reason != "" {
		return reason
	}
	return tr.otherTitleIn(rest)
}

// seriesNumberAfter is signal A. toks[k:] is what follows the wanted title.
func (tr *trailingRules) seriesNumberAfter(toks []relTok, k int) string {
	i := k
	if seriesNumberMarkers[toks[i].text] {
		i++
	}
	if i >= len(toks) {
		return ""
	}
	num := toks[i].text
	if len(num) > 2 || !isAllDigits(num) {
		return ""
	}
	n, _ := strconv.Atoi(num)
	if n < 2 {
		return ""
	}
	if i+1 < len(toks) {
		next := toks[i+1].text
		switch {
		case next == "of", isAllDigits(next), numberUnits[next]:
			return ""
		case bitrateNumbers[n] && isFormatToken(next):
			return ""
		}
	}
	if tr.posKnown {
		if n >= tr.posLo && n <= tr.posHi {
			return ""
		}
		return reasonLaterNumber
	}
	if i+1 >= len(toks) || !dashBefore(toks[i], toks[i+1]) {
		return ""
	}
	if tr.startsAnotherTitle(toks[i+1:]) {
		return reasonLaterNumber
	}
	return ""
}

func isFormatToken(tok string) bool {
	for _, f := range formatTokens {
		if tok == f {
			return true
		}
	}
	return false
}

// startsAnotherTitle reports whether the first significant word of toks is
// ordinary title text rather than release furniture. Only the FIRST word is
// read: a release group or a narrator further along must not count.
func (tr *trailingRules) startsAnotherTitle(toks []relTok) bool {
	for _, t := range toks {
		if len(newznab.SigWords(t.text)) == 0 {
			continue
		}
		if creditRoleWords[t.text] {
			return false
		}
		for _, set := range tr.authorSets {
			if benignTrailingToken(t.text, set) {
				return false
			}
		}
		return !benignTrailingToken(t.text, nil)
	}
	return false
}

// otherTitleIn is signal B. rest is what follows the wanted title.
func (tr *trailingRules) otherTitleIn(rest []relTok) string {
	if len(tr.others) == 0 {
		return ""
	}
	words := make(map[string]bool, len(rest))
	texts := make([]string, len(rest))
	for i, t := range rest {
		words[t.text] = true
		texts[i] = t.text
	}
	haystack := strings.Join(texts, " ")
	for _, ot := range tr.others {
		present := true
		for _, w := range ot.words {
			if !words[w] {
				present = false
				break
			}
		}
		if present && ContainsPhrase(haystack, ot.ident) {
			return reasonAnotherTitle
		}
	}
	return ""
}

// AuthorTitlesExcluding returns the titles of books other than selfID, for
// MatchCriteria.AuthorTitles. The caller loads the author's books once per
// search; nothing here touches the database, so the cost per release is a
// string comparison.
func AuthorTitlesExcluding(selfID int64, books []models.Book) []string {
	titles := make([]string, 0, len(books))
	for _, b := range books {
		if b.ID == selfID || strings.TrimSpace(b.Title) == "" {
			continue
		}
		titles = append(titles, b.Title)
	}
	return titles
}
