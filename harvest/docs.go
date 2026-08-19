package harvest

// The sink that turns a fetched file into documents.
//
// [Ingest] streams bytes and knows nothing about what is in them. A [Decoder]
// maps somebody else's record onto ours and passes no judgment. Docs is where
// the two meet the ingest contract: every decoded document is put to
// [doc.Document.Admit], the ones that pass go on to whatever the caller does
// with them, and the ones that fail go to the reject store with the reason.
//
// Nothing is dropped silently and nothing is fixed up. A document that arrives
// without a URL is not given one, and a document whose language identifier says
// English is not relabeled. The reject store exists so that the rate of both is
// a number somebody can look up rather than a thing somebody suspects.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/reject"
)

// Stage is what the reject store records as the stage that made the call. It
// carries the decoder version rather than the binary version, because the
// interesting question about a rejection rate is what changed in the mapping.
const Stage = Extractor

// minLangScore is the language identification score below which a document is
// rejected as not Vietnamese rather than admitted as weakly Vietnamese.
//
// It is low on purpose. This is a gate against records the producer's own
// identifier was unsure about, not a quality filter, and the real language
// pass runs in sift against a reference set. Setting it high here would throw
// documents away before anything had measured what was in them.
const minLangScore = 0.5

// ErrNothingAdmitted reports a file that produced documents and admitted none.
//
// It is an error rather than a line in a report. A shard that yields nothing
// means either the mapping is wrong or the source cannot satisfy the contract,
// and either way the next sixty files will do the same thing. Finding that out
// after one file is better than finding it out after ninety gigabytes of
// somebody else's bandwidth.
var ErrNothingAdmitted = errors.New("harvest: the file produced documents and none of them were admitted")

// Docs is a [Sink] that decodes a fetched file into documents and puts each one
// to the ingest contract.
type Docs struct {
	// Emit receives every document that satisfies the contract, in file order.
	// It is nil in a run that only wants the counts, which is what measuring a
	// source costs before anything is written anywhere.
	Emit func(*doc.Document) error

	// Rejects receives every document that does not. A nil reject store is a
	// decision rather than a default: the counts below still come out, so what
	// is given up is the ability to look at what was thrown away, not the
	// ability to count it.
	Rejects *reject.Writer

	mu       sync.Mutex
	admitted int64
	rejected int64
	reasons  map[reject.Reason]int64
}

// Consume implements [Sink].
func (d *Docs) Consume(ctx context.Context, p Pinned, f File, r io.Reader) (int64, error) {
	dec, ok := DecoderFor(p.Source)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrNoDecoder, p.Source)
	}
	take, done := d.taking(ctx)
	return done(p, f, dec.Decode(p, f, r, take))
}

// ConsumeAt implements [RandomSink], for the sources whose files are Parquet.
//
// Everything after the decoder is the same as [Docs.Consume]. The contract does
// not know how a document's bytes reached it and it does not get a second set of
// rules for the sources that could not be streamed.
func (d *Docs) ConsumeAt(ctx context.Context, p Pinned, f File, r io.ReaderAt, size int64) (int64, error) {
	dec, ok := RandomDecoderFor(p.Source)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrNoDecoder, p.Source)
	}
	take, done := d.taking(ctx)
	return done(p, f, dec.DecodeAt(p, f, r, size, take))
}

// taking returns the function a decoder emits into and the function that turns
// what it did into the sink's answer.
func (d *Docs) taking(ctx context.Context) (take func(*doc.Document) error, done func(Pinned, File, error) (int64, error)) {
	var admitted, rejected int64

	take = func(rec *doc.Document) error {
		// Checked per document rather than per file. A 26.6 GB shard is several
		// million records and an hour of work, and a ctrl-C that waits for the
		// end of the file is a ctrl-C that does not work.
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := rec.Admit(); err != nil {
			rejected++
			return d.record(rec, err)
		}
		admitted++
		d.tally(1, 0, "")
		if d.Emit == nil {
			return nil
		}
		return d.Emit(rec)
	}

	done = func(p Pinned, f File, err error) (int64, error) {
		if err != nil {
			return admitted, err
		}
		if admitted == 0 && rejected > 0 {
			return 0, fmt.Errorf("%w: %s from %s, %d rejected", ErrNothingAdmitted, f.Path, p.Source, rejected)
		}
		return admitted, nil
	}
	return take, done
}

// record writes a rejection and returns an error only if the reject store itself
// failed, since a rejection is an ordinary outcome and not a fault.
func (d *Docs) record(rec *doc.Document, err error) error {
	reason := reasonFor(rec)
	d.tally(0, 1, reason)
	if d.Rejects == nil {
		return nil
	}
	return d.Rejects.Reject(rec, Stage, reason, err.Error())
}

func (d *Docs) tally(admitted, rejected int64, reason reject.Reason) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.admitted += admitted
	d.rejected += rejected
	if reason == "" {
		return
	}
	if d.reasons == nil {
		d.reasons = make(map[reject.Reason]int64)
	}
	d.reasons[reason]++
}

// reasonFor classifies a rejection from the document rather than from the text
// of the error.
//
// The error says everything that is wrong at once, which is what a person
// reading one row wants and what a query cannot use. The reason is the closed
// set the reject store counts by, and deriving it by matching strings against a
// message written to be readable is how a report starts reading zero for a
// reason that is happening constantly.
//
// A missing language score is a contract failure and not a language one. The
// distinction is the whole point of the column: language means the producer
// looked at the document and said it was not Vietnamese enough, and a source
// that publishes no score at all has said nothing about any of its documents.
// Counting those as a language finding would report MADLAD-400 as ninety five
// gigabytes of documents that failed language identification, which is the
// opposite of what happened.
func reasonFor(rec *doc.Document) reject.Reason {
	switch {
	case rec.Lang != "" && rec.Lang != "vie":
		return reject.ReasonLanguage
	case rec.LangScore > 0 && rec.LangScore < minLangScore:
		return reject.ReasonLanguage
	}
	return reject.ReasonContract
}

// Admitted returns how many documents have passed the contract.
func (d *Docs) Admitted() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.admitted
}

// Rejected returns how many documents have failed it.
func (d *Docs) Rejected() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rejected
}

// Reasons returns the rejections so far broken down by reason. The map is a
// copy, so a caller can hold it across further work.
func (d *Docs) Reasons() map[reject.Reason]int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[reject.Reason]int64, len(d.reasons))
	for k, v := range d.reasons {
		out[k] = v
	}
	return out
}
