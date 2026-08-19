// Package wait reads the wait between requests to one host off a crawl that was
// running, rather than off the configuration that was supposed to produce it.
//
// Chờ is to wait. Politeness is the one part of a crawler where the setting and
// the behavior come apart quietly. A crawl delay is configured once, in a file,
// in seconds, and everything downstream of that number is a scheduler, a
// connection pool, a retry path, a redirect that lands on the same host under a
// different name, and a DNS answer that gives two addresses for one site. Any of
// those can put two requests on the wire a hundred milliseconds apart while the
// configuration still says four seconds, and nothing in the crawl notices,
// because the crawl is measuring throughput and the thing that went wrong is a
// gap.
//
// So the milestone item asks for this verified on the real box under real load
// rather than in a simulator, and the two halves of that sentence are separate
// requirements. Real box, because the delay is enforced by a scheduler competing
// with several hundred other fetches for four cores, and a scheduler that keeps
// its promises with one host in flight is not evidence of anything. Real load,
// for the same reason, which is why a reading taken while the box was idle is
// refused here rather than reported with a note.
//
// Four things are checked and they fail differently. The observed minimum gap
// against the delay we configured, which is the crawl doing what it was told.
// The delay we configured against what robots.txt asked for, since the larger of
// those two is not ours to choose. The peak concurrency to one host against the
// cap, because a crawl can honor every gap on every connection and still have
// six of them open. And the rate of 429 and 503 answers, which is the site's own
// opinion of our crawl delay, and it outranks the number we read out of its
// robots file.
package wait

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// MinSeconds is the shortest window a delay may be read off. A gap of four
// seconds means nothing over a minute of fetching.
const MinSeconds = 600.0

// MinFetches is the fewest requests to one host before the gaps between them
// are worth quoting.
const MinFetches = 50

// MinLoad is the fewest fetches in flight box wide for a reading to have been
// taken under load. Below it the scheduler was not competing with anything and
// the delay it kept is not the delay it will keep.
const MinLoad = 100

// MaxErrors is the most of a host's answers that may come back 429 or 503
// before the site is telling us the delay is too small whatever robots said.
const MaxErrors = 0.01

// Slack is how much of the required delay the shortest observed gap may fall
// under, since a timer and a socket do not agree to the millisecond.
const Slack = 0.95

// A Host is what one host got, measured while the crawl was running.
type Host struct {
	Host string `json:"host"`

	// Box is where the fetching happened, because a delay held on a machine
	// with nothing else to do is not the claim this item makes.
	Box string `json:"box"`

	Fetches int     `json:"fetches"`
	Seconds float64 `json:"seconds"`

	// Delay is the crawl delay we configured and Robots is what the host's
	// robots.txt asked for, in seconds, zero when it asks for nothing.
	Delay  float64 `json:"delay"`
	Robots float64 `json:"robots"`

	// MinGap is the shortest gap between two requests to this host and MeanGap
	// is the average one. The minimum is the number that matters, because the
	// average of a violated delay and a long queue wait looks polite.
	MinGap  float64 `json:"min_gap"`
	MeanGap float64 `json:"mean_gap"`

	// Cap is the per host concurrency we configured and Peak is the most
	// requests this host had in flight at once.
	Cap  int `json:"cap"`
	Peak int `json:"peak"`

	// Load is how many fetches the box had in flight while this was measured.
	Load int `json:"load"`

	// Throttled and Unavailable are 429 and 503 answers.
	Throttled   int `json:"throttled"`
	Unavailable int `json:"unavailable"`
}

// Required is the delay that actually binds, which is the larger of ours and
// the one the host asked for.
func (h Host) Required() float64 {
	if h.Robots > h.Delay {
		return h.Robots
	}
	return h.Delay
}

// Margin is the shortest observed gap as a share of the required delay. Under
// one is a delay that was not kept.
func (h Host) Margin() float64 {
	if h.Required() <= 0 {
		return 0
	}
	return h.MinGap / h.Required()
}

// Kept reports whether the shortest gap cleared the delay that binds.
func (h Host) Kept() bool { return h.Required() > 0 && h.Margin() >= Slack }

// Ignored reports whether robots asked for more than we configured.
func (h Host) Ignored() bool { return h.Robots > h.Delay }

// Overrun reports whether more requests were in flight to this host than the
// cap allows.
func (h Host) Overrun() bool { return h.Cap > 0 && h.Peak > h.Cap }

// Errors is the share of answers that came back 429 or 503.
func (h Host) Errors() float64 {
	if h.Fetches <= 0 {
		return 0
	}
	return float64(h.Throttled+h.Unavailable) / float64(h.Fetches)
}

// Refused reports whether the host itself says the delay is too small.
func (h Host) Refused() bool { return h.Errors() > MaxErrors }

// Loaded reports whether the box was busy while this was measured.
func (h Host) Loaded() bool { return h.Load >= MinLoad }

// Rate is requests a second to this host, which is the reciprocal of the gap the
// crawl actually held.
func (h Host) Rate() float64 {
	if h.Seconds <= 0 {
		return 0
	}
	return float64(h.Fetches) / h.Seconds
}

// Blocking is every reason this host's reading is not evidence of politeness.
func (h Host) Blocking() []string {
	var why []string
	if h.Host == "" {
		return []string{"a reading with no host on it is an aggregate, and a crawl delay is a promise made to one site at a time"}
	}
	if h.Box == "" {
		why = append(why, fmt.Sprintf("%s does not say which box fetched it, and the whole of this item is that the delay was held on a real one", h.Host))
	}
	if h.Seconds < MinSeconds {
		why = append(why, fmt.Sprintf(
			"%s was watched for %s, under the %s a gap of seconds needs to mean anything, so what was measured is a burst rather than a rate",
			h.Host, seconds(h.Seconds), seconds(MinSeconds)))
	}
	if h.Fetches < MinFetches {
		why = append(why, fmt.Sprintf(
			"%s was fetched %d times, under %d, and the shortest gap in a handful of requests is the shortest gap in a handful of requests",
			h.Host, h.Fetches, MinFetches))
	}
	if h.Delay <= 0 {
		why = append(why, fmt.Sprintf("%s records no configured crawl delay, so there is nothing for the gaps to have been held against", h.Host))
	}
	if h.MinGap <= 0 {
		why = append(why, fmt.Sprintf("%s records no shortest gap, and the mean gap is the number that hides exactly the failure this looks for", h.Host))
	}
	if !h.Loaded() {
		why = append(why, fmt.Sprintf(
			"%s was measured with %d fetches in flight on %s, under %d, and a delay held by a scheduler with nothing else to do is a simulator with a real network stack under it",
			h.Host, h.Load, h.Box, MinLoad))
	}
	if h.Cap <= 0 {
		why = append(why, fmt.Sprintf("%s records no per host concurrency cap, and a crawl can hold every gap on every connection and still open six of them", h.Host))
	}
	return why
}

// A Run is the hosts a crawl was watched on.
type Run struct {
	// Crawl is what was running, so a reading can be traced back to the fetches
	// it was taken from.
	Crawl string `json:"crawl"`

	Hosts []Host `json:"hosts"`
}

// Ranked is the hosts by how close they came to the delay they owed, closest
// first, since the one that came nearest is the one worth reading.
func (r Run) Ranked() []Host {
	out := slices.Clone(r.Hosts)
	slices.SortStableFunc(out, func(a, b Host) int {
		switch {
		case a.Margin() < b.Margin():
			return -1
		case a.Margin() > b.Margin():
			return 1
		default:
			return strings.Compare(a.Host, b.Host)
		}
	})
	return out
}

// Closest is the host that came nearest to its delay, and false when nothing
// was watched.
func (r Run) Closest() (Host, bool) {
	if len(r.Hosts) == 0 {
		return Host{}, false
	}
	// Taken off the ranking rather than found again, so that two hosts at the
	// same margin name the same one in the table and in the sentence.
	return r.Ranked()[0], true
}

// Broken is every host the delay was not held on.
func (r Run) Broken() []Host { return r.filter(func(h Host) bool { return !h.Kept() }) }

// Overrun is every host more requests were in flight to than the cap allows.
func (r Run) Overrun() []Host { return r.filter(Host.Overrun) }

// Refused is every host that answered enough 429s and 503s to be saying so
// itself.
func (r Run) Refused() []Host { return r.filter(Host.Refused) }

// Asked is every host whose robots.txt wants a longer gap than the crawl's own
// delay. It is not a fault, since the configured delay is a floor and robots is
// read per host at run time, but it is the set where the gap that has to be held
// is somebody else's number rather than ours.
func (r Run) Asked() []Host { return r.filter(Host.Ignored) }

// Load is the lowest box wide load any of these readings was taken under, which
// is the load the weakest claim in the set rests on.
func (r Run) Load() int {
	if len(r.Hosts) == 0 {
		return 0
	}
	low := r.Hosts[0].Load
	for _, h := range r.Hosts[1:] {
		if h.Load < low {
			low = h.Load
		}
	}
	return low
}

// Blocking is every reason this run does not verify politeness.
func (r Run) Blocking() []string {
	if len(r.Hosts) == 0 {
		return []string{"no host was watched, so the crawl delay is a line of configuration and this item is asking what happened on the wire"}
	}
	var why []string
	if r.Crawl == "" {
		why = append(why, "the readings do not say which crawl they came off, and a politeness claim belongs to a run rather than to a repository")
	}
	seen := map[string]bool{}
	boxes := map[string]bool{}
	for _, h := range r.Hosts {
		if seen[h.Host] {
			why = append(why, fmt.Sprintf("%s appears twice, and two readings of one host are not two hosts", h.Host))
		}
		seen[h.Host] = true
		if h.Box != "" {
			boxes[h.Box] = true
		}
		why = append(why, h.Blocking()...)
	}
	if len(boxes) > 1 {
		why = append(why, "the readings come off more than one box, and a delay held under the load of one machine is not evidence about another")
	}
	return why
}

// Settled reports whether this is a verification rather than a sample of one.
func (r Run) Settled() bool { return len(r.Blocking()) == 0 }

// Holds reports whether the crawl was polite on every host it was watched on.
func (r Run) Holds() bool {
	return r.Settled() && len(r.Broken()) == 0 && len(r.Overrun()) == 0 && len(r.Refused()) == 0
}

// Verdict is the run in one sentence.
func (r Run) Verdict() string {
	h, ok := r.Closest()
	if !ok {
		return "no host was watched, so what is published is the configuration rather than the behavior"
	}
	if why := r.Blocking(); len(why) > 0 {
		return why[0]
	}
	if bad := r.Broken(); len(bad) > 0 {
		w := bad[0]
		return fmt.Sprintf(
			"%s came back at %s between requests against the %s it owed, so the delay is a number in a config file and the socket did something else",
			w.Host, seconds(w.MinGap), seconds(w.Required()))
	}
	if over := r.Overrun(); len(over) > 0 {
		w := over[0]
		return fmt.Sprintf(
			"%s held its gap on every connection and had %d of them open against a cap of %d, which is the same load on the site arriving in parallel instead of in sequence",
			w.Host, w.Peak, w.Cap)
	}
	if bad := r.Refused(); len(bad) > 0 {
		w := bad[0]
		return fmt.Sprintf(
			"%s answered %s of its requests with 429 or 503, which is the site's opinion of our crawl delay and it outranks the one we read out of its robots file",
			w.Host, share(w.Errors()))
	}
	return fmt.Sprintf(
		"the crawl held its delay on %s under %d fetches in flight on %s, and the closest it came was %s on %s against the %s that host was owed",
		plural(len(r.Hosts), "host"), r.Load(), h.Box, seconds(h.MinGap), h.Host, seconds(h.Required()))
}

func (r Run) filter(keep func(Host) bool) []Host {
	var out []Host
	for _, h := range r.Ranked() {
		if keep(h) {
			out = append(out, h)
		}
	}
	return out
}

// ReadRun loads a run from a file of one JSON host reading per line.
func ReadRun(crawl, path string) (Run, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Run{}, fmt.Errorf("cho: %w", err)
	}
	r := Run{Crawl: crawl}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var h Host
		if err := dec.Decode(&h); err != nil {
			return Run{}, fmt.Errorf("cho: %s line %d: %w", path, i+1, err)
		}
		r.Hosts = append(r.Hosts, h)
	}
	if len(r.Hosts) == 0 {
		return Run{}, fmt.Errorf("cho: %s holds no readings", path)
	}
	return r, nil
}

// seconds renders a delay the way a robots file writes one, which is in whole
// seconds when it is one and to the millisecond when the failure is that it was
// not.
func seconds(f float64) string {
	if f >= 1 && f == float64(int(f)) {
		return fmt.Sprintf("%.0fs", f)
	}
	return fmt.Sprintf("%.2fs", f)
}

func share(f float64) string { return fmt.Sprintf("%.1f%%", 100*f) }

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
