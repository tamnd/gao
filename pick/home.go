package pick

// Where a benchmark lives and what pinning it means.
//
// A roster entry that names a revision is making a claim a reader has to be able
// to check, and "ViMMRC 2.0" is not that claim. It is a name the authors chose,
// it is the same name after the files behind it change, and a release note
// carrying it says the corpus was checked against whatever was at that name on
// the day. So a pin here is an object id and an address to ask for it, and both
// halves have to be present or the entry is not pinned.
//
// The address is machine readable because the step that fetches the items has to
// read it, and because a reason written in prose is a reason nobody can act on.
// Two of the three schemes are somebody else's copy: the Hugging Face Hub and a
// git repository. The third is this repository, for the benchmarks gao builds
// itself, and it is here because those were sitting unpinned on the roster for a
// reason that did not survive being written down. The set is fixed and hashed in
// the source before it is built, the digest can be printed by anybody with the
// repository, and waiting to pin it until somebody has uploaded it to the Hub
// would be pinning the upload rather than the set.

import (
	"fmt"
	"strings"
)

// Home is the authoritative copy of a benchmark, written as `hf:owner/name`,
// `git:https://host/owner/name`, or `gao:<command>` for the sets this repository
// fixes itself.
//
// Authoritative means published by the people who made it. Most of these
// benchmarks also exist as third party copies on the Hub, and pinning one of
// those pins the copy: it says the corpus was checked against what somebody
// uploaded, which is a weaker statement than it looks and is not the statement a
// release note is making.
type Home struct {
	// Scheme is HuggingFace, Git or Built.
	Scheme string

	// Path is the repository, `owner/name` for the Hub, a URL for git, and the
	// command that prints the digest for a set built here.
	Path string
}

// The schemes a home can have.
const (
	HuggingFace = "hf"
	Git         = "git"

	// Built is a benchmark this repository fixes, addressed by the command that
	// prints its digest.
	Built = "gao"
)

// ParseHome reads an address off a roster entry.
func ParseHome(s string) (Home, error) {
	scheme, path, ok := strings.Cut(s, ":")
	if !ok {
		return Home{}, fmt.Errorf("nhat: %q is not an address, and an address is hf:owner/name or git:https://host/owner/name", s)
	}
	h := Home{Scheme: scheme, Path: path}
	switch scheme {
	case HuggingFace:
		owner, name, ok := strings.Cut(path, "/")
		if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
			return Home{}, fmt.Errorf("nhat: %q is not a Hub repository, and a Hub repository is owner/name", s)
		}
	case Git:
		if !strings.HasPrefix(path, "https://") {
			return Home{}, fmt.Errorf("nhat: %q is not a git repository, and a git repository is an https URL", s)
		}
	case Built:
		if !command(path) {
			return Home{}, fmt.Errorf("nhat: %q is not a command, and a set built here is addressed by the command that prints its digest", s)
		}
	default:
		return Home{}, fmt.Errorf("nhat: %q has scheme %q, and an address is %s or %s", s, scheme, HuggingFace, Git)
	}
	return h, nil
}

// String is the address as it is written on the roster.
func (h Home) String() string { return h.Scheme + ":" + h.Path }

// Ask is the address the current revision can be read from, which is what the
// step that builds a list uses to find out whether a pin has fallen behind.
//
// It is not fetched here and nothing in this package fetches it. A test that
// reaches the network fails on a train, and a check that only runs where there
// is a network is a check that stops running.
func (h Home) Ask() string {
	switch h.Scheme {
	case HuggingFace:
		return "https://huggingface.co/api/datasets/" + h.Path
	case Git:
		return strings.TrimSuffix(h.Path, ".git") + "/commits"
	case Built:
		return "gao " + h.Path
	}
	return ""
}

// command reports whether s is an invocation of the gao binary, which is what a
// Built address has to be for the reader to be able to run it.
//
// It is not checked against the command table, because this package would then
// depend on every package that builds a benchmark in order to answer a question
// about a string. What it rules out is the thing that would actually get written
// here by mistake, which is a sentence.
func command(s string) bool {
	fields := strings.Fields(s)
	if len(fields) == 0 || len(fields) > 3 || s != strings.Join(fields, " ") {
		return false
	}
	for _, f := range fields {
		for _, c := range f {
			if c < 'a' || c > 'z' {
				return false
			}
		}
	}
	return true
}

// The lengths an object id comes in. Forty hex characters is a Hub or git
// revision, and sixty four is the digest of a set fixed in this repository.
const (
	revisionLen = 40
	digestLen   = 64
)

// pinned reports whether s is an object id rather than a name.
//
// A tag moves and a version number is a name, and the failure this rule prevents
// is a release note that says a benchmark was checked at 2.0 when 2.0 has since
// been reuploaded. Hex of a fixed length is the one form that cannot do that,
// whether it came from the Hub or from hashing a set here.
func pinned(s string) bool {
	if len(s) != revisionLen && len(s) != digestLen {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// Pinned reports whether this entry names a revision that can be asked for.
func (e Entry) Pinned() bool { return e.Home != "" && pinned(e.Version) }

// checkPin is the rule an entry has to satisfy either way.
//
// An entry is pinned, in which case it has an address and an object id and
// nothing to explain, or it is not, in which case it says why in words a person
// can act on. What is not allowed is the third state, an entry with no revision
// and no account of itself, because that is the one that reaches a release as a
// surprise.
func (e Entry) checkPin() error {
	switch {
	case e.Pinned():
		if e.Pending != "" {
			return fmt.Errorf("nhat: %s is pinned at %s and still says it is waiting on %q", e.Name, e.Version, e.Pending)
		}
		h, err := ParseHome(e.Home)
		if err != nil {
			return fmt.Errorf("nhat: %s: %w", e.Name, err)
		}
		if err := h.fits(e.Name, e.Version); err != nil {
			return err
		}
	case e.Home != "" && e.Version != Unpinned && e.Version != "":
		return fmt.Errorf("nhat: %s is at revision %q, and a revision is the %d character object id its home answers with rather than a name that can be moved", e.Name, e.Version, revisionLen)
	default:
		if e.Version != Unpinned && e.Version != "" {
			return fmt.Errorf("nhat: %s is at revision %q with no home to ask for it, so nobody can check what it was", e.Name, e.Version)
		}
		if e.Pending == "" {
			return fmt.Errorf("nhat: %s has no revision and no reason, and an unpinned benchmark has to say what it is waiting for", e.Name)
		}
		if e.Home != "" {
			if _, err := ParseHome(e.Home); err != nil {
				return fmt.Errorf("nhat: %s: %w", e.Name, err)
			}
		}
	}
	return nil
}

// Blocking is every benchmark that cannot go into a release note yet, with the
// reason it gives, sorted by name.
//
// This is Unpinned with the reasons attached, and it is the form the question is
// actually asked in. A list of twelve names is a list of twelve things to look
// up. A list of twelve names and reasons is a list of the four different problems
// they turn out to be.
func (ros Roster) Blocking() []string {
	out := make([]string, 0, len(ros.Benchmarks))
	for _, name := range ros.Unpinned() {
		for _, e := range ros.Benchmarks {
			if e.Name == name {
				out = append(out, e.Name+": "+e.Pending)
				break
			}
		}
	}
	return out
}

// fits reports whether a revision is the kind of object id this home answers
// with.
//
// The two lengths are not interchangeable and the mistake they catch is a real
// one: a Hub repository cannot answer for a digest we computed, and a set built
// here has no forty character revision to give. Either way the entry would read
// as pinned and nobody could check it.
func (h Home) fits(name, version string) error {
	switch {
	case h.Scheme == Built && len(version) != digestLen:
		return fmt.Errorf("nhat: %s is built here and pinned at a %d character revision, and a set built here is pinned at the %d character digest its command prints", name, len(version), digestLen)
	case h.Scheme != Built && len(version) != revisionLen:
		return fmt.Errorf("nhat: %s lives at %s and is pinned at a %d character revision, and %s answers with a %d character object id", name, h, len(version), h.Scheme, revisionLen)
	}
	return nil
}
