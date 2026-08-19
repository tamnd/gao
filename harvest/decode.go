package harvest

// Turning somebody else's rows into ours.
//
// Six corpora, six layouts, and no two of them agree on what a document record
// contains. HPLT ships the WARC file and offset it was extracted from, FineWeb2
// ships a URL and a dump identifier, and MADLAD-400 ships a string of text and
// nothing else. The ingest contract in doc does not bend for any of them: a
// document either carries its provenance or it goes to the reject store, because
// a corpus where some rows can be traced and some cannot is a corpus where no
// row can be trusted.
//
// So a decoder is a mapping and not a parser. Its job is to say which upstream
// field is the URL, which one is the fetch time, and which of the source's own
// measurements are worth keeping, and then to hand over a document that the
// contract can rule on. Nothing here decides whether a document is good enough.
// [Docs] runs the contract and the reject store records what failed.
//
// Two things are done here rather than left to a later stage, and both are
// done because they are part of the document's identity rather than part of
// its quality. The text is put into NFC, since doc_id is a hash of the text
// and a hash of two different encodings of the same string is two different
// documents. And the diacritic verdict is computed, since Vietnamese written
// without tone marks is still Vietnamese and still useful, but it is not the
// same distribution and it must not enter the corpus unlabeled. The rest of
// normalization, tone mark placement and the legacy font encodings, belongs to
// normalize and will recompute doc_id when it runs.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/text/unicode/norm"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/law"
)

// DecoderVersion is the version of the mapping in this file, and it is what the
// extractor column of every document produced here carries.
//
// It moves when the mapping changes, not when the binary does. The question the
// extractor column answers is whether two documents were built by the same
// rules, and a build number would answer a question nobody asked.
const DecoderVersion = "1.0.0"

// Extractor is the value of the extractor column for an ingested document.
//
// The gat in it is the old name of this package, kept deliberately. The column
// exists to say whether two documents were built by the same rules, and tens of
// millions of rows already carry this string, so renaming it to match the
// package would split one program's output into two values that mean the same
// thing. That is a worse answer to the question the column asks than a stale
// name is.
const Extractor = "gao-gat@" + DecoderVersion

// PipelineVersion is the value of the pipeline_version column for a document
// that has been ingested and nothing else.
//
// The leading zero is the point. No cleaning stage has touched these rows, and
// a reader who sees 0.x knows not to compare them against rows that came
// through normalize, sift, and mill.
const PipelineVersion = "0.1.0"

// maxRowBytes is the largest upstream record the decoder will read. It is the
// same ceiling the segment format uses, so a row that fits here fits in the
// store it is headed for.
const maxRowBytes = 64 << 20

// ErrNoDecoder is returned for a source whose layout nothing here reads yet.
var ErrNoDecoder = errors.New("harvest: no decoder for this source")

// ErrBadRow is returned for an upstream record that does not parse. It names the
// file and the line.
//
// A bad row fails the file rather than being skipped. One unreadable line in a
// twenty gigabyte shard is either a fault in this mapping or a fault in what the
// host published, and both are worth stopping for. Skipping is how a corpus
// loses three percent of a source without anybody finding out.
var ErrBadRow = errors.New("harvest: the file has a record that does not parse")

// Decoder turns the bytes of one pinned file into documents.
//
// Decode reads r to the end and calls emit once per upstream record, in file
// order. An error from emit stops the decode and is returned unchanged, so a
// caller can stop an ingest from inside the sink.
type Decoder interface {
	Decode(p Pinned, f File, r io.Reader, emit func(*doc.Document) error) error
}

// DecoderFor returns the decoder for a source whose files are read as a stream,
// which is the ones that ship JSON lines.
func DecoderFor(s doc.Source) (Decoder, bool) {
	switch s {
	case doc.SourceHPLT3:
		return jsonRows{row: hpltRow}, true
	case doc.SourceMADLAD400:
		return jsonRows{row: madladRow}, true
	}
	return nil, false
}

// Access says how a source's files have to be read.
//
// It is a property of the format and not a preference. A stream is hashed as it
// goes and checked at the end against the pinned digest, so it is what a source
// gets unless its format makes it impossible, and Parquet's footer at the end of
// the file makes it impossible.
type Access int

const (
	// Stream reads the file forwards, once, verifying it.
	Stream Access = iota

	// Random reads the parts of the file that are wanted, by range request,
	// verifying nothing but the pinned length of what it asked for.
	Random
)

// String implements [fmt.Stringer], and the values are what the ledger records.
func (a Access) String() string {
	if a == Random {
		return "random"
	}
	return "stream"
}

// AccessFor returns how a source's files have to be read.
func AccessFor(s doc.Source) Access {
	if _, ok := RandomDecoderFor(s); ok {
		return Random
	}
	return Stream
}

// Decodable reports whether every source in the list has a decoder by either
// route, and returns the ones that do not.
func Decodable(sources []Pinned) (ok bool, missing []doc.Source) {
	for _, p := range sources {
		_, stream := DecoderFor(p.Source)
		_, random := RandomDecoderFor(p.Source)
		if !stream && !random {
			missing = append(missing, p.Source)
		}
	}
	return len(missing) == 0, missing
}

// row is one upstream record, already parsed, with everything the mapping needs
// to place it: which source and file it came from and which line of that file it
// was.
type row struct {
	Pin  Pinned
	File File
	Line int64

	// Raw is the record as it arrived, before any of this touched it. Its hash
	// is the document's raw_id, which is what links a row in the store back to
	// the bytes the host served.
	Raw []byte
}

// Locator is the value of the source_locator column: the file inside the source
// and the line inside the file. It is the row's identity within the dataset, and
// it is enough to go back and read the record again.
func (r row) Locator() string {
	return r.File.Path + ":" + strconv.FormatInt(r.Line, 10)
}

// jsonRows decodes a file of one JSON object per line, decompressing by whatever
// the file extension says, and hands each line to row.
type jsonRows struct {
	row func(r row) (*doc.Document, error)
}

// Decode implements [Decoder].
func (j jsonRows) Decode(p Pinned, f File, r io.Reader, emit func(*doc.Document) error) error {
	plain, closeFn, err := decompress(f.Path, r)
	if err != nil {
		return err
	}
	defer closeFn()

	lines := bufio.NewScanner(plain)
	lines.Buffer(make([]byte, 0, 1<<20), maxRowBytes)

	var n int64
	for lines.Scan() {
		n++
		raw := lines.Bytes()
		if len(trimSpace(raw)) == 0 {
			continue
		}
		d, err := j.row(row{Pin: p, File: f, Line: n, Raw: raw})
		if err != nil {
			// A scanner hands back whatever it is still holding when the read
			// under it fails, so the tail of a stream that stopped arrives here
			// looking like a record somebody wrote wrong. Ask the reader before
			// blaming the file. HPLT v3 vie_Latn/6_1.jsonl.zst failed at line
			// 17685466 with "unexpected end of JSON input" after most of 25.2 GB,
			// and from in here a download that stopped and a genuinely half
			// written line are the same bytes, so the run named the one of the two
			// it had no way to check.
			if rErr := lines.Err(); rErr != nil {
				return fmt.Errorf("harvest: reading %s: the stream stopped inside line %d: %w", f.Path, n, rErr)
			}
			return fmt.Errorf("%w: %s line %d: %w", ErrBadRow, f.Path, n, err)
		}
		if err := emit(d); err != nil {
			return err
		}
	}
	if err := lines.Err(); err != nil {
		return fmt.Errorf("harvest: reading %s: %w", f.Path, err)
	}
	return nil
}

// trimSpace is strings.TrimSpace for bytes without the allocation, since it runs
// once per record across half a billion of them.
func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n') {
		b = b[1:]
	}
	for len(b) > 0 {
		if c := b[len(b)-1]; c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			b = b[:len(b)-1]
			continue
		}
		break
	}
	return b
}

// decompress wraps r in the decompressor the file extension calls for. The
// returned function releases the decompressor and never closes r, which belongs
// to the fetch.
func decompress(name string, r io.Reader) (io.Reader, func(), error) {
	switch path.Ext(name) {
	case ".zst":
		z, err := zstd.NewReader(r)
		if err != nil {
			return nil, nil, fmt.Errorf("harvest: opening %s: %w", name, err)
		}
		return z, z.Close, nil
	case ".gz":
		g, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, fmt.Errorf("harvest: opening %s: %w", name, err)
		}
		return g, func() { _ = g.Close() }, nil
	case ".jsonl", ".json":
		return r, func() {}, nil
	}
	return nil, nil, fmt.Errorf("harvest: %s is compressed in a way this does not read", name)
}

// build fills in every column that does not depend on which source the row came
// from, and it is the only place any of them are set, so a new decoder cannot
// forget one.
//
// text is normalized here rather than by the caller because doc_id is a hash of
// it, and a document whose identity was computed over unnormalized text is a
// document that changes identity the first time anything normalizes it.
func build(r row, text string) *doc.Document {
	text = norm.NFC.String(text)
	verdict, ratio := diacritics(text)

	d := &doc.Document{
		DocID:         doc.SumString(text),
		RawID:         doc.Sum(r.Raw),
		Text:          text,
		SchemaVersion: doc.SchemaVersion,
	}
	d.Source = r.Pin.Source
	d.SourceLocator = r.Locator()
	d.Extractor = Extractor
	d.PipelineVersion = PipelineVersion
	d.Lang = "vie"
	d.Diacritics = verdict
	d.NChars = doc.Chars(text)
	d.NSyllables = doc.Syllables(text)
	d.Heuristics = map[string]float32{"diacritic_ratio": ratio}
	d.LicenseClass, d.LicenseEvidence = license(r.Pin)
	return d
}

// license returns the class and the evidence for a source, read from the
// determination table rather than from the manifest, so that the string in the
// record is the same string luat prints and the two cannot drift.
//
// The manifest carries the class as well, and a disagreement between them is a
// bug in one of the two tables, so it is checked in a test rather than resolved
// at runtime.
func license(p Pinned) (doc.LicenseClass, string) {
	for _, det := range law.For(p.Source) {
		return det.Class, det.Evidence
	}
	return p.Class, ""
}

// hostOf returns the hostname of a URL, which is a column of its own because
// takedowns, budgets, and sorting all operate on it and recomputing it from the
// URL at half a billion rows is not free.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// The diacritic verdict.
//
// Vietnamese without tone marks is a real register: it is what people type on a
// phone keyboard that is set to English, and what a great deal of forum and
// comment text looks like. It is Vietnamese and it is worth having. It is also a
// different distribution from the written language, and a training mixture that
// wants one and not the other cannot separate them after the fact, so the
// verdict goes in a column at ingest.
//
// It is computed per line rather than over the document, because the case worth
// catching is a page where the article carries tone marks and the comments under
// it do not. Over a whole document that reads as a weak present, which is the
// one answer that is wrong.

// diacriticFloor is the share of a line's letters that have to carry a mark for
// the line to count as written with diacritics. Vietnamese prose runs between a
// quarter and two fifths, and unmarked Vietnamese runs at zero, so anything in
// this range separates them with room to spare.
const diacriticFloor = 0.05

// judgeableLetters is the shortest line the verdict is taken from, in letters.
// Below it a heading or a row of numbers would vote as loudly as a paragraph.
const judgeableLetters = 24

// diacritics returns the verdict for the document, present, absent, or mixed,
// and the share of its letters that carry a mark. The share is returned as well
// as the verdict because the reject store keeps raw values and not conclusions:
// a threshold nobody can recompute is a threshold nobody can retune.
func diacritics(text string) (verdict string, ratio float32) {
	var letters, marked, judged, withMarks int
	for line := range strings.Lines(text) {
		l, m := marks(line)
		letters += l
		marked += m
		if l < judgeableLetters {
			continue
		}
		judged++
		if float64(m)/float64(l) >= diacriticFloor {
			withMarks++
		}
	}
	if letters > 0 {
		ratio = float32(marked) / float32(letters)
	}

	switch {
	case judged == 0:
		// Nothing long enough to judge, so the document as a whole is the only
		// evidence there is.
		if float64(ratio) >= diacriticFloor {
			return "present", ratio
		}
		return "absent", ratio
	case float64(withMarks)/float64(judged) >= 0.85:
		return "present", ratio
	case float64(withMarks)/float64(judged) <= 0.15:
		return "absent", ratio
	}
	return "mixed", ratio
}

// marks counts the letters in a string and how many of them carry a Vietnamese
// diacritic.
//
// It counts on the decomposed form, so that á, ắ, and ặ are a letter and one or
// two combining marks rather than three unrelated code points, which is what
// lets one rule cover the whole alphabet. Đ is the exception the rule does not
// reach: it is a modified letter that Unicode does not decompose, so it is named.
func marks(s string) (letters, marked int) {
	open := false // the letter just seen can still be marked
	for _, r := range norm.NFD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
			if open {
				marked++
				open = false
			}
		case unicode.IsLetter(r):
			letters++
			if r == 'đ' || r == 'Đ' {
				marked++
				open = false
				continue
			}
			open = true
		default:
			open = false
		}
	}
	return letters, marked
}

// unmarshal is json.Unmarshal with the error wrapped in something that says what
// was being read, since the row that failed is one of several million.
func unmarshal(raw []byte, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("not a %T: %w", v, err)
	}
	return nil
}
