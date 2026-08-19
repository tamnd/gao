package inspect

// The numbers.

import "fmt"

// A Score is what one reading of one page came to.
//
// Everything in it is a count. The rates are methods rather than fields so that
// a score can be added to another score and stay meaningful, which is what makes
// a whole evaluation set one number rather than an average of averages.
type Score struct {
	// Chars is how many characters the page holds, and Read is how many the
	// machine produced. The two differ when characters were dropped or invented,
	// and printing both is the cheapest way to see a reading that lost a column
	// or repeated a line.
	Chars int `json:"chars"`
	Read  int `json:"read"`

	// Marked is how many of the page's characters carry a tone or a letter mark,
	// and Toned is how many carry a tone. They are the denominators of the two
	// rates this package exists for.
	Marked int `json:"marked"`
	Toned  int `json:"toned"`

	// Wrong, Dropped and Added are the character errors: a character read as a
	// different one, a character the reading does not have, and a character the
	// reading has and the page does not. Together over Chars they are the
	// character error rate.
	Wrong   int `json:"wrong"`
	Dropped int `json:"dropped"`
	Added   int `json:"added"`

	// Lost is the diacritic errors: a marked character on the page whose marks
	// did not survive intact. A character read as a different letter counts
	// here too when it was marked, because the mark did not survive that either,
	// and a rate that let a lost letter hide a lost tone would be a rate an
	// engine could game by failing harder.
	Lost int `json:"lost"`

	// The tone failures, separated because they come from different faults. A
	// dropped tone is an engine that cannot see a small mark. A wrong tone is an
	// engine that can see it and cannot tell two of them apart, which produces a
	// real Vietnamese word that means something else and is the worse of the
	// two. An invented tone is an engine hallucinating on noise.
	ToneDropped int `json:"tone_dropped"`
	ToneWrong   int `json:"tone_wrong"`
	ToneAdded   int `json:"tone_added"`

	// ModWrong is a letter mark that did not survive: a for ă, o for ơ, u for ư,
	// or one of those read as another.
	ModWrong int `json:"mod_wrong"`

	// DD is đ read as d or d read as đ, counted on its own because it is the
	// single commonest thing an engine trained on Latin script gets wrong on
	// Vietnamese, and because S4 sets a gate on it by name.
	DD int `json:"dd"`

	// Confusion is what the page said against what was read, tone by tone, over
	// the characters that lined up as the same letter. A character read as a
	// different letter is not in it: the tone on a letter that was not read is
	// not a tone the engine got wrong, and counting it here would put letter
	// errors in the matrix that exists to isolate tone errors.
	//
	// The ngang row and the ngang column are where dropped and invented tones
	// land, which is why ngang has to be a tone here rather than the absence of
	// one. The ngang to ngang cell stays empty, because filling it would mean
	// counting every space and every consonant on the page, and a matrix whose
	// largest number is the spaces is a matrix nobody reads.
	Confusion [6][6]int `json:"confusion"`
}

// Measure reads a page against what a machine made of it.
//
// The argument order is the one that reads as a sentence and it is also the one
// that matters: ref is the page, read is the machine. Every rate below is a
// share of the page.
func Measure(ref, read string) Score {
	return MeasureLetters(Letters(ref), Letters(read))
}

// MeasureLetters is [Measure] over text that has already been taken apart, for a
// caller measuring the same reference against several readings.
func MeasureLetters(ref, read []Letter) Score {
	s := Score{Chars: len(ref), Read: len(read)}
	for _, l := range ref {
		if l.Marked() {
			s.Marked++
		}
		if l.Tone != Ngang {
			s.Toned++
		}
	}

	for _, op := range Align(ref, read) {
		switch {
		case !op.HaveRead:
			s.Dropped++
			if op.Ref.Marked() {
				s.Lost++
			}
			continue
		case !op.HaveRef:
			s.Added++
			continue
		}

		a, b := op.Ref, op.Read
		if a.Base != b.Base {
			s.Wrong++
			if a.Marked() {
				s.Lost++
			}
			continue
		}
		if a.Tone != Ngang || b.Tone != Ngang {
			s.Confusion[a.Tone][b.Tone]++
		}
		if a.Mod == b.Mod && a.Tone == b.Tone {
			continue
		}

		// The letter came through and something on it did not, which is the case
		// this whole package is built to be able to report.
		s.Wrong++
		if a.Marked() {
			s.Lost++
		}
		switch {
		case a.Tone != Ngang && b.Tone == Ngang:
			s.ToneDropped++
		case a.Tone == Ngang && b.Tone != Ngang:
			s.ToneAdded++
		case a.Tone != b.Tone:
			s.ToneWrong++
		}
		if a.Mod != b.Mod {
			s.ModWrong++
			if a.Mod == stroke || b.Mod == stroke {
				s.DD++
			}
		}
	}
	return s
}

// Add folds another score in, so an evaluation set is one score over all of it.
//
// The rates are recomputed from the totals rather than averaged, because a
// per page average weights a caption the same as a page of body text, and the
// question is how much of the corpus survives rather than how many pages did.
func (s *Score) Add(o Score) {
	s.Chars += o.Chars
	s.Read += o.Read
	s.Marked += o.Marked
	s.Toned += o.Toned
	s.Wrong += o.Wrong
	s.Dropped += o.Dropped
	s.Added += o.Added
	s.Lost += o.Lost
	s.ToneDropped += o.ToneDropped
	s.ToneWrong += o.ToneWrong
	s.ToneAdded += o.ToneAdded
	s.ModWrong += o.ModWrong
	s.DD += o.DD
	for i := range s.Confusion {
		for j := range s.Confusion[i] {
			s.Confusion[i][j] += o.Confusion[i][j]
		}
	}
}

// CER is the character error rate: what share of the page did not come through
// as itself. It is the number everybody quotes and it is here so that the
// difference between it and [Score.DER] can be seen rather than argued about.
func (s Score) CER() float64 { return rate(s.Wrong+s.Dropped+s.Added, s.Chars) }

// DER is the diacritic error rate: what share of the page's marks did not
// survive.
//
// The denominator is the marked characters rather than all of them, and that is
// the decision that makes this number worth having. Marked characters are about
// a quarter of a Vietnamese page, so this rate is roughly four times as
// sensitive as a rate over every character, and it moves for exactly one reason
// rather than for every reason at once. A reading that lost one mark in
// fourteen reads as 2% damage across the page and as 8% of the language's marks
// gone, and the second number is the one that says whether the corpus is worth
// keeping.
//
// A mark the reading invented on a character that had none is not in this rate.
// It is counted in [Score.ToneAdded] and reported on its own line, because it is
// a different failure and folding it in would make one number answer two
// questions. There is no reference mark for it to be a share of, and a rate
// whose numerator can outrun its denominator is a rate nobody can set a gate on.
// A tone invented on a character that already carried a letter mark does count,
// since that character is one of the page's marked ones and its marks did not
// come through as they were written.
func (s Score) DER() float64 { return rate(s.Lost, s.Marked) }

// ToneDeletionRate is the share of the page's tones the reading did not write,
// which S4 gates separately from DER because it is the failure that turns a
// document into fluent nonsense rather than into visible damage.
func (s Score) ToneDeletionRate() float64 { return rate(s.ToneDropped, s.Toned) }

// Accuracy is one minus the character error rate, for the one place it belongs,
// which is next to a diacritic error rate that contradicts it.
func (s Score) Accuracy() float64 { return 1 - s.CER() }

func rate(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return float64(n) / float64(of)
}

// Gate is a set of thresholds a reading has to clear.
//
// The numbers are not in this package. They are written down in the milestone
// and passed in, because a metric that carries the threshold it is judged by is
// a metric that quietly moves when the threshold does.
type Gate struct {
	CER          float64
	DER          float64
	ToneDeletion float64
	DD           float64
}

// S4 is the gate the OCR slice is held to, written where the checker can reach
// it and stated in the milestone as the authority.
var S4 = Gate{CER: 0.030, DER: 0.015, ToneDeletion: 0.008, DD: 0.005}

// Check reports which parts of the gate a score fails, in the order they are
// worth reading. An empty result is a pass.
func (g Gate) Check(s Score) []string {
	var out []string
	for _, c := range []struct {
		name  string
		got   float64
		limit float64
	}{
		{"diacritic error rate", s.DER(), g.DER},
		{"tone deletion", s.ToneDeletionRate(), g.ToneDeletion},
		{"đ and d confusion", rate(s.DD, s.Chars), g.DD},
		{"character error rate", s.CER(), g.CER},
	} {
		if c.got > c.limit {
			out = append(out, fmt.Sprintf("%s is %.2f%% and the gate is %.2f%%", c.name, c.got*100, c.limit*100))
		}
	}
	return out
}
