package giao

import (
	"errors"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/gat"
)

// entry is a finished file in a ledger, at the size and finish time the test
// cares about.
func entry(box string, bytes int64, finished string) gat.Entry {
	return gat.Entry{
		Source:   doc.Source("glotcc"),
		Revision: "9ad140b6be3a",
		Path:     "v1.0/vie-Latn/vie-Latn_0.parquet",
		Bytes:    bytes,
		Finished: finished,
		Box:      box,
	}
}

// The shape of a real run: three files, and the reading covers the two whose
// window the ledger knows.
func TestMeasureCountsFromTheFirstFinish(t *testing.T) {
	es := []gat.Entry{
		entry("server3", 2_100_000_000, "2026-08-18T06:19:00Z"),
		entry("server3", 2_100_000_000, "2026-08-18T06:39:00Z"),
		entry("server3", 2_100_000_000, "2026-08-18T06:59:00Z"),
	}
	r, err := Measure(es)
	if err != nil {
		t.Fatal(err)
	}
	if r.Box != "server3" {
		t.Errorf("box %q, want server3", r.Box)
	}
	if r.Bytes != 4_200_000_000 {
		t.Errorf("measured across %d bytes, want the two files after the first", r.Bytes)
	}
	if r.Seconds != 2400 {
		t.Errorf("measured across %v seconds, want the 40 minutes between the first finish and the last", r.Seconds)
	}
	if r.On != "2026-08-18" {
		t.Errorf("dated %q, want the day the run ended", r.On)
	}
	if !strings.Contains(r.How, "glotcc") {
		t.Errorf("how says %q, and it should say what was fetched", r.How)
	}
	if got := r.Rate(); got != 1_750_000 {
		t.Errorf("rate %v bytes per second, want 1750000", got)
	}
}

// The ledger is append only and a run is sequential, so the entries arrive in
// order. Nothing guarantees it, and a reading that depends on the file order is
// a reading that changes when somebody sorts the file.
func TestMeasureDoesNotCareWhatOrderTheLedgerIsIn(t *testing.T) {
	forward, err := Measure([]gat.Entry{
		entry("server1", 4_300_000_000, "2026-08-18T11:00:00Z"),
		entry("server1", 4_300_000_000, "2026-08-18T12:00:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	backward, err := Measure([]gat.Entry{
		entry("server1", 4_300_000_000, "2026-08-18T12:00:00Z"),
		entry("server1", 4_300_000_000, "2026-08-18T11:00:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if forward != backward {
		t.Errorf("the same ledger read in two orders gave %+v and %+v", forward, backward)
	}
}

func TestMeasureRefusals(t *testing.T) {
	cases := []struct {
		name string
		es   []gat.Entry
		want string
	}{
		{"nothing finished", nil, "no file has finished yet"},
		{
			"one file", []gat.Entry{entry("server3", 9_000_000_000, "2026-08-18T06:19:00Z")},
			"one finish time is not a duration",
		},
		{
			"two boxes", []gat.Entry{
				entry("server1", 4_300_000_000, "2026-08-18T11:00:00Z"),
				entry("server3", 4_300_000_000, "2026-08-18T12:00:00Z"),
			},
			"more than one run",
		},
		{
			"an unlabelled box", []gat.Entry{
				entry("", 4_300_000_000, "2026-08-18T11:00:00Z"),
				entry("", 4_300_000_000, "2026-08-18T12:00:00Z"),
			},
			"belongs to nobody",
		},
		{
			"too little moved", []gat.Entry{
				entry("server3", 9_000_000_000, "2026-08-18T06:19:00Z"),
				entry("server3", 200_000_000, "2026-08-18T06:29:00Z"),
			},
			"a reading is taken across at least 1.0 GB",
		},
		{
			"no clock", []gat.Entry{
				entry("server3", 2_100_000_000, "2026-08-18T06:19:00Z"),
				entry("server3", 2_100_000_000, "2026-08-18T06:19:00Z"),
			},
			"the run took no time",
		},
		{
			"a finish that is not a time", []gat.Entry{
				entry("server3", 2_100_000_000, "2026-08-18T06:19:00Z"),
				entry("server3", 2_100_000_000, "yesterday"),
			},
			"which is not a time",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Measure(c.es)
			if err == nil {
				t.Fatal("the ledger was accepted as a reading")
			}
			if !errors.Is(err, ErrNotAReading) {
				t.Errorf("error %v, and a caller cannot tell what kind it is", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q, want it to say %q", err, c.want)
			}
		})
	}
}

// A reading has to be one a schedule will take, or deriving it accomplished
// nothing.
func TestAMeasuredReadingIsASchedule(t *testing.T) {
	r, err := Measure([]gat.Entry{
		entry("server3", 2_100_000_000, "2026-08-18T06:19:00Z"),
		entry("server3", 2_100_000_000, "2026-08-18T06:39:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	s := Divide([]Reading{r})
	if why := s.Blocking(); len(why) > 0 {
		t.Errorf("a reading straight off a ledger was refused: %v", why)
	}
}
