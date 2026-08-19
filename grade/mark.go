package grade

// The diacritic restoration specialist.

import (
	"strings"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/inspect"
	"github.com/tamnd/gao/mark"
	"github.com/tamnd/gao/normalize"
)

// MinMarked is the share of characters that have to carry a mark for a page to
// be worth training on. It is dau's floor, for dau's reason: a page typed bare
// is not an answer key, it is a second copy of the question.
const MinMarked = 0.12

// A Mark verifies diacritic restoration.
//
// This is the arm this project gets for free and nobody else has. Taking the
// marks off a page of Vietnamese is a function, putting them back is not, so
// every page in the corpus is a prompt whose exact answer is already sitting
// beside it. That is 300B tokens of perfectly verified reward signal for a task
// that cannot be done without understanding the language, and it costs no
// annotator and no reward model.
//
// The key is loaded rather than derived, because the verifier is given the bare
// text and only the corpus knows what was under it.
type Mark struct {
	key map[string]string
}

// NewMark returns a verifier with nothing in its key yet.
func NewMark() *Mark { return &Mark{key: map[string]string{}} }

// Learn adds one page. It reports whether the page was taken.
//
// A page under [MinMarked] is refused, and so is one whose bare form is already
// in the key with different marks on it. The second case is the one worth
// naming: two pages that differ only in their marks give the same prompt two
// answers, and a verifier holding both would score a correct restoration of one
// of them as wrong roughly half the time.
func (d *Mark) Learn(page string) bool {
	if blank(page) || markedShare(page) < MinMarked {
		return false
	}
	// The page is trimmed before it becomes a key, because a document read off
	// disk ends in a newline and a prompt read out of a rollout file does not,
	// and a key that misses on that grades nothing and says the prompt is
	// unknown.
	page = strings.TrimSpace(page)
	prompt := normalize.Bare(page)
	if had, ok := d.key[prompt]; ok && had != page {
		delete(d.key, prompt)
		return false
	}
	d.key[prompt] = page
	return true
}

// Items is how many pages the key holds.
func (d *Mark) Items() int { return len(d.key) }

// Specialist is the arm this verifies.
func (d *Mark) Specialist() string { return "dau" }

// Verify grades one restoration.
//
// The reward is the share of the page's marks that came back. It is not
// character accuracy, which starts at about 76% for a model that does nothing
// at all, because only about a quarter of Vietnamese characters carry a mark.
//
// The floor is still high: answering every bare spelling with the marked
// spelling it most often has, using no context whatsoever, restores about two
// thirds of the marks. That floor does not need to be subtracted here. The
// group centers every reward on the mean of what the same prompt produced, so a
// constant that every rollout collects cancels before it reaches a gradient,
// and subtracting it here would mean choosing a number and defending it.
//
// An answer that changed the letters rather than only the marks scores zero
// rather than being aligned against the page. Alignment puts a judgment inside
// a reward, and a specialist that can collect partial credit for a paraphrase
// will learn to paraphrase.
func (d *Mark) Verify(prompt, answer string) Verdict {
	prompt = strings.TrimSpace(prompt)
	page, ok := d.key[prompt]
	if !ok {
		return unchecked(d.Specialist(), "the key does not hold this prompt, so there is nothing to check the answer against")
	}
	if blank(answer) {
		return checked(d.Specialist(), 0, "the answer is empty")
	}

	bare := normalize.Bare(answer)
	switch {
	case bare == prompt:
		r := mark.Grade(mark.NewItem(doc.SumString(page), page), answer)
		return checked(d.Specialist(), 1-r.Score.DER(),
			"%d of %d marks came back, and %d of %d syllables are exactly right",
			r.Score.Marked-r.Score.Lost, r.Score.Marked, r.Right, r.Syllables)

	case strings.HasPrefix(prompt, bare):
		// The answer is the beginning of the right page and then nothing. That
		// is a rollout that hit the length limit, not a wrong restoration, and
		// scoring it zero would teach the model to answer briefly.
		return unchecked(d.Specialist(),
			"the answer stops after %d characters of a %d character page, which is a rollout cut off rather than an answer",
			len(bare), len(prompt))

	default:
		return checked(d.Specialist(), 0, "the answer is not the page with marks added, so it rewrote the text rather than restoring it")
	}
}

// markedShare is the share of characters carrying a mark, counted the way
// inspect counts them so that this floor and dau's are the same floor.
func markedShare(text string) float64 {
	letters := inspect.Letters(text)
	if len(letters) == 0 {
		return 0
	}
	marked := 0
	for _, l := range letters {
		if l.Marked() {
			marked++
		}
	}
	return float64(marked) / float64(len(letters))
}
