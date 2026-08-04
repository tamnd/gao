package phoi

// What this stage throws away, which is very little.

import "github.com/tamnd/gao/vo"

// Reject says whether a document should not go on to the next stage, and why.
//
// Both limits are about whether the document is text rather than about whether
// it is any good. Quality is a later question and a harder one, and it is worth
// asking only of documents that are what they claim to be. So this stage drops a
// file that was sniffed as text and is really a binary, and a page whose words
// nobody can recover, and nothing else. A document with one broken word in it
// goes on with the count attached.
//
// The control check comes first because it is the more fundamental failure. A
// binary has no syllables, so its residue rate is a ratio over nothing and says
// nothing, and reporting "input method keystrokes" for a font file would send
// whoever reads it looking in the wrong place.
func Reject(r Result) (vo.Reason, bool) {
	switch {
	case r.ControlRate() > ControlLimit:
		return vo.ReasonControl, true
	case r.ResidueRate() > ResidueLimit:
		return vo.ReasonResidue, true
	}
	return "", false
}
