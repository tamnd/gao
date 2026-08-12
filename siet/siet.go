// Package siet fixes the GRPO step this project trains its specialists with,
// and reads a run back against it.
//
// Siết is to tighten. What is tightened is the ratio between the policy being
// trained and the policy the rollouts came off, and every knob in this package
// is about how hard to tighten it and what to do with the samples that escape.
//
// The training loop itself is not interesting and it is not here. It is
// published in a dozen repositories and any of them will do. What decides what
// the specialists become is four settings that a loop leaves to whoever calls
// it, and each of the four is the fix for a failure that has a name.
//
// Clipping is decoupled, with a higher upper bound than lower. Symmetric
// clipping caps how far a token's probability may rise by the same amount it
// caps the fall, and since most tokens fall, the run loses its spread and the
// model converges on whatever it was already going to say. That is entropy
// collapse and it is the ordinary way a GRPO run dies. A wider upper bound
// leaves room for the rare token that turned out to be right.
//
// The loss is aggregated over tokens rather than over sequences. Sequence level
// aggregation gives every answer the same weight whatever its length, which
// divides a long correct answer's gradient by its own length. The specialist
// this project cares most about restores tone marks over a whole document, so
// its correct answers are long by construction, and sequence aggregation would
// train it toward short ones.
//
// Prompts whose rollouts all scored the same are dropped rather than kept at
// zero advantage. By the middle of a run they are most of the batch, because
// the model has learned the easy prompts and cannot do the hard ones, and a
// step spent on them is a step of arithmetic that moves nothing. Dropping them
// costs a batch that no longer fills itself, which is what the oversampling
// factor is for and why this package refuses a configuration that drops without
// oversampling.
//
// Answers cut off by the length limit are filtered rather than penalized. A
// length penalty is a reward signal about length, and a model that gets one
// learns to stop early rather than to answer briefly. cham already refuses to
// grade a truncated rollout, so the filtering happens where the verifier is and
// what is left here is the count, because a run whose truncation share climbs
// is a run whose length limit has quietly become its grader.
//
// A configuration is checked before it runs and a log is checked after, and the
// two checks are different. The first asks whether these settings can be what
// they claim to be. The second asks whether they did anything: an upper clip
// bound that never binds is symmetric clipping wearing a different name, and it
// will be reported as the fix for an entropy collapse that it did not prevent.
package siet

import (
	"fmt"
	"sort"
	"strings"
)

// Group is how many rollouts one prompt is sampled into, from the plan.
//
// The group is its own baseline, so the group size is the sample size of every
// advantage in the batch.
const Group = 16

// MinGroup is the smallest group this project will run.
//
// Below eight the baseline is noise on the prompts that matter, which are the
// hard Vietnamese ones where the rollouts disagree, and a noisy baseline gives
// the largest advantages to the prompts it understood least.
const MinGroup = 8

// EpsLow and EpsHigh are the decoupled clipping bounds.
//
// The gap between them is the whole of clip-higher. Equal bounds are symmetric
// clipping, and the run that comes out of them collapses in a way that is
// visible in the entropy long before it is visible in the reward.
const (
	EpsLow  = 0.20
	EpsHigh = 0.28
)

// MaxEpsHigh is the widest upper bound worth calling a trust region.
//
// This is a judgement rather than a measurement. Past a half the ratio may
// change by more than the update was ever meant to allow in one step, and at
// that point the clipping is decoration and the run is ordinary policy gradient
// with extra arithmetic.
const MaxEpsHigh = 0.50

// Context is the base model's context, which is the ceiling every length in a
// post-training configuration sits under.
const Context = 131072

// Aggregation is how the loss is summed before it is divided.
const (
	Token    = "token"
	Sequence = "sequence"
)

// Collapse is the share of its starting entropy a run may lose before the
// entropy is the story.
//
// Some fall is the run learning. Half of it gone is the distribution closing,
// and it does not come back on its own: the rollouts a collapsed policy
// produces are the ones that collapsed it.
const Collapse = 0.50

// MaxTruncated is the share of rollouts that may hit the length limit before
// the limit is deciding the reward.
//
// A few percent is the tail of a length distribution. A fifth is a run where
// the verifier is scoring what fitted rather than what was answered, and the
// number climbs quietly because every one of those rollouts is dropped and
// dropped rollouts do not appear in the reward.
const MaxTruncated = 0.10

// Window is how many steps at each end of a run are read as the run's start and
// its present.
//
// A single step is a sample of one and the two numbers that matter here, the
// entropy and the yield, both move step to step by more than the trend they are
// being read for.
const Window = 10

// A Recipe is the GRPO step as it is configured, which is the part of the
// training run that this project decides rather than inherits.
type Recipe struct {
	// Group is rollouts per prompt.
	Group int `json:"group"`

	// EpsLow and EpsHigh are the clipping bounds, decoupled.
	EpsLow  float64 `json:"eps_low"`
	EpsHigh float64 `json:"eps_high"`

	// Aggregation is Token or Sequence.
	Aggregation string `json:"aggregation"`

	// Penalized says whether an answer that hit the length limit is scored down
	// rather than dropped.
	Penalized bool `json:"overlong_penalized"`

	// MaxResponse is the length limit in tokens, and Prompt is what the prompt
	// side is allowed, because the two of them share the context.
	MaxResponse int `json:"max_response"`
	Prompt      int `json:"max_prompt"`

	// Batch is the prompts a step is meant to train on, after the flat groups
	// have been dropped, and Oversample is how many are sampled to get them.
	Batch      int     `json:"batch"`
	Oversample float64 `json:"oversample"`

	// KL is the coefficient on the penalty against the reference policy, and
	// Ablated says whether anybody here ran the comparison that chose it.
	KL      float64 `json:"kl"`
	Ablated bool    `json:"kl_ablated"`
}

// Plan is the recipe the training plan fixes, and it is what gao runs unless
// somebody writes down why not.
func Plan() Recipe {
	return Recipe{
		Group:       Group,
		EpsLow:      EpsLow,
		EpsHigh:     EpsHigh,
		Aggregation: Token,
		MaxResponse: 8192,
		Prompt:      32768,
		Batch:       512,
		Oversample:  3.0,
		KL:          0,
		Ablated:     true,
	}
}

// A Row is one line of the recipe as it is printed, which is the same table the
// plan carries and the reason each line is in it.
type Row struct {
	Element string `json:"element"`
	Setting string `json:"setting"`
	Why     string `json:"why"`
}

// Rows renders the recipe with the reason for every setting beside it, because
// a table of numbers with no reasons is a table somebody will tune.
func (r Recipe) Rows() []Row {
	kl := fmt.Sprintf("%g", r.KL)
	if r.KL == 0 {
		kl = "none"
	}
	return []Row{
		{"critic", "none, the group is the baseline", "a value network is a second model whose errors become the objective"},
		{"group size", fmt.Sprintf("%d rollouts a prompt", r.Group), "the group is its own baseline, so this is the sample size of every advantage"},
		{"clipping", fmt.Sprintf("%.2f low, %.2f high", r.EpsLow, r.EpsHigh), "a wider upper bound is what keeps the run from closing on what it already says"},
		{"aggregation", r.Aggregation, "over sequences a long correct answer is divided by its own length"},
		{"flat groups", fmt.Sprintf("dropped, %.1fx sampled to fill", r.Oversample), "by mid run they are most of the batch and none of them moves anything"},
		{"overlong", overlong(r.Penalized), "a length penalty trains stopping early rather than answering briefly"},
		{"lengths", fmt.Sprintf("%d prompt, %d response", r.Prompt, r.MaxResponse), "both sit under the base model's context and the sum is what has to fit"},
		{"kl to reference", kl, "the evidence is mixed and domain dependent, so it is ablated rather than copied"},
		{"reward", "the verifier, and nothing learned", "an unpublished reward model is an unfalsifiable reward"},
	}
}

func overlong(penalized bool) string {
	if penalized {
		return "penalized"
	}
	return "filtered"
}

// Blocking is everything in a configuration that cannot be what it says it is.
// A recipe with anything here is not run.
func (r Recipe) Blocking() []string {
	var why []string

	switch {
	case r.EpsHigh < r.EpsLow:
		why = append(why, fmt.Sprintf("the upper clip bound is %.2f against a lower bound of %.2f, so the run tightens what should be loose and the collapse it is meant to prevent arrives faster", r.EpsHigh, r.EpsLow))
	case r.EpsHigh == r.EpsLow:
		why = append(why, fmt.Sprintf("both clip bounds are %.2f, which is symmetric clipping with clip-higher written next to it", r.EpsLow))
	case r.EpsHigh > MaxEpsHigh:
		why = append(why, fmt.Sprintf("the upper clip bound is %.2f, and past %.2f the ratio may move further in one step than the update was meant to allow, so there is no trust region left to speak of", r.EpsHigh, MaxEpsHigh))
	}
	if r.EpsLow <= 0 {
		why = append(why, fmt.Sprintf("the lower clip bound is %.2f, so nothing bounds a token whose probability is falling", r.EpsLow))
	}

	if r.Group < MinGroup {
		why = append(why, fmt.Sprintf("a group of %d is a baseline off %d samples, and under %d the largest advantages in the batch come from the prompts the model understood least", r.Group, r.Group, MinGroup))
	}

	if r.Aggregation != Token {
		why = append(why, fmt.Sprintf("the loss is aggregated over %ss, which divides a long correct answer by its own length, and restoring tone marks over a document is long by construction", r.Aggregation))
	}

	if r.Penalized {
		why = append(why, "answers that hit the length limit are penalized rather than filtered, which is a reward signal about length and it trains stopping early")
	}

	switch {
	case r.MaxResponse <= 0 || r.Prompt <= 0:
		why = append(why, "the configuration does not say how long a prompt and an answer may be, and the limit that is not written down is the one that ends up doing the grading")
	case r.Prompt+r.MaxResponse > Context:
		why = append(why, fmt.Sprintf("a prompt of %d and an answer of %d need %d tokens against the %d the base model has, so the longest prompts in the set cannot be answered at all", r.Prompt, r.MaxResponse, r.Prompt+r.MaxResponse, Context))
	}

	if r.Batch <= 0 {
		why = append(why, "the configuration does not say how many prompts a step trains on, which is what the oversampling is sized against")
	}
	if r.Oversample <= 1 {
		why = append(why, fmt.Sprintf("flat groups are dropped and the sampler draws %.1fx the batch, so every dropped group is a smaller step and the batch size in the run notes is a batch size nothing ran at", r.Oversample))
	}

	if r.KL != 0 && !r.Ablated {
		why = append(why, fmt.Sprintf("the run carries a KL of %g that nobody ablated, and a coefficient copied from another paper is a coefficient about another model's reference policy", r.KL))
	}

	return why
}

// Holds says whether the configuration may be trained with.
func (r Recipe) Holds() bool { return len(r.Blocking()) == 0 }

// A Step is one training step as the log recorded it.
type Step struct {
	Step int    `json:"step"`
	Box  string `json:"box"`

	// Groups is the prompts sampled and Kept is what survived the drop.
	Groups int `json:"groups"`
	Kept   int `json:"kept"`

	// Rollouts is the answers generated and Truncated is how many of them ran
	// into the length limit.
	Rollouts  int `json:"rollouts"`
	Truncated int `json:"truncated"`

	// ClipLow and ClipHigh are the share of tokens that hit each bound. The
	// upper one is the only evidence that decoupling the bounds did anything.
	ClipLow  float64 `json:"clip_low"`
	ClipHigh float64 `json:"clip_high"`

	// Entropy is the policy's entropy in nats a token, and Reward is the mean
	// over the rollouts a verifier could check.
	Entropy float64 `json:"entropy"`
	Reward  float64 `json:"reward"`
}

// A Run is a configuration and the steps that came off it.
type Run struct {
	Specialist string `json:"specialist"`
	Recipe     Recipe `json:"recipe"`
	Steps      []Step `json:"steps"`
}

// Sort puts the steps in the order they were taken, since a log written by
// several workers arrives in the order they finished.
func (r *Run) Sort() {
	sort.SliceStable(r.Steps, func(i, j int) bool { return r.Steps[i].Step < r.Steps[j].Step })
}

// Box names the hardware every step agrees on, and is empty when they do not
// agree or when nobody wrote it down.
func (r Run) Box() string {
	if len(r.Steps) == 0 {
		return ""
	}
	box := r.Steps[0].Box
	for _, s := range r.Steps {
		if s.Box != box {
			return ""
		}
	}
	return box
}

// Yield is the share of sampled groups that produced a gradient, over the whole
// run.
func (r Run) Yield() float64 {
	var groups, kept int
	for _, s := range r.Steps {
		groups += s.Groups
		kept += s.Kept
	}
	return share(kept, groups)
}

// Late is the yield over the last steps of the run, which is the one that says
// whether the oversampling is still enough.
func (r Run) Late() float64 {
	var groups, kept int
	for _, s := range r.tail() {
		groups += s.Groups
		kept += s.Kept
	}
	return share(kept, groups)
}

// Truncation is the share of rollouts that hit the length limit.
func (r Run) Truncation() float64 {
	var rollouts, cut int
	for _, s := range r.Steps {
		rollouts += s.Rollouts
		cut += s.Truncated
	}
	return share(cut, rollouts)
}

// Entropy reports where the run started and where it is now, averaged over a
// window at each end.
func (r Run) Entropy() (start, now float64) {
	return mean(r.head(), func(s Step) float64 { return s.Entropy }),
		mean(r.tail(), func(s Step) float64 { return s.Entropy })
}

// Reward is the same reading for the mean reward, which is what the entropy has
// to be read against: a run that lost its spread and gained nothing is a
// different event from a run that lost it because it learned the task.
func (r Run) Reward() (start, now float64) {
	return mean(r.head(), func(s Step) float64 { return s.Reward }),
		mean(r.tail(), func(s Step) float64 { return s.Reward })
}

// Binds says whether the upper clip bound ever bound anything. It is the only
// evidence in a log that decoupling the bounds was more than a line in a
// configuration file.
func (r Run) Binds() bool {
	for _, s := range r.Steps {
		if s.ClipHigh > 0 {
			return true
		}
	}
	return false
}

// Fills says whether the sampler drew enough prompts to fill the batch it was
// configured for, at the yield the run is actually running at.
func (r Run) Fills() bool {
	if r.Late() <= 0 {
		return false
	}
	return r.Recipe.Oversample*r.Late() >= 1
}

// Needed is the oversampling factor the late yield asks for, which is the
// number to change when a run stops filling.
func (r Run) Needed() float64 {
	if r.Late() <= 0 {
		return 0
	}
	return 1 / r.Late()
}

// Blocking is everything wrong with the log itself. None of it is a fact about
// the training run, and all of it stops the run from being read as one.
func (r Run) Blocking() []string {
	var why []string
	if len(r.Steps) == 0 {
		why = append(why, "there are no steps in this log, so there is nothing to read")
		return why
	}
	if why := r.Recipe.Blocking(); len(why) > 0 {
		return []string{fmt.Sprintf("the configuration these steps came off does not hold: %s", why[0])}
	}
	if r.Box() == "" {
		why = append(why, "the steps do not agree on the hardware they were taken on, or none of them names it, and a training curve with no hardware under it cannot be compared with another one")
	}

	seen := map[int]bool{}
	for _, s := range r.Steps {
		if seen[s.Step] {
			why = append(why, fmt.Sprintf("step %d is in the log twice, and a step counted twice is a step whose rollouts are counted twice", s.Step))
			break
		}
		seen[s.Step] = true
	}

	for _, s := range r.Steps {
		switch {
		case s.Kept > s.Groups:
			why = append(why, fmt.Sprintf("step %d kept %d of %d groups, which is more than it sampled", s.Step, s.Kept, s.Groups))
		case s.Truncated > s.Rollouts:
			why = append(why, fmt.Sprintf("step %d truncated %d of %d rollouts, which is more than it generated", s.Step, s.Truncated, s.Rollouts))
		case s.Groups > 0 && s.Rollouts != s.Groups*r.Recipe.Group:
			why = append(why, fmt.Sprintf("step %d sampled %d groups of %d, which is %d rollouts, and the log records %d", s.Step, s.Groups, r.Recipe.Group, s.Groups*r.Recipe.Group, s.Rollouts))
		case s.ClipLow < 0 || s.ClipLow > 1 || s.ClipHigh < 0 || s.ClipHigh > 1:
			why = append(why, fmt.Sprintf("step %d reports clipped shares of %.3f and %.3f, and a share of the tokens is in [0,1]", s.Step, s.ClipLow, s.ClipHigh))
		case s.Entropy < 0:
			why = append(why, fmt.Sprintf("step %d reports an entropy of %.3f", s.Step, s.Entropy))
		case s.Reward < 0 || s.Reward > 1:
			why = append(why, fmt.Sprintf("step %d reports a mean reward of %.3f, and a verifier here returns a share of something countable", s.Step, s.Reward))
		}
		if len(why) > 0 {
			break
		}
	}
	return why
}

// Faults is what the run says about the training, in named sentences. Unlike
// Blocking, everything here is a reading rather than a reason to refuse one.
func (r Run) Faults() []string {
	var faults []string
	if len(r.Steps) == 0 {
		return nil
	}

	start, now := r.Entropy()
	rewardStart, rewardNow := r.Reward()
	if start > 0 && now < start*Collapse {
		faults = append(faults, fmt.Sprintf(
			"the entropy went from %.3f to %.3f, which is under %.0f%% of where it started, and the reward went from %.3f to %.3f, so this is the policy closing rather than the policy learning",
			start, now, Collapse*100, rewardStart, rewardNow))
	}

	if !r.Binds() {
		faults = append(faults, fmt.Sprintf(
			"no step clipped a token at the upper bound, so %.2f and %.2f behaved as one bound and this run is not evidence about clip-higher either way",
			r.Recipe.EpsLow, r.Recipe.EpsHigh))
	}

	if cut := r.Truncation(); cut > MaxTruncated {
		faults = append(faults, fmt.Sprintf(
			"%.1f%% of rollouts hit the %d token limit against a line of %.0f%%, and every one of them was dropped unchecked, so the length limit is grading answers the verifier never saw",
			cut*100, r.Recipe.MaxResponse, MaxTruncated*100))
	}

	if !r.Fills() {
		faults = append(faults, fmt.Sprintf(
			"the late yield is %.1f%% and the sampler draws %.1fx the batch, so a step trains on fewer than the %d prompts it is configured for and wants %.1fx to fill",
			r.Late()*100, r.Recipe.Oversample, r.Recipe.Batch, r.Needed()))
	}

	return faults
}

// Holds says whether the run is one the specialist may be trained out of.
func (r Run) Holds() bool { return len(r.Blocking()) == 0 && len(r.Faults()) == 0 }

// Verdict is the sentence that goes in the run notes.
func (r Run) Verdict() string {
	if why := r.Blocking(); len(why) > 0 {
		return "This log is not a training run that can be read: " + why[0] + "."
	}
	start, now := r.Entropy()
	rewardStart, rewardNow := r.Reward()
	head := fmt.Sprintf("%s ran %d steps on %s, %.3f to %.3f reward and %.3f to %.3f entropy, at %.0f%% yield.",
		r.name(), len(r.Steps), r.Box(), rewardStart, rewardNow, start, now, r.Yield()*100)

	if faults := r.Faults(); len(faults) > 0 {
		return fmt.Sprintf("%s %s to read before the reward is: %s.", head, count(len(faults), "thing"), strings.Join(faults, ". "))
	}
	return head + fmt.Sprintf(" The upper bound clipped tokens rather than sitting unused, %.1f%% of rollouts hit the length limit against a line of %.0f%%, and %.1fx sampling still fills the %d prompt batch at this yield, so what the reward did is what the training did.",
		r.Truncation()*100, MaxTruncated*100, r.Recipe.Oversample, r.Recipe.Batch)
}

func (r Run) name() string {
	if r.Specialist == "" {
		return "the run"
	}
	return r.Specialist
}

func (r Run) head() []Step {
	if len(r.Steps) <= Window {
		return r.Steps
	}
	return r.Steps[:Window]
}

func (r Run) tail() []Step {
	if len(r.Steps) <= Window {
		return r.Steps
	}
	return r.Steps[len(r.Steps)-Window:]
}

func share(part, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func mean(steps []Step, f func(Step) float64) float64 {
	if len(steps) == 0 {
		return 0
	}
	var total float64
	for _, s := range steps {
		total += f(s)
	}
	return total / float64(len(steps))
}

// count writes a count with its noun, since the verdict reads as a sentence
// rather than as a field.
func count(n int, noun string) string {
	if n == 1 {
		return "One " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
