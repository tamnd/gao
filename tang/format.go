package tang

import "fmt"

// tokens renders a count in the unit this project's claim is written in.
func tokens(n int64) string { return fmt.Sprintf("%.1fB", float64(n)/1e9) }

// size renders a byte count at the scale a corpus layer is actually quoted at.
func size(n int64) string {
	switch {
	case n >= 1e12:
		return fmt.Sprintf("%.1f TB", float64(n)/1e12)
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
}

func share(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
