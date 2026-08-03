package doc

import "testing"

func TestLicenseClassNames(t *testing.T) {
	want := map[LicenseClass]string{
		LicenseUnknown:               "unknown",
		LicenseOpen:                  "open",
		LicensePermissiveAttribution: "permissive-attribution",
		LicenseRestricted:            "restricted",
		LicenseUnredistributable:     "unredistributable",
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

func TestOnlyTwoClassesArePublishable(t *testing.T) {
	// The unknown case is one of the three that is not, which is the whole point
	// of making it the zero value.
	publishable := 0
	for _, c := range []LicenseClass{
		LicenseUnknown, LicenseOpen, LicensePermissiveAttribution,
		LicenseRestricted, LicenseUnredistributable,
	} {
		if c.Publishable() {
			publishable++
		}
	}
	if publishable != 2 {
		t.Errorf("%d license classes are publishable, want 2", publishable)
	}
	if !LicensePermissiveAttribution.RequiresAttribution() {
		t.Error("permissive-attribution does not require attribution")
	}
	if LicenseOpen.RequiresAttribution() {
		t.Error("open requires attribution")
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
