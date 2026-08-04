package phoi

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/vo"
)

// The stage keeps almost everything. A document with damage in it is a document
// with damage in it, and throwing it away here would be making a quality
// judgement two stages before anybody measures quality.
func TestOrdinaryDamageDoesNotCostTheDocument(t *testing.T) {
	long := strings.Repeat("Việt Nam là một quốc gia ở Đông Nam Á. ", 40)

	for _, tc := range []struct{ name, text string }{
		{"a homoglyph", "ðường dài, và một chiếc xe."},
		{"a zero width space", "Vi\u200bệt Nam là một quốc gia ở Đông Nam Á."},
		{"the old tone convention", "Hoà bình, thuỷ chung, khoẻ mạnh, và luỹ tre làng."},
		{"one broken word in a long post", strings.Repeat("Hôm qua tôi đi học và gặp lại bạn cũ ở phố cũ lúc chiều muộn. ", 5) + "Ở dduwowngj kia."},
		{"one control character in a long document", long + "\x01"},
	} {
		if reason, ok := Reject(Normalize(tc.text)); ok {
			t.Errorf("%s: rejected as %q, want the document kept", tc.name, reason)
		}
	}
}

// A page whose words came out as keystrokes is a page nobody can read and
// nobody can repair, because repairing it means guessing which word was meant.
func TestADocumentOfKeystrokesIsRejected(t *testing.T) {
	text := strings.Repeat("dduwowngj ddaji hocj ", 20)
	reason, ok := Reject(Normalize(text))
	if !ok {
		t.Fatal("a document of nothing but keystrokes was kept")
	}
	if reason != vo.ReasonResidue {
		t.Errorf("rejected as %q, want %q", reason, vo.ReasonResidue)
	}
}

// The control rate is how a font or an archive that survived a content type
// sniff announces itself, and it is the one thing this stage refuses outright.
func TestABinaryThatWasSniffedAsTextIsRejected(t *testing.T) {
	reason, ok := Reject(Normalize("\x00\x01\x02\x03tên\x04\x05\x06\x07"))
	if !ok {
		t.Fatal("a run of control characters was kept")
	}
	if reason != vo.ReasonControl {
		t.Errorf("rejected as %q, want %q", reason, vo.ReasonControl)
	}
}

// A binary has no syllables, so its residue rate is a ratio over nothing. If the
// order of the two checks ever flips, a font file gets reported as bad
// Vietnamese and whoever reads that goes looking in the wrong place.
func TestABinaryIsRejectedForBeingABinary(t *testing.T) {
	r := Normalize(strings.Repeat("\x00\x01dduwowngj\x02\x03", 10))
	if r.Residue == 0 {
		t.Fatal("this case is meant to trip both limits at once and trips neither")
	}
	if reason, _ := Reject(r); reason != vo.ReasonControl {
		t.Errorf("rejected as %q, want %q", reason, vo.ReasonControl)
	}
}

// Nothing at all is not a rejection here. An empty document has no rate to be
// over any limit, and it is the ingest contract that has something to say about
// it rather than this stage.
func TestAnEmptyDocumentIsNotThisStagesProblem(t *testing.T) {
	if reason, ok := Reject(Normalize("")); ok {
		t.Errorf("an empty document was rejected as %q", reason)
	}
}

// Every reason this stage can give has to be one the reject store knows, or a
// rejection is written down as a string nobody can group by.
func TestTheReasonsAreTheStoresReasons(t *testing.T) {
	for _, text := range []string{
		strings.Repeat("dduwowngj ", 20),
		"\x00\x01\x02\x03tên\x04\x05\x06\x07",
	} {
		reason, ok := Reject(Normalize(text))
		if !ok {
			t.Fatalf("Normalize(%q) was kept, want it rejected", text)
		}
		if !reason.Valid() {
			t.Errorf("Normalize(%q) was rejected as %q, which the reject store does not define", text, reason)
		}
	}
}

// The tally counts rejections so that a run over a shard reports how many
// documents this stage dropped without anybody adding the column up by hand.
func TestTheTallyCountsRejections(t *testing.T) {
	var got Tally
	for _, text := range []string{
		"Hà Nội mùa này trời trở lạnh.",
		strings.Repeat("dduwowngj ", 20),
		"\x00\x01\x02\x03tên\x04\x05\x06\x07",
	} {
		got.Add(Normalize(text))
	}
	if got.Rejected != 2 {
		t.Errorf("the tally counted %d rejections, want 2", got.Rejected)
	}
}
