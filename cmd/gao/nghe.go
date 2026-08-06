package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/may"
	"github.com/tamnd/gao/nghe"
)

func runNghe(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("nghe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	name := fs.String("set", "gao-voice", "the speech artifact these tracks belong to")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao nghe [-set NAME] [-json] tracks.jsonl

To listen: whether a transcript belongs to the audio it came off.

Nobody has the words that were actually said, so there is no reference to score
against and no word error rate to quote. That matters less than it sounds,
because the failure worth catching is not a wrong word. It is a decoder that
meets silence, or music, or a tone it has no model for, and emits the same
sentence until the recording ends.

That failure survives everything downstream. The loop is fluent Vietnamese, so
gao sang admits it. It lives inside one document, so nothing that looks for
duplicate documents sees it. It reads as speech and trains a model to repeat
itself. The only place it can be caught is here.

So three numbers are read off each track and none of them needs a reference. The
longest run of one line back to back, and the share of the lines that are
distinct, which are the same loop from two directions. And the syllables the
transcript carries against the seconds of speech it claims to carry them in,
since a transcript at one syllable a second dropped the audio and one at twelve
invented it.

Human authored subtitles and generated ones are counted apart. Where a recording
has both, the human track is admitted and the machine one is superseded, which
is not a loss and is not counted as one.

Exits 1 if this is not a reading of a decoder, or 2 if it is one that says the
decoder is the problem.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	s, err := nghe.ReadSet(*name, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao nghe: %v\n", err)
		return 1
	}

	// There is one GPU on this fleet, and a decode recorded against a machine
	// that does not have it is not a decode anybody can run again.
	var claims []string
	for _, t := range s.Tracks {
		if !t.Generated() || t.Box == "" {
			continue
		}
		b, ok := may.Lookup(t.Box)
		switch {
		case !ok:
			claims = append(claims, fmt.Sprintf(
				"%s was decoded on %s, which is not a box on this fleet, so the VRAM it reports is nobody's to reproduce",
				t.Track, t.Box))
		case !b.HasGPU():
			claims = append(claims, fmt.Sprintf(
				"%s reports %.1f GB of VRAM off %s, which has no card in it",
				t.Track, t.VRAM, b.Name))
		case t.VRAM > float64(b.GPUMemory)/(1<<30):
			claims = append(claims, fmt.Sprintf(
				"%s peaked at %.1f GB on %s, which holds %s, so the decode that produced this transcript did not fit the card it ran on",
				t.Track, t.VRAM, b.Name, gigabytes(b.GPUMemory)))
		}
	}

	report := ngheReport{
		Set: s.Set, Tracks: len(s.Tracks),
		Hours: s.Hours(), Written: s.Written(), Lost: s.Lost(), Share: s.Share(),
		Admitted: len(s.Admitted()), Dropped: len(s.Dropped()),
		Looping: len(s.Looping()), Drifting: len(s.Drifting()),
		Superseded: len(s.Superseded()),
		MaxLost:    nghe.MaxLost, MaxRepeat: nghe.MaxRepeat, MinVariety: nghe.MinVariety,
		MinRate: nghe.MinRate, MaxRate: nghe.MaxRate,
		Holds:    s.Holds() && len(claims) == 0,
		Blocking: append(s.Blocking(), claims...), Verdict: s.Verdict(),
	}
	if w, ok := s.Worst(); ok {
		report.Worst = w.Track
		report.Margin = w.Margin()
	}
	if near, ok := s.Nearest(); ok {
		report.Nearest = near.Track
	}
	for _, t := range s.Ranked() {
		report.Readings = append(report.Readings, ngheReading{
			Track: t.Track, Source: t.Source, Model: t.Model, Box: t.Box,
			Seconds: t.Seconds, Spoken: t.Spoken, Covered: t.Covered(),
			Segments: t.Segments, Distinct: t.Distinct, Repeats: t.Repeats,
			Variety: t.Variety(), Syllables: t.Syllables, Rate: t.Rate(),
			VRAM: t.VRAM, Margin: t.Margin(), Kept: t.Kept(),
			Looped: t.Looped(), Drifted: t.Drifted(),
		})
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printNghe(stdout, s, claims)
	}
	if len(s.Blocking()) > 0 || len(claims) > 0 {
		return 1
	}
	if !s.Holds() {
		return 2
	}
	return 0
}

// ngheReading is one track as the table carries it.
type ngheReading struct {
	Track  string `json:"track"`
	Source string `json:"source"`
	Model  string `json:"model,omitempty"`
	Box    string `json:"box,omitempty"`

	Seconds float64 `json:"seconds"`
	Spoken  float64 `json:"spoken"`
	Covered float64 `json:"covered"`

	Segments int     `json:"segments"`
	Distinct int     `json:"distinct"`
	Repeats  int     `json:"repeats"`
	Variety  float64 `json:"variety"`

	Syllables int     `json:"syllables"`
	Rate      float64 `json:"rate"`

	VRAM float64 `json:"vram,omitempty"`

	// Margin is how much room the track has before the nearest gate, where one
	// is the gate itself.
	Margin float64 `json:"margin"`

	Kept    bool `json:"kept"`
	Looped  bool `json:"looped"`
	Drifted bool `json:"drifted"`
}

type ngheReport struct {
	Set    string `json:"set"`
	Tracks int    `json:"tracks"`

	Hours   float64 `json:"hours"`
	Written float64 `json:"written"`
	Lost    float64 `json:"lost"`
	Share   float64 `json:"share"`

	Readings []ngheReading `json:"readings"`

	Admitted   int `json:"admitted"`
	Dropped    int `json:"dropped"`
	Looping    int `json:"looping"`
	Drifting   int `json:"drifting"`
	Superseded int `json:"superseded"`

	Worst  string  `json:"worst"`
	Margin float64 `json:"margin"`

	// Nearest is the admitted track closest to a gate, which is a different
	// track from Worst whenever anything was dropped.
	Nearest string `json:"nearest"`

	MaxLost    float64 `json:"max_lost"`
	MaxRepeat  int     `json:"max_repeat"`
	MinVariety float64 `json:"min_variety"`
	MinRate    float64 `json:"min_rate"`
	MaxRate    float64 `json:"max_rate"`

	Holds bool `json:"holds"`

	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printNghe(w io.Writer, s nghe.Set, claims []string) {
	beaten := map[string]bool{}
	for _, t := range s.Superseded() {
		beaten[t.Track] = true
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "track\tsource\tbox\tlength\tspeech\tlines\tdistinct\tlongest run\tsyllables/s\tVRAM\tkept\n")
	for _, t := range s.Ranked() {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%d\t%.1f\t%s\t%s\n",
			t.Track, t.Source, where(t.Box), clock(t.Seconds), clock(t.Spoken),
			t.Segments, percent(t.Variety()), t.Repeats, t.Rate(), vram(t.VRAM),
			outcome(t, t.Generated() && beaten[t.Track]))
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s, %s of audio with %s of it written by a person rather than decoded.\n",
		s.Set, clock(3600*s.Hours()), clock(3600*s.Written()))
	fmt.Fprintf(w, "A track is dropped when one line runs %d times back to back, or under %s of its lines are distinct, or the words do not fit the speech at %.1f to %.1f syllables a second.\n",
		nghe.MaxRepeat, percent(nghe.MinVariety), nghe.MinRate, nghe.MaxRate)
	fmt.Fprintf(w, "Bad recordings are a corpus and a bad decoder is a setting, so what is gated is the share of the hours lost rather than the count of the tracks, and the line is %s.\n",
		percent(nghe.MaxLost))
	if beaten := s.Superseded(); len(beaten) > 0 {
		fmt.Fprintf(w, "%s superseded by subtitles a person wrote for the same recording, which is not hours lost.\n",
			count(len(beaten), "machine transcript"))
	}

	why := append(s.Blocking(), claims...)
	if len(why) > 0 {
		fmt.Fprintf(w, "\n%s:\n", count(len(why), "fault"))
		for _, one := range why {
			fmt.Fprintf(w, "  %s\n", one)
		}
		return
	}
	fmt.Fprintf(w, "\n%s.\n", s.Verdict())
}

// clock renders a duration in hours above an hour and minutes below one, which
// is how a recording gets described out loud.
func clock(f float64) string {
	if f >= 3600 {
		return fmt.Sprintf("%.1fh", f/3600)
	}
	return fmt.Sprintf("%.0fm", f/60)
}

// where renders the machine a decode ran on, where a human authored track ran
// nowhere.
func where(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// vram renders the peak a decode needed, where a person typing subtitles needed
// none rather than zero.
func vram(f float64) string {
	if f <= 0 {
		return "none"
	}
	return fmt.Sprintf("%.1f GB", f)
}

// outcome says what happens to the track in the word the pipeline uses for it.
func outcome(t nghe.Track, superseded bool) string {
	switch {
	case superseded:
		return "written"
	case t.Looped():
		return "loop"
	case t.Drifted():
		return "drift"
	default:
		return "yes"
	}
}
