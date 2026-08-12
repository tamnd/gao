package bang

// The units a scoreboard is quoted in.
//
// They are in the package rather than in the command because the verdict is a
// sentence, and "ahead by 0.30000000000000004" is not one.

import (
	"fmt"
	"strconv"
)

// points writes a number of benchmark points, at the one decimal place the
// benchmarks on this roster are reported to.
func points(f float64) string { return fmt.Sprintf("%.1f points", f) }

// margin writes a margin as the clause it goes in, since ahead and behind are
// the two things a reader is looking for and a minus sign is easy to miss.
func margin(f float64) string {
	switch {
	case f > 0:
		return fmt.Sprintf("%.1f ahead", f)
	case f < 0:
		return fmt.Sprintf("%.1f behind", -f)
	}
	return "level"
}

// plural writes a count with its noun.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// itoa is a count in a sentence.
func itoa(n int) string { return strconv.Itoa(n) }
