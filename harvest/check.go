package harvest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tamnd/gao/doc"
)

// Drift detection.
//
// The manifest is pinned and the hosts are not. A dataset gets re-uploaded, a
// file gets replaced, a language partition gets rebuilt, and none of that
// reaches gao unless somebody looks. Looking is what this file does, and it is
// the only code in the package that touches the network before an ingest starts.
//
// It reports and it never rewrites. The manifest is edited by a person, in a
// commit, with the new numbers read at the same time, because a manifest that
// re-pins itself is a manifest that silently changes what a released corpus was
// built from. That is the exact failure this package exists to prevent, and it
// would be a strange thing to automate.

// MaxRevisionBytes caps what [Current] will read from a host. A dataset card
// with every language listed runs to a few hundred kilobytes, so this is
// generous, and it is here because the other end of the connection is not ours.
const MaxRevisionBytes = 8 << 20

// Drift is what a source was pinned at and what its host serves now.
type Drift struct {
	Source  doc.Source
	Pinned  string
	Current string
}

// Moved reports whether the host has changed since the manifest was written.
func (d Drift) Moved() bool { return d.Pinned != d.Current }

// String implements [fmt.Stringer].
func (d Drift) String() string {
	if !d.Moved() {
		return fmt.Sprintf("%s is at %s, unchanged", d.Source, short(d.Pinned))
	}
	return fmt.Sprintf("%s was pinned at %s and now serves %s", d.Source, short(d.Pinned), short(d.Current))
}

// short trims a revision for reading, keeping the algorithm prefix on a digest
// so that a sha256 does not print as if it were a commit.
func short(rev string) string {
	const n = 12
	if algo, hash, ok := strings.Cut(rev, ":"); ok {
		if len(hash) > n {
			return algo + ":" + hash[:n]
		}
		return rev
	}
	if len(rev) > n {
		return rev[:n]
	}
	return rev
}

// Check reports whether a pinned source has moved.
//
// A drift is not an error. It is a fact about the world that somebody has to
// decide about, and the decision is usually to leave the pin alone, because a
// corpus is built from what was there rather than from what is there now.
func Check(ctx context.Context, c *http.Client, p Pinned) (Drift, error) {
	now, err := Current(ctx, c, p)
	if err != nil {
		return Drift{}, err
	}
	return Drift{Source: p.Source, Pinned: p.Revision, Current: now}, nil
}

// Current reads the revision a host serves now, in the same form the manifest
// pins it: a commit SHA for a Hub source, and the digest of the file that fixes
// the file list for a direct one.
func Current(ctx context.Context, c *http.Client, p Pinned) (string, error) {
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.RevisionURL, nil)
	if err != nil {
		return "", fmt.Errorf("harvest: %s: %w", p.Source, err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("harvest: %s: %w", p.Source, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("harvest: %s: %s answered %s", p.Source, p.RevisionURL, resp.Status)
	}
	body := io.LimitReader(resp.Body, MaxRevisionBytes)

	if p.Origin == Direct {
		h := sha256.New()
		if _, err := io.Copy(h, body); err != nil {
			return "", fmt.Errorf("harvest: %s: reading %s: %w", p.Source, p.RevisionURL, err)
		}
		return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
	}

	var info struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(body).Decode(&info); err != nil {
		return "", fmt.Errorf("harvest: %s: reading %s: %w", p.Source, p.RevisionURL, err)
	}
	if !commitSHA.MatchString(info.SHA) {
		return "", fmt.Errorf("harvest: %s: %s reports its revision as %q, which is not a commit SHA", p.Source, p.RevisionURL, info.SHA)
	}
	return info.SHA, nil
}
