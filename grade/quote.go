package grade

// The legal citation specialist.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A Citation is one reference to a Vietnamese legal instrument.
//
// Vietnamese legal drafting numbers instruments to a fixed form, which is why
// this arm is checkable at all. A document is a number, a year, and a code that
// says who issued it and what kind of instrument it is, so a citation is either
// a document that exists or a string that looks like one.
type Citation struct {
	// Kind is the instrument in Vietnamese, lower cased: luật, nghị định,
	// thông tư, quyết định, and the rest.
	Kind string `json:"kind"`

	// Number and Year are the two numbers in the identifier. Year is zero for
	// the forms that carry none, such as 749/QĐ-TTg.
	Number int `json:"number"`
	Year   int `json:"year"`

	// Body is the code after the year: NĐ-CP, QH14, TT-BTTTT, QĐ-TTg. It names
	// the issuing authority and the instrument type at once.
	Body string `json:"body"`

	// Article is the điều the answer pointed at, or zero when it cited the
	// instrument as a whole.
	Article int `json:"article"`

	// Text is the citation as the answer wrote it, kept so a verdict can quote
	// what it rejected.
	Text string `json:"text"`
}

// ID is the identifier without the instrument name, which is what the register
// is keyed on. Two instruments cannot share it.
func (c Citation) ID() string {
	if c.Year == 0 {
		return fmt.Sprintf("%d/%s", c.Number, c.Body)
	}
	return fmt.Sprintf("%d/%d/%s", c.Number, c.Year, c.Body)
}

// String is the citation the way a document would write it.
func (c Citation) String() string {
	if c.Article == 0 {
		return fmt.Sprintf("%s số %s", c.Kind, c.ID())
	}
	return fmt.Sprintf("Điều %d %s số %s", c.Article, c.Kind, c.ID())
}

// bodies is which codes each instrument may carry.
//
// This is the drafting rule rather than a convention. Only the Government
// issues a nghị định, so a nghị định whose code is not NĐ-CP is wrong however
// plausible it reads, and that is exactly the shape a hallucinated citation
// takes: the right kind of thing, numbered like a real one, issued by a body
// that cannot issue it.
var bodies = map[string][]string{
	"luật":               {"QH"},
	"bộ luật":            {"QH"},
	"pháp lệnh":          {"UBTVQH"},
	"nghị quyết":         {"QH", "UBTVQH", "NQ-"},
	"nghị định":          {"NĐ-CP"},
	"quyết định":         {"QĐ-"},
	"thông tư":           {"TT-"},
	"thông tư liên tịch": {"TTLT-"},
	"chỉ thị":            {"CT-"},
}

// Kinds is every instrument this recognizes, longest first, which is also the
// order the pattern has to try them in so that bộ luật is not read as luật.
func Kinds() []string {
	out := make([]string, 0, len(bodies))
	for k := range bodies {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// citation matches an instrument reference with an optional article in front of
// it. The case insensitive groups are marked one at a time rather than with a
// flag over the whole pattern, because the issuing code is upper case and a
// pattern that stopped caring would accept nđ-cp.
var citation = regexp.MustCompile(
	`(?:(?i:điều)\s+(\d+)\s*,?\s+(?:(?i:của)\s+)?)?` +
		`((?i:` + strings.Join(Kinds(), "|") + `))\s+(?:(?i:số)\s+)?` +
		`(\d+)\s*/\s*(?:(\d{4})\s*/\s*)?` +
		`([A-ZĐ]{2}[A-ZĐ0-9]*(?:-[A-ZĐa-z0-9]+)*)`)

// Citations is every instrument an answer referred to, in the order it referred
// to them, with repeats removed.
//
// A repeat is dropped rather than counted twice on purpose. An answer that
// leans on one instrument and says so five times has cited one instrument, and
// counting the repeats would let a model raise its precision by restating the
// citation it was already sure of.
func Citations(text string) []Citation {
	var out []Citation
	seen := map[string]bool{}
	for _, m := range citation.FindAllStringSubmatch(text, -1) {
		c := Citation{
			Kind:    strings.ToLower(strings.Join(strings.Fields(m[2]), " ")),
			Number:  atoi(m[3]),
			Year:    atoi(m[4]),
			Body:    m[5],
			Article: atoi(m[1]),
			Text:    strings.TrimSpace(m[0]),
		}
		key := c.Kind + " " + c.ID() + " " + strconv.Itoa(c.Article)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

// Wellformed reports whether the instrument type and the issuing code agree,
// and says what is wrong when they do not.
func (c Citation) Wellformed() (bool, string) {
	allowed, ok := bodies[c.Kind]
	if !ok {
		return false, fmt.Sprintf("%q is not an instrument type this checks", c.Kind)
	}
	for _, prefix := range allowed {
		if strings.HasPrefix(c.Body, prefix) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("a %s cannot carry the code %s, which belongs to another issuing body", c.Kind, c.Body)
}

// An Entry is one instrument the register holds.
type Entry struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`

	// Articles is how many điều the instrument has, so that a citation pointing
	// past the end of a real document is caught. Zero means the register did not
	// record it and the article is not checked.
	Articles int `json:"articles"`
}

// A Register is the instruments that exist.
//
// It is built from the legal shard, which is the one part of the corpus whose
// documents carry identifiers that either match something or do not. Without it
// this verifier checks the shape of a citation and nothing else, and shape is
// exactly what a model hallucinating a citation gets right.
type Register struct {
	entries map[string]Entry
}

// NewRegister returns an empty register.
func NewRegister() *Register { return &Register{entries: map[string]Entry{}} }

// Add records one instrument. It reports whether it was recorded, which is
// false for an identifier whose kind and code disagree, since a register that
// admits those cannot catch them.
func (r *Register) Add(kind, id string, articles int) bool {
	c, ok := parseID(strings.ToLower(kind), id)
	if !ok {
		return false
	}
	if ok, _ := c.Wellformed(); !ok {
		return false
	}
	r.entries[c.ID()] = Entry{Kind: c.Kind, ID: c.ID(), Articles: articles}
	return true
}

// Size is how many instruments the register holds.
func (r *Register) Size() int { return len(r.entries) }

// Check says whether a citation refers to something that exists, and what is
// wrong with it when it does not.
func (r *Register) Check(c Citation) (bool, string) {
	if ok, why := c.Wellformed(); !ok {
		return false, why
	}
	e, ok := r.entries[c.ID()]
	if !ok {
		return false, fmt.Sprintf("%s is not in the register, so it is a citation shaped string rather than a citation", c.ID())
	}
	if e.Kind != c.Kind {
		return false, fmt.Sprintf("%s is a %s and the answer called it a %s", c.ID(), e.Kind, c.Kind)
	}
	if c.Article > 0 && e.Articles > 0 && c.Article > e.Articles {
		return false, fmt.Sprintf("%s has %d articles and the answer cited article %d", c.ID(), e.Articles, c.Article)
	}
	return true, ""
}

// parseID reads an identifier such as 13/2023/NĐ-CP on its own.
func parseID(kind, id string) (Citation, bool) {
	cs := Citations(kind + " số " + id)
	if len(cs) != 1 {
		return Citation{}, false
	}
	return cs[0], true
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// A Quote verifies that an answer rests on the instruments it should, and on
// nothing it made up.
//
// The reward is the harmonic mean of two numbers, and it is two numbers because
// either one on its own is trivially gamed. Precision alone is collected by
// citing the Constitution in every answer and nothing else. Recall alone is
// collected by listing every instrument in the register. The arm exists to
// remove hallucinated citations without teaching the model to stop citing, and
// only the pair does that.
type Quote struct {
	register *Register
	want     map[string][]Citation
}

// NewQuote returns a verifier over a register.
func NewQuote(r *Register) *Quote {
	return &Quote{register: r, want: map[string][]Citation{}}
}

// Ask records what an answer to this prompt has to rest on. It reports whether
// the prompt was taken.
//
// Every required instrument has to be in the register already. A key that asks
// for a document the register does not hold makes the item unwinnable, and an
// unwinnable item is a group where every rollout scores the same, which is
// compute spent to learn nothing.
func (t *Quote) Ask(prompt string, must ...string) bool {
	if blank(prompt) || len(must) == 0 {
		return false
	}
	var want []Citation
	for _, s := range must {
		cs := Citations(s)
		if len(cs) != 1 {
			return false
		}
		if ok, _ := t.register.Check(cs[0]); !ok {
			return false
		}
		want = append(want, cs[0])
	}
	t.want[prompt] = want
	return true
}

// Items is how many prompts the key holds.
func (t *Quote) Items() int { return len(t.want) }

// Specialist is the arm this verifies.
func (t *Quote) Specialist() string { return "trich" }

// Verify grades one answer.
func (t *Quote) Verify(prompt, answer string) Verdict {
	want, ok := t.want[prompt]
	if !ok {
		return unchecked(t.Specialist(), "the key does not hold this prompt, so there is nothing the answer is supposed to rest on")
	}

	need := map[string]bool{}
	for _, c := range want {
		need[c.ID()] = true
	}

	got := Citations(answer)
	if len(got) == 0 {
		return checked(t.Specialist(), 0,
			"the answer cites nothing, and an answer that rests on nothing checkable is the thing this arm exists to stop")
	}

	hit, wrong, why := 0, 0, ""
	found := map[string]bool{}
	for _, c := range got {
		ok, bad := t.register.Check(c)
		if !ok {
			wrong++
			if why == "" {
				why = bad
			}
			continue
		}
		if need[c.ID()] && !found[c.ID()] {
			found[c.ID()] = true
			hit++
		}
	}

	precision := float64(hit) / float64(len(got))
	recall := float64(hit) / float64(len(want))
	reward := 0.0
	if precision+recall > 0 {
		reward = 2 * precision * recall / (precision + recall)
	}

	if why == "" {
		why = fmt.Sprintf("%d of the %d citations offered are among the %d the answer had to rest on",
			hit, len(got), len(want))
	} else {
		why = fmt.Sprintf("%d of %d citations landed, and %d did not: %s", hit, len(got), wrong, why)
	}
	return checked(t.Specialist(), reward, "%s", why)
}
