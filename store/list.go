package store

// Finding out what is already in a repo.
//
// A push knows the one path it is writing and asks about that path. A pass that
// wants to read the store back does not know the paths at all: a source was
// ingested by whichever box had room, over however many runs it took, and what
// came out is however many parts that was. The repo itself is the record of
// that, so the way to find out is to ask it.
//
// The Hub answers with a page at a time and a Link header pointing at the next
// one, which matters at the size this reaches. A source of 234 GB is around a
// hundred and fifty parts, and a listing that took the first page and stopped
// would quietly measure a third of a corpus and report it as the whole.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// listPage is how many entries a listing asks for at a time, which is the most
// the Hub will answer with.
const listPage = 1000

// Stored is one file in a repo, as the Hub describes it.
type Stored struct {
	// Path is the path inside the repo.
	Path string

	// Bytes is the length of the file itself, which for a file kept in LFS is
	// the length of the object and not of the pointer committed in its place.
	Bytes int64

	// OID names the bytes: the sha256 of the object for a file the Hub keeps in
	// LFS, and the git blob id for the rest. Which one it is is [Stored.LFS],
	// and the distinction is the same one a push has to make.
	OID string
	LFS bool
}

// Parquet reports whether this file is a part rather than something else in the
// repo, which is how a listing of a whole repo becomes a list of data.
func (s Stored) Parquet() bool { return strings.HasSuffix(s.Path, ParquetExt) }

// List returns every file in the repo under prefix, following the Hub's paging
// to the end.
//
// An empty prefix lists the whole repo. The prefix is a path prefix rather than
// a pattern, which is enough for what asks: the layout partitions by snapshot,
// so one snapshot is one directory and asking for it is asking for a directory.
func (p *Pusher) List(ctx context.Context, prefix string) ([]Stored, error) {
	url := fmt.Sprintf("%s/api/datasets/%s/tree/%s/%s?recursive=true&limit=%d",
		p.api(), p.Repo, p.branch(), strings.Trim(prefix, "/"), listPage)

	var all []Stored
	for url != "" {
		page, next, err := p.listPage(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("store: listing %s in %s: %w", prefix, p.Repo, err)
		}
		all = append(all, page...)
		url = next
	}
	return all, nil
}

// treeEntry is one line of the tree API's answer. A directory has no lfs block
// and is dropped, because what asks for a listing wants files.
type treeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	OID  string `json:"oid"`
	Size int64  `json:"size"`
	LFS  *struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
	} `json:"lfs"`
}

// listPage reads one page of the listing and returns where the next one is, or
// an empty string when this was the last.
func (p *Pusher) listPage(ctx context.Context, url string) ([]Stored, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	p.authorize(req)

	resp, err := p.client().Do(req)
	if err != nil {
		return nil, "", err
	}
	defer closeBody(resp)

	switch {
	case resp.StatusCode == http.StatusNotFound:
		// A repo that has nothing under the prefix answers 404 rather than an
		// empty list, and a snapshot nobody has written to yet is a fact about
		// the run rather than an error in the listing.
		return nil, "", nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, "", p.refused(resp)
	case resp.StatusCode/100 != 2:
		return nil, "", fmt.Errorf("%s: %s", resp.Status, snippet(resp.Body))
	}

	var entries []treeEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, "", err
	}

	page := make([]Stored, 0, len(entries))
	for _, e := range entries {
		if e.Type != "file" {
			continue
		}
		s := Stored{Path: e.Path, Bytes: e.Size, OID: e.OID}
		if e.LFS != nil {
			s.Bytes, s.OID, s.LFS = e.LFS.Size, e.LFS.OID, true
		}
		page = append(page, s)
	}
	return page, nextLink(resp.Header.Get("Link")), nil
}

// nextLink returns the url the Link header points at as the next page, or an
// empty string when it does not point at one.
//
// The header is a comma separated list of `<url>; rel="name"`, and the only
// relation that matters here is next. Parsing it by hand rather than reaching
// for a library is worth it for a header this shape, and getting it wrong fails
// loudly: a missed next link truncates a listing, which the caller notices as a
// corpus that is smaller than it should be.
func nextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		url, rel, ok := strings.Cut(strings.TrimSpace(part), ";")
		if !ok {
			continue
		}
		if !strings.Contains(rel, `rel="next"`) {
			continue
		}
		url = strings.TrimSpace(url)
		if !strings.HasPrefix(url, "<") || !strings.HasSuffix(url, ">") {
			continue
		}
		return url[1 : len(url)-1]
	}
	return ""
}

// ResolveURL is where the bytes of a path in this repo are served from.
//
// It is a redirect for anything the Hub keeps in LFS, which every part is, and
// following it is the reader's business rather than this function's.
func (p *Pusher) ResolveURL(path string) string {
	return fmt.Sprintf("%s/datasets/%s/resolve/%s/%s", p.api(), p.Repo, p.branch(), path)
}
