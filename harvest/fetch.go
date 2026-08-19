package harvest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"time"

	"github.com/tamnd/gao/fleet"
)

// The download half of acquisition.
//
// Nothing here writes the file it is downloading to disk. The largest pinned
// file is a 26.6 GB HPLT shard and server1 peaks at 4.1 GB, so a fetcher that
// landed the file first and read it second would not run on the box that has to
// run it. [Fetcher.Open] hands back a reader, the caller streams it through
// whatever it is building, and the bytes are never all in one place at once.
//
// The consequence is that a dropped connection cannot be answered by starting
// the file again. A 26.6 GB shard over a link that fails every few hours would
// never finish, so the reader reconnects at the byte it stopped at with a Range
// request and carries on, and the caller does not see it happen. The hash rolls
// forward across the reconnect because the bytes do: nothing is read twice, so
// nothing needs to be hashed twice.
//
// Verification is at the end and it is not optional. A truncated response is a
// successful HTTP request, so the size is checked against the manifest, and
// where the host publishes a digest that is checked too. Where it does not, and
// HPLT publishes none while the Hub withholds them for gated repos, the fetcher
// computes one and hands it back for the ledger to record, so the second fetch
// of a file has something to compare against even though the first did not.

// DefaultRetries is how many times a fetch reconnects, without the transfer
// moving, before it gives up on the file.
//
// Five rather than one, because the thing being retried is not a request that
// might be answered differently. It is a transfer that was working, ran for
// possibly an hour, and stopped, and the cost of abandoning it is the hour.
//
// The five are consecutive, and that is the part that took a real run to get
// right. Counted over the life of a file instead, the budget is spent by a link
// that drops once every few gigabytes and never recovers, because a reconnect
// that worked did not give anything back. gamingpc lost the same 26.4 GB HPLT
// shard twice that way, the second time at byte 25,137,168,622, which is 95% of
// it. A reconnect followed by real bytes now resets the count, so a transfer
// that keeps moving keeps going, while a host that answers a range request and
// then sends nothing still stops after five.
const DefaultRetries = 5

// RetryCeiling is how many reconnects one file gets in total, for each of the
// fetcher's consecutive retries.
//
// Nothing real reaches it. The shard above dropped five times across 25 GB and
// the ceiling on the default budget is five hundred. It exists because the
// consecutive rule on its own has no end: a host handing out a few bytes per
// connection makes progress by this definition every time, resets the budget
// every time, and would hold a fetch open forever.
const RetryCeiling = 100

// DefaultRetryWait is how long a fetch waits before reconnecting. It backs off
// linearly, so the fifth attempt waits five times this.
const DefaultRetryWait = 2 * time.Second

// ErrGated is returned when a gated source is fetched without a token, or with
// one that has not accepted the repo's terms.
var ErrGated = errors.New("harvest: the source is gated")

// ErrTruncated is returned when a host stops sending before the byte count in
// the manifest.
var ErrTruncated = errors.New("harvest: the host sent less than the manifest says the file is")

// ErrDigestMismatch is returned when a file downloads completely and hashes to
// something other than what was pinned.
var ErrDigestMismatch = errors.New("harvest: the file does not hash to its pinned digest")

// Fetcher opens pinned files. The zero value works and uses
// [http.DefaultClient], no token, and [DefaultRetries].
type Fetcher struct {
	// Client is the HTTP client. A nil client is [http.DefaultClient].
	Client *http.Client

	// Token is a Hugging Face access token, sent to hub sources only. An empty
	// token is fine for everything except the gated source.
	Token string

	// Retries is how many times a dropped connection is resumed without the
	// transfer moving. Zero means [DefaultRetries]. A negative value means no
	// retry at all, which is what the tests want and what nothing else should.
	Retries int

	// RetryWait is the base backoff between reconnects. Zero means
	// [DefaultRetryWait].
	RetryWait time.Duration
}

func (f *Fetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return http.DefaultClient
}

func (f *Fetcher) retries() int {
	switch {
	case f.Retries < 0:
		return 0
	case f.Retries == 0:
		return DefaultRetries
	default:
		return f.Retries
	}
}

// ceiling is the total number of reconnects one file gets, whatever the
// consecutive budget does. A fetcher with no retries has no ceiling to reach.
func (f *Fetcher) ceiling() int { return f.retries() * RetryCeiling }

func (f *Fetcher) retryWait() time.Duration {
	if f.RetryWait <= 0 {
		return DefaultRetryWait
	}
	return f.RetryWait
}

// Open starts downloading one pinned file and returns a reader over its bytes.
//
// The reader is not buffered and not decompressed. What comes out is exactly
// what the host sent, because the digest in the manifest is over exactly that,
// and a fetcher that decompressed on the way would be verifying something other
// than what it was given.
func (f *Fetcher) Open(ctx context.Context, p Pinned, file File) (*Body, error) {
	b := &Body{
		fetcher: f,
		ctx:     ctx,
		pin:     p,
		file:    file,
		url:     p.URL(file),
		sum:     sha256.New(),
	}
	if err := b.connect(); err != nil {
		return nil, err
	}
	return b, nil
}

// Body is a file being downloaded. It reads like any other stream and quietly
// reconnects underneath when the host drops the connection.
type Body struct {
	fetcher *Fetcher
	ctx     context.Context
	pin     Pinned
	file    File
	url     string

	resp *http.Response
	sum  hash.Hash
	read int64

	// stall is the reconnects since the last byte arrived, tries is the
	// reconnects over the whole file, and mark is the offset the last reconnect
	// started from. The budget is spent by stall and the ceiling by tries, so
	// the two questions a stuck fetch raises, is it moving and how long has it
	// been at this, have separate answers.
	stall int
	tries int
	mark  int64

	done bool
	err  error
}

// connect issues the request, resuming from the current offset if there is one.
func (b *Body) connect() error {
	req, err := http.NewRequestWithContext(b.ctx, http.MethodGet, b.url, nil)
	if err != nil {
		return fmt.Errorf("harvest: fetching %s from %s: %w", b.file.Path, b.pin.Source, err)
	}
	if b.pin.Origin == Hub && b.fetcher.Token != "" {
		req.Header.Set("Authorization", "Bearer "+b.fetcher.Token)
	}
	if b.read > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", b.read))
	}

	resp, err := b.fetcher.client().Do(req)
	if err != nil {
		return fmt.Errorf("harvest: fetching %s from %s: %w", b.file.Path, b.pin.Source, err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		drain(resp)
		if b.pin.Gated {
			return fmt.Errorf("%w: %s answered %s for %s, so accept the terms at %s and set %s",
				ErrGated, b.pin.Repo, resp.Status, b.file.Path, b.pin.Page(), fleet.TokenEnv)
		}
		return fmt.Errorf("harvest: fetching %s from %s: %s", b.file.Path, b.pin.Source, resp.Status)

	case b.read > 0 && resp.StatusCode == http.StatusOK:
		// The host ignored the Range header and started the file over. Taking
		// it would mean hashing the first bytes twice and reporting a size
		// larger than the file, so this is a failure rather than a slow path.
		drain(resp)
		return fmt.Errorf("harvest: resuming %s from %s: the host restarted the file at byte 0 instead of %d",
			b.file.Path, b.pin.Source, b.read)

	case b.read > 0 && resp.StatusCode != http.StatusPartialContent:
		drain(resp)
		return fmt.Errorf("harvest: resuming %s from %s: %s", b.file.Path, b.pin.Source, resp.Status)

	case b.read == 0 && resp.StatusCode != http.StatusOK:
		drain(resp)
		return fmt.Errorf("harvest: fetching %s from %s: %s", b.file.Path, b.pin.Source, resp.Status)
	}

	b.resp = resp
	return nil
}

// drain closes a response body that is not going to be read.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
}

// Read implements [io.Reader].
//
// A short read followed by an error is a reconnect, not a failure, for as long
// as the reconnects keep producing bytes. The caller sees a stream that stalls
// for a couple of seconds and then continues.
func (b *Body) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	for {
		n, err := b.resp.Body.Read(p)
		if n > 0 {
			b.read += int64(n)
			_, _ = b.sum.Write(p[:n])
			if b.read > b.mark {
				// The reconnect worked and the file is moving again, so the
				// budget belongs to the next stall rather than to this one.
				b.stall = 0
			}
		}
		switch {
		case err == nil:
			return n, nil
		case errors.Is(err, io.EOF):
			b.done = true
			if vErr := b.verify(); vErr != nil {
				b.err = vErr
				return n, vErr
			}
			b.err = io.EOF
			return n, io.EOF
		case b.ctx.Err() != nil:
			// A canceled caller is not a flaky host, and reconnecting for one
			// would turn a cancel into a retry storm.
			b.err = b.ctx.Err()
			return n, b.err
		case b.stall >= b.fetcher.retries():
			b.err = fmt.Errorf("harvest: fetching %s from %s: gave up at byte %d after %d reconnects that moved nothing, %d in all: %w",
				b.file.Path, b.pin.Source, b.read, b.stall, b.tries, err)
			return n, b.err
		case b.tries >= b.fetcher.ceiling():
			b.err = fmt.Errorf("harvest: fetching %s from %s: gave up at byte %d after %d reconnects, which is the ceiling on a budget of %d: %w",
				b.file.Path, b.pin.Source, b.read, b.tries, b.fetcher.retries(), err)
			return n, b.err
		}

		b.stall++
		b.tries++
		b.mark = b.read
		_ = b.resp.Body.Close()
		if waitErr := sleep(b.ctx, time.Duration(b.stall)*b.fetcher.retryWait()); waitErr != nil {
			b.err = waitErr
			return n, b.err
		}
		if cErr := b.connect(); cErr != nil {
			b.err = cErr
			return n, b.err
		}
		if n > 0 {
			return n, nil
		}
	}
}

// sleep waits for d or until the context is done.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// verify checks the finished file against the manifest.
func (b *Body) verify() error {
	if b.read != b.file.Bytes {
		return fmt.Errorf("%w: %s from %s is %d bytes and the host sent %d",
			ErrTruncated, b.file.Path, b.pin.Source, b.file.Bytes, b.read)
	}
	if b.file.Digest == "" {
		return nil
	}
	if got := b.Digest(); got != b.file.Digest {
		return fmt.Errorf("%w: %s from %s was pinned at %s and hashes to %s",
			ErrDigestMismatch, b.file.Path, b.pin.Source, b.file.Digest, got)
	}
	return nil
}

// Digest returns the sha256 of everything read so far, which is the digest of
// the file once the read has finished.
//
// It is meaningful before the end too, and no caller should treat it as if it
// were: it is here so that a completed fetch of a file the host publishes no
// digest for still produces one to record.
func (b *Body) Digest() string {
	return "sha256:" + hex.EncodeToString(b.sum.Sum(nil))
}

// Read returns how many bytes have come out of the host so far.
func (b *Body) Offset() int64 { return b.read }

// Reconnects returns how many times the connection dropped and was resumed,
// which is the number worth logging when an ingest takes longer than it should.
func (b *Body) Reconnects() int { return b.tries }

// Close releases the connection. Closing a body that has not been read to the
// end abandons the file rather than verifying it, because a caller that stopped
// early did so on purpose.
func (b *Body) Close() error {
	if b.resp == nil {
		return nil
	}
	err := b.resp.Body.Close()
	b.resp = nil
	if b.err == nil {
		b.err = errors.New("harvest: read from a closed body")
	}
	return err
}

// Done reports whether the file was read to the end and passed verification.
func (b *Body) Done() bool { return b.done && (b.err == nil || errors.Is(b.err, io.EOF)) }
