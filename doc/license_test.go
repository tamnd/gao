package doc

import "testing"

func TestLicenseClassNames(t *testing.T) {
	want := map[LicenseClass]string{
		LicenseUnknown:               "unknown",
		LicenseOpen:                  "open",
		LicensePermissiveAttribution: "permissive-attribution",
		LicenseCrawled:               "crawled",
		LicenseRestricted:            "restricted",
		LicenseUnredistributable:     "unredistributable",
	}
	if len(want) != len(LicenseClasses()) {
		t.Errorf("%d classes have a name pinned here and there are %d", len(want), len(LicenseClasses()))
	}
	for c, name := range want {
		if got := c.String(); got != name {
			t.Errorf("%d stringifies as %q, want %q", uint8(c), got, name)
		}
		parsed, err := ParseLicenseClass(name)
		if err != nil {
			t.Errorf("ParseLicenseClass(%q): %v", name, err)
			continue
		}
		if parsed != c {
			t.Errorf("ParseLicenseClass(%q) = %s", name, parsed)
		}
	}
	if LicenseClass(200).Valid() {
		t.Error("an out of range class reported itself valid")
	}
	if got := LicenseClass(200).String(); got != "LicenseClass(200)" {
		t.Errorf("an out of range class stringifies as %q", got)
	}
	if _, err := ParseLicenseClass("public-domain-ish"); err == nil {
		t.Error("an unknown name parsed without complaint")
	}
}

// Which classes publish is the most consequential fact in this package, so it is
// pinned one class at a time rather than counted. The count is what this test
// used to do, and a count is satisfied by any two classes: it would have gone on
// passing if publishable had swapped restricted in for open.
//
// The list walks [LicenseClasses] so that adding a class fails here until
// somebody decides which side it is on. That is the intended failure. A new
// class defaulting quietly to unpublishable would be the safe direction and
// still wrong, because nobody would have written down that it was a decision.
func TestWhichClassesPublishIsPinnedOneAtATime(t *testing.T) {
	want := map[LicenseClass]bool{
		LicenseOpen:                  true,
		LicensePermissiveAttribution: true,
		LicenseCrawled:               true,
		LicenseRestricted:            false,
		LicenseUnredistributable:     false,
		// The zero value, and not publishable, which is the whole point of
		// making the failure to determine the zero value.
		LicenseUnknown: false,
	}
	for _, c := range LicenseClasses() {
		w, ok := want[c]
		if !ok {
			t.Errorf("%s is a class and nothing here says whether it publishes", c)
			continue
		}
		if c.Publishable() != w {
			t.Errorf("%s publishes=%v, want %v", c, c.Publishable(), w)
		}
	}
}

// Attribution is a separate question from publication and lands on the same
// column either way, so the two are pinned apart.
func TestAttributionIsPinnedOneClassAtATime(t *testing.T) {
	want := map[LicenseClass]bool{
		LicenseOpen:                  false,
		LicensePermissiveAttribution: true,
		// No clause requires it, and a page published as fetched still has to
		// say where it was fetched from, or the claim cannot be checked and the
		// page cannot be asked for back.
		LicenseCrawled:           true,
		LicenseRestricted:        false,
		LicenseUnredistributable: false,
		LicenseUnknown:           false,
	}
	for _, c := range LicenseClasses() {
		w, ok := want[c]
		if !ok {
			t.Errorf("%s is a class and nothing here says whether it carries attribution", c)
			continue
		}
		if c.RequiresAttribution() != w {
			t.Errorf("%s requires attribution=%v, want %v", c, c.RequiresAttribution(), w)
		}
	}
}

// Every class is in the list exactly once, since three tests and two tables walk
// it as though that were true.
func TestEveryClassIsListedOnce(t *testing.T) {
	seen := map[LicenseClass]bool{}
	for _, c := range LicenseClasses() {
		if seen[c] {
			t.Errorf("%s is listed twice", c)
		}
		if !c.Valid() {
			t.Errorf("%s is listed and is not a valid class", c)
		}
		seen[c] = true
	}
	for c := range licenseNames {
		if !seen[c] {
			t.Errorf("%s has a name and is not in the list", c)
		}
	}
}

func TestLicenseClassesHandsOutACopy(t *testing.T) {
	got := LicenseClasses()
	got[0] = LicenseUnknown
	if LicenseClasses()[0] == LicenseUnknown {
		t.Error("editing the returned slice edited the list")
	}
}

func TestEverySourceIsDescribedAndListed(t *testing.T) {
	listed := Sources()
	if len(listed) != len(sources) {
		t.Errorf("Sources() returns %d entries, the table has %d", len(listed), len(sources))
	}
	for _, s := range listed {
		if !s.Valid() {
			t.Errorf("%s is listed but not valid", s)
		}
		if s.Describe() == "" {
			t.Errorf("%s has no description", s)
		}
	}
	if Source("nope").Valid() {
		t.Error("an undefined source reported itself valid")
	}
	if got := Source("nope").Describe(); got != `unknown source "nope"` {
		t.Errorf("an undefined source describes itself as %q", got)
	}
}
