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
	LicenseRestricted:            "restricted",
	LicenseUnredistributable:     "unredistributable",
}

// Publishable reports whether documents of this class may appear in a published
// artifact. Only two classes qualify, and the unknown case is one of the three
// that does not.
func (c LicenseClass) Publishable() bool {
	return c == LicenseOpen || c == LicensePermissiveAttribution
}

// RequiresAttribution reports whether a published artifact containing this
// document must carry its attribution.
func (c LicenseClass) RequiresAttribution() bool {
	return c == LicensePermissiveAttribution
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
