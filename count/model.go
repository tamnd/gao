package count

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
)

// A Model is a tokenizer pinned by digest.
//
// The digest is the whole point. The file that defines gao's unit of measurement
// is fetched over the network from a host that has no obligation to us, and the
// failure mode is not a broken download but a successful one: ask for a gated
// file without credentials and a 401 body lands in the file, 129 bytes of English
// prose where a protobuf should be. Nothing about that looks like an error to a
// program that only checks whether the write succeeded.
type Model struct {
	// Name is what a published count names as its tokenizer.
	Name string

	// Vocab is the vocabulary size, checked after loading, because a file that
	// parses as a SentencePiece model is not necessarily this SentencePiece
	// model.
	Vocab int

	// Bytes and Digest are what the file must be.
	Bytes  int64
	Digest string

	// From is where the file can be fetched. It is not the authority for what
	// the file is, which is what Digest is for.
	From string

	// Origin is where the model actually comes from, recorded because a reader
	// checking this pin deserves to know that the mirror is a mirror.
	Origin string
}

// Gemma3 is the tokenizer that defines a gao token.
//
// Four separately uploaded repositories in the unsloth mirror of the Gemma-3
// family, from 270m to 27b, carry this file byte for byte identically, which is
// expected because the family shares one tokenizer, and which is worth having
// checked because it is the only corroboration available. Google's own
// repositories are gated: reaching the file at its origin means accepting a
// license in a browser first, and a program cannot do that.
//
// That gating is a real property of this project rather than an inconvenience.
// The unit that every published gao number is quoted in is defined by an artifact
// whose canonical copy a third party cannot fetch without agreeing to terms. The
// digest is what makes the mirror usable anyway: it does not matter who serves
// the bytes if the bytes are known.
var Gemma3 = Model{
	Name:   "gemma-3",
	Vocab:  262144,
	Bytes:  4689074,
	Digest: "1299c11d7cf632ef3b4e11937501358ada021bbdf7c47638d13c0ee982f2e79c",
	From:   "https://huggingface.co/unsloth/gemma-3-27b-it/resolve/main/tokenizer.model",
	Origin: "google/gemma-3-27b-it, which is gated",
}

// ErrWrongModel reports a tokenizer file that is not the pinned one.
var ErrWrongModel = errors.New("dem: the tokenizer file is not the pinned one")

// Verify reads r to the end and reports whether it is the pinned model. It
// returns the bytes it read, so a caller that has to hold the file in memory
// anyway does not read it twice.
//
// The size is checked before the digest so that the common failure says
// something useful. A digest mismatch on a 129 byte file tells a reader that
// something is wrong; the size tells them what.
func (m Model) Verify(r io.Reader) ([]byte, error) {
	h := sha256.New()
	b, err := io.ReadAll(io.TeeReader(r, h))
	if err != nil {
		return nil, fmt.Errorf("dem: reading the tokenizer: %w", err)
	}
	if int64(len(b)) != m.Bytes {
		return nil, fmt.Errorf("%w: %s is %d bytes, expected %d", ErrWrongModel, m.Name, len(b), m.Bytes)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != m.Digest {
		return nil, fmt.Errorf("%w: %s has digest %s, expected %s", ErrWrongModel, m.Name, got, m.Digest)
	}
	return b, nil
}

// VerifyFile is [Model.Verify] against a path, which is how the fleet loads a
// tokenizer that was staged onto the box ahead of the run.
func (m Model) VerifyFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return m.Verify(f)
}

// Fetch downloads the model and verifies it. A nil client means
// [http.DefaultClient].
//
// The HTTP status is checked before the body, so that a refusal is reported as a
// refusal. Without that the digest still catches it, but the message would be
// about a length mismatch when the real news is that the host said no.
func (m Model) Fetch(ctx context.Context, c *http.Client) ([]byte, error) {
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.From, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dem: fetching %s: %w", m.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dem: fetching %s: %s said %s", m.Name, m.From, resp.Status)
	}
	return m.Verify(resp.Body)
}

// Save writes verified model bytes to a path. It verifies again rather than
// trusting the caller, because the one thing this file must never be is
// unchecked.
func (m Model) Save(path string, b []byte) error {
	if _, err := m.Verify(bytes.NewReader(b)); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
