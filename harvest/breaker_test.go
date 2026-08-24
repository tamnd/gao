package harvest

import (
	"fmt"
	"testing"
)

func TestAHostGetsMoreThanOneChance(t *testing.T) {
	t.Parallel()

	b := newBreaker(0, nil)
	for i := range DefaultStrikes - 1 {
		b.failed("nobody.example")
		if b.dead("nobody.example") {
			t.Fatalf("gave up after %d failures, and the rule is %d", i+1, DefaultStrikes)
		}
	}
	b.failed("nobody.example")
	if !b.dead("nobody.example") {
		t.Fatalf("still asking after %d failures", DefaultStrikes)
	}
}

func TestAHostThatAnsweredOnceIsNeverGivenUpOn(t *testing.T) {
	t.Parallel()

	b := newBreaker(0, nil)
	b.answered("busy.example")
	for range 50 {
		b.failed("busy.example")
	}
	if b.dead("busy.example") {
		t.Fatal("gave up on a host that had answered, which is a busy host rather than a dead one")
	}
}

func TestAnAnswerAfterFailuresClearsThem(t *testing.T) {
	t.Parallel()

	b := newBreaker(0, nil)
	b.failed("slow.example")
	b.failed("slow.example")
	b.answered("slow.example")
	b.failed("slow.example")
	b.failed("slow.example")
	if b.dead("slow.example") {
		t.Fatal("counted failures from either side of an answer together")
	}
}

func TestAHostNobodyHasAskedAboutIsFine(t *testing.T) {
	t.Parallel()

	if newBreaker(0, nil).dead("unseen.example") {
		t.Fatal("gave up on a host before asking it anything")
	}
}

func TestTheStrikeCountCanBeSet(t *testing.T) {
	t.Parallel()

	b := newBreaker(1, nil)
	b.failed("once.example")
	if !b.dead("once.example") {
		t.Fatal("asked for one strike and got the default")
	}
}

func TestDroppedCountsOnlyTheHostsGivenUpOn(t *testing.T) {
	t.Parallel()

	b := newBreaker(2, nil)
	b.failed("gone.example")
	b.failed("gone.example")
	b.failed("flaky.example")
	b.answered("fine.example")
	b.failed("fine.example")
	b.failed("fine.example")

	if got := b.dropped(); got != 1 {
		t.Fatalf("dropped %d hosts and only gone.example is dead", got)
	}
}

func TestManyWorkersReportingOneHostAgree(t *testing.T) {
	t.Parallel()

	b := newBreaker(1000, nil)
	done := make(chan struct{})
	for range 20 {
		go func() {
			for range 50 {
				b.failed("loud.example")
				b.dead("loud.example")
			}
			done <- struct{}{}
		}()
	}
	for range 20 {
		<-done
	}
	s := b.shard("loud.example")
	s.mu.Lock()
	got := s.hosts["loud.example"].fails
	s.mu.Unlock()
	if got != 1000 {
		t.Fatalf("counted %d of 1000 failures, so the count is not shared safely", got)
	}
}

// BenchmarkBreaker measures the breaker the way a crawl uses it, which is every
// worker on the box asking about a different host at the same time.
//
// The pair is what one URL costs: dead before the request goes out and answered
// when it comes back. The hosts are distinct because a crawl's hosts are
// distinct, and that is the whole point of sharding by name: two workers on two
// hosts have no reason to wait for each other and used to anyway.
func BenchmarkBreaker(b *testing.B) {
	br := newBreaker(3, nil)
	hosts := make([]string, 4096)
	for i := range hosts {
		hosts[i] = fmt.Sprintf("host%d.example", i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			h := hosts[i%len(hosts)]
			i++
			br.dead(h)
			br.answered(h)
		}
	})
}

// A record has to be found in the shard it was written to, which is the one
// thing sharding can get wrong and the one thing no behaviour test would catch:
// a breaker that wrote to one map and read from another would simply forget
// every host, and forgetting a host is allowed.
func TestABreakerFindsAHostInTheShardItPutItIn(t *testing.T) {
	t.Parallel()

	b := newBreaker(3, nil)
	hosts := make([]string, 2000)
	for i := range hosts {
		hosts[i] = fmt.Sprintf("host%d.example", i)
	}
	for _, h := range hosts {
		for range 3 {
			b.failed(h)
		}
	}
	if got := b.dropped(); got != len(hosts) {
		t.Fatalf("dropped %d of %d, so a record went into one shard and was looked for in another", got, len(hosts))
	}
	for _, h := range hosts {
		if !b.dead(h) {
			t.Fatalf("%s struck out three times and is not dead", h)
		}
	}

	// And the spread is worth asserting rather than assuming. A hash that sent
	// every host to one shard would pass every test above and fix nothing.
	used := 0
	for i := range b.shards {
		if len(b.shards[i].hosts) > 0 {
			used++
		}
	}
	if used != breakerShards {
		t.Errorf("2,000 hosts landed in %d of %d shards", used, breakerShards)
	}
}
