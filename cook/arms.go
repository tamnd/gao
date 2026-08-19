package cook

// The continued pretraining comparison.
//
// One question decides whether two years of corpus work was worth doing: does
// gao's data train a better Vietnamese model than the data everybody already
// uses. It is answered by continued pretraining rather than by a run from
// scratch because it is three times cheaper and it answers early, and because a
// corpus that cannot win this comparison has a problem a larger model will not
// fix.
//
// The arms are locked here, before any of them runs, because an arm added after
// a result exists is not a comparison. So is an arm quietly given a longer
// schedule than the others.

import (
	"fmt"
	"strings"

	"github.com/tamnd/gao/fleet"
)

// An Arm is one side of the comparison. It carries the data and nothing else.
//
// Everything that is not data lives in [Matched] and is shared by construction
// rather than by three copies that have to be kept the same. That is the whole
// design of this file: a comparison whose arms differ in two things measures
// neither of them, and the cheapest way to make that impossible is to give the
// arms nowhere to put a second difference.
type Arm struct {
	// ID is what the trained model is called.
	ID string
	// Data is what it is trained on.
	Data string
	// Why is what this arm is in the comparison to separate.
	Why string
}

// Arms is the comparison, locked.
//
// The third arm is the one most projects skip and the one that makes the result
// mean something. Without it, a win for gao says the corpus is better and does
// not say whether that is because it is larger or because it is cleaner, and
// those two answers have completely different consequences for what anybody
// should do next.
func Arms() []Arm {
	return []Arm{
		{"com-8B-cpt-gao", "gao", "the corpus, as it ships"},
		{"com-8B-cpt-culturax", "CulturaX Vietnamese", "the data everybody currently uses, as it ships"},
		{"com-8B-cpt-culturax-filtered", "CulturaX Vietnamese through gao's cleaning", "separates having more data from cleaning it better, which is the difference the other two arms cannot see"},
	}
}

// A Recipe is everything the arms hold in common.
type Recipe struct {
	// Tokens is the length of each run.
	Tokens int64
	// Vietnamese is the Vietnamese share, as a percentage.
	Vietnamese float64
	// Replay is the share matched to the base model's own pretraining
	// distribution, as a percentage.
	Replay float64
	// Batch is the token count per optimizer step.
	Batch int64
	// LR is the schedule, in words, because it is the same words for all three.
	LR string
	// Tokenizer is whose vocabulary the runs use.
	Tokenizer string
	// Gate is what gao has to clear for the from scratch run to start.
	Gate string
}

// Matched is the recipe every arm runs under.
//
// The replay share is high by the usual standard on purpose. A continued
// pretraining run pushed to ninety percent Vietnamese produces a model that is
// better at Vietnamese and materially worse at reasoning, and since it inherits
// all of its reasoning from the base, that trade is a net loss. Thirty percent
// is the prior and the slate ablates fifty and ten around it.
func Matched() Recipe {
	return Recipe{
		Tokens:     200 * billion,
		Vietnamese: 70,
		Replay:     30,
		Batch:      4 << 20,
		LR:         "re-warm to a tenth of the base's peak over the first 1% of the run, hold, then decay to zero over the last 20%",
		Tokenizer:  "the base model's, unchanged, because a vocabulary swap and a data change in one run measure each other",
		Gate:       "gao beats CulturaX by at least 4 points of VMLU and beats its own base by at least 6, or no GPU-hour is spent on the from scratch run",
	}
}

// CheckArms reports anything about the comparison that would keep it from
// measuring what it is for.
func CheckArms() []string {
	var out []string
	arms := Arms()
	if len(arms) != 3 {
		out = append(out, fmt.Sprintf("the comparison has %d arms, and it is defined with three", len(arms)))
	}
	seen := map[string]bool{}
	for _, a := range arms {
		if seen[a.Data] {
			out = append(out, fmt.Sprintf("%s trains on the same data as another arm, so one of them measures nothing", a.ID))
		}
		seen[a.Data] = true
		if a.Why == "" {
			out = append(out, fmt.Sprintf("%s is in the comparison with nothing written down about what it separates", a.ID))
		}
	}
	if r := Matched(); r.Vietnamese+r.Replay != 100 {
		out = append(out, fmt.Sprintf("the recipe reads %.0f%% Vietnamese and replays %.0f%%, which is not a run", r.Vietnamese, r.Replay))
	}
	return out
}

// Fleet is what the four boxes do while any of this runs.
//
// It is written down because the fleet is the hardware we own, and every other
// slice of this project runs on it, so the assumption that this one does too is
// the natural one to make and it is wrong by three orders of magnitude. The
// arithmetic is in [Shortfall]. What the fleet does here is prepare the data,
// generate the synthetic slice on the one GPU, and run the evaluation that
// decides the answer, which is the part worth keeping on hardware nobody else
// controls.
func Fleet() string {
	return strings.TrimSpace(`
prepare the data:      every box, since the mixture is built out of the store
generate gao-synth:    gamingpc, the only GPU, and the generator card records the box and the batch settings
run the evaluations:   gamingpc, so the numbers that decide the gate come off hardware we own
train anything:        nowhere on the fleet, and the gap is below, in the unit that settles the argument
`)
}

// Shortfall is how far the fleet is from the memory a run needs, as a
// multiple, along with the two numbers it comes from.
//
// The from scratch run is planned for 256 accelerators with 80 GB each. The
// fleet has one card with 24 GB. Stating that as a ratio rather than as "does
// not fit" is what stops somebody proposing a smaller batch size as though the
// gap were a factor of two.
func Shortfall() (need, have int64, times float64) {
	const (
		accelerators = 256
		perCard      = 80 << 30
	)
	need = accelerators * perCard
	for _, b := range fleet.Boxes {
		if b.HasGPU() {
			have += b.GPUMemory
		}
	}
	if have == 0 {
		return need, 0, 0
	}
	return need, have, float64(need) / float64(have)
}
