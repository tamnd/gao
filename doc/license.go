package doc

import "fmt"

// LicenseClass is the redistribution status of a single document. It is a
// per-document column rather than a per-corpus property, because a corpus
// assembled from four acquisition paths and hundreds of thousands of hosts does
// not have one license, it has a distribution of them.
//
// The zero value is [LicenseUnknown] and it is deliberately not publishable. A
// document whose license was never determined looks exactly like a document
// whose license was determined to be permissive if the unknown case is the
// default, and the difference is the entire question.
type LicenseClass uint8

const (
	// LicenseUnknown means no determination was made. Never publishable.
	LicenseUnknown LicenseClass = iota

	// LicenseOpen covers material with no redistribution restriction we need to
	// carry: public domain, government works that Vietnamese intellectual
	// property law places outside copyright, and permissive licenses with no
	// attribution requirement.
	LicenseOpen

	// LicensePermissiveAttribution covers material redistributable with
	// attribution, which gao carries in the record rather than in a notices file
	// nobody reads.
	LicensePermissiveAttribution

	// LicenseCrawled covers a page gao fetched off the open web that made no
	// grant and reserved nothing.
	//
	// It is its own class rather than either of the two above it because no
	// license was granted, and rather than the two below it because none was
	// refused either. What it records is a publication posture and not a
	// permission: the page was publicly reachable, robots.txt allowed it, the
	// page carried no text and data mining reservation, and it is published as
	// fetched with its address attached and a takedown path attached to that.
	//
	// This is the posture every web-derived corpus has been published under.
	// Common Crawl fetches under it and ships the page bodies under its own
	// terms of use, and FineWeb, FineWeb-2, HPLT, GlotCC and MADLAD-400 are all
	// derived from those bodies. gao reads pages the same way and passes them on
	// the same way, which is what makes gao's crawl comparable to the corpora it
	// is measured against rather than a smaller thing with a stricter rule.
	//
	// The class does not weaken anything upstream of it. A reservation still
	// lands in [LicenseUnredistributable], a robots.txt refusal still stops the
	// fetch, and a takedown still moves a row out of here.
	LicenseCrawled

	// LicenseRestricted covers material with a term gao cannot satisfy in a bulk
	// release, most often non-commercial or share-alike. Retained in the store,
	// excluded from the published artifact, counted separately.
	LicenseRestricted

	// LicenseUnredistributable covers material we may process but may not pass
	// on: reserved under a machine-readable text and data mining opt-out, behind
	// terms that forbid redistribution, or subject to a takedown.
	LicenseUnredistributable
)

var licenseNames = map[LicenseClass]string{
	LicenseUnknown:               "unknown",
	LicenseOpen:                  "open",
	LicensePermissiveAttribution: "permissive-attribution",
	LicenseCrawled:               "crawled",
	LicenseRestricted:            "restricted",
	LicenseUnredistributable:     "unredistributable",
}

// classes is every class in the order a person would want to read them:
// publishable first, strongest grant first, then the two that are withheld, then
// the failure to determine.
//
// It exists so that adding a class is one edit rather than five. The posture
// table, the totals sheet and three tests all need to walk every class, and each
// of them used to carry its own copy of the list, which meant a new class was
// added to the type and silently missing from the places that decide what
// happens to it.
var classes = []LicenseClass{
	LicenseOpen, LicensePermissiveAttribution, LicenseCrawled,
	LicenseRestricted, LicenseUnredistributable, LicenseUnknown,
}

// LicenseClasses returns every class, publishable first.
func LicenseClasses() []LicenseClass {
	out := make([]LicenseClass, len(classes))
	copy(out, classes)
	return out
}

// Publishable reports whether documents of this class may appear in a published
// artifact. Three classes qualify, and the unknown case is one of the three that
// does not.
func (c LicenseClass) Publishable() bool {
	return c == LicenseOpen || c == LicensePermissiveAttribution || c == LicenseCrawled
}

// RequiresAttribution reports whether a published artifact containing this
// document must carry its attribution.
//
// A crawled page requires it for a different reason than a licensed one. There
// is no attribution clause to satisfy, because there is no license; what there
// is, is the position that a page published as fetched has to say where it was
// fetched from, or nobody can check the claim and nobody can ask for it to be
// removed. Either way the obligation lands on the same column: url is required
// on every row by the ingest contract, so this is a check gao cannot fail
// silently.
func (c LicenseClass) RequiresAttribution() bool {
	return c == LicensePermissiveAttribution || c == LicenseCrawled
}

// Valid reports whether the class is one of the defined constants.
func (c LicenseClass) Valid() bool {
	_, ok := licenseNames[c]
	return ok
}

// String implements [fmt.Stringer].
func (c LicenseClass) String() string {
	if n, ok := licenseNames[c]; ok {
		return n
	}
	return fmt.Sprintf("LicenseClass(%d)", uint8(c))
}

// MarshalText implements [encoding.TextMarshaler]. The record stores the name
// rather than the ordinal so that inserting a class later does not silently
// reinterpret every existing row.
func (c LicenseClass) MarshalText() ([]byte, error) {
	if !c.Valid() {
		return nil, fmt.Errorf("doc: %s is not a license class", c)
	}
	return []byte(c.String()), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler].
func (c *LicenseClass) UnmarshalText(text []byte) error {
	s := string(text)
	for class, name := range licenseNames {
		if name == s {
			*c = class
			return nil
		}
	}
	return fmt.Errorf("doc: %q is not a license class", s)
}

// ParseLicenseClass returns the class with the given name.
func ParseLicenseClass(s string) (LicenseClass, error) {
	var c LicenseClass
	err := c.UnmarshalText([]byte(s))
	return c, err
}
