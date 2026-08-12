package gian

// The units the readings are quoted in.
//
// They are in the package rather than in the command because the verdict is a
// sentence, and a sentence with a raw integer in the middle of it is a field
// with prose around it.

import (
	"fmt"
	"os"
)

// tokens writes a token count the way the training plan quotes one.
func tokens(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// thousands writes a document count with separators, because a pool of 812394
// documents and one of 8123940 look the same at a glance and differ by an order
// of magnitude.
func thousands(n int64) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		return "-" + thousands(-n)
	}
	out := make([]byte, 0, len(s)+len(s)/3)
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// gigabytes writes what the parts weigh on disk.
func gigabytes(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// sizeOf is what a part weighs on the filesystem, and zero when it cannot be
// asked. A pool that cannot stat a file it just read is not worth failing over,
// since the lengths are the measurement and the size is the context.
func sizeOf(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}
