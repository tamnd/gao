package frontier

import "testing"

// TestEveryBoxGetsHostsAndNoHostGetsTwoBoxes is the property the fleet split
// rests on. It is not that the split is even, which it is not exactly, but that
// it is total and stable: every host lands somewhere, it lands in range, and it
// lands in the same place every time it is asked.
func TestEveryBoxGetsHostsAndNoHostGetsTwoBoxes(t *testing.T) {
	hosts := []string{
		"vnexpress.net", "tuoitre.vn", "thanhnien.vn", "dantri.com.vn",
		"voz.vn", "otofun.net", "webtretho.com", "tinhte.vn",
		"example.com", "vi.wikipedia.org", "baochinhphu.vn", "kenh14.vn",
	}
	const fleet = 3
	seen := make(map[int]int)
	for _, host := range hosts {
		box := Box(host, fleet)
		if box < 0 || box >= fleet {
			t.Fatalf("Box(%q, %d) = %d, which is not a box in the fleet", host, fleet, box)
		}
		if again := Box(host, fleet); again != box {
			t.Errorf("Box(%q, %d) answered %d and then %d", host, fleet, box, again)
		}
		seen[box]++
	}
	for box := range fleet {
		if seen[box] == 0 {
			t.Errorf("box %d of %d owns none of %d hosts", box, fleet, len(hosts))
		}
	}
}

// TestAFleetOfOneOwnsEverything, because a single box crawl passes n of one and
// a rule that sent some of its hosts to a box that does not exist would be a
// crawl that quietly fetched a fraction of its seed.
func TestAFleetOfOneOwnsEverything(t *testing.T) {
	for _, n := range []int{0, 1} {
		for _, host := range []string{"vnexpress.net", "example.com", ""} {
			if got := Box(host, n); got != 0 {
				t.Errorf("Box(%q, %d) = %d, want 0", host, n, got)
			}
		}
	}
}
