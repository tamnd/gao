package seed

// Seeding a crawl with pages rather than with front doors.
//
// Every seed list this crawler has been given so far has been a list of hosts,
// and a host means its home page. A home page is the worst page on a site to
// start from and the crawl's own numbers say so: it is navigation, a masthead,
// a carousel and a footer, so it usually fails the sift, and the links on it go
// to section fronts which are more of the same. About one page in four survives
// a crawl, and a run seeded with home pages spends its first minutes proving
// that home pages are not writing.
//
// There is a better list and we already published it. `open-index/vitco-clean`
// carries 16,041,798 distinct addresses across 970,687 hosts, and every one of
// them is the address of a document that was Vietnamese enough to pass this
// pipeline's own filter. Those are articles, forum threads and papers rather
// than front doors, and the links on an article go to sibling articles, which is
// the shape a frontier wants.
//
// This does not fetch any of them. It reads one column out of a published
// dataset and prints addresses, which the crawl then takes the way it takes any
// other seed file. What it saves is the reason to guess: the addresses come from
// documents that were kept, so the question of whether a seed is worth fetching
// has already been answered once by a pipeline rather than by a hostname.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/parquet-go/parquet-go"

	"github.com/tamnd/gao/count"
	"github.com/tamnd/gao/frontier"
	"github.com/tamnd/gao/store"
)

// PagesRepo is the published dataset the addresses come out of by default.
const PagesRepo = "open-index/vitco-clean"

// PagesColumn is the column holding them.
const PagesColumn = "url"

// DefaultPerHost is how many addresses one host contributes unless told
// otherwise.
//
// Eight rather than everything. The frontier hands out at most two URLs per host
// per batch, so a host with two hundred addresses in the seed does not get
// crawled two hundred times faster, it gets its extra hundred and ninety eight
// deferred over and over while the batch fills from somewhere else. What breadth
// is worth having is across hosts. Eight is enough that a site is entered at
// several points rather than one, which is what makes the difference against a
// home page, and few enough that a source with twenty addresses per host on
// average still spreads over the whole inventory.
const DefaultPerHost = 8

// Pages reads document addresses out of a published dataset.
type Pages struct {
	// Repo is the dataset. Empty means [PagesRepo].
	Repo string

	// Token is a Hugging Face token. The published datasets are public, so it is
	// only needed for a repo that is not.
	Token string

	// API overrides the Hub address. It is for tests.
	API string

	// Prefix limits the read to part of the repo, which is how one source is
	// taken out of a dataset holding several. Empty reads all of it.
	Prefix string

	// PerHost caps how many addresses one host contributes. Zero means
	// [DefaultPerHost] and a negative number means no cap.
	PerHost int

	// Shard and Fleet take only the hosts this box owns, under the same rule the
	// crawler splits on. A Fleet of zero or one takes everything.
	Shard, Fleet int

	// Any keeps addresses this crawler cannot read, which is what a caller
	// wanting the raw inventory rather than a seed list asks for.
	Any bool

	// Limit stops after this many addresses. Zero reads the whole dataset.
	Limit int
}

// PagesReport is what a read did.
type PagesReport struct {
	// Parts is how many files were read and Rows is how many addresses were in
	// them.
	Parts int
	Rows  int

	// Kept is how many were printed and Hosts is how many distinct hosts they
	// came from.
	Kept  int
	Hosts int

	// Foreign is addresses on hosts another box owns, Capped is addresses past
	// a host's share, Binary is addresses naming something this crawler cannot
	// read, and Bad is addresses that would not parse. A dataset this large has
	// a few of the last, and a run that stopped on one would be a run that
	// cannot read the corpus it published.
	Foreign int
	Capped  int
	Binary  int
	Bad     int
}

// Read calls yield with every address the filters admit.
//
// The parts are taken one source at a time in rotation rather than in path
// order, and that is not tidiness. Path order puts every part of finepdfs before
// the first part of fineweb2, so a run stopped by -max comes back holding
// nothing but PDFs off one upstream. Rotating means any prefix of the output is
// a mix of the sources, which is what a truncated read has to be to be worth
// anything.
//
// Nothing is shuffled beyond that. Shuffling 16 million addresses would mean
// holding 16 million addresses, and reading a column rather than a dataset is
// exactly the decision not to.
func (p Pages) Read(ctx context.Context, yield func(string) error) (PagesReport, error) {
	var out PagesReport

	repo := p.Repo
	if repo == "" {
		repo = PagesRepo
	}
	perHost := p.PerHost
	switch {
	case perHost == 0:
		perHost = DefaultPerHost
	case perHost < 0:
		perHost = 0 // no cap
	}

	st := &count.Store{Repo: repo, Token: p.Token, API: p.API}
	parts, err := p.parts(ctx, repo)
	if err != nil {
		return out, err
	}
	if len(parts) == 0 {
		return out, fmt.Errorf("seed: %s holds no parts under %q", repo, p.dir())
	}

	seen := make(map[string]int, 1<<20)
	for _, part := range parts {
		if p.Limit > 0 && out.Kept >= p.Limit {
			break
		}
		out.Parts++
		if err := p.readPart(ctx, st, part, perHost, seen, &out, yield); err != nil {
			return out, err
		}
	}
	out.Hosts = len(seen)
	return out, nil
}

// dir is the directory in the repo the parts are read from. A dataset holding
// several sources files each of them under its own name, so a prefix of
// "hplt3" reads that source and an empty one reads all of them.
func (p Pages) dir() string {
	if p.Prefix == "" {
		return store.DataDir
	}
	return store.DataDir + "/" + strings.Trim(p.Prefix, "/")
}

// parts is the parquet files under [Pages.dir], one source at a time in
// rotation: the first part of each source, then the second of each, and so on.
func (p Pages) parts(ctx context.Context, repo string) ([]store.Stored, error) {
	files, err := (&store.Pusher{Repo: repo, Token: p.Token, API: p.API}).List(ctx, p.dir())
	if err != nil {
		return nil, err
	}
	bySource := map[string][]store.Stored{}
	var order []string
	for _, f := range files {
		if !f.Parquet() {
			continue
		}
		s := sourceOf(f.Path)
		if _, seen := bySource[s]; !seen {
			order = append(order, s)
		}
		bySource[s] = append(bySource[s], f)
	}
	sort.Strings(order)
	deepest := 0
	for _, s := range order {
		sort.Slice(bySource[s], func(i, j int) bool { return bySource[s][i].Path < bySource[s][j].Path })
		deepest = max(deepest, len(bySource[s]))
	}

	out := make([]store.Stored, 0, len(files))
	for i := range deepest {
		for _, s := range order {
			if i < len(bySource[s]) {
				out = append(out, bySource[s][i])
			}
		}
	}
	return out, nil
}

// sourceOf is the source directory a part is filed under, which for
// data/hplt3/part-00000-of-00774.parquet is hplt3.
func sourceOf(path string) string {
	rest := strings.TrimPrefix(path, store.DataDir+"/")
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return ""
}

// errEnough ends a read early without making the caller's yield say so. It never
// escapes Read.
var errEnough = errors.New("seed: enough")

func (p Pages) readPart(ctx context.Context, st *count.Store, part store.Stored,
	perHost int, seen map[string]int, out *PagesReport, yield func(string) error,
) error {
	r, err := st.Open(ctx, part)
	if err != nil {
		return err
	}
	// The page index and the bloom filters are the two structures that are
	// optional, large and read eagerly, and one column read forwards wants
	// neither of them.
	f, err := parquet.OpenFile(r, part.Bytes,
		parquet.SkipPageIndex(true), parquet.SkipBloomFilters(true))
	if err != nil {
		return fmt.Errorf("seed: opening %s: %w", part.Path, err)
	}

	err = count.EachString(f, PagesColumn, func(raw string) error {
		out.Rows++
		host, ok := hostOf(raw)
		if !ok {
			out.Bad++
			return nil
		}
		if !p.Any && !markup(raw) {
			out.Binary++
			return nil
		}
		if p.Fleet > 1 && frontier.Box(host, p.Fleet) != p.Shard {
			out.Foreign++
			return nil
		}
		if perHost > 0 && seen[host] >= perHost {
			out.Capped++
			return nil
		}
		seen[host]++
		if err := yield(raw); err != nil {
			return err
		}
		out.Kept++
		if p.Limit > 0 && out.Kept >= p.Limit {
			return errEnough
		}
		return nil
	})
	switch {
	case errors.Is(err, errEnough):
		return nil
	case err != nil:
		return fmt.Errorf("seed: reading %s of %s: %w", PagesColumn, part.Path, err)
	}
	return nil
}

// markup reports whether an address is worth handing to this crawler, which
// keeps text/html and application/xhtml+xml and nothing else.
//
// The cost of getting this wrong is one sided and large. The crawler decides on
// the Content-Type, which arrives with the response, so a PDF is resolved,
// connected to, fetched in full and then dropped, and a PDF is megabytes where a
// page is kilobytes. It has no links either, so it does not even pay the crawl
// back in discovery. One published source is nothing but PDFs and the other two
// carry some, and 1.7% of the whole inventory names one outright.
//
// It reads the extension of the path, which is a guess and is deliberately a
// narrow one. A path with no extension is markup as far as this is concerned,
// and so is a download handler named .aspx or .ashx that hands back a PDF, and
// so is anything naming the file in its query string rather than its path. Over
// 20,000 real addresses that last case leaks ten, which is 0.05%. The failure
// worth avoiding is throwing away a page, not fetching a stray file.
func markup(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	path := u.Path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		path = path[i+1:]
	}
	i := strings.LastIndexByte(path, '.')
	if i < 0 {
		return true
	}
	return !notMarkup[strings.ToLower(path[i+1:])]
}

// notMarkup is the extensions a crawler that reads HTML has no use for. It is
// the file types that actually turn up in the published inventory rather than
// every type there is, since an extension nobody writes costs nothing to miss.
var notMarkup = map[string]bool{
	"pdf": true, "doc": true, "docx": true, "xls": true, "xlsx": true,
	"ppt": true, "pptx": true, "rtf": true, "odt": true, "ods": true, "odp": true,
	"epub": true, "mobi": true, "djvu": true, "ps": true,
	"zip": true, "rar": true, "7z": true, "gz": true, "bz2": true, "xz": true,
	"tar": true, "tgz": true, "exe": true, "dmg": true, "apk": true, "iso": true,
	"jpg": true, "jpeg": true, "png": true, "gif": true, "bmp": true, "webp": true,
	"svg": true, "ico": true, "tif": true, "tiff": true, "psd": true,
	"mp3": true, "mp4": true, "avi": true, "mkv": true, "mov": true, "wmv": true,
	"flv": true, "webm": true, "wav": true, "flac": true, "m4a": true, "ogg": true,
	"css": true, "js": true, "json": true, "xml": true, "rss": true, "csv": true,
	"txt": true, "woff": true, "woff2": true, "ttf": true, "eot": true,
}

// hostOf is the host of an address, lowercased, or false for anything that is
// not an absolute http address. A dataset assembled from several upstreams has
// a few rows that are neither.
func hostOf(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return "", false
	}
	return strings.ToLower(u.Hostname()), true
}
