package main

// Keeping the parts index up with the parts.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tamnd/gao/count"
	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/store"
)

// indexGrace is how long the refresh that runs after the crawl has stopped gets.
// It is the same reasoning as the push grace: a crawl is stopped by a signal, and
// under the run's own context every one of these would be canceled before it
// began.
const indexGrace = 10 * time.Minute

// indexEvery refreshes the parts index of each repo the crawl writes to, for as
// long as ctx runs, and once more after it stops.
//
// The index is the only thing in a working repo that says what is in it, and
// until this existed nothing wrote it while a crawl was running. A fleet that
// pushed for two days left an index describing the first afternoon, which does
// not read as an index that is behind. It reads as a repo that is nearly empty.
//
// Exactly one box in a fleet may run this. Three crawlers each reading the
// index, adding their own parts and writing it back is a lost update, and the
// file ends up holding whichever box wrote last. What is written here is not an
// append: it lists the repo and reads every footer, so the one box that runs it
// picks up the other boxes' parts without needing to hear from them.
//
// That is also why the flag is a duration rather than a switch. A footer pass is
// a few kilobytes per part and it is not free, and how often it is worth paying
// depends on how fast the repo is growing.
func indexEvery(ctx context.Context, out io.Writer, every time.Duration, token string, sets []store.Dataset) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// The last parts of the run are pushed as the sink closes, which is
			// after this returns, so the refresh that matters most is this one.
			last, cancel := context.WithTimeout(context.WithoutCancel(ctx), indexGrace)
			indexRound(last, out, token, sets)
			cancel()
			return
		case <-t.C:
			indexRound(ctx, out, token, sets)
		}
	}
}

func indexRound(ctx context.Context, out io.Writer, token string, sets []store.Dataset) {
	for _, d := range sets {
		err := indexOnce(ctx, out, token, d)
		switch {
		case err == nil:
		case ctx.Err() != nil:
			// The crawl stopped partway through a pass, which is the ordinary
			// way a pass ends on a run shorter than the timer. The round that
			// follows the stop reads the repo again from the beginning, so
			// there is nothing here to report.
			return
		default:
			// A refresh that fails for any other reason is not a reason to stop
			// crawling. The next tick reads the same repo from the beginning, so
			// a failed pass leaves nothing half written behind it.
			fmt.Fprintf(out, "index %s: %v\n", d.Repo(), err)
		}
	}
}

// indexOnce reads every part's footer in one repo and puts the index and the
// card it generates back on the repo.
//
// The card goes with the index for the same reason gao store index sends both.
// A repo whose front page says one document count and whose index says another
// is worse than a repo with a stale index, because a reader has no way to tell
// which of the two was written first.
func indexOnce(ctx context.Context, out io.Writer, token string, d store.Dataset) error {
	report, err := count.IndexOf(ctx, &count.Store{Repo: d.Repo(), Token: token, API: pushAPI()}, nil)
	if err != nil {
		return err
	}
	var body strings.Builder
	if err := store.WriteIndex(&body, report.Rows); err != nil {
		return err
	}

	p := &store.Pusher{Repo: d.Repo(), Token: token, API: pushAPI(),
		Message: "Index the parts and say so on the card"}
	var moved bool
	for _, w := range []struct {
		what string
		body []byte
	}{
		{store.IndexName, []byte(body.String())},
		{store.CardName, []byte(store.Card(d, nil, report.Rows))},
	} {
		sent, err := p.PushText(ctx, w.what, w.body)
		if err != nil {
			return err
		}
		moved = moved || !sent.Skipped()
	}
	// A repo that has not taken a part since the last pass generates the same
	// two files and both pushes are skipped, which is most of them once a crawl
	// is running faster than the timer. Saying so every time would bury the
	// crawl's own progress lines.
	if moved {
		fmt.Fprintf(out, "indexed %s, %d parts and %s documents, read with %s of footers\n",
			d.Repo(), len(report.Rows), thousands(report.Documents()), fleet.Size(report.Moved))
	}
	return nil
}
