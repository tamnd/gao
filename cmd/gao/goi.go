package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/goi"
	"github.com/tamnd/gao/may"
)

func runGoi(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("goi", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	name := fs.String("name", "", "what to call the release, defaulting to the snapshot the shards are stamped with")
	all := fs.Bool("loose", false, "name every shard outside the size band rather than the first few")
	columns := fs.Bool("columns", false, "print every column rather than the ten that weigh the most")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao goi [-name release] [-loose] [-json] shard.parquet...

Weigh a release: what it costs on disk, column by column and shard by shard.

The reading comes out of the Parquet footers, so weighing a few hundred
gigabytes reads a few megabytes and the bytes read are reported next to the
bytes weighed. That is what makes this a check the fleet can run rather than
one that needs the release resident on the box taking it.

P06-1 says the natural corpus publishes in under 420 GB and P06-4 says the
columns that are not the text cost under 12% of it. The second is the one worth
watching, because it decides whether the provenance columns this project
insists on are affordable at half a billion documents or are a rule that gets
dropped the first time somebody reads a storage bill.

Shards that are not one release are refused rather than added up. Two snapshots
summed read as a release twice the size, and a repo that withholds text summed
with one that ships it reads as a release whose text got cheaper. Both are one
glob away at any time.

Exits 1 when these are not one release, and 2 when they are one that misses
P06-1 or P06-4.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}

	r, err := goi.Weigh(*name, fs.Args())
	if err != nil {
		fmt.Fprintf(stderr, "gao goi: %v\n", err)
		return 1
	}
	if r.Name == "" {
		r.Name = r.Snapshot()
	}
	if r.Name == "" {
		r.Name = "these shards"
	}

	report := goiReport{
		Name:     r.Name,
		Snapshot: r.Snapshot(),
		Box:      may.Label(),
		Shards:   len(r.Parts),
		Rows:     r.Rows(),
		Bytes:    r.Bytes(),
		Read:     r.Read(),
		Stored:   r.Stored(),
		Text:     r.TextBytes(),
		Metadata: r.Metadata(),
		Share:    r.Share(),
		Max:      goi.MaxMetadata,
		Ceiling:  goi.Ceiling,
		Ratio:    r.Ratio(),
		Target:   goi.TargetShard,
		Holds:    r.Holds(),
		Blocking: r.Blocking(),
		Verdict:  r.Verdict(),
	}
	for _, c := range r.Columns() {
		report.Columns = append(report.Columns, goiColumn{
			Name:         c.Name,
			Compressed:   c.Compressed,
			Uncompressed: c.Uncompressed,
			Share:        c.Share(r.Stored()),
		})
	}
	for _, p := range r.Loose() {
		report.Loose = append(report.Loose, goiShard{Path: p.Path, Bytes: p.Bytes, Rows: p.Rows})
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printGoi(stdout, report, *all, *columns)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

// goiColumns is how many columns the table prints before folding the rest into
// one row.
const goiColumns = 10

type goiColumn struct {
	Name         string  `json:"name"`
	Compressed   int64   `json:"compressed"`
	Uncompressed int64   `json:"uncompressed"`
	Share        float64 `json:"share"`
}

type goiShard struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Rows  int64  `json:"rows"`
}

type goiReport struct {
	Name     string `json:"name"`
	Snapshot string `json:"snapshot,omitempty"`

	// Box is where the reading was taken, which is "unmeasured" off the fleet.
	Box string `json:"box"`

	Shards int   `json:"shards"`
	Rows   int64 `json:"rows"`

	// Bytes is the release on disk and Read is what weighing it cost.
	Bytes int64 `json:"bytes"`
	Read  int64 `json:"read"`

	// Stored is the column total, which is Bytes less the framing.
	Stored int64 `json:"stored"`

	Columns []goiColumn `json:"columns"`

	Text     int64   `json:"text"`
	Metadata int64   `json:"metadata"`
	Share    float64 `json:"metadata_share"`
	Max      float64 `json:"metadata_line"`
	Ceiling  int64   `json:"ceiling"`
	Ratio    float64 `json:"ratio"`

	Target int64      `json:"shard_target"`
	Loose  []goiShard `json:"loose,omitempty"`

	Holds    bool     `json:"holds"`
	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printGoi(w io.Writer, r goiReport, all, columns bool) {
	// The record has forty odd leaf columns and the tail of them is repeated
	// nested keys weighing a few hundred bytes each, which is a fact about the
	// schema rather than about the release. The rest is folded into one row so
	// that the table answers what the release is made of.
	shown := r.Columns
	if !columns && len(shown) > goiColumns {
		shown = shown[:goiColumns]
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "column\tstored\tuncompressed\tof release\n")
	for _, c := range shown {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Name, small(c.Compressed), small(c.Uncompressed), percent(c.Share))
	}
	if rest := r.Columns[len(shown):]; len(rest) > 0 {
		var compressed, uncompressed int64
		var share float64
		for _, c := range rest {
			compressed += c.Compressed
			uncompressed += c.Uncompressed
			share += c.Share
		}
		fmt.Fprintf(tw, "%d more columns, which -columns prints\t%s\t%s\t%s\n",
			len(rest), small(compressed), small(uncompressed), percent(share))
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s over %s, holding %s, weighed on %s.\n",
		small(r.Bytes), plural(r.Shards, "shard"), plural64(r.Rows, "document"), r.Box)
	if r.Box == "unmeasured" {
		fmt.Fprint(w, "That box is not on the fleet, so this is a check rather than the release reading.\n")
	}
	if server1, ok := may.Lookup("server1"); ok {
		fmt.Fprintf(w, "Weighing it read %s of footers, against the %s server1 has free, so the smallest box on the fleet can take this reading.\n",
			small(r.Read), disk(server1.FreeDisk))
	}

	if len(r.Loose) > 0 {
		fmt.Fprintf(w, "\n%s outside the band around the %s shard target:\n", plural(len(r.Loose), "shard"), disk(r.Target))
		shown := r.Loose
		if !all && len(shown) > 5 {
			shown = shown[:5]
		}
		lw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, s := range shown {
			fmt.Fprintf(lw, "  %s\t%s\t%s\n", s.Path, small(s.Bytes), plural64(s.Rows, "document"))
		}
		_ = lw.Flush()
		if len(shown) < len(r.Loose) {
			fmt.Fprintf(w, "  and %d more, which -loose prints\n", len(r.Loose)-len(shown))
		}
	}

	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "\nThese shards are not one release, so they are not added up:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}

	fmt.Fprint(w, "\n")
	gw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(gw, "P06-1, the release on disk\t%s\tagainst %s\t%s\n", small(r.Bytes), disk(r.Ceiling), yesno(r.Bytes < r.Ceiling))
	fmt.Fprintf(gw, "P06-4, the metadata columns\t%s\tagainst %s\t%s\n", percent(r.Share), percent(r.Max), yesno(r.Share < r.Max))
	_ = gw.Flush()

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

// plural64 is plural for a count that comes off a corpus rather than off a
// slice, which is why it reads in millions once there are enough of them.
func plural64(n int64, noun string) string {
	switch {
	case n == 1:
		return "1 " + noun
	case n >= 1_000_000:
		return fmt.Sprintf("%s %ss", millions(n), noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// small writes a byte count that might be a release and might be a handful of
// documents. disk stops at whole megabytes, which is the right floor for a batch
// on a box and the wrong one for a column of a few test parts, and it also puts
// a shard of eleven and a half megabytes next to a verdict calling the same
// bytes 11.5 MB.
func small(n int64) string {
	switch {
	case n >= 1<<30:
		return disk(n)
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
