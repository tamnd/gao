package harvest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// The loop that turns a pinned manifest into a corpus.
//
// It is deliberately thin, because the two hard parts are elsewhere and the
// value of this file is that it does not add a third. [Fetcher] handles a host
// that drops a 26.6 GB transfer at hour two. [Ledger] handles a process that is
// killed at file 90 of 154. What is left is: for each file the ledger has not
// seen, open it, hand the stream to a sink, record what happened.
//
// The sink is an interface because what happens to the bytes is not acquisition's
// decision and because the alternative was a fetcher that knows about Parquet. A
// sink sees one file at a time, streaming, and never sees the whole thing.
//
// One file at a time, in manifest order, and no parallelism inside a source. The
// limit here is not CPU, it is that the far end is somebody else's server and
// four connections to it is a way of being asked to stop. Parallelism in this
// project happens across boxes, where the fleet already splits the manifest and
// each box takes a range of files.

// A Sink consumes one downloaded file.
//
// It is given a reader over the raw bytes exactly as the host sent them,
// compressed if the host compresses them, and it returns how many documents it
// took out of the file. Returning an error abandons that file, and the ingest
// stops rather than continuing, because a sink that fails on one file is going
// to fail on the next one and a half ingested corpus that looks whole is the
// expensive outcome.
type Sink interface {
	// Consume reads r to the end and returns the number of documents it found.
	Consume(ctx context.Context, p Pinned, f File, r io.Reader) (documents int64, err error)
}

// A RandomSink also consumes a file that cannot be streamed.
//
// Only the Parquet sources need it, and they need it because of where the format
// puts its footer rather than because of anything about their contents. A sink
// that does not implement this fails on those sources rather than being handed a
// stream it cannot use, since the alternative is a decoder reading a columnar
// file forwards and reporting that it is corrupt.
type RandomSink interface {
	Sink

	// ConsumeAt reads whichever parts of the file it needs and returns the
	// number of documents it found. The size is the pinned length.
	ConsumeAt(ctx context.Context, p Pinned, f File, r io.ReaderAt, size int64) (documents int64, err error)
}

// SinkFunc adapts a function to a [Sink].
type SinkFunc func(ctx context.Context, p Pinned, f File, r io.Reader) (int64, error)

// Consume implements [Sink].
func (fn SinkFunc) Consume(ctx context.Context, p Pinned, f File, r io.Reader) (int64, error) {
	return fn(ctx, p, f, r)
}

// Count is the sink that reads a file, verifies it, and keeps nothing.
//
// It is not a placeholder for the real sink so much as the thing that proves the
// fetch layer works against the real hosts before anything is built on top of
// it. Running it over a source answers whether the manifest's byte counts are
// right, whether the digests match, and how often the connection drops, and it
// answers all three without needing a decoder for six different file layouts.
//
// It reports zero documents rather than a byte count. How many documents are in
// a file is the decoder's answer, and a sink without a decoder should say
// nothing rather than put a number in the ledger that means something else.
var Count Sink = SinkFunc(func(_ context.Context, _ Pinned, _ File, r io.Reader) (int64, error) {
	_, err := io.Copy(io.Discard, r)
	return 0, err
})

// Progress is called after each file, so a command can print something during a
// transfer that runs for days. It is called from the ingest goroutine and it
// must not block for long.
type Progress func(Report)

// Report is what an ingest says about one file it just finished.
type Report struct {
	Pin        Pinned
	File       File
	Documents  int64
	Digest     string
	Reconnects int
	Elapsed    time.Duration
	Err        error

	// Access is how the file was read. A randomly read file has no digest, and
	// Moved and Requests are what it cost instead: how many bytes crossed the
	// wire and how many requests it took to move them.
	Access   Access
	Moved    int64
	Requests int64
}

// Ingest fetches the files in a plan and feeds each one to a sink.
type Ingest struct {
	// Fetcher opens the files. A nil fetcher is the zero [Fetcher].
	Fetcher *Fetcher

	// Ledger records what finished. It is required, because an ingest with
	// nowhere to record progress is an ingest that starts from zero every time
	// it is interrupted, and it will be interrupted.
	Ledger *Ledger

	// Sink consumes each file. A nil sink is [Count].
	Sink Sink

	// Box labels the ledger entries with the machine that produced them.
	Box string

	// Progress, if set, is called after every file including a failed one.
	Progress Progress
}

// Run works through todo in order and returns how many files it finished.
//
// It stops at the first failure and returns the error, having recorded
// everything that succeeded before it. Stopping is the right behavior for a
// transfer this long: a host that has started refusing, a disk that has filled,
// or a decoder that has hit a layout it does not know are all conditions that
// the next file will hit too, and grinding through 60 more failures to report
// them together helps nobody.
func (in *Ingest) Run(ctx context.Context, todo []Work) (int, error) {
	if in.Ledger == nil {
		return 0, errors.New("harvest: an ingest needs a ledger, or an interruption costs the whole run")
	}
	fetcher := in.Fetcher
	if fetcher == nil {
		fetcher = &Fetcher{}
	}
	sink := in.Sink
	if sink == nil {
		sink = Count
	}

	var done int
	for _, w := range todo {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		rep, err := in.one(ctx, fetcher, sink, w)
		if in.Progress != nil {
			in.Progress(rep)
		}
		if err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

// one fetches and consumes a single file, and records it if it finished.
func (in *Ingest) one(ctx context.Context, fetcher *Fetcher, sink Sink, w Work) (Report, error) {
	// Out of order only when the format requires it and the sink can use it.
	// Verifying a source is still a stream, Parquet included: the footer at the
	// end of the file stops a decoder from reading forwards, it does not stop a
	// digest, and a run that is checking the manifest rather than decoding it
	// should still come away with one.
	if rs, ok := sink.(RandomSink); ok && AccessFor(w.Pin.Source) == Random {
		return in.random(ctx, fetcher, rs, w)
	}

	start := time.Now()
	rep := Report{Pin: w.Pin, File: w.File}

	body, err := fetcher.Open(ctx, w.Pin, w.File)
	if err != nil {
		rep.Elapsed, rep.Err = time.Since(start), err
		return rep, err
	}
	defer func() { _ = body.Close() }()

	documents, err := sink.Consume(ctx, w.Pin, w.File, body)
	rep.Documents = documents
	rep.Digest = body.Digest()
	rep.Reconnects = body.Reconnects()
	rep.Elapsed = time.Since(start)
	if err != nil {
		rep.Err = fmt.Errorf("harvest: %s from %s: %w", w.File.Path, w.Pin.Source, err)
		return rep, rep.Err
	}

	// A sink that stopped early leaves a body that verified nothing, and the
	// difference between that and a complete file is invisible from the byte
	// count alone, so it is checked rather than assumed.
	if !body.Done() {
		rep.Err = fmt.Errorf("%w: %s from %s stopped at byte %d of %d",
			ErrTruncated, w.File.Path, w.Pin.Source, body.Offset(), w.File.Bytes)
		return rep, rep.Err
	}

	entry := Entry{
		Source:     w.Pin.Source,
		Revision:   w.Pin.Revision,
		Path:       w.File.Path,
		Bytes:      body.Offset(),
		Digest:     body.Digest(),
		Documents:  documents,
		Reconnects: body.Reconnects(),
		Box:        in.Box,
	}
	if err := in.Ledger.Record(entry); err != nil {
		rep.Err = err
		return rep, err
	}
	return rep, nil
}

// random reads a single file out of order and records it if it finished.
//
// There is no equivalent of the truncation check that ends [Ingest.one]. A
// streamed file that a sink abandoned early is caught by comparing what was read
// against what was pinned, and a file read in pieces has no such position: the
// decoder is meant to skip the parts it does not want, so a small number of bytes
// moved is the intended outcome rather than a short read. What replaces the check
// is the decoder itself, which reads the row groups the footer declares and fails
// if the file does not have them.
func (in *Ingest) random(ctx context.Context, fetcher *Fetcher, sink RandomSink, w Work) (Report, error) {
	start := time.Now()
	rep := Report{Pin: w.Pin, File: w.File, Access: Random}

	r, err := fetcher.OpenAt(ctx, w.Pin, w.File)
	if err != nil {
		rep.Elapsed, rep.Err = time.Since(start), err
		return rep, err
	}

	documents, err := sink.ConsumeAt(ctx, w.Pin, w.File, r, r.Size())
	rep.Documents = documents
	rep.Moved = r.Bytes()
	rep.Requests = r.Requests()
	rep.Elapsed = time.Since(start)
	if err != nil {
		rep.Err = fmt.Errorf("harvest: %s from %s: %w", w.File.Path, w.Pin.Source, err)
		return rep, rep.Err
	}

	entry := Entry{
		Source:   w.Pin.Source,
		Revision: w.Pin.Revision,
		Path:     w.File.Path,
		// The pinned length, because that is what this entry accounts for and
		// what a restart must not fetch again. What actually crossed the wire is
		// Moved, and the two being different is the point of reading this way.
		Bytes:     w.File.Bytes,
		Documents: documents,
		Access:    Random.String(),
		Moved:     rep.Moved,
		Requests:  rep.Requests,
		Box:       in.Box,
	}
	if err := in.Ledger.Record(entry); err != nil {
		rep.Err = err
		return rep, err
	}
	return rep, nil
}
