package main

// Rebuilding a snapshot and checking the bytes come out the same.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/normalize"
	"github.com/tamnd/gao/sift"
	"github.com/tamnd/gao/store"
)

// registerStageChecks tells kho what the cleaning stages do.
//
// It lives here rather than in kho because phoi and sang read and write through
// kho, so kho reaching back for them would be a cycle. Both checks are the same
// shape: run the stage on its own output and see whether it changes anything. A
// stage that is a function has to leave its own output alone, so a document that
// moves under it was not produced by this version of it.
func registerStageChecks() {
	store.RegisterStageCheck("phoi", func(d *doc.Document) error {
		if r := normalize.Normalize(d.Text); r.Changed {
			return errors.New("normalizing it again changes it")
		}
		return nil
	})
	store.RegisterStageCheck("sang", func(d *doc.Document) error {
		if reason, why, rejected := sift.Default().Reject(sift.Measure(d.Text)); rejected {
			return fmt.Errorf("it would be rejected now as %s: %s", reason, why)
		}
		return nil
	})
}

func runStoreReproduce(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("store reproduce", flag.ContinueOnError)
	fs.SetOutput(stderr)
	verbose := fs.Bool("v", false, "print a line per shard rather than only the ones that differ")
	stop := fs.Bool("stop", false, "return after the first shard that does not rebuild")
	frame := fs.Int("frame-bytes", store.DefaultFrameBytes, "the frame size the snapshot was written at")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao store reproduce [flags] <snapshot dir>

Rebuilds every shard of a snapshot from the documents it holds and checks the
bytes against what the manifest recorded.

This is half of what reproducible means and it is the half that has to hold
first. The other half is that the pipeline computes the same documents from the
same inputs, which cannot be checked from a snapshot alone because the inputs are
not in it. What can be checked is that writing those documents produces the same
file, and until that is established a stage rerun that disagrees is
indistinguishable from a compressor that disagrees.

So a difference here is this build of gao rather than the corpus. The documents
are the same by construction: they came out of the shard being rebuilt. What
changes bytes is a compressor version, a writer setting, or a change in gao, and
the report names the versions for that reason.

Nothing is written to disk. The rebuild is compared against the recording as it
is produced, so a 512 MB shard costs a buffer rather than 512 MB of free space,
which is what lets this run on server1.

The snapshot is verified before any of it, because asking whether bytes rebuild
to what a manifest says is not worth answering until something has established
that the manifest is the one that was signed.

Any stage in the manifest that this binary knows how to re-run is also run over
every document as it streams past, and a stage it does not know is listed as not
checked rather than passed over quietly.

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

	registerStageChecks()

	opts := []store.ReproduceOption{store.ReproduceFrameBytes(*frame)}
	if *stop {
		opts = append(opts, store.ReproduceStopEarly())
	}
	if *verbose {
		opts = append(opts, store.ReproduceProgress(func(rb store.Rebuild) {
			mark := "same"
			if !rb.Same {
				mark = "differs"
			}
			fmt.Fprintf(stdout, "%-32s %8d docs  %10d bytes  %s\n", rb.Name, rb.Documents, rb.Bytes, mark)
		}))
	}

	report, err := store.Reproduce(fs.Arg(0), opts...)
	if report == nil {
		fmt.Fprintf(stderr, "gao store reproduce: %v\n", err)
		return 1
	}
	if *verbose {
		fmt.Fprintln(stdout)
	}

	fmt.Fprintf(stdout, "snapshot   %s\n", report.Snapshot)
	fmt.Fprintf(stdout, "built by   %s\n", report.Env)
	fmt.Fprintf(stdout, "documents  %d\n", report.Documents)
	fmt.Fprintf(stdout, "shards     %d rebuilt to the same bytes, %d did not\n", report.Same, report.Different)

	if len(report.Stages) > 0 {
		fmt.Fprintln(stdout, "\nstages")
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		for _, st := range report.Stages {
			switch {
			case !st.Ran:
				fmt.Fprintf(tw, "  %s\tnot checked\t%s\n", st.Name, st.Why)
			case st.Disagreed > 0:
				fmt.Fprintf(tw, "  %s\t%d of %d documents disagree\t\n", st.Name, st.Disagreed, st.Checked)
			default:
				fmt.Fprintf(tw, "  %s\t%d documents agree\t\n", st.Name, st.Checked)
			}
		}
		_ = tw.Flush()
	}

	for _, st := range report.Stages {
		if st.Disagreed == 0 {
			continue
		}
		fmt.Fprintf(stdout, "\n%s, starting with:\n", st.Name)
		for _, id := range st.Sample {
			fmt.Fprintf(stdout, "  %s\n", id)
		}
	}

	if report.Different > 0 {
		fmt.Fprintln(stdout, "\nthe shards that did not rebuild:")
		for _, rb := range report.Shards {
			if rb.Same {
				continue
			}
			fmt.Fprintf(stdout, "  %s\n", rb.Name)
			fmt.Fprintf(stdout, "    recorded  %s  %d bytes\n", rb.Want, rb.Bytes)
			fmt.Fprintf(stdout, "    rebuilt   %s  %d bytes\n", rb.Got, rb.Rebuilt)
			where := fmt.Sprintf("frame %d", rb.Frame)
			if rb.Frame < 0 {
				// Past the last frame is the segment index and the trailer, and
				// that is a different fault from a frame that came out
				// differently: the documents compressed identically and the
				// bookkeeping written after them did not.
				where = "the index at the end of the segment, past every frame"
			}
			fmt.Fprintf(stdout, "    first differ at byte %d, in %s\n", rb.Diff, where)
		}
	}

	if err != nil {
		fmt.Fprintf(stderr, "\ngao kho reproduce: %v\n", err)
		if errors.Is(err, store.ErrNotReproducible) {
			fmt.Fprintln(stderr, "the documents are intact. what differs is how they were written, so compare the versions above against the box that wrote the snapshot")
		}
		return 1
	}
	fmt.Fprintln(stdout, "\nok")
	return 0
}
