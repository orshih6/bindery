package indexer

import (
	"regexp"
	"strconv"
	"strings"
)

// The main title reading (#3268, part 2).
//
// A "Main: Subtitle" book is also searched under its main title alone, because
// releases drop the subtitle ("The Hobbit" for "The Hobbit: Or There and Back
// Again"). That reading used to be judged only on the title words and the
// volume markers, so "Legion (2012)" satisfied "Legion: Skin Deep" and "The
// High Druid of Shannara 3 - Straken" satisfied "High Druid of Shannara: Jarka
// Ruus". Three rules now sit on top of it. All of them apply only to a release
// that carries the main title and NOT the subtitle: a release that names the
// subtitle too ("Prelude to Foundation") is not a main title only release.
//
//  1. The trailing title rules (series number, another library title) read
//     what follows the main title, exactly as they do for the full title.
//  2. When some other result in the same search carries the FULL title and is
//     approved on it, a main title only release is its sibling and is dropped.
//     Only when nothing carries the full title is the main title reading used
//     at all. The exception is a subtitle that only numbers the book ("Dune:
//     Part One", "Foo: I", "Foo: 1"), because releases drop such a subtitle
//     and keep the main title.
//  3. A main title only release is dropped when the wanted book is a later
//     entry that the main title alone cannot be: the stored series position is
//     2 or more, the subtitle itself numbers the book 2 or more ("Dune: Part
//     Two"), or the main title is itself the title of another of the author's
//     books in the library ("Legion" for "Legion: Skin Deep"). The release is
//     kept if it carries the wanted number right after the main title ("Legion
//     2"), because then it names the book.
//
// A lone main title release with no position, no library title to compare it
// with, and no full title in the batch stays approved: nothing in the release
// or the library says it is another book. That is the rule's known limit.

// FilterDebug reasons for the main title reading.
const (
	reasonMainTitleOnlyFullFound = "release carries only the main title, and another result carries the full title"
	reasonMainTitleOnlySequel    = "release carries only the main title, but this book is a later entry or the main title is another of the author's books"
)

// subtitleInfo describes the "Main: Subtitle" shape of the wanted title.
type subtitleInfo struct {
	has        bool
	ident      []string // identity words of the subtitle
	seriesLike bool     // the subtitle only numbers the book
	n          int      // the number it names, when seriesLike
}

func newSubtitleInfo(title string) subtitleInfo {
	i := strings.Index(title, ":")
	if i <= 0 {
		return subtitleInfo{}
	}
	sub := strings.TrimSpace(title[i+1:])
	if sub == "" {
		return subtitleInfo{}
	}
	si := subtitleInfo{has: true, ident: titleIdentityKeywords(sub)}
	si.n, si.seriesLike = seriesLikeSubtitle(sub)
	return si
}

// mainTitleOnly judges a release that matched through the main title alone and
// returns the drop reason, or "" to keep it. identity is the release's identity
// words joined; anyFull reports whether another result carries the full title.
func (si subtitleInfo) mainTitleOnly(tr *trailingRules, raw, n, identity string, anyFull func() bool, primary ...[]string) string {
	if !si.has {
		return ""
	}
	// The release names the subtitle as well, so it is not main title only.
	if len(si.ident) > 0 && ContainsPhrase(identity, si.ident) {
		return ""
	}
	// A subtitle that only numbers the book as the first ("Part One", "I")
	// is dropped by releases as a matter of course.
	if si.seriesLike && si.n <= 1 {
		return ""
	}
	if anyFull() {
		return reasonMainTitleOnlyFullFound
	}
	want := 0
	sequel := false
	switch {
	case si.seriesLike:
		want, sequel = si.n, true
	case tr.posKnown && tr.posLo >= 2:
		sequel = true
		if tr.posLo == tr.posHi {
			want = tr.posLo
		}
	default:
		for _, p := range primary {
			if len(p) > 0 && tr.otherFull[strings.Join(p, " ")] {
				sequel = true
				if tr.posKnown && tr.posLo == tr.posHi {
					want = tr.posLo
				}
				break
			}
		}
	}
	if !sequel {
		return ""
	}
	if want > 0 {
		if got, ok := numberAfter(raw, n, primary...); ok && got == want {
			return ""
		}
	}
	return reasonMainTitleOnlySequel
}

var (
	// subtitleMarkers are the words that may precede the number in a subtitle
	// that only numbers the book.
	subtitleMarkers = map[string]bool{
		"book": true, "part": true, "volume": true, "vol": true, "episode": true,
		"ep": true, "chapter": true, "tome": true, "band": true, "no": true,
		"number": true, "issue": true,
	}
	numberWords = map[string]int{
		"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
		"seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12,
	}
	romanRe = regexp.MustCompile(`^(?:M{0,3})(?:CM|CD|D?C{0,3})(?:XC|XL|L?X{0,3})(?:IX|IV|V?I{0,3})$`)
)

// seriesLikeSubtitle reports whether sub only numbers the book, and which
// number: a bare number ("3"), a roman numeral written in capitals ("II"), or a
// marker word and a number, roman numeral or number word ("Book 1", "Part
// One", "Volume 4", "Episode 5"). A roman numeral needs capitals so "Mix" and
// "Civil" are words; a number word needs a marker so "Two" alone is a title.
func seriesLikeSubtitle(sub string) (int, bool) {
	fields := strings.Fields(strings.NewReplacer(".", " ", ",", " ").Replace(sub))
	if len(fields) == 0 || len(fields) > 2 {
		return 0, false
	}
	marked := false
	if len(fields) == 2 {
		if !subtitleMarkers[strings.ToLower(fields[0])] {
			return 0, false
		}
		marked = true
	}
	tok := fields[len(fields)-1]
	if isAllDigits(tok) && len(tok) <= 3 {
		v, _ := strconv.Atoi(tok)
		return v, true
	}
	if marked {
		if v, ok := numberWords[strings.ToLower(tok)]; ok {
			return v, true
		}
	}
	if tok == strings.ToUpper(tok) && romanRe.MatchString(tok) {
		return romanValue(tok), true
	}
	return 0, false
}

func romanValue(s string) int {
	vals := map[byte]int{'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000}
	total := 0
	for i := 0; i < len(s); i++ {
		v := vals[s[i]]
		if i+1 < len(s) && vals[s[i+1]] > v {
			total -= v
		} else {
			total += v
		}
	}
	return total
}
