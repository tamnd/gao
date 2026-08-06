package nhat

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
// There are exactly two schemes, which covers every benchmark on the roster that
// has an authoritative copy at all: the Hugging Face Hub and a git repository.

import (
	"fmt"
	"strings"
)

// Home is the authoritative copy of a benchmark, written as `hf:owner/name` or
// `git:https://host/owner/name`.
//
// Authoritative means published by the people who made it. Most of these
// benchmarks also exist as third party copies on the Hub, and pinning one of
// those pins the copy: it says the corpus was checked against what somebody
// uploaded, which is a weaker statement than it looks and is not the statement a
// release note is making.
type Home struct {
	// Scheme is HuggingFace or Git.
	Scheme string

	// Path is the repository, `owner/name` for the Hub and a URL for git.
	Path string
}

// The schemes a home can have.
const (
	HuggingFace = "hf"
	Git         = "git"
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
	}
	return ""
}

// revisionLen is the length of an object id, on the Hub and in git alike.
const revisionLen = 40

// pinned reports whether s is an object id rather than a name.
//
// A tag moves and a version number is a name, and the failure this rule prevents
// is a release note that says a benchmark was checked at 2.0 when 2.0 has since
// been reuploaded. Forty hex characters is the one form that cannot do that.
func pinned(s string) bool {
	if len(s) != revisionLen {
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
		if _, err := ParseHome(e.Home); err != nil {
			return fmt.Errorf("nhat: %s: %w", e.Name, err)
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
