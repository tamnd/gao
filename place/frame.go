package place

import "fmt"

// The frame itself: the draw, then the rubric.
//
// Both are here rather than in a data file, so that changing either one is a
// commit with a diff on it and a digest that moves, rather than an edit to a
// JSON file somebody made during a labeling session.

// Fixed is the published frame.
func Fixed() Frame {
	return Frame{
		Version: "1.0",
		Size:    Documents,
		Seed:    "gao-refset-1.0",
		Note:    "fixed before the first document was drawn, because a rubric written during labeling gets written toward the labels already collected and nothing in the finished set shows that it was",
		Slices:  slicesOf(),
		Rules:   rules(),
	}
}

// slicesOf is the draw. The shares are not the shares of the corpus, and that
// is deliberate: a reference set drawn in proportion to the corpus is 85% web
// text, which trains a classifier that has seen almost nothing of what the
// corpus is short of.
//
// CulturaX held 10% of this draw and holds none of it now. It is gated, the
// terms were never granted, and the manifest drops it, so a frame that asks a
// labeler for two thousand documents out of it is asking for documents that are
// not in the corpus. The share went to the two derived sets that are, since the
// job it was doing here was showing the rubric a set built to somebody else's
// recipe.
func slicesOf() []Slice {
	return []Slice{
		{"hplt3", 0.30,
			"the largest source and the one the headline token count rests on, so the classifier has to be right about it before it is right about anything"},
		{"crawl", 0.25,
			"the only source nobody else has cleaned, which makes it the one where a quality call is load bearing rather than a second opinion on somebody else's filter"},
		{"fineweb2", 0.20,
			"already filtered upstream, and a share this size is what says whether our rubric agrees with that filter or quietly replaces it"},
		{"finepdfs", 0.15,
			"three times its share of the corpus, because PDFs are where the edited long form is and a classifier that has seen fifty of them will call the rest of them boilerplate"},
		{"glotcc", 0.10,
			"the smallest source, at twice the share its size argues for, because a derived set built to a different recipe is where the rubric is most likely to behave differently and nobody would notice at 5%"},
	}
}

// rules is the rubric. Each band says what puts a document in it, which band it
// gets mixed up with, and how to tell those two apart, because the boundary is
// where every disagreement between two labelers happens.
func rules() []Rule {
	return []Rule{
		{
			Band:     Rich,
			Decides:  "somebody was paid to write it carefully, or wrote it as if they had been. Edited long form, technical explanation, legal and medical prose, a textbook chapter, a court judgment, a manual that assumes the reader is trying to do the thing.",
			Confused: Plain,
			Apart:    "effort rather than subject. A blog post about tax law is plain, a filed tax ruling is rich. The question is whether the text would survive an editor, not whether the topic sounds serious.",
			Examples: []string{
				"a forum answer that runs eight paragraphs and cites the circular it is about is rich, because the effort is on the page whatever the venue is",
				"a hospital's public page about a procedure is rich, and the same hospital's news item about opening a new wing is plain",
				"a novel chapter is rich, and it is the case people get wrong because the register is not technical",
			},
		},
		{
			Band:     Plain,
			Decides:  "ordinary Vietnamese somebody wrote for a reason: a forum post, a news item, a review, a recipe, a complaint. Most of a good corpus is this and most of the language is this.",
			Confused: Thin,
			Apart:    "whether a person wanted to say it. A short review saying the food was salty and the parking was hard is plain, because somebody meant it. A six hundred word page saying a restaurant is a great choice for those seeking dining is thin, because nobody did.",
			Examples: []string{
				"two sentences is still plain if those two sentences say something, since length is not the scale",
				"a news item rewritten from a press release is plain, because the press release was written by a person about a real thing",
				"a product page written by the seller is plain, and the same page written to rank for a search is thin",
			},
		},
		{
			Band:     Thin,
			Decides:  "Vietnamese prose with nothing in it. Search engine filler, spun rewrites of a page that already existed, a paragraph of boilerplate under a different headline, text generated to sit around an advertisement.",
			Confused: Unusable,
			Apart:    "whether it is sentences at all. Thin is a page of grammatical Vietnamese that says nothing, unusable is a page that is not sentences. A model can learn Vietnamese from thin text and learns nothing from unusable text, which is why the line is here and not somewhere more flattering.",
			Examples: []string{
				"machine translation that came out grammatical is thin rather than unusable, because the sentences parse",
				"a page that is one paragraph repeated nine times is thin, since the paragraph is prose",
				"a list of city names with a sentence of filler between each is unusable, because the sentences are the packaging rather than the page",
			},
		},
		{
			Band:     Unusable,
			Decides:  "not prose, or not Vietnamese. Navigation, tag clouds, tables of numbers, price lists, code with a Vietnamese comment on it, machine translation that never became a sentence, a page that is one image and a caption.",
			Confused: Thin,
			Apart:    "the same line from the other side. If deleting every sentence leaves the page saying what it said, it is unusable. If the sentences are the page and they are empty, it is thin.",
			Examples: []string{
				"a legal document reproduced as a table of article numbers is unusable, even though the source is the kind of thing rich comes from",
				"a page of Vietnamese in a language the labeler does not read is not unusable, and a labeler who cannot tell should say so in the note rather than place it",
				"a transcript with no punctuation is prose and is not unusable, since the sentences are there and the typography is not",
			},
		},
	}
}

// Describe is the frame in a sentence.
func (f Frame) Describe() string {
	return fmt.Sprintf("%s drawn across %s into %s, at seed %q, with %.0f%% of them labeled twice. Fixed and hashed before the first document was drawn, because a rubric written during labeling gets written toward the labels already collected.",
		plural(f.Size, "document"), plural(len(f.Slices), "source"), plural(len(f.Rules), "band"), f.Seed, 100*Double)
}
