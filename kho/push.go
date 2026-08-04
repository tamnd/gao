package kho

// Getting a finished part off the box that wrote it.
//
// This is the other half of the offload claim. [Roll] bounds what a worker holds
// by closing a part every so often; this is what makes closing a part mean
// something, by moving it to the Hub so the local copy can go. Without it a run
// still fills the disk, only in tidier pieces.
//
// The Hub stores files this size through Git LFS, and the LFS protocol is what
// makes a resumed run cheap. An upload is three steps: ask what mode the path
// takes, ask the LFS endpoint whether it already has an object with this digest,
// and commit the path against that digest. The middle step is where a re-run
// stops. The Hub keys objects by their sha256, so a part that was uploaded
// before a run died is already there under the same digest, the batch endpoint
// says so by returning no upload action, and the transfer is skipped while the
// commit still happens. Nothing has to be remembered locally for that to work,
// which is the point: a ledger of what has been pushed is a second source of
// truth and it is wrong the moment a push succeeds and the process dies before
// the write.
//
// There is a cheaper check in front of it. If the path is already committed and
// already points at these bytes, there is nothing to do at all, and one HEAD
// request answers that. The LFS check stays behind it because the two failures
// are different: the HEAD covers a run that finished the push, and the batch
// covers a run that finished the transfer and not the commit.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/tamnd/gao/may"
)

// HubAPI is the Hugging Face endpoint. It is a variable only so that a test can
// point at a server it controls, and no caller should be setting it.
const HubAPI = "https://huggingface.co"

// MainBranch is the revision a push commits to when none is named.
const MainBranch = "main"

// MaxUpload is the largest file this pushes in one request. The storage backend
// takes 5 GB in a single PUT and more than that only in multiple parts, and
// nothing here should ever come close: a part rolls over at [TextPerPart] of
// text, which is under 2 GB of Parquet at any compression ratio worth having.
// A file over this is a bug in whatever wrote it, so it is refused with that
// said rather than handed to a backend that will refuse it less clearly.
const MaxUpload = 5 << 30

// InlineMax is the largest file this commits inline, as base64 in the commit
// body, for a path the repo does not track with LFS. Parquet is tracked by
// every dataset repo's default attributes, so reaching this at all means the
// repo's .gitattributes is not what it should be, and the limit is low because
// the failure should be loud rather than a slow upload of a gigabyte through a
// JSON field.
const InlineMax = 10 << 20

// ErrUnauthorized is returned when the Hub refuses the token.
var ErrUnauthorized = errors.New("kho: the hub refused the token")

// Pusher uploads files into one dataset repo.
type Pusher struct {
	// Repo is the full repo id, org and name.
	Repo string

	// Token is a Hugging Face access token with write access to Repo.
	Token string

	// Client is the HTTP client. A nil client is [http.DefaultClient].
	Client *http.Client

	// API overrides [HubAPI]. It is for tests.
	API string

	// Branch is the revision to commit on. Empty is [MainBranch].
	Branch string

	// Message is the commit summary. Empty is a generated one naming the file.
	Message string
}

// Pushed is what one push did, which is worth reporting because most of the
// time on a resumed run it did nothing and a run that says "pushed" either way
// is a run nobody can tell has resumed.
type Pushed struct {
	// Path is the path inside the repo.
	Path string

	// Bytes is the size of the file.
	Bytes int64

	// OID is the sha256 the Hub stores the object under.
	OID string

	// Committed reports whether this push wrote a commit. It is false when the
	// path was already pointing at these bytes.
	Committed bool

	// Transferred reports whether the bytes went over the wire. It is false
	// when the Hub already held an object with this digest, which is what makes
	// resuming an interrupted push cheap.
	Transferred bool
}

// Skipped reports whether the push had nothing to do, which is the state a
// second run over the same parts should be in.
func (p Pushed) Skipped() bool { return !p.Committed && !p.Transferred }

func (p *Pusher) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

func (p *Pusher) api() string {
	if p.API != "" {
		return strings.TrimSuffix(p.API, "/")
	}
	return HubAPI
}

func (p *Pusher) branch() string {
	if p.Branch != "" {
		return p.Branch
	}
	return MainBranch
}

// Push uploads the file at local to path inside the repo and reports what it
// had to do.
//
// The path is the path inside the repo, which is the same path the file sits at
// under the directory it was written to, because [Roll] writes the repo layout
// rather than a local one that has to be translated on the way out.
func (p *Pusher) Push(ctx context.Context, local, path string) (Pushed, error) {
	oid, size, sample, err := digest(local)
	if err != nil {
		return Pushed{}, err
	}
	if size > MaxUpload {
		return Pushed{}, fmt.Errorf("kho: %s is %d bytes and nothing here should be over %d, so this is a bug in whatever wrote it rather than a large file", path, size, int64(MaxUpload))
	}
	done := Pushed{Path: path, Bytes: size, OID: oid}

	present, err := p.present(ctx, path, oid)
	if err != nil {
		return done, err
	}
	if present {
		return done, nil
	}

	mode, err := p.preupload(ctx, path, size, sample)
	if err != nil {
		return done, err
	}
	if mode != "lfs" {
		if size > InlineMax {
			return done, fmt.Errorf("kho: %s wants to be committed inline at %d bytes, which means %s does not track it with lfs and its .gitattributes needs fixing before anything is pushed", path, size, p.Repo)
		}
		body, rErr := os.ReadFile(local) //nolint:gosec // the path is one this process wrote
		if rErr != nil {
			return done, rErr
		}
		if err := p.commit(ctx, commitFile(path, body)); err != nil {
			return done, err
		}
		done.Committed, done.Transferred = true, true
		return done, nil
	}

	action, err := p.batch(ctx, oid, size)
	if err != nil {
		return done, err
	}
	if action != nil {
		if err := p.transfer(ctx, action, local, size); err != nil {
			return done, err
		}
		done.Transferred = true
	}
	if err := p.commit(ctx, commitLFS(path, oid, size)); err != nil {
		return done, err
	}
	done.Committed = true
	return done, nil
}

// digest returns the sha256 the Hub keys objects by, the size, and the first
// bytes of the file, which the preupload check wants so it can decide the mode
// from content rather than from the extension alone.
func digest(local string) (oid string, size int64, sample []byte, err error) {
	f, err := os.Open(local) //nolint:gosec // the path is one this process wrote
	if err != nil {
		return "", 0, nil, err
	}
	defer f.Close() //nolint:errcheck // read only

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", 0, nil, err
	}
	sample = make([]byte, 512)
	m, err := io.ReadFull(f, sample)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", 0, nil, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, sample[:m], nil
}

// present reports whether the path is already committed and already points at
// these bytes.
//
// The Hub answers a resolve request for an LFS file with the object's digest in
// X-Linked-Etag, so this is one HEAD rather than a download. A path that holds
// something else comes back false and gets overwritten, which is what should
// happen: the path is a function of what is in it, so a difference means the
// file was rewritten and the new one wins.
func (p *Pusher) present(ctx context.Context, path, oid string) (bool, error) {
	url := fmt.Sprintf("%s/datasets/%s/resolve/%s/%s", p.api(), p.Repo, p.branch(), path)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false, err
	}
	p.authorize(req)
	resp, err := p.client().Do(req)
	if err != nil {
		return false, fmt.Errorf("kho: asking %s whether it has %s: %w", p.Repo, path, err)
	}
	defer closeBody(resp)

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return false, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, p.refused(resp)
	default:
		return false, fmt.Errorf("kho: asking %s whether it has %s: %s", p.Repo, path, resp.Status)
	}

	tag := resp.Header.Get("X-Linked-Etag")
	if tag == "" {
		tag = resp.Header.Get("Etag")
	}
	return strings.Trim(tag, `"`) == oid, nil
}

type preuploadFile struct {
	Path         string `json:"path"`
	Sample       string `json:"sample"`
	Size         int64  `json:"size"`
	UploadMode   string `json:"uploadMode"`
	ShouldIgnore bool   `json:"shouldIgnore"`
}

// preupload asks the Hub how the path is to be uploaded. The answer is lfs for
// anything the repo's attributes track, which for a dataset repo is every
// Parquet file, and regular for the rest.
func (p *Pusher) preupload(ctx context.Context, path string, size int64, sample []byte) (string, error) {
	in := struct {
		Files []preuploadFile `json:"files"`
	}{Files: []preuploadFile{{
		Path:   path,
		Sample: base64.StdEncoding.EncodeToString(sample),
		Size:   size,
	}}}
	var out struct {
		Files []preuploadFile `json:"files"`
	}
	url := fmt.Sprintf("%s/api/datasets/%s/preupload/%s", p.api(), p.Repo, p.branch())
	if err := p.post(ctx, url, "application/json", in, &out); err != nil {
		return "", fmt.Errorf("kho: asking %s how to upload %s: %w", p.Repo, path, err)
	}
	if len(out.Files) != 1 {
		return "", fmt.Errorf("kho: asking %s how to upload %s: the hub answered about %d files", p.Repo, path, len(out.Files))
	}
	if out.Files[0].ShouldIgnore {
		return "", fmt.Errorf("kho: %s says %s is ignored, which means its .gitignore excludes what this run is producing", p.Repo, path)
	}
	return out.Files[0].UploadMode, nil
}

// lfsAction is where to PUT the bytes and what headers to send with them.
type lfsAction struct {
	Href   string            `json:"href"`
	Header map[string]string `json:"header"`
}

// batch asks the LFS endpoint whether it needs the bytes. A nil action means it
// already has an object with this digest, which is the whole of resume: an
// interrupted run repeats the ask and skips the transfer.
func (p *Pusher) batch(ctx context.Context, oid string, size int64) (*lfsAction, error) {
	in := map[string]any{
		"operation": "upload",
		"transfers": []string{"basic"},
		"hash_algo": "sha_256",
		"objects":   []map[string]any{{"oid": oid, "size": size}},
	}
	var out struct {
		Objects []struct {
			OID     string `json:"oid"`
			Actions struct {
				Upload *lfsAction `json:"upload"`
			} `json:"actions"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"objects"`
	}
	url := fmt.Sprintf("%s/datasets/%s.git/info/lfs/objects/batch", p.api(), p.Repo)
	if err := p.post(ctx, url, "application/vnd.git-lfs+json", in, &out); err != nil {
		return nil, fmt.Errorf("kho: asking %s whether it holds %s already: %w", p.Repo, oid, err)
	}
	if len(out.Objects) != 1 {
		return nil, fmt.Errorf("kho: asking %s whether it holds %s already: the hub answered about %d objects", p.Repo, oid, len(out.Objects))
	}
	o := out.Objects[0]
	if o.Error != nil {
		return nil, fmt.Errorf("kho: %s refused the object %s: %s", p.Repo, oid, o.Error.Message)
	}
	return o.Actions.Upload, nil
}

// transfer sends the bytes to wherever the batch response pointed, which is
// storage rather than the Hub and takes the headers it was given rather than
// this repo's token.
func (p *Pusher) transfer(ctx context.Context, a *lfsAction, local string, size int64) error {
	f, err := os.Open(local) //nolint:gosec // the path is one this process wrote
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // read only

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, a.Href, f)
	if err != nil {
		return err
	}
	req.ContentLength = size
	for k, v := range a.Header {
		req.Header.Set(k, v)
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return fmt.Errorf("kho: uploading %s: %w", local, err)
	}
	defer closeBody(resp)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("kho: uploading %s: %s: %s", local, resp.Status, snippet(resp.Body))
	}
	return nil
}

// commitOp is one line of the newline delimited body a commit is made of.
type commitOp struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

func commitLFS(path, oid string, size int64) commitOp {
	return commitOp{Key: "lfsFile", Value: map[string]any{
		"path": path, "algo": "sha256", "oid": oid, "size": size,
	}}
}

func commitFile(path string, body []byte) commitOp {
	return commitOp{Key: "file", Value: map[string]any{
		"path": path, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(body),
	}}
}

// commit points the path at what was uploaded.
func (p *Pusher) commit(ctx context.Context, op commitOp) error {
	summary := p.Message
	if summary == "" {
		summary = "Add " + opPath(op)
	}
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	header := commitOp{Key: "header", Value: map[string]any{"summary": summary}}
	for _, line := range []commitOp{header, op} {
		if err := enc.Encode(line); err != nil {
			return err
		}
	}
	url := fmt.Sprintf("%s/api/datasets/%s/commit/%s", p.api(), p.Repo, p.branch())
	if err := p.send(ctx, url, "application/x-ndjson", &body, nil); err != nil {
		return fmt.Errorf("kho: committing %s to %s: %w", opPath(op), p.Repo, err)
	}
	return nil
}

func opPath(op commitOp) string {
	if m, ok := op.Value.(map[string]any); ok {
		if s, ok := m["path"].(string); ok {
			return s
		}
	}
	return "the file"
}

// EnsureRepo creates the repo if it is not there and does nothing if it is.
//
// The tier decides visibility rather than an argument, because a working repo
// that got created public would be publishing unfiltered text and the decision
// is not one a caller should be able to make differently on a Tuesday.
func (p *Pusher) EnsureRepo(ctx context.Context, d Dataset) error {
	org, name, ok := strings.Cut(d.Repo(), "/")
	if !ok {
		return fmt.Errorf("kho: %s is not an org and a name", d.Repo())
	}
	in := map[string]any{
		"type":         "dataset",
		"name":         name,
		"organization": org,
		"private":      !d.Public(),
	}
	url := p.api() + "/api/repos/create"
	err := p.post(ctx, url, "application/json", in, nil)
	if err != nil && strings.Contains(err.Error(), "409") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("kho: creating %s: %w", d.Repo(), err)
	}
	return nil
}

// post marshals in, sends it, and decodes the response into out if out is not
// nil.
func (p *Pusher) post(ctx context.Context, url, contentType string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return p.send(ctx, url, contentType, bytes.NewReader(body), out)
}

func (p *Pusher) send(ctx context.Context, url, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	if contentType == "application/vnd.git-lfs+json" {
		req.Header.Set("Accept", contentType)
	}
	p.authorize(req)

	resp, err := p.client().Do(req)
	if err != nil {
		return err
	}
	defer closeBody(resp)

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return p.refused(resp)
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("%s: %s", resp.Status, snippet(resp.Body))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (p *Pusher) authorize(req *http.Request) {
	if p.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
}

// refused turns a 401 or a 403 into the sentence somebody needs, which is not
// "forbidden" but which token and which scope.
func (p *Pusher) refused(resp *http.Response) error {
	if p.Token == "" {
		return fmt.Errorf("%w: %s for %s and no token was set, so set %s to one with write access to %s",
			ErrUnauthorized, resp.Status, p.Repo, may.TokenEnv, Org)
	}
	return fmt.Errorf("%w: %s for %s, so the token needs write access to %s and read access to its contents",
		ErrUnauthorized, resp.Status, p.Repo, Org)
}

func closeBody(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
}

// snippet returns the beginning of an error body, which is where the Hub puts
// the reason and which is worth having in the error rather than in a debugger.
func snippet(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, 512))
	if err != nil || len(b) == 0 {
		return "no reason given"
	}
	return strings.TrimSpace(string(b))
}
