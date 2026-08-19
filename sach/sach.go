// Package sach runs the cleaning line over a document and says what came out.
//
// The stages already exist one package at a time: phoi normalizes, sang
// measures and sifts, xay computes the key duplicates are found on, che covers
// the personal data. What did not exist is the thing that runs them in order,
// keeps the account of what each one removed, and writes the result somewhere.
// Four report commands over a corpus of a quarter of a terabyte are four
// opinions about it. This is the stage that acts on them.
//
// # The order is the design
//
// Normalization first, because every stage after it compares strings and two
// spellings of one word are two documents to a hash and two rows in an
// embedding table. Then the sift, because there is no point measuring the
// quality of a page that is not Vietnamese prose. Then deduplication, because
// the corpus is four projects' readings of the same crawls and the duplicate
// rate between them is the largest single fact about it. Then the personal
// data, last of the four that change the text, so that what gets covered is
// covered in the document that is actually going to ship.
//
// The one stage that is missing from the middle is the quality classifier, and
// it is missing rather than stubbed. gao-qual is trained against a hand built
// reference set that does not exist yet, and a filter with an untrained model
// behind it would remove documents for a reason nobody could defend. What that
// costs is written down in the report rather than papered over: this line finds
// Vietnamese prose, and finding good Vietnamese prose is a later stage.
//
// # What is done to a document and what is only recorded
//
// The text is changed by two stages, normalization and redaction, and both say
// so on the row: pipeline_version moves off the ingest's 0.x, and pii_level
// records what was covered. Everything sang measures is recorded and nothing it
// measures is a verdict stored on the row, so a corpus filtered at one threshold
// can be re-filtered at another without going back to the text. That is the same
// rule sang states for itself and this stage is where it would have been easiest
// to break.
//
// Deduplication is the exception worth naming. A streaming pass sees a document
// before it has seen the copies of it, so it can say this is the first copy and
// it cannot say how many copies there turned out to be. dup_cluster is written,
// dup_cluster_size is left at zero rather than at a one that would be a lie, and
// the number of copies dropped is in the run report. The published cluster
// column is what makes the remaining question a query rather than a re-read.
package sach

import (
	"github.com/tamnd/gao/che"
	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
	"github.com/tamnd/gao/phoi"
	"github.com/tamnd/gao/sang"
	"github.com/tamnd/gao/vo"
	"github.com/tamnd/gao/xay"
)

// PipelineVersion is what the pipeline_version column carries for a document
// that has been through this line.
//
// The ingest writes 0.1.0 and says in its own comment what the leading zero is
// for: no cleaning stage has touched those rows. This is the version that says
// one has. It moves when the line changes what it does to a document, not when
// the binary is rebuilt, because the question the column answers is whether two
// documents were cleaned by the same rules.
const PipelineVersion = "1.0.0"

// DefaultKeys is how many documents a run sizes its deduplication set for
// unless it is told otherwise.
//
// The raw corpus holds around a hundred and seventeen million documents, and a
// set sized for that is a table of 2^28 slots, which is 2.1 GB. Every box in
// this fleet has that, and a run over one source rather than all four should be
// told a smaller number rather than pay for the whole corpus.
const DefaultKeys = 120_000_000

// Clean is the dataset this line publishes to.
//
// It panics on a name that is not in the hub table, which is a mistake in this
// package rather than in a caller's arguments, and is worth finding at the first
// call rather than at the first push.
func Clean() kho.Dataset {
	d, ok := kho.Lookup("vitco-clean")
	if !ok {
		panic("sach: the hub has no vitco-clean")
	}
	return d
}

// Stage names the part of the line a document was removed at. It is a small
// closed set for the same reason [vo.Reason] is: the report breaks removals
// down by it, and a free text field turns that into string matching.
type Stage string

const (
	// StageNormalize is phoi: the document is not text, or its words cannot be
	// recovered.
	StageNormalize Stage = "phoi"

	// StageSift is sang: the document is not Vietnamese prose of some length.
	StageSift Stage = "sang"

	// StageMill is xay: gao already has this document.
	StageMill Stage = "xay"

	// StageContract is the ingest contract, checked again on the way out. A
	// document that fails it here was made to fail by something this line did,
	// which is a bug rather than a property of the corpus, so it is a stage of
	// its own and not folded into one of the four above.
	StageContract Stage = "contract"
)

// Line is the cleaning line: every threshold it applies and every set it
// remembers, in one value.
//
// It is not safe for concurrent use by itself. [Seen] is, which is the part
// that has to be shared, so a run gives every worker its own Line over one Seen.
type Line struct {
	// Limits is what sang sifts against. The defaults are sang's, which are
	// Gopher's shapes at Vietnamese sizes and are labeled by that package as
	// starting points rather than as findings.
	Limits sang.Limits

	// Level is how much of the personal data is covered. L1 is the level for
	// text whose upstream is already published under its own terms, which is
	// every source in this corpus.
	Level che.Level

	// Seen is the set of documents already admitted, by cluster. A nil Seen
	// turns deduplication off, which is what the ablation that measures what
	// deduplication costs would want and is not what a run wants.
	Seen *Seen
}

// New returns the line as it runs, which is sang's default thresholds, L1
// redaction, and a deduplication set sized for keys documents.
func New(keys int) *Line {
	return &Line{Limits: sang.Default(), Level: che.L1, Seen: NewSeen(keys)}
}

// Verdict is what the line did to one document.
//
// The measurements are on it rather than only on the document because the
// report wants them for the documents that were removed as well, and a removed
// document is not written anywhere that could carry them.
type Verdict struct {
	// Kept says whether the document goes to the clean corpus.
	Kept bool

	// Stage and Reason are why it does not, and are empty when it does.
	Stage  Stage
	Reason vo.Reason

	// Normalized is what normalization did, and Measured is what the document
	// measured. Measured is the zero value for a document that did not reach
	// the sift.
	Normalized phoi.Result
	Measured   sang.Result

	// Found is the personal data the document held, which is what it held
	// rather than what was covered: the level decides how much of it the text
	// loses and the count is worth having either way.
	Found []che.Span
}

// Run puts one document through the line and returns what happened.
//
// The document is modified in place whether it is kept or not, so a caller that
// writes rejects gets the normalized text on them rather than the raw text,
// which is what makes a rejection readable: the reason a document failed the
// sift is a property of the text the sift saw.
func (l *Line) Run(d *doc.Document) Verdict {
	var v Verdict

	v.Normalized = phoi.Normalize(d.Text)
	d.Text = v.Normalized.Text
	if reason, bad := phoi.Reject(v.Normalized); bad {
		v.Stage, v.Reason = StageNormalize, reason
		return v
	}

	v.Measured = sang.Measure(d.Text)
	if reason, _, bad := l.Limits.Reject(v.Measured); bad {
		v.Stage, v.Reason = StageSift, reason
		l.measure(d, v.Measured)
		return v
	}
	l.measure(d, v.Measured)

	// The cluster is written before the set is asked, so a dropped copy carries
	// the identity of the copy that was kept. A reject store row that cannot be
	// joined back to the document it duplicates is a row nobody can act on.
	d.DupCluster = Cluster(d.Text)
	if l.Seen != nil && !l.Seen.Add(d.DupCluster) {
		v.Stage, v.Reason = StageMill, vo.ReasonDuplicate
		return v
	}
	d.IsRepresentative = true

	text, found := che.Redact(d.Text, l.Level)
	d.Text = text
	v.Found = found
	d.PIILevel = doc.RedactionLevel(l.Level)
	d.PIITypes = kinds(found)

	// The spans are deliberately not written. They are byte offsets into the
	// text that was searched, and that text no longer exists once the level
	// covers anything, so the offsets on a redacted row point at the wrong
	// bytes. They are also, on a corpus where the identifier is covered and the
	// paragraph around it is not, a published index of where the identifiers
	// were. What survives is the kinds, which is the column a reader filters on.
	d.PIISpans = nil

	d.PipelineVersion = PipelineVersion
	d.NChars = doc.Chars(d.Text)
	d.NSyllables = doc.Syllables(d.Text)

	// Identity is blake3 of the normalized text and this stage changed the
	// text, so the identity moves with it. raw_id and source_locator are what
	// still point at the upstream record, and they are untouched.
	d.DocID = doc.SumString(d.Text)

	if err := d.Admit(); err != nil {
		v.Stage, v.Reason = StageContract, vo.ReasonContract
		return v
	}
	v.Kept = true
	return v
}

// measure writes what sang measured onto the document.
//
// It runs for a document the sift removed as well as for one it kept, because a
// reject that carries its measurements can be argued with and one that carries
// a reason code alone cannot.
func (l *Line) measure(d *doc.Document, r sang.Result) {
	d.Lang = kho.LangValue
	d.LangScore = float32(r.Language.Rate())
	d.Diacritics = r.Diacritic()
	d.Heuristics = r.Heuristics()
	d.NChars = doc.Chars(d.Text)
	d.NSyllables = doc.Syllables(d.Text)
}

// Cluster is the duplicate cluster a document belongs to.
//
// It is the first sixteen bytes of blake3 of [xay.Key], which is the text with
// everything a republisher changes taken out of it: case, punctuation, the i
// and y pair, and the spacing. Two documents with the same cluster are the same
// document as far as this corpus is concerned, and the fact that the key is
// never written back is why the corpus still holds both spellings.
func Cluster(text string) doc.Cluster {
	sum := doc.SumString(xay.Key(text))
	var c doc.Cluster
	copy(c[:], sum[:len(c)])
	return c
}

// kinds is the distinct kinds of personal data a document held, in the order
// che declares them so that two rows with the same kinds hold the same list.
func kinds(found []che.Span) []string {
	if len(found) == 0 {
		return nil
	}
	held := map[che.Kind]bool{}
	for _, s := range found {
		held[s.Kind] = true
	}
	out := make([]string, 0, len(held))
	for _, k := range che.Kinds() {
		if held[k] {
			out = append(out, string(k))
		}
	}
	return out
}
