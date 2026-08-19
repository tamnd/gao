// Command gao builds, cleans, and publishes the gao Vietnamese corpus.
//
// The subcommands are named for the stages of rice processing, because rice
// processing and corpus processing turn out to be the same handful of verbs:
// harvest, normalize, sift, mill, and store. Each one answers to its English
// name and to the Vietnamese one the package is named for, so gao clean and
// gao sach are the same command.
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
//
// The name is the English verb and the alias is the Vietnamese one. Both
// dispatch to the same function, so nothing written against the old names
// stops working, and the help prints the pair so the metaphor is still
// legible to anybody reading it.
type command struct {
	name  string
	alias string
	short string
	run   func(stdout, stderr io.Writer, args []string) int
}

func commands() []command {
	return []command{
		{name: "ask", alias: "hoi", short: "whether a question about a long document actually needs the document", run: runHoi},
		{name: "assign", alias: "giao", short: "which box fetches which file of the ingest, and what the whole thing costs in wall clock", run: runGiao},
		{name: "attach", alias: "dinh", short: "page images kept joined to the text that came off them, and moved off the box that made them", run: runDinh},
		{name: "board", alias: "bang", short: "the release scores, with the benchmarks written in Vietnamese kept apart from the translated ones", run: runBang},
		{name: "choose", alias: "chon", short: "score the base models against the six criteria, in the order they were written", run: runChon},
		{name: "clean", alias: "sach", short: "run the whole line over the raw corpus and publish what comes out", run: runSach},
		{name: "clear", alias: "don", short: "whether the crawl gets its bytes off the box faster than it writes them", run: runDon},
		{name: "compare", alias: "so", short: "a human evaluation read back, and whether the raters read the answers or the layout", run: runSo},
		{name: "cook", alias: "nau", short: "the training plan, its token budget, its curriculum, and the arithmetic between them", run: runNau},
		{name: "count", alias: "dem", short: "fetch the tokenizer that defines a gao token, print what an ingest counted", run: runDem},
		{name: "cover", alias: "che", short: "find the personal data in a document and tag over it", run: runChe},
		{name: "efficiency", alias: "hieu", short: "what fraction of the hardware a training run turns into gradient", run: runHieu},
		{name: "estimate", alias: "uoc", short: "what a sampled count is worth, as an interval and as a stopping rule", run: runUoc},
		{name: "fill", alias: "dien", short: "build and score the cloze proxy the ablation slate is run against", run: runDien},
		{name: "fleet", alias: "box", short: "print the fleet inventory and the disk budget it implies", run: runBox},
		{name: "follow", alias: "theo", short: "vi-adherence, whether the answer comes back in the language the question was asked in", run: runTheo},
		{name: "frontier", alias: "bien", short: "canonicalize URLs, shape them, and say what the budget would ask for", run: runBien},
		{name: "grade", alias: "cham", short: "grade sampled answers against a verifier, since a published reward is the only arguable one", run: runCham},
		{name: "graft", alias: "ghep", short: "what adding Vietnamese tokens to a base vocabulary bought and cost", run: runGhep},
		{name: "harvest", alias: "gat", short: "print the ingest manifest, check it for drift", run: runGat},
		{name: "hesitate", alias: "ngai", short: "vi-overrefusal, whether a model will talk about Vietnam, in pairs", run: runNgai},
		{name: "husk", alias: "boc", short: "peel the posts out of a forum thread and leave the page behind", run: runBoc},
		{name: "inspect", alias: "soi", short: "measure what a machine read against what the page says", run: runSoi},
		{name: "keep", alias: "giu", short: "what the distilled model kept of each specialist's gain, against merging the same checkpoints", run: runGiu},
		{name: "law", alias: "luat", short: "the legal position: counsel questions, license determinations, what ships", run: runLuat},
		{name: "layers", alias: "tang", short: "what an estimate taken bucket by bucket is worth over the buckets nobody opened", run: runTang},
		{name: "listen", alias: "nghe", short: "whether a transcript belongs to the audio it came off, without a reference to score it against", run: runNghe},
		{name: "mark", alias: "dau", short: "build and score the diacritic restoration task set out of the corpus", run: runDau},
		{name: "mill", alias: "xay", short: "find the documents a corpus holds more than one copy of", run: runXay},
		{name: "mix", alias: "tron", short: "the finetuning set composed with native origin kept a column rather than a note", run: runTron},
		{name: "needle", alias: "kim", short: "vi-needle, whether a long context in Vietnamese is read or skimmed", run: runKim},
		{name: "normalize", alias: "phoi", short: "normalize Vietnamese text, or report what normalizing it would do", run: runPhoi},
		{name: "pack", alias: "goi", short: "what a release costs on disk, column by column, read out of the footers", run: runGoi},
		{name: "pick", alias: "nhat", short: "find the documents that hold a benchmark gao is judged on", run: runNhat},
		{name: "place", alias: "xep", short: "the gao-refset draw and rubric, fixed before a document is labeled", run: runXep},
		{name: "plan", alias: "ke", short: "print the build plan: slices, gates, and kill criteria", run: runPlan},
		{name: "predict", alias: "doan", short: "the predictions register, written before the measurements and scored against them", run: runDoan},
		{name: "pull", alias: "keo", short: "what it costs to get back into a training run once the host is gone", run: runKeo},
		{name: "repeat", alias: "lap", short: "whether a generated set is a corpus or one prompt run a million times", run: runLap},
		{name: "route", alias: "chia", short: "route a PDF to direct extraction, to a legacy transcode, or to OCR", run: runChia},
		{name: "sample", alias: "mau", short: "which shards of a layer nobody has read get read, decided before the reading", run: runMau},
		{name: "seal", alias: "chot", short: "the evaluation harness, fixed and hashed before any result exists", run: runChot},
		{name: "seed", alias: "mam", short: "find hosts nobody handed us a list of", run: runMam},
		{name: "separate", alias: "tach", short: "read a forum page as the thread it is, since generic extraction keeps the menu and drops the posts", run: runTach},
		{name: "sift", alias: "sang", short: "measure documents and say which of them are Vietnamese prose", run: runSang},
		{name: "sink", alias: "chim", short: "what an FP8 E4M3 step lost to zero, which the loss curve will not tell anybody", run: runChim},
		{name: "slice", alias: "lat", short: "check a release slice is a view over its snapshot rather than a second copy of it", run: runLat},
		{name: "sow", alias: "gieo", short: "the generator card for gao-synth, and the recipe it is written against", run: runGieo},
		{name: "spike", alias: "vot", short: "whether the loss spiked, what rewinding to the last checkpoint would have cost, and whether the log could have held the answer", run: runVot},
		{name: "store", alias: "kho", short: "work with the store: verify a snapshot, generate a signing key", run: runKho},
		{name: "stretch", alias: "gian", short: "the context extension ladder, and whether the corpus holds enough naturally long Vietnamese to climb it", run: runGian},
		{name: "syllable", alias: "tieng", short: "what a syllable-atomic tokenizer would govern, and what it gives up, counted before the slate runs", run: runTieng},
		{name: "takedown", alias: "xoa", short: "the takedown register: who asked us to remove something, and how long it took", run: runXoa},
		{name: "taste", alias: "nem", short: "read the sample gao sample drew and say what a stored byte of each layer holds", run: runNem},
		{name: "throughput", alias: "nhip", short: "what each pipeline stage runs at, with the box on every number", run: runNhip},
		{name: "tighten", alias: "siet", short: "the GRPO step the specialists are trained with, and a run read back against it", run: runSiet},
		{name: "total", alias: "cong", short: "what a release holds, what of it ships, and what the headline number is a count of", run: runCong},
		{name: "trust", alias: "tin", short: "whether the cheap benchmark orders recipes the way the expensive one does", run: runTin},
		{name: "try", alias: "thu", short: "the forty run ablation slate, fixed before any of it runs, and what came back", run: runThu},
		{name: "wait", alias: "cho", short: "what the crawl actually left between requests to one host, on a real box under load", run: runCho},
		{name: "weigh", alias: "can", short: "whether the three continued pretraining arms differ in their data and in nothing else", run: runCan},
		{name: "yield", alias: "suat", short: "the crawl's net yield, per target class, read while it is still running", run: runSuat},
		{name: "version", short: "print the version", run: runVersion},
		{name: "help", short: "print this help"}, // handled in main so it can see the table
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
		if c.run == nil {
			continue
		}
		if c.name == name || (c.alias != "" && c.alias == name) {
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
		alias := ""
		if c.alias != "" {
			alias = "(" + c.alias + ")"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", c.name, alias, c.short)
	}
	_ = tw.Flush()
	fmt.Fprint(w, "\nthe name in brackets is the Vietnamese one, and it runs the same command.\nrun 'gao <command> -h' for the flags of a single command.\n")
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
