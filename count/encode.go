package count

// Getting at the pieces, not just how many there are.

import (
	"strconv"
	"strings"
)

// A Piece is one token together with the text it stands for.
//
// Text is the surface: what this token contributes to the output, with the
// model's space marker turned back into a space, so that joining the pieces of
// a document reproduces the document. A byte fallback token's surface is the
// single byte it carries, which on Vietnamese is usually a fragment of a
// character rather than a character, and that is not a defect in this type. It
// is the thing gates T4 and T5 are looking for.
type Piece struct {
	ID   int
	Text string
}

// spaceMarker is the character SentencePiece writes where a space was. It is
// U+2581 LOWER ONE EIGHTH BLOCK, which is a real character that real text can
// contain, and a document containing it is one of the few ways a round trip
// through this tokenizer loses information. Gate T1 finds those.
const spaceMarker = "▁"

// Encode splits the text into pieces.
//
// No beginning or end of sequence marker is added, for the reason in [Tokenizer.Count]:
// those belong to a training run's packing rather than to the text.
func (t *Tokenizer) Encode(text string) []Piece {
	toks := t.proc.Encode(text)
	ps := make([]Piece, len(toks))
	for i, tok := range toks {
		ps[i] = Piece{ID: tok.ID, Text: surface(tok.Text)}
	}
	return ps
}

// Decode turns token identities back into text. It is the other half of the
// only property a tokenizer has to have.
func (t *Tokenizer) Decode(ids []int) string { return t.proc.Decode(ids) }

// Name is what a published count names as its tokenizer.
func (t *Tokenizer) Name() string { return t.model.Name }

// Vocab is how many pieces the vocabulary holds, which is the range of
// identities a vocabulary audit has to walk.
func (t *Tokenizer) Vocab() int { return t.model.Vocab }

// surface turns a vocabulary piece into the text it stands for.
func surface(piece string) string {
	if b, ok := fallbackByte(piece); ok {
		return string([]byte{b})
	}
	return strings.ReplaceAll(piece, spaceMarker, " ")
}

// fallbackByte reports whether a piece is one of the 256 byte fallback pieces,
// and which byte it carries.
//
// The test is on the spelling because the library does not export the piece
// type, and the spelling is safe: a byte piece is written <0x00> through <0xff>
// and is a control piece in the proto, so no ordinary piece of text can be
// spelled that way and reach here. Getting this wrong in the lenient direction
// would turn a byte fallback into a six character token in the report, which is
// exactly the thing T4 exists to see, so it is worth being exact about.
func fallbackByte(piece string) (byte, bool) {
	if len(piece) != 6 || !strings.HasPrefix(piece, "<0x") || piece[5] != '>' {
		return 0, false
	}
	n, err := strconv.ParseUint(piece[3:5], 16, 8)
	if err != nil {
		return 0, false
	}
	return byte(n), true
}

// A compile time note that the pinned tokenizer is a thing the gates can run
// on. The gates take an interface so that a tokenizer that violates a gate can
// be written in a test, since a gate nobody has seen fail is a gate nobody has
// tested.
var _ Encoder = (*Tokenizer)(nil)
