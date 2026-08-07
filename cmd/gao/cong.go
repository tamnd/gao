package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/tamnd/gao/cong"
)

func runCong(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("cong", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	release := fs.String("release", "gao-v1.0", "the release these counts are for")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao cong [-release name] [-json] counts.jsonl

Add up a release and state what the headline number is a count of.

One JSON object per line, one line per source per origin per license class. The
arithmetic is trivial and every hard part is about what may be added to what.
Natural text and generated text are never summed and the headline is the natural
one. The publishable subset is stated apart from the total, since license class
decides what a third party can download. Per source contribution is a table,
because where the tokens came from is what anybody checking the headline asks
second.

Counts from two tokenizers are refused rather than added, since two tokenizers
are two units. So are counts off two snapshots, since a release is a snapshot
and adding across them publishes a corpus that did not exist at one moment.

The ratios against HPLT v3, PhoGPT and CulturaX are computed from the number
that came back rather than quoted from the plan. The kill criterion for this
slice says a corpus under 250B natural tokens publishes its real number and
restates those ratios, and a ratio restated by hand is one that gets restated
once.

Exits 1 when the counts are not a release count, and 2 when the corpus came in
under the kill criterion.

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

	s, err := cong.ReadSheet(*release, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao cong: %v\n", err)
		return 1
	}

	report := congReport{
		Release:     s.Release,
		Snapshot:    s.Snapshot(),
		Tokenizer:   s.Tokenizer(),
		Headline:    s.Headline(),
		Target:      int64(cong.Target),
		Floor:       int64(cong.Floor),
		Corpus:      s.Corpus(),
		Publishable: s.Publishable(),
		Generated:   s.Generated(),
		Sources:     s.Sources(),
		Classes:     s.Classes(),
		Against:     s.Restate(),
		Holds:       s.Holds(),
		Killed:      s.Killed(),
		Blocking:    s.Blocking(),
		Verdict:     s.Verdict(),
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printCong(stdout, s, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case report.Killed:
		return 2
	}
	return 0
}

type congReport struct {
	Release   string `json:"release"`
	Snapshot  string `json:"snapshot"`
	Tokenizer string `json:"tokenizer"`

	// Headline is natural tokens and nothing else, which is the one number this
	// command exists to keep honest.
	Headline int64 `json:"headline"`
	Target   int64 `json:"target"`
	Floor    int64 `json:"floor"`

	Corpus      cong.Total `json:"corpus"`
	Publishable cong.Total `json:"publishable"`
	Generated   cong.Total `json:"generated"`

	Sources []cong.Total `json:"sources"`
	Classes []cong.Total `json:"classes"`

	// Against is the headline stated over each incumbent, computed rather than
	// quoted.
	Against []string `json:"against"`

	Holds    bool     `json:"holds"`
	Killed   bool     `json:"killed"`
	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printCong(w io.Writer, s cong.Sheet, r congReport) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "source\tdocuments\tbytes\tcharacters\tsyllables\ttokens\tshare\n")
	for _, t := range r.Sources {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			t.Source, millions(t.Documents), corpusSize(t.Bytes), billions(t.Chars),
			billions(t.Syllables), billions(t.Tokens), percent(t.Share(r.Headline)))
	}
	_ = tw.Flush()

	if len(r.Classes) > 0 {
		fmt.Fprint(w, "\n")
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprint(tw, "license class\tdocuments\ttokens\tshare\tships\n")
		for _, t := range r.Classes {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				t.Source, millions(t.Documents), billions(t.Tokens),
				percent(t.Share(r.Headline)), ships(t.Source))
		}
		_ = tw.Flush()
	}

	fmt.Fprintf(w, "\n%s, counted off %s in %s tokens.\n", r.Release, r.Snapshot, r.Tokenizer)
	fmt.Fprintf(w, "The headline is %s of natural tokens over %s documents, and it is the natural number because a reader who downloads a Vietnamese corpus believes a person wrote it.\n",
		billions(r.Headline), millions(r.Corpus.Documents))
	if r.Generated.Tokens > 0 {
		fmt.Fprintf(w, "%s of generated text sits beside it on its own line, added to nothing, since %s and %s are answers to different questions.\n",
			billions(r.Generated.Tokens), billions(r.Headline), billions(r.Headline+r.Generated.Tokens))
	}
	fmt.Fprintf(w, "%s of the headline ships and %s stays in the store, which license class decides rather than preference.\n",
		billions(r.Publishable.Tokens), billions(r.Corpus.Tokens-r.Publishable.Tokens))

	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "\nThese counts are not a release count:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}

	fmt.Fprintf(w, "\nAgainst the corpora the claim is written over, that is %s.\n", strings.Join(s.Restate(), ", "))
	fmt.Fprintf(w, "%s\n", r.Verdict)
}

// ships says what a license class means for a reader rather than making them
// remember which two of the five are publishable.
func ships(class string) string {
	switch class {
	case "open", "permissive-attribution":
		return "yes"
	case "unknown":
		return "undetermined"
	default:
		return "held"
	}
}

// corpusSize prints bytes of text the way the project quotes a corpus size,
// which is never a file size.
func corpusSize(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTP"[exp])
}
