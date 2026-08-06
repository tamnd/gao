package xep

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/zeebo/blake3"
)

// pilot is the published frame drawn small enough to label in a test, which is
// what a pilot is for outside a test as well.
func pilot() Frame {
	f := Fixed()
	f.Size = 240
	return f
}

func docOf(n int) doc.Hash {
	return doc.Hash(blake3.Sum256(fmt.Appendf(nil, "doc-%d", n)))
}

// labeling is what a labeling pass that went well returns, with change applied
// first so a test can break exactly one thing.
func labeling(f Frame, change func([]Label) []Label) []Label {
	digest := f.Digest()
	var out []Label
	n := 0
	for _, sl := range f.Slices {
		for i := 0; i < sl.Wanted(f.Size); i++ {
			band := Bands[n%len(Bands)]
			out = append(out, Label{Doc: docOf(n), Source: sl.Source, By: "an", Band: band, Frame: digest})
			if n%8 == 0 {
				second := band
				if n%32 == 0 {
					second = Bands[next(band)]
				}
				out = append(out, Label{Doc: docOf(n), Source: sl.Source, By: "binh", Band: second, Frame: digest})
			}
			n++
		}
	}
	if change != nil {
		out = change(out)
	}
	return out
}

// next is the band one step along the scale, turning back at the end of it.
func next(b Band) int {
	i := slices.Index(Bands, b)
	if i+1 < len(Bands) {
		return i + 1
	}
	return i - 1
}

func TestTheFrameWePublishDrawsWhatItSaysItDraws(t *testing.T) {
	if faults := Fixed().Faults(); len(faults) > 0 {
		t.Fatalf("the frame we publish is faulted:\n  %s", strings.Join(faults, "\n  "))
	}
	if got := Fixed().Size; got != Documents {
		t.Errorf("the published frame draws %d and gao-refset is %d", got, Documents)
	}
}

func TestTheDigestMovesWhenTheRubricDoes(t *testing.T) {
	f := Fixed()
	before := f.Digest()
	f.Rules[0].Apart = "whichever one feels right"
	if f.Digest() == before {
		t.Error("the line between two bands moved and the digest did not, so a rubric change publishes under the old identity")
	}
}

func TestTheDigestDoesNotMoveWhenTheNoteDoes(t *testing.T) {
	f := Fixed()
	before := f.Digest()
	f.Note = "rewritten for the release notes"
	if f.Digest() != before {
		t.Error("explaining the frame better changed its identity, so nobody will improve the explanation")
	}
}

func TestTheDigestDoesNotDependOnTheOrderTheSlicesAreWrittenIn(t *testing.T) {
	f := Fixed()
	before := f.Digest()
	slices.Reverse(f.Slices)
	slices.Reverse(f.Rules)
	if f.Digest() != before {
		t.Error("reordering the frame changed its identity")
	}
}

func TestSharesThatDoNotComeToOneAreReported(t *testing.T) {
	f := Fixed()
	f.Slices[0].Share = 0.50
	if !faulted(f, "so the set is drawn from a corpus that is not the one it labels") {
		t.Errorf("shares summing to %v were accepted:\n  %s", 1.2, strings.Join(f.Faults(), "\n  "))
	}
}

func TestASliceWithNoReasonForItsShareIsReported(t *testing.T) {
	f := Fixed()
	f.Slices[2].Why = "  "
	if !faulted(f, "does not say why it gets the share it gets") {
		t.Error("a share nobody can explain was accepted")
	}
}

func TestAFrameWithNoSeedIsReported(t *testing.T) {
	f := Fixed()
	f.Seed = ""
	if !faulted(f, "nobody else can make the same one") {
		t.Error("a draw nobody else can reproduce was accepted")
	}
}

func TestABandThatDoesNotSayWhatItGetsMixedUpWithIsReported(t *testing.T) {
	f := Fixed()
	f.Rules[1].Apart = ""
	if !faulted(f, "the only part of a rubric two labelers ever need") {
		t.Error("a band with no boundary on it was accepted")
	}
}

func TestABandConfusedWithOneTwoStepsAwayIsReported(t *testing.T) {
	f := Fixed()
	f.Rules[0].Confused = Unusable
	if !faulted(f, "those are not next to each other") {
		t.Error("a rubric whose boundaries do not follow its scale was accepted")
	}
}

func TestARuleWithNoExamplesIsReported(t *testing.T) {
	f := Fixed()
	f.Rules[3].Examples = nil
	if !faulted(f, "gets argued about at labeling time") {
		t.Error("a rule with no worked call under it was accepted")
	}
}

func TestABandTheRubricLeavesOutIsReported(t *testing.T) {
	f := Fixed()
	f.Rules = f.Rules[:3]
	if !faulted(f, "is on the scale and the rubric does not describe it") {
		t.Error("a scale with a band missing from its rubric was accepted")
	}
}

func TestABandOffTheScaleIsReported(t *testing.T) {
	f := Fixed()
	f.Rules[0].Band = "excellent"
	if !faulted(f, "is not on the scale") {
		t.Error("a band nothing is adjacent to was accepted")
	}
}

func TestDistanceIsTheScale(t *testing.T) {
	for _, c := range []struct {
		a, b Band
		want int
	}{
		{Rich, Rich, 0},
		{Rich, Plain, 1},
		{Plain, Rich, 1},
		{Rich, Unusable, 3},
		{Thin, Unusable, 1},
		{Rich, "excellent", -1},
	} {
		if got := Distance(c.a, c.b); got != c.want {
			t.Errorf("%s to %s is %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestALabelingThatWentWellPasses(t *testing.T) {
	f := pilot()
	sc := f.Read(labeling(f, nil))
	if reasons := sc.Publishable(); len(reasons) > 0 {
		t.Fatalf("an honest labeling was faulted:\n  %s", strings.Join(reasons, "\n  "))
	}
	if !sc.Passed {
		t.Errorf("a labeling that met every gate did not pass: %+v", sc)
	}
	if sc.Labeled != f.Size {
		t.Errorf("%d documents came back, want %d", sc.Labeled, f.Size)
	}
	if sc.Exact < MinExact || sc.Adjacent != 1 {
		t.Errorf("agreement came out at exact %.3f adjacent %.3f", sc.Exact, sc.Adjacent)
	}
	if len(sc.ByPerson) != 2 {
		t.Errorf("%d people labeled it", len(sc.ByPerson))
	}
}

func TestARubricPeopleDoNotAgreeOnFails(t *testing.T) {
	f := pilot()
	sc := f.Read(labeling(f, func(ls []Label) []Label {
		for i, l := range ls {
			if l.By == "binh" {
				ls[i].Band = Bands[next(l.Band)]
			}
		}
		return ls
	}))
	if sc.Passed {
		t.Error("a rubric two people never agreed on passed")
	}
	if !has(sc.Publishable(), "the rubric is not deciding the band and the labeler is") {
		t.Errorf("the score does not say what low agreement means:\n  %s", strings.Join(sc.Publishable(), "\n  "))
	}
}

func TestPeopleMissingByTwoBandsIsADifferentComplaint(t *testing.T) {
	f := pilot()
	sc := f.Read(labeling(f, func(ls []Label) []Label {
		for i, l := range ls {
			if l.By == "binh" {
				ls[i].Band = Unusable
				if l.Band == Unusable {
					ls[i].Band = Rich
				}
			}
		}
		return ls
	}))
	if !has(sc.Publishable(), "four words in a list rather than a scale") {
		t.Errorf("a scale people miss by two steps was not called one:\n  %s", strings.Join(sc.Publishable(), "\n  "))
	}
}

func TestOnePersonsLabelsAreNotAgreement(t *testing.T) {
	f := pilot()
	sc := f.Read(labeling(f, func(ls []Label) []Label {
		out := ls[:0]
		for _, l := range ls {
			if l.By == "an" {
				out = append(out, l)
			}
		}
		return out
	}))
	if sc.Passed {
		t.Error("a set nobody checked passed")
	}
	if !has(sc.Publishable(), "one person's reading of it") {
		t.Errorf("the score does not say one person labeled the whole thing:\n  %s", strings.Join(sc.Publishable(), "\n  "))
	}
	if !has(sc.Publishable(), "a labeler having a good week") {
		t.Error("the score does not say the double labeled share is short")
	}
}

func TestAPersonPlacingTheSameDocumentTwiceIsReported(t *testing.T) {
	f := pilot()
	sc := f.Read(labeling(f, func(ls []Label) []Label {
		return append(ls, Label{Doc: ls[0].Doc, Source: ls[0].Source, By: ls[0].By, Band: Thin, Frame: f.Digest()})
	}))
	if !has(sc.Publishable(), "a person agreeing with themselves is not agreement") {
		t.Errorf("a repeat was counted as a second opinion:\n  %s", strings.Join(sc.Publishable(), "\n  "))
	}
}

func TestALabelFromASourceTheFrameDoesNotDrawIsReported(t *testing.T) {
	f := pilot()
	sc := f.Read(labeling(f, func(ls []Label) []Label {
		return append(ls, Label{Doc: docOf(9001), Source: "madlad400", By: "an", Band: Plain})
	}))
	if !has(sc.Publishable(), "somebody had it open") {
		t.Errorf("a document from outside the draw was labeled into the set:\n  %s", strings.Join(sc.Publishable(), "\n  "))
	}
	if !slices.Contains(sc.Unknown, "madlad400") {
		t.Errorf("the source is not named: %v", sc.Unknown)
	}
}

func TestABandInventedDuringLabelingIsReported(t *testing.T) {
	f := pilot()
	sc := f.Read(labeling(f, func(ls []Label) []Label {
		ls[3].Band = "borderline"
		return ls
	}))
	if !has(sc.Publishable(), "a rubric change with no digest on it") {
		t.Errorf("a band nobody wrote down was accepted:\n  %s", strings.Join(sc.Publishable(), "\n  "))
	}
}

func TestLabelsPlacedAgainstADifferentFrameAreReported(t *testing.T) {
	f := pilot()
	other := f
	other.Seed = "something else"
	sc := f.Read(labeling(f, func(ls []Label) []Label {
		ls[0].Frame = other.Digest()
		ls[1].Frame = other.Digest()
		return ls
	}))
	if !has(sc.Publishable(), "the rubric moved between the labeling and the reading") {
		t.Errorf("labels against an older rubric were folded in:\n  %s", strings.Join(sc.Publishable(), "\n  "))
	}
}

func TestABandNobodyUsedIsReported(t *testing.T) {
	f := pilot()
	sc := f.Read(labeling(f, func(ls []Label) []Label {
		for i, l := range ls {
			if l.Band == Rich {
				ls[i].Band = Plain
			}
		}
		return ls
	}))
	if !has(sc.Publishable(), "is a band that is not in the rubric whatever the rubric says") {
		t.Errorf("a band with nothing in it went unremarked:\n  %s", strings.Join(sc.Publishable(), "\n  "))
	}
}

func TestASourceThatCameBackShortOfItsShareIsReported(t *testing.T) {
	f := pilot()
	sc := f.Read(labeling(f, func(ls []Label) []Label {
		out := ls[:0]
		for _, l := range ls {
			if l.Source != "finepdfs" {
				out = append(out, l)
			}
		}
		return out
	}))
	if !has(sc.Publishable(), "nothing came back from finepdfs") {
		t.Errorf("a missing source went unremarked:\n  %s", strings.Join(sc.Publishable(), "\n  "))
	}
	if worst := sc.Worst()[0]; worst.Source != "finepdfs" {
		t.Errorf("the worst source is %s", worst.Source)
	}
}

func TestTheFirstLabelIsTheBandOfRecord(t *testing.T) {
	f := pilot()
	d := f.Digest()
	sc := f.Read([]Label{
		{Doc: docOf(1), Source: "hplt3", By: "an", Band: Rich, Frame: d},
		{Doc: docOf(1), Source: "hplt3", By: "binh", Band: Plain, Frame: d},
	})
	for _, b := range sc.ByBand {
		if b.Band == Rich && b.Documents != 1 {
			t.Errorf("the second opinion overruled the first: %+v", sc.ByBand)
		}
		if b.Band == Plain && b.Documents != 0 {
			t.Errorf("the document was counted in two bands: %+v", sc.ByBand)
		}
	}
	if sc.Pairs != 1 || sc.Exact != 0 || sc.Adjacent != 1 {
		t.Errorf("one disagreement one band wide read as %d pairs, exact %.2f, adjacent %.2f", sc.Pairs, sc.Exact, sc.Adjacent)
	}
}

func TestALabelWithNobodysNameOnItIsRefused(t *testing.T) {
	path := write(t, `{"doc":"`+docOf(1).String()+`","source":"hplt3","band":"plain"}`)
	if _, err := ReadLabels(path); err == nil || !strings.Contains(err.Error(), "cannot be checked against a second one") {
		t.Errorf("an unattributed label was read: %v", err)
	}
}

func TestALabelWithNoDocumentOnItIsRefused(t *testing.T) {
	path := write(t, `{"source":"hplt3","by":"an","band":"plain"}`)
	if _, err := ReadLabels(path); err == nil || !strings.Contains(err.Error(), "a label with no document on it") {
		t.Errorf("a label about nothing was read: %v", err)
	}
}

func TestAFieldNobodyWroteDownIsRefused(t *testing.T) {
	path := write(t, `{"doc":"`+docOf(1).String()+`","source":"hplt3","by":"an","band":"plain","confidence":0.9}`)
	if _, err := ReadLabels(path); err == nil {
		t.Error("a label carrying a field the format does not have was read, so a labeling tool can add a column nobody reads")
	}
}

func TestAnEmptyLabelFileIsRefused(t *testing.T) {
	path := write(t, "\n\n")
	if _, err := ReadLabels(path); err == nil || !strings.Contains(err.Error(), "it holds no labels") {
		t.Errorf("an empty file read as a labeling: %v", err)
	}
}

func TestAFrameSurvivesARoundTrip(t *testing.T) {
	b, err := json.Marshal(Fixed())
	if err != nil {
		t.Fatal(err)
	}
	path := write(t, string(b))
	back, err := ReadFrame(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Digest() != Fixed().Digest() {
		t.Error("the frame does not survive being written and read")
	}
}

func TestAFrameFileThatIsNotThereSaysSo(t *testing.T) {
	if _, err := ReadFrame(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("a frame that does not exist read as one")
	}
}

func TestALongListOfDocumentsIsCutShort(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e", "f", "g"}
	if got := join(ids); got != "a, b, c, d, e and 2 more" {
		t.Errorf("the list reads %q", got)
	}
	if got := join(ids[:2]); got != "a, b" {
		t.Errorf("a short list was cut: %q", got)
	}
}

func TestASliceKnowsHowManyItWants(t *testing.T) {
	if got := (Slice{Share: 0.25}).Wanted(240); got != 60 {
		t.Errorf("a quarter of 240 came out as %d", got)
	}
}

func TestTheFrameDescribesItselfInASentence(t *testing.T) {
	d := Fixed().Describe()
	if !strings.Contains(d, "200000 documents") || !strings.Contains(d, "6 sources") || !strings.Contains(d, "4 bands") {
		t.Errorf("the frame does not describe its own shape: %s", d)
	}
	if strings.Contains(d, "\n") {
		t.Errorf("the description is not a sentence: %q", d)
	}
}

func faulted(f Frame, want string) bool {
	return has(f.Faults(), want)
}

func has(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
