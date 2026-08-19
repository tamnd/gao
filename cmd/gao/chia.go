package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/tamnd/gao/chia"
	"github.com/tamnd/gao/may"
)

func runChia(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("route", flag.ContinueOnError)
	fs.SetOutput(stderr)
	long := fs.Bool("why", false, "print the reason and the measurements for each document")
	box := fs.String("box", "", "the box this ran on, defaulting to the one it is running on")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao route [-why] [-box name] file.pdf...

Divides PDFs three ways before any of them is extracted: T for a born digital
text layer, L for a text layer in a legacy Vietnamese font encoding, and O for a
page image that has to go to OCR. A document the scan will not guess about comes
back unroutable with the reason.

The distribution at the end is the number this slice is costed from, since T and
L are milliseconds a page and O is a GPU second.

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

	label := *box
	if label == "" {
		// A routing distribution with no hardware on it is a number nobody can
		// reproduce, so a run off the fleet says so rather than leaving it blank.
		if b, ok := may.Current(); ok {
			label = b.Name
		} else {
			label = "a box that is not on the fleet"
		}
	}
	d := chia.NewDistribution(label)

	failed := false
	for _, name := range fs.Args() {
		b, err := os.ReadFile(name)
		if err != nil {
			fmt.Fprintf(stderr, "gao route: %v\n", err)
			failed = true
			continue
		}
		r := chia.Read(b)
		d.Add(r)
		if *long {
			fmt.Fprintf(stdout, "%s\n  %s %s: %s\n", name, r.Route.Letter(), r.Route, r.Why)
			fmt.Fprintf(stdout, "  %d pages, %.0f characters a page, %.0f%% of the stream bytes are image\n",
				r.Pages, r.GlyphsPerPage(), 100*r.ImageShare)
			if len(r.Fonts) > 0 {
				fmt.Fprintf(stdout, "  fonts: %s\n", strings.Join(r.Fonts, ", "))
			}
			continue
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", r.Route.Letter(), name, r.Why)
	}

	fmt.Fprintf(stdout, "\n%s", d)
	if sets := d.Charsets(); len(sets) > 0 {
		names := make([]string, 0, len(sets))
		for k := range sets {
			names = append(names, k)
		}
		sort.Strings(names)
		fmt.Fprintln(stdout)
		for _, n := range names {
			fmt.Fprintf(stdout, "%-12s %6d\n", n, sets[n])
		}
	}
	if failed {
		return 1
	}
	return 0
}
