package doc

import (
	"regexp"
	"testing"
)

func TestSlicesAreCompleteAndInOrder(t *testing.T) {
	if len(Slices) != 10 {
		t.Fatalf("got %d slices, want 10", len(Slices))
	}
	id := regexp.MustCompile(`^S[0-9]$`)
	for i, s := range Slices {
		if want := "S" + string(rune('0'+i)); s.ID != want {
			t.Errorf("slice %d has id %q, want %q", i, s.ID, want)
		}
		if !id.MatchString(s.ID) {
			t.Errorf("slice %d has a malformed id %q", i, s.ID)
		}
		if s.Title == "" || s.Ships == "" || s.Gate == "" || s.Kill == "" {
			t.Errorf("%s has an empty field: %+v", s.ID, s)
		}
	}
}

func TestSliceByID(t *testing.T) {
	s, ok := SliceByID("S7")
	if !ok {
		t.Fatal("SliceByID(S7) did not find the slice")
	}
	if s.Title != "The continued pretraining gate" {
		t.Errorf("SliceByID(S7) returned %q", s.Title)
	}
	if _, ok := SliceByID("S99"); ok {
		t.Error("SliceByID(S99) reported a slice that does not exist")
	}
}
