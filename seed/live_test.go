package seed

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// listen opens a real listener on loopback and returns its port, so the probe
// under test does a real DNS lookup of localhost and a real TCP connect rather
// than being handed a fake dialer that would only prove the test's own wiring.
func listen(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	return port
}

func TestAHostThatAnswersIsLiveAndOneThatDoesNotIsNot(t *testing.T) {
	open := listen(t)

	// A port nothing is on. Opening a listener and closing it is how to get a
	// port that is certainly free rather than one that probably is.
	shut, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	_, closed, _ := net.SplitHostPort(shut.Addr().String())
	_ = shut.Close()

	got, err := Probe(t.Context(), []string{"localhost"}, ProbeOptions{
		Ports:   []string{open},
		Timeout: 2 * time.Second,
		Rest:    -1,
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(got) != 1 || !got[0].Live {
		t.Fatalf("a host with something listening came back %+v", got)
	}
	if got[0].Addr == "" {
		t.Error("a live host came back with no address, so a caller cannot see a seed list collapse onto one parking page")
	}

	got, err = Probe(t.Context(), []string{"localhost"}, ProbeOptions{
		Ports:   []string{closed},
		Timeout: 2 * time.Second,
		Rest:    -1,
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(got) != 1 || got[0].Live {
		t.Fatalf("a host with nothing listening came back %+v", got)
	}
	if got[0].Why != "no listener" {
		t.Errorf("a host that resolved and refused came back as %q, and the reason it failed is the difference between gone and unreachable", got[0].Why)
	}
}

func TestASecondPortIsTriedWhenTheFirstRefuses(t *testing.T) {
	shut, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	_, closed, _ := net.SplitHostPort(shut.Addr().String())
	_ = shut.Close()
	open := listen(t)

	// 443 then 80 is the real order, and a host serving only 80 has to come back
	// live or the screen would drop every plain HTTP site in the inventory.
	got, err := Probe(t.Context(), []string{"localhost"}, ProbeOptions{
		Ports:   []string{closed, open},
		Timeout: 2 * time.Second,
		Rest:    -1,
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(got) != 1 || !got[0].Live {
		t.Fatalf("a host answering on the second port came back %+v", got)
	}
}

func TestAHostWhoseNameDoesNotResolveIsNotProbedFurther(t *testing.T) {
	got, err := Probe(t.Context(), []string{"nothing.invalid"}, ProbeOptions{
		Timeout: 2 * time.Second,
		Rest:    -1,
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(got) != 1 || got[0].Live {
		t.Fatalf("an unresolvable host came back %+v", got)
	}
	// .invalid is reserved by RFC 2606 precisely so it cannot resolve, so this
	// is a name that does not exist rather than a resolver that gave up.
	if got[0].Why != "no such host" {
		t.Errorf("an unresolvable host came back as %q", got[0].Why)
	}
}

func TestResultsComeBackInTheOrderTheHostsWereGiven(t *testing.T) {
	open := listen(t)
	in := []string{"localhost", "nothing.invalid", "localhost", "also-nothing.invalid"}

	got, err := Probe(t.Context(), in, ProbeOptions{
		Ports:       []string{open},
		Timeout:     2 * time.Second,
		Concurrency: 4,
		Rest:        -1,
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("Probe returned %d results for %d hosts", len(got), len(in))
	}
	// The point of the ordering is that two passes over the same list can be
	// diffed. Probes finish out of order, so this is a real risk rather than a
	// theoretical one.
	for i, want := range in {
		if got[i].Name != want {
			t.Errorf("result %d is %q and the host at that position was %q", i, got[i].Name, want)
		}
	}
	if live := Live(got); len(live) != 2 {
		t.Errorf("Live returned %v", live)
	}
}

func TestACancelledPassKeepsWhatItAlreadyFound(t *testing.T) {
	open := listen(t)
	hosts := make([]string, 40)
	for i := range hosts {
		hosts[i] = "localhost"
	}

	ctx, cancel := context.WithCancel(t.Context())
	got, err := Probe(ctx, hosts, ProbeOptions{
		Ports:   []string{open},
		Timeout: 2 * time.Second,
		Batch:   10,
		Rest:    time.Minute,
		Progress: func(done, _ int) {
			// Cancel during the pause after the first batch, which is where a
			// pass over the real inventory spends most of its life.
			if done >= 10 {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled pass returned err %v", err)
	}
	// Half a screened list is worth keeping, and an hour long pass that returns
	// nothing when it is interrupted is an hour thrown away.
	if len(got) == 0 {
		t.Fatal("a cancelled pass threw away everything it had already probed")
	}
	if len(got) >= len(hosts) {
		t.Errorf("a cancelled pass returned all %d results, so it did not stop", len(got))
	}
	for i, r := range got {
		if !r.Live {
			t.Errorf("result %d of a cancelled pass is not live, so it was returned unprobed: %+v", i, r)
		}
	}
}

func TestTallySaysWhenTheResolverRatherThanTheWebWasMeasured(t *testing.T) {
	// The failure mode this catches is the one that cost a real measurement.
	// Probing 20,000 hosts in one pass reported 95.6% with no DNS, which looked
	// like a fact about the hosts and was the resolver falling over.
	rs := make([]Liveness, 0, 100)
	for range 10 {
		rs = append(rs, Liveness{Name: "up.example", Live: true})
	}
	for range 90 {
		rs = append(rs, Liveness{Name: "slow.example", Why: "dns timeout"})
	}

	s := Tally(rs)
	if s.Total != 100 || s.Live != 10 || s.By["dns timeout"] != 90 {
		t.Fatalf("Tally came back %+v", s)
	}
	out := s.String()
	if !strings.Contains(out, "resolver") {
		t.Errorf("a pass that is 90%% DNS timeouts did not say the resolver was the problem:\n%s", out)
	}

	// A pass whose failures are genuinely dead names must not cry resolver, or
	// the warning stops meaning anything.
	clean := make([]Liveness, 0, 100)
	for range 10 {
		clean = append(clean, Liveness{Name: "up.example", Live: true})
	}
	for range 90 {
		clean = append(clean, Liveness{Name: "gone.example", Why: "no such host"})
	}
	if out := Tally(clean).String(); strings.Contains(out, "resolver") {
		t.Errorf("a pass whose failures are dead names blamed the resolver:\n%s", out)
	}
}
