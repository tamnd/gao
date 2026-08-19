package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/tamnd/gao/count"
	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/harvest"
	"github.com/tamnd/gao/layers"
	"github.com/tamnd/gao/sample"
	"github.com/tamnd/gao/taste"
)

func runTaste(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("taste", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	source := fs.String("source", "", "the source being read, which is also the name it goes by in the ingest manifest")
	seed := fs.String("seed", "", "the seed the shards are drawn with, the same one gao sample was run at")
	layerFile := fs.String("layers", "", "the layer file, the same one gao layers reads")
	want := fs.Int64("bytes", sample.Want, "how much to read off each unread layer")
	tokenizer := fs.String("tokenizer", "", "count tokens with the tokenizer at this path")
	out := fs.String("out", "", "write the layer file the reading produces here, for gao layers to read")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao taste -source name -seed s -layers layers.jsonl [-bytes N] [-tokenizer PATH] [-out FILE] [-json] files.jsonl

Read the sample gao sample drew and say what a stored byte of each layer holds.

The flags are gao sample's flags, and the plan is drawn here rather than handed
over, so the digest this prints is the digest that command prints for the same
inputs. What gets fetched is the front of each drawn shard, by range request,
which is the only read a compressed stream allows and the reason the plan is
about which files rather than which offsets.

The sha256 of every prefix read is recorded. A third party with the seed draws
the same shards, and with the digests they can confirm they read the same bytes
of them rather than only the same files.

Without -tokenizer the token column is zero and the report says so. The pinned
tokenizer runs at around 1 MB of text per second per core, which is hours
against a corpus and minutes against a sample, so this is the one place in the
pipeline where tokenizing is affordable. Run 'gao count model -o PATH' to fetch it.

With -out the layer file goes where gao layers reads it, carrying every layer of
the plan whether it was read or not, since the layers nobody opened are what
tang exists to bound.

Exits 1 when this is not a reading anybody should publish, and 2 when it is one
that is worth less than the line it fills in.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *layerFile == "" {
		fs.Usage()
		return 2
	}

	kind, ok := taste.Source(*source)
	if !ok {
		fmt.Fprintf(stderr, "gao taste: %q is not a source gao ingests, so nothing here knows how to read its shards\n", *source)
		return 2
	}
	decoder, ok := harvest.DecoderFor(kind)
	if !ok {
		fmt.Fprintf(stderr, "gao taste: %s is read as whole files rather than as a stream, and a prefix of one is not a document\n", *source)
		return 2
	}

	read, err := layers.ReadLayers(*layerFile)
	if err != nil {
		fmt.Fprintf(stderr, "gao taste: %v\n", err)
		return 1
	}
	files, err := sample.ReadFiles(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao taste: %v\n", err)
		return 1
	}

	p := sample.ReadPlan(*source, *seed, *want, read, files)
	if why := p.Blocking(); len(why) > 0 {
		fmt.Fprint(stderr, "gao taste: this is not a plan anybody can run, so no shard was fetched:\n")
		for _, one := range why {
			fmt.Fprintf(stderr, "  %s\n", one)
		}
		return 1
	}

	// Both loaded before the first byte moves. A tokenizer at a path that does
	// not exist and a source the manifest does not pin are mistakes somebody
	// makes while typing the command, and they should cost a second rather than
	// the length of the reading.
	var tok *count.Tokenizer
	if *tokenizer != "" {
		tok, err = count.Open(count.Gemma3, *tokenizer)
		if err != nil {
			fmt.Fprintf(stderr, "gao taste: %v\n", err)
			fmt.Fprint(stderr, "run 'gao count model -o PATH' to fetch the tokenizer gao counts with\n")
			return 1
		}
	}
	pin, err := pinnedFor(kind)
	if err != nil {
		fmt.Fprintf(stderr, "gao taste: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Where the shard by shard progress goes. It belongs with the report, and
	// with -json the report is something a program parses, so a run that printed
	// its progress there would hand back twelve lines of prose and a JSON object
	// in one stream. The first attempt at capturing this reading did exactly that
	// and wrote a 78 byte file.
	progress := stdout
	if *asJSON {
		progress = stderr
	}

	fetcher := &harvest.Fetcher{Token: fleet.Token()}
	var takes []taste.Take
	for _, r := range p.Reads {
		for _, t := range r.Takes {
			one, err := readTake(ctx, progress, fetcher, decoder, pin, r.Layer, t, tok)
			if err != nil {
				fmt.Fprintf(stderr, "gao taste: %v\n", err)
				return 1
			}
			takes = append(takes, one)
		}
	}

	s := taste.Of(p, tokenizerLabel(tok), takes)
	report := tasteReport{
		Sample:   s,
		Faults:   s.Faults(),
		Blocking: s.Blocking(),
		Holds:    s.Holds(),
		Verdict:  s.Verdict(),
	}

	if *out != "" {
		if err := writeLayers(*out, s.Layers(read)); err != nil {
			fmt.Fprintf(stderr, "gao taste: %v\n", err)
			return 1
		}
		fmt.Fprintf(progress, "\n%s holds the reading, for gao layers to read.\n", *out)
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printTaste(stdout, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

// readTake fetches the front of one shard and counts what came out of it.
//
// The prefix is taken with a limit over the stream rather than with a range
// request written here, because the fetcher already reconnects at the byte it
// stopped at and a prefix that gave up on the first dropped connection would
// fail more often than the reading takes.
func readTake(ctx context.Context, stdout io.Writer, f *harvest.Fetcher, d harvest.Decoder, pin harvest.Pinned, layer string, t sample.Take, tok *count.Tokenizer) (taste.Take, error) {
	file, ok := fileIn(pin, t.Path)
	if !ok {
		return taste.Take{}, fmt.Errorf("the listing draws %s and the manifest does not pin it, so there is nothing to fetch it from", t.Path)
	}

	started := time.Now()
	body, err := f.Open(ctx, pin, file)
	if err != nil {
		return taste.Take{}, err
	}
	// Closed rather than verified. A prefix is not the file, so the pinned
	// digest is not the digest of what was read, and the fetcher is explicit
	// that a caller who stopped early did so on purpose.
	defer func() { _ = body.Close() }()

	out := taste.Take{Layer: layer, Path: t.Path, Bytes: t.Bytes, Of: t.Of, Whole: t.Bytes >= t.Of}
	count := func(doc *doc.Document) error {
		if tok != nil {
			doc.NTokens = uint32(tok.Count(doc.Text))
		}
		out.Add(doc)
		return nil
	}

	err = d.Decode(pin, file, io.LimitReader(body, t.Bytes), count)
	// A prefix ends inside a record, so the decoder stopping there is the read
	// working rather than the read failing. Anything else is a real fault and
	// goes back to the caller.
	if err != nil && !endedInsideARecord(err, out.Whole) {
		return taste.Take{}, fmt.Errorf("reading %s: %w", t.Path, err)
	}
	out.Digest = body.Digest()

	fmt.Fprintf(stdout, "%-14s %-30s %8s  %s  %d documents\n",
		layer, t.Path, corpusSize(t.Bytes), round(time.Since(started)), out.Documents)
	return out, nil
}

// endedInsideARecord reports whether the decoder stopped because the bytes ran
// out mid record, which is what every take that is not a whole file does.
//
// A take of a whole shard is held to the ordinary standard, since there the
// stream really did end and a decoder that stopped early found something wrong
// with the file.
func endedInsideARecord(err error, whole bool) bool {
	if whole {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	// The decoder reports the truncated tail as a sentence rather than as a
	// sentinel, because from in there a download that stopped and a half written
	// line are the same bytes. Here they are not: this asked for a prefix.
	for _, said := range []string{
		"the stream stopped inside line",
		"unexpected EOF",
		"unexpected end of JSON input",
	} {
		if strings.Contains(err.Error(), said) {
			return true
		}
	}
	return false
}

// fileIn finds the pinned file the listing named.
func fileIn(p harvest.Pinned, path string) (harvest.File, bool) {
	for _, f := range p.Files {
		if f.Path == path {
			return f, true
		}
	}
	return harvest.File{}, false
}

// pinnedFor reads the ingest manifest and returns the one source wanted.
func pinnedFor(s doc.Source) (harvest.Pinned, error) {
	p, ok := harvest.Pin(s)
	if !ok {
		return harvest.Pinned{}, fmt.Errorf("the ingest manifest does not pin %s, so there is nowhere to fetch its shards from", s)
	}
	return p, nil
}

// writeLayers writes the layer file layers reads, one JSON object per line.
func writeLayers(path string, ls []layers.Layer) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	for _, l := range ls {
		if err := enc.Encode(l); err != nil {
			return err
		}
	}
	return f.Close()
}

type tasteReport struct {
	taste.Sample

	Faults   []string `json:"faults,omitempty"`
	Blocking []string `json:"blocking,omitempty"`
	Holds    bool     `json:"holds"`
	Verdict  string   `json:"verdict"`
}

func printTaste(w io.Writer, r tasteReport) {
	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "\nThis is not a reading:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}

	fmt.Fprintf(w, "\n%s, %s read off %s at seed %s.\n",
		r.Source, corpusSize(r.Read), plural(len(r.Readings), "layer"), r.Seed)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "layer\tshards\tread\tdocuments\ttext\tpacks at\tthe layer holds\n")
	for _, one := range r.Readings {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%d\t%s\t%.2fx\t%s\n",
			one.Layer, one.Drawn, corpusSize(one.Read), one.Documents,
			corpusSize(one.Bytes), one.Pack(), corpusSize(one.Text()))
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\nseed %s, plan %s.\n", r.Seed, r.Digest)

	if len(r.Faults) > 0 {
		fmt.Fprint(w, "\nThis is worth less than the line it fills in:\n")
		for _, f := range r.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}
	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}
