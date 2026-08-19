package count_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/count"
)

// fixture returns a model pinned to exactly these bytes, which is how the
// verification is tested without a 4.7 MB file in the repository.
func fixture(body string) (count.Model, []byte) {
	b := []byte(body)
	sum := sha256.Sum256(b)
	return count.Model{
		Name:   "fixture",
		Vocab:  4,
		Bytes:  int64(len(b)),
		Digest: hex.EncodeToString(sum[:]),
	}, b
}

func TestAModelAcceptsTheBytesItWasPinnedTo(t *testing.T) {
	m, b := fixture("the pinned bytes")

	got, err := m.Verify(strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("Verify on the pinned bytes: %v", err)
	}
	if string(got) != string(b) {
		t.Errorf("Verify returned %q, want %q", got, b)
	}
}

func TestAModelRefusesBytesThatAreNotTheOnesItWasPinnedTo(t *testing.T) {
	m, _ := fixture("the pinned bytes")

	_, err := m.Verify(strings.NewReader("the pinned bytes!"))
	if !errors.Is(err, count.ErrWrongModel) {
		t.Fatalf("Verify on longer bytes returned %v, want ErrWrongModel", err)
	}
}

func TestAModelRefusesBytesOfTheRightLengthAndTheWrongContent(t *testing.T) {
	m, _ := fixture("the pinned bytes")

	_, err := m.Verify(strings.NewReader("the PINNED bytes"))
	if !errors.Is(err, count.ErrWrongModel) {
		t.Fatalf("Verify on altered bytes returned %v, want ErrWrongModel", err)
	}
	if !strings.Contains(err.Error(), "digest") {
		t.Errorf("the message for altered bytes is %q, and it should say the digest is wrong", err)
	}
}

// The failure this pin actually exists to catch. Ask a gated repository for a
// file without credentials and the 401 body is served with a 2xx-looking success
// at the transport layer as far as a naive fetch is concerned: the write
// succeeds, the file exists, and it contains an apology in English.
func TestAModelRefusesTheErrorPageThatGetsServedInsteadOfAGatedFile(t *testing.T) {
	m, _ := fixture(strings.Repeat("tokenizer bytes go here", 400))
	page := "Access to model google/gemma-3-27b-it is restricted. You must have access to it and be authenticated to access it."

	_, err := m.Verify(strings.NewReader(page))
	if !errors.Is(err, count.ErrWrongModel) {
		t.Fatalf("Verify on an error page returned %v, want ErrWrongModel", err)
	}
	if !strings.Contains(err.Error(), "bytes") {
		t.Errorf("the message for a short file is %q, and it should say how long the file was", err)
	}
}

func TestAModelVerifiesAFileOnDisk(t *testing.T) {
	m, b := fixture("staged onto the box before the run")
	path := filepath.Join(t.TempDir(), "tokenizer.model")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := m.VerifyFile(path); err != nil {
		t.Fatalf("VerifyFile on the staged file: %v", err)
	}
	if _, err := m.VerifyFile(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("VerifyFile on an absent file returned no error")
	}
}

// The pin itself, checked for the things that go stale silently. A digest that
// is not 64 hex characters is a typo, and a model with no source is one nobody
// can fetch.
func TestTheGemma3PinIsComplete(t *testing.T) {
	m := count.Gemma3

	if len(m.Digest) != 64 {
		t.Errorf("the Gemma-3 digest is %d characters, and a sha256 is 64", len(m.Digest))
	}
	if _, err := hex.DecodeString(m.Digest); err != nil {
		t.Errorf("the Gemma-3 digest is not hex: %v", err)
	}
	if m.Vocab != 262144 {
		t.Errorf("the Gemma-3 vocabulary is pinned at %d, and a gao token is one of 262144", m.Vocab)
	}
	if m.From == "" || m.Origin == "" {
		t.Error("the Gemma-3 pin should say both where the file can be fetched and where it comes from")
	}
	if !strings.Contains(m.Origin, "gated") {
		t.Error("the Gemma-3 pin should record that its origin is gated, since that is why From is a mirror")
	}
}
