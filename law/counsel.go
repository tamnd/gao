// Package law is gao's legal position, recorded as data rather than as prose in
// a document nobody opens twice.
//
// Three things live here. The ten questions gao has put to counsel, each with the
// position gao acts on until an answer arrives. The license determination for
// every source, one row per body of material, with the evidence that decided it.
// And the publication posture, which is the rule that says what actually ships
// for a document of each license class.
//
// Two of those three are load bearing on code. A determination is what sets a
// document's license class and license evidence at ingest, and the posture is
// what the release step reads to decide whether a shard carries text or only
// metadata and a URL. A rule that lives in a paragraph gets applied by whoever
// remembers the paragraph.
//
// The questions are here for a different reason. Legal review on this project is
// a check rather than a blocker, which only works if every question carries a
// written default that the work proceeds under while the answer is outstanding.
// Writing the defaults down is the whole difference between proceeding under a
// stated assumption and proceeding without noticing there was a question.
//
// None of this is legal advice and none of it is counsel's answer. It is gao's
// position, and where the position is an assumption, [Question.Answered] says so.
package law

// FiledOn is when the questions below went to counsel, in ISO 8601. A question
// without a filing date is a question somebody meant to ask.
const FiledOn = "2026-08-03"

// Question is one item on the agenda for counsel.
//
// Every question carries a default, and the default is not a guess about what
// counsel will say. It is the position gao acts on, chosen so that acting on it
// and being wrong is recoverable: exclude rather than include, redact rather than
// keep, file rather than wait.
type Question struct {
	// ID is the label the question is referred to by everywhere else, including
	// the milestone issues and the risk register.
	ID string

	// Ask is the question as it was put.
	Ask string

	// Default is what gao does until an answer arrives.
	Default string

	// Answer is counsel's reply, empty until it lands. When it is set it
	// supersedes Default, and [Question.Position] is the accessor that knows
	// which one is in force.
	Answer string

	// Filed reports whether the question has actually been put to counsel, as
	// opposed to written down here and forgotten.
	Filed bool

	// Stakes is what the answer changes. It is the field that says whether a
	// question is worth chasing, and for most of these the honest answer is that
	// the default already handles it.
	Stakes string
}

// Answered reports whether counsel has replied.
func (q Question) Answered() bool { return q.Answer != "" }

// Position returns what gao acts on: counsel's answer when it has arrived, and
// the written default until then.
func (q Question) Position() string {
	if q.Answered() {
		return q.Answer
	}
	return q.Default
}

// questions is the agenda. The order is the order they were filed in, which is
// roughly the order they arise in the pipeline, and it is stable because other
// documents cite the numbers.
var questions = []Question{
	{
		ID:      "Q1",
		Ask:     `Does "text and data mining" under Law 131/2025 and Decree 134/2026 cover corpus construction and generative model training, or only analytical mining?`,
		Default: "assume it covers training, and proceed",
		Filed:   true,
		Stakes:  "the whole corpus. A narrow reading does not end the project, it changes what the project ships: gao becomes a recipe rather than a download, which is the kill criterion S0 carries and the fallback in RecipeOnly.",
	},
	{
		ID:      "Q2",
		Ask:     "Does a text and data mining rights reservation prohibit training, or only redistribution?",
		Default: "assume it prohibits both, and exclude reserved material from training as well as from the release",
		Filed:   true,
		Stakes:  "the most tokens of any question here. Reserved material is a small share of the crawl and excluding it is a filter that already exists, so the expensive reading is affordable.",
	},
	{
		ID:      "Q3",
		Ask:     "What is the lawful basis under the personal data protection law for processing publicly available personal data in a research corpus, and is legitimate interest available?",
		Default: "proceed with the level 1 and level 2 redaction the record already carries, and a written balancing test",
		Filed:   true,
		Stakes:  "whether the crawl can process personal data at all, which is most of the web, since a page with a byline has personal data on it.",
	},
	{
		ID:      "Q4",
		Ask:     "Is redacting names only where they co-occur with other identifiers defensible, given that redacting every name is not technically achievable on Vietnamese?",
		Default: "proceed, with the reasoning written down rather than assumed",
		Filed:   true,
		Stakes:  "the redaction design. Vietnamese personal names are ordinary words, so a redactor that removes every name removes a large part of the language with it.",
	},
	{
		ID:      "Q5",
		Ask:     "Does training on GPUs outside Vietnam require a cross border transfer impact assessment, and on what timeline?",
		Default: "assume yes, and file before cluster procurement rather than before the training run",
		Filed:   true,
		Stakes:  "the earliest deadline on the list, which is why it went first. The assessment takes longer than the purchase order, so filing it when the cluster is booked is filing it late.",
	},
	{
		ID:      "Q6",
		Ask:     "Does publishing open weights and a public corpus cross any registration or risk classification threshold under the law on artificial intelligence?",
		Default: "assume the transparency obligations apply, and meet them generously",
		Filed:   true,
		Stakes:  "the release process rather than the corpus. Meeting the obligations generously costs a model card and a dataset card that gao writes anyway.",
	},
	{
		ID:      "Q7",
		Ask:     "Does the share alike term on Wikipedia's license propagate to a corpus that includes it, and does keeping it in its own shard contain that?",
		Default: "keep Vietnamese Wikipedia in its own shard with its own license marking, and do not blend it",
		Filed:   true,
		Stakes:  "whether a term attached to half a billion tokens reaches three hundred billion. Shard separation is cheap and it is what makes the answer contained rather than corpus wide.",
	},
	{
		ID:      "Q8",
		Ask:     "Do the terms of a generator model restrict the use of its outputs to train another model?",
		Default: "read the specific license, and exclude any generator whose terms prohibit it",
		Filed:   true,
		Stakes:  "whether a given generator can be used for gao-synth at all. It is per generator rather than general, so it is answered once per model rather than once.",
	},
	{
		ID:      "Q9",
		Ask:     "What is the correct takedown response time and record keeping obligation for a published dataset under Vietnamese law?",
		Default: "seven days, with a permanent tombstone record of what was removed and why",
		Filed:   true,
		Stakes:  "the takedown path, which the store already implements: a removal is a tombstone rather than an edit, so the record of the removal survives the removal.",
	},
	{
		ID:      "Q10",
		Ask:     "Does publishing URLs and metadata without the text create any liability distinct from publishing the text?",
		Default: "assume it does not, since it is what the field already does",
		Filed:   true,
		Stakes:  "the restricted class, which is where most of the crawl lands. If this one comes back badly the restricted class publishes nothing and the headline count halves.",
	},
}

// Questions returns the agenda, in filing order.
func Questions() []Question {
	out := make([]Question, len(questions))
	copy(out, questions)
	return out
}

// Ask returns the question with the given ID.
func Ask(id string) (Question, bool) {
	for _, q := range questions {
		if q.ID == id {
			return q, true
		}
	}
	return Question{}, false
}

// Outstanding returns the questions counsel has not answered yet, which are the
// ones running under a written default.
func Outstanding() []Question {
	var out []Question
	for _, q := range questions {
		if !q.Answered() {
			out = append(out, q)
		}
	}
	return out
}
