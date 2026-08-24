package harvest

import "testing"

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
	b.mu.Lock()
	got := b.hosts["loud.example"].fails
	b.mu.Unlock()
	if got != 1000 {
		t.Fatalf("counted %d of 1000 failures, so the count is not shared safely", got)
	}
}
