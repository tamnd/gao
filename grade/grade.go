// Package grade grades an answer against something that can be checked.
//
// Cham is to mark a paper. The post-training stack here is supervised
// finetuning, then reinforcement learning with verifiable rewards run as
// parallel specialists, then distillation of the specialists back into one
// model. There is no reward model anywhere in it, deliberately. A reward model
// is a second model whose mistakes become the first model's objective, and in
// Vietnamese, where the preference data would itself be translated, those
// mistakes would be systematic rather than random.
//
// So every specialist is trained against a program that can say whether an
// answer is right. That program is the specialist. The training loop is
// interchangeable and published in a dozen repositories, and the thing that
// decides what the model becomes is the check, which almost nobody publishes.
// An unpublished verifier is an unfalsifiable reward: a number that cannot be
// reproduced, argued with, or shown to have been gamed. Everything in this
// package ships with the models.
//
// Three rules shape the interface.
//
// A verifier that cannot check is not a verifier that returns zero. A rollout
// that hit the length limit mid sentence is a missing measurement, not a wrong
// answer, and scoring it zero teaches the model that long answers are bad,
// which is the opposite of what anybody wants from a long context model. So a
// [Verdict] carries Checked separately from Reward, and a group drops what it
// could not check rather than averaging it in. That is the overlong filtering
// the plan asks for, and it belongs here rather than in the trainer, because
// only the verifier knows whether it managed to look at the answer.
//
// A verifier has to be beatable only by doing the task. The interesting failure
// of a verifiable reward is not that it is wrong, it is that it is right about
// something easier than the task. Every verifier here ships with the answers
// that would beat it if it were built badly, as tests: the empty answer, the
// prompt handed back, the answer with the shape and none of the substance. A
// verifier that scores any of those above zero is a reward that trains the
// model to produce them.
//
// A verifier runs on CPU, without a network, and returns the same verdict every
// time. Seven specialists training in parallel each generate rollouts faster
// than one GPU can score them, so a verifier that wants a card is a verifier
// that becomes the bottleneck it exists to feed. Nothing in this interface
// takes a context or a client, which is the rule stated as a type.
package grade

import (
	"fmt"
	"strings"
)

// A Verdict is one answer checked by one verifier.
type Verdict struct {
	// Specialist names which verifier produced this.
	Specialist string `json:"specialist"`

	// Reward is in [0,1]. It is a share of something countable rather than a
	// judgment on a scale, because a scale invites a threshold and a threshold
	// invites tuning the threshold until the numbers look right.
	Reward float64 `json:"reward"`

	// Checked says whether the verifier managed to look at the answer at all.
	//
	// False is not zero and must not be treated as zero. An answer that was cut
	// off at the length limit, or that arrived in a form the verifier cannot
	// parse, has not been shown to be wrong. [Group.Add] drops it.
	Checked bool `json:"checked"`

	// Why is the verdict in one line, for the sample log that makes a reward
	// arguable after the fact.
	Why string `json:"why"`
}

// Verifier is one specialist's check.
//
// It takes the prompt as well as the answer because most of these checks are
// against the question rather than against a key: diacritic restoration is
// verified by comparing the answer to the prompt with marks on, and a citation
// is verified against what the prompt asked for.
type Verifier interface {
	// Specialist is the name the roster knows this by.
	Specialist() string

	// Verify grades one answer. It must not touch the network, must not need a
	// GPU, and must return the same verdict for the same two strings every time.
	Verify(prompt, answer string) Verdict
}

// A Specialist is one arm of the reinforcement learning stage.
//
// Seven of them train in parallel rather than one model on seven mixed tasks,
// because a mixed run averages the gradients of tasks that want different
// things and the result is a model mediocre at all seven. The cost of running
// them apart is that they have to be put back together, which is what the
// distillation stage is for and what P09-2 predicts the size of.
type Specialist struct {
	// Name is the arm, in Vietnamese, matching the package that verifies it
	// where one exists.
	Name string `json:"name"`

	// Task is what the model is asked to do.
	Task string `json:"task"`

	// Checked is what the reward is computed from. This is the sentence that
	// decides whether the arm is worth running, so it says what is counted
	// rather than what is hoped for.
	Checked string `json:"checked"`

	// Source is where the verifier gets its ground truth.
	Source string `json:"source"`

	// Written says whether the verifier is in this package. The roster is
	// published whole, including the arms that are specified and not built, so
	// that a reader can see what is missing rather than infer it from silence.
	Written bool `json:"written"`
}

// Specialists is the roster.
//
// The first two are the ones this project gets cheaply and nobody else has.
// Diacritic restoration is a task whose answer key is the corpus itself, so the
// training set is 300B tokens of perfectly verified signal for something that
// needs real Vietnamese to do. Legal citation is verified against a register
// built out of the legal shard, which is the one part of the corpus where the
// documents have identifiers that either exist or do not.
//
// The remaining five are specified here and not written. Each names what would
// have to be countable for the arm to be worth running, which is the honest way
// to carry an unbuilt verifier in a published roster.
func Specialists() []Specialist {
	return []Specialist{
		{
			Name:    "dau",
			Task:    "restore the diacritics on a page of Vietnamese typed bare",
			Checked: "the share of the page's marks that came back, with an answer that changed the letters scoring zero rather than being aligned",
			Source:  "any page of the corpus, since normalize.Bare turns it into a question whose answer sits beside it",
			Written: true,
		},
		{
			Name:    "trich",
			Task:    "answer a legal question and cite the instruments the answer rests on",
			Checked: "the share of the citations in the answer that parse, exist in the register, and carry a body code the document type can have",
			Source:  "a register built from the legal shard, which is the part of the corpus whose documents have identifiers",
			Written: true,
		},
		{
			Name:    "kim",
			Task:    "find and quote the one passage in a long document that answers the question",
			Checked: "whether the quoted span is the planted span, byte for byte, which is why the needle is planted rather than found",
			Source:  "vi-needle, built on gamingpc from long documents in the corpus",
			Written: false,
		},
		{
			Name:    "theo",
			Task:    "follow an instruction with a countable constraint in it, in Vietnamese",
			Checked: "whether the constraint holds, by a parser rather than by a model, over constraints chosen because a program can check them",
			Source:  "vi-adherence, whose items are written constraint first",
			Written: false,
		},
		{
			Name:    "toan",
			Task:    "solve a problem stated in Vietnamese and give the final answer in a fixed place",
			Checked: "exact match on the final answer after normalization, with the working unscored, since scoring the working needs a judge",
			Source:  "school level problems with published answers, translated only where the mathematics does not care",
			Written: false,
		},
		{
			Name:    "ma",
			Task:    "write a function to a Vietnamese specification",
			Checked: "whether the hidden tests pass in a sandbox, which is the one arm here that needs isolation rather than only CPU",
			Source:  "problems whose tests are written with the problem rather than generated from a reference solution",
			Written: false,
		},
		{
			Name:    "tu-choi",
			Task:    "refuse what should be refused and answer what should not",
			Checked: "refusal detected by a classifier over both halves at once, since a model that refuses everything scores perfectly on one half",
			Source:  "vi-overrefusal, built with the harmless half weighted equally with the harmful half",
			Written: false,
		},
	}
}

// Lookup finds a specialist by name.
func Lookup(name string) (Specialist, bool) {
	for _, s := range Specialists() {
		if s.Name == name {
			return s, true
		}
	}
	return Specialist{}, false
}

// unchecked is the verdict for an answer the verifier could not look at.
func unchecked(specialist, why string, args ...any) Verdict {
	return Verdict{Specialist: specialist, Checked: false, Why: fmt.Sprintf(why, args...)}
}

// checked is the verdict for an answer the verifier did look at, clamped,
// because a reward outside [0,1] is a bug in a verifier rather than a very good
// or very bad answer and it should not reach a trainer either way.
func checked(specialist string, reward float64, why string, args ...any) Verdict {
	switch {
	case reward < 0:
		reward = 0
	case reward > 1:
		reward = 1
	}
	return Verdict{
		Specialist: specialist,
		Reward:     reward,
		Checked:    true,
		Why:        fmt.Sprintf(why, args...),
	}
}

// String renders one verdict the way it goes into the sample log.
func (v Verdict) String() string {
	if !v.Checked {
		return fmt.Sprintf("%s: not checked, %s", v.Specialist, v.Why)
	}
	return fmt.Sprintf("%s: %.3f, %s", v.Specialist, v.Reward, v.Why)
}

// blank reports whether an answer has nothing in it. It is checked first by
// every verifier here, because an empty string satisfies a surprising number of
// checks written in terms of what an answer must not contain.
func blank(s string) bool { return strings.TrimSpace(s) == "" }

// Overlong is the verdict for a rollout the sampler stopped at the token limit.
//
// It is here because most verifiers cannot tell a truncated answer from a bad
// one by looking at it. [Mark] can, since a cut off restoration is a prefix of
// the page it was restoring, but a legal answer that stops mid sentence just
// looks like an answer with fewer citations in it, and scoring it as one trains
// the model out of the long answers the arm exists to produce. The sampler
// knows it hit the limit, so the sampler says so.
func Overlong(specialist string) Verdict {
	return unchecked(specialist, "the sampler stopped this answer at the token limit, so it has not been shown to be wrong")
}
