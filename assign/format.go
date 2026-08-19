package assign

// The units a schedule is quoted in.
//
// They are in the package rather than in the command because the verdict is a
// sentence, and eleven days is a sentence while 990720 seconds is a field.

import "fmt"

// hours writes a duration at the unit somebody would book it in. An ingest is
// measured in days and a single file in hours, and printing either in the
// other's unit is how a plan gets read wrong at a glance.
func hours(seconds float64) string {
	switch {
	case seconds >= 48*3600:
		return fmt.Sprintf("%.1f days", seconds/(24*3600))
	case seconds >= 3600:
		return fmt.Sprintf("%.1f hours", seconds/3600)
	case seconds >= 90:
		return fmt.Sprintf("%.0f minutes", seconds/60)
	case seconds >= 60:
		return "1 minute"
	case seconds >= 2:
		return fmt.Sprintf("%.0f seconds", seconds)
	}
	return fmt.Sprintf("%.1f seconds", seconds)
}

// bytesOf writes a transfer size in the decimal units a host quotes a file in,
// since the manifest's byte counts came off the hosts.
func bytesOf(n int64) string {
	switch {
	case n >= 1e12:
		return fmt.Sprintf("%.1f TB", float64(n)/1e12)
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
	return fmt.Sprintf("%d bytes", n)
}

// plural writes a count with its noun, since one box and 1 boxes are not the
// same sentence.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	if noun == "box" {
		return fmt.Sprintf("%d boxes", n)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
