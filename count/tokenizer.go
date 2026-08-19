package count

import (
	"bytes"
	"fmt"

	"github.com/eliben/go-sentencepiece"
)

// A Tokenizer counts tokens under a pinned [Model].
//
// It is safe for concurrent use. Everything it holds is built when the model
// loads and read-only afterwards, which matters because the ingest decodes files
// in parallel and a tokenizer behind a mutex would serialize them.
type Tokenizer struct {
	proc  *sentencepiece.Processor
	model Model
}

// Load builds a tokenizer from verified model bytes.
//
// It takes bytes rather than a path or a reader so that verification is not
// optional. The only way to get the bytes is [Model.Verify] or [Model.VerifyFile],
// so there is no route to a tokenizer that was never checked against its digest.
func Load(m Model, b []byte) (*Tokenizer, error) {
	proc, err := sentencepiece.NewProcessor(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("dem: %s does not parse as a tokenizer: %w", m.Name, err)
	}
	if got := proc.ModelInfo().VocabularySize; got != m.Vocab {
		return nil, fmt.Errorf("%w: %s has a vocabulary of %d, expected %d", ErrWrongModel, m.Name, got, m.Vocab)
	}
	return &Tokenizer{proc: proc, model: m}, nil
}

// Open loads the tokenizer from a file, verifying it first.
func Open(m Model, path string) (*Tokenizer, error) {
	b, err := m.VerifyFile(path)
	if err != nil {
		return nil, err
	}
	return Load(m, b)
}

// Model reports which tokenizer this is, so that a published count can name it
// rather than referring to whatever was on the box that day.
func (t *Tokenizer) Model() Model { return t.model }

// Count returns the number of tokens in the text.
//
// No beginning or end of sequence marker is added. Those are a property of a
// training run's packing rather than of the text, and adding two per document to
// half a billion documents would put a billion tokens into the corpus size that
// no document contains.
func (t *Tokenizer) Count(text string) int {
	return len(t.proc.Encode(text))
}
