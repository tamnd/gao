// Command gao builds, cleans, and publishes the gao Vietnamese corpus.
//
// The subcommands are named for the stages of rice processing, because rice
// processing and corpus processing turn out to be the same handful of verbs:
// harvest (gat), dry (phoi), sift (sang), mill (xay), and store (kho).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/tamnd/gao/doc"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// command is one subcommand of the gao binary.
type command struct {
	name  string
	short string
	run   func(stdout, stderr io.Writer, args []string) int
}

func commands() []command {
	return []command{
		{"bang", "the board: the release scores, with the benchmarks written in Vietnamese kept apart from the translated ones", runBang},
		{"bien", "the frontier: canonicalize URLs, shape them, and say what the budget would ask for", runBien},
		{"boc", "to husk: peel the posts out of a forum thread and leave the page behind", runBoc},
		{"box", "print the fleet inventory and the disk budget it implies", runBox},
		{"can", "to weigh: whether the three continued pretraining arms differ in their data and in nothing else", runCan},
		{"cham", "mark: grade sampled answers against a verifier, since a published reward is the only arguable one", runCham},
		{"che", "cover: find the personal data in a document and tag over it", runChe},
		{"chia", "divide: route a PDF to direct extraction, to a legacy transcode, or to OCR", runChia},
		{"chim", "to sink: what an FP8 E4M3 step lost to zero, which the loss curve will not tell anybody", runChim},
		{"cho", "to wait: what the crawl actually left between requests to one host, on a real box under load", runCho},
		{"chon", "choose: score the base models against the six criteria, in the order they were written", runChon},
		{"chot", "close the ledger: the evaluation harness, fixed and hashed before any result exists", runChot},
		{"cong", "add up: what a release holds, what of it ships, and what the headline number is a count of", runCong},
		{"dau", "the mark: build and score the diacritic restoration task set out of the corpus", runDau},
		{"dem", "count: fetch the tokenizer that defines a gao token, print what an ingest counted", runDem},
		{"dien", "fill in: build and score the cloze proxy the ablation slate is run against", runDien},
		{"dinh", "to attach: page images kept joined to the text that came off them, and moved off the box that made them", runDinh},
		{"doan", "to guess: the predictions register, written before the measurements and scored against them", runDoan},
		{"don", "clear away: whether the crawl gets its bytes off the box faster than it writes them", runDon},
		{"gat", "work with acquisition: print the ingest manifest, check it for drift", runGat},
		{"ghep", "to graft: what adding Vietnamese tokens to a base vocabulary bought and cost", runGhep},
		{"gian", "to stretch: the context extension ladder, and whether the corpus holds enough naturally long Vietnamese to climb it", runGian},
		{"giao", "to hand over: which box fetches which file of the ingest, and what the whole thing costs in wall clock", runGiao},
		{"gieo", "to sow: the generator card for gao-synth, and the recipe it is written against", runGieo},
		{"giu", "to keep: what the distilled model kept of each specialist's gain, against merging the same checkpoints", runGiu},
		{"goi", "to wrap: what a release costs on disk, column by column, read out of the footers", runGoi},
		{"hieu", "the effect: what fraction of the hardware a training run turns into gradient", runHieu},
		{"hoi", "to ask: whether a question about a long document actually needs the document", runHoi},
		{"keo", "to pull: what it costs to get back into a training run once the host is gone", runKeo},
		{"kho", "work with the store: verify a snapshot, generate a signing key", runKho},
		{"kim", "the needle: vi-needle, whether a long context in Vietnamese is read or skimmed", runKim},
		{"lat", "a slice: check a release slice is a view over its snapshot rather than a second copy of it", runLat},
		{"luat", "print the legal position: counsel questions, license determinations, what ships", runLuat},
		{"mam", "the seed: find hosts nobody handed us a list of", runMam},
		{"nau", "cook: the training plan, its token budget, its curriculum, and the arithmetic between them", runNau},
		{"ngai", "to hesitate: vi-overrefusal, whether a model will talk about Vietnam, in pairs", runNgai},
		{"nghe", "to listen: whether a transcript belongs to the audio it came off, without a reference to score it against", runNghe},
		{"nhat", "pick out the grit: find the documents that hold a benchmark gao is judged on", runNhat},
		{"nhip", "the beat: what each pipeline stage runs at, with the box on every number", runNhip},
		{"phoi", "normalize Vietnamese text, or report what normalizing it would do", runPhoi},
		{"plan", "print the build plan: slices, gates, and kill criteria", runPlan},
		{"sang", "sift: measure documents and say which of them are Vietnamese prose", runSang},
		{"siet", "to tighten: the GRPO step the specialists are trained with, and a run read back against it", runSiet},
		{"soi", "hold a reading up to the light: measure what a machine read against what the page says", runSoi},
		{"suat", "a rate: the crawl's net yield, per target class, read while it is still running", runSuat},
		{"tach", "separate: read a forum page as the thread it is, since generic extraction keeps the menu and drops the posts", runTach},
		{"theo", "to follow: vi-adherence, whether the answer comes back in the language the question was asked in", runTheo},
		{"thu", "to try: the forty run ablation slate, fixed before any of it runs, and what came back", runThu},
		{"tin", "to believe: whether the cheap benchmark orders recipes the way the expensive one does", runTin},
		{"tron", "to mix: the finetuning set composed with native origin kept a column rather than a note", runTron},
		{"uoc", "to estimate: what a sampled count is worth, as an interval and as a stopping rule", runUoc},
		{"version", "print the version", runVersion},
		{"xay", "mill: find the documents a corpus holds more than one copy of", runXay},
		{"xep", "to place: the gao-refset draw and rubric, fixed before a document is labeled", runXep},
		{"xoa", "the takedown register: who asked us to remove something, and how long it took", runXoa},
		{"help", "print this help", nil}, // handled in main so it can see the table
	}
}

func main() {
	os.Exit(run(os.Stdout, os.Stderr, os.Args[1:]))
}

func run(stdout, stderr io.Writer, args []string) int {
	// -version is accepted as a flag as well as a subcommand, because release
	// tooling reaches for the flag and people reach for the subcommand.
	if len(args) == 1 && (args[0] == "-version" || args[0] == "--version") {
		return runVersion(stdout, stderr, nil)
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(stdout)
		return 0
	}

	name := args[0]
	for _, c := range commands() {
		if c.name == name && c.run != nil {
			return c.run(stdout, stderr, args[1:])
		}
	}

	fmt.Fprintf(stderr, "gao: unknown command %q\n", name)
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprint(w, "gao builds the largest Vietnamese text corpus.\n\nusage: gao <command> [flags]\n\ncommands:\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range commands() {
		fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.short)
	}
	_ = tw.Flush()
	fmt.Fprint(w, "\nrun 'gao <command> -h' for the flags of a single command.\n")
}

func runVersion(stdout, _ io.Writer, _ []string) int {
	fmt.Fprintln(stdout, version)
	return 0
}

func runPlan(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	full := fs.Bool("full", false, "include the gate and kill criterion for each slice")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao plan [-full]\n\nPrints the build plan. Each slice ships something usable even if the next one never runs.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if !*full {
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		for _, s := range doc.Slices {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", s.ID, s.Title, s.Ships)
		}
		_ = tw.Flush()
		return 0
	}

	for i, s := range doc.Slices {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "%s  %s\n", s.ID, s.Title)
		fmt.Fprintf(stdout, "  ships: %s\n", s.Ships)
		fmt.Fprintf(stdout, "  gate:  %s\n", s.Gate)
		fmt.Fprintf(stdout, "  kill:  %s\n", s.Kill)
	}
	return 0
}
