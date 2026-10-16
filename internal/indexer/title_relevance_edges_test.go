package indexer

import (
	"reflect"
	"testing"
)

func TestNewSubtitleInfo(t *testing.T) {
	cases := []struct {
		title      string
		has        bool
		seriesLike bool
		n          int
	}{
		{"Plain Title", false, false, 0},
		{": Leading Colon", false, false, 0},
		{"Main:", false, false, 0},
		{"Main:    ", false, false, 0},
		{"Main: Book 2", true, true, 2},
		{"Main: A Real Subtitle", true, false, 0},
	}
	for _, tc := range cases {
		si := newSubtitleInfo(tc.title)
		if si.has != tc.has || si.seriesLike != tc.seriesLike || si.n != tc.n {
			t.Errorf("newSubtitleInfo(%q) = %+v, want has=%v seriesLike=%v n=%d", tc.title, si, tc.has, tc.seriesLike, tc.n)
		}
	}
	if si := newSubtitleInfo("Main: A Real Subtitle"); len(si.ident) == 0 {
		t.Error("a real subtitle must carry identity words")
	}
}

func TestMainTitleOnly_ReleaseNamingTheSubtitleIsKept(t *testing.T) {
	si := newSubtitleInfo("Main: A Real Subtitle")
	anyFull := func(string) bool { t.Fatal("anyFull consulted although the release names the subtitle"); return false }
	got := si.mainTitleOnly(nil, "Main A Real Subtitle epub", "main a real subtitle epub", "main real subtitle", anyFull)
	if got != "" {
		t.Fatalf("got %q, want keep", got)
	}
}

func TestMainTitleOnly_FullTitleElsewhereInBatchDropsTheSibling(t *testing.T) {
	si := newSubtitleInfo("Main: A Real Subtitle")
	var asked string
	anyFull := func(mt string) bool { asked = mt; return true }
	got := si.mainTitleOnly(nil, "Main epub", "main epub", "main", anyFull)
	if got != reasonMainTitleOnlyFullFound {
		t.Fatalf("got %q, want %q", got, reasonMainTitleOnlyFullFound)
	}
	if asked != "ebook" {
		t.Fatalf("anyFull asked about %q, want the release's own media type", asked)
	}

	// A release that names no format has no media type to compare, so the
	// batch rule cannot apply and it is kept.
	anyFull = func(string) bool { t.Fatal("anyFull consulted for a release with no media type"); return false }
	if got := si.mainTitleOnly(nil, "Main", "main", "main", anyFull); got != "" {
		t.Fatalf("got %q, want keep", got)
	}
}

func TestFullTitleCounts(t *testing.T) {
	ident := []string{"main", "title"}
	authors := [][]string{{"jane", "austen"}}
	count := func(raw string) bool {
		return fullTitleCounts(raw, NormalizeRelease(raw), authors, ident)
	}
	cases := []struct {
		raw  string
		want bool
	}{
		{"Main Title", true},
		{"Main Title epub", true},
		{"Main Title - Jane Austen epub", true},
		{"Main Title Summary epub", false},
		{"Main Title Study Guide", false},
		{"Main Title: Insights and Key Takeaways", false},
		{"Unrelated Words epub", false},
		{"Main Title and Some Unexplained Words", false},
	}
	for _, tc := range cases {
		if got := count(tc.raw); got != tc.want {
			t.Errorf("fullTitleCounts(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
	if fullTitleCounts("Main Title epub", NormalizeRelease("Main Title epub"), authors) {
		t.Error("with no identity phrase the title cannot be located and must not count")
	}
}

func TestNumbersAfter(t *testing.T) {
	ident := []string{"main", "title"}
	cases := []struct {
		name    string
		raw     string
		guarded bool
		want    []int
	}{
		{"nothing follows", "Main Title epub", true, nil},
		{"book marker then digit", "Main Title Book 3 epub", true, []int{3}},
		{"bare digit ending the release", "Main Title 4", true, []int{4}},
		{"a digit before an ordinary word is a count when guarded", "Main Title 4 epub", true, nil},
		{"and a number when not guarded", "Main Title 4 epub", false, []int{4}},
		{"capital roman numeral", "Main Title III epub", true, []int{3}},
		{"number word after a marker", "Main Title Book Two epub", true, []int{2}},
		{"a bitrate is a count when guarded", "Main Title 64 kbps", true, nil},
		{"the same digits are read when not guarded", "Main Title 64 kbps", false, []int{64}},
		{"title not in the release", "Other Words 3", true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := numbersAfter(tc.raw, NormalizeRelease(tc.raw), tc.guarded, ident)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("numbersAfter(%q, guarded=%v) = %v, want %v", tc.raw, tc.guarded, got, tc.want)
			}
		})
	}
}
