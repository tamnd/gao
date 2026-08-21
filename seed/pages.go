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
	// a host's share, and Bad is addresses that would not parse. A dataset this
	// large has a few of the last, and a run that stopped on one would be a run
	// that cannot read the corpus it published.
	Foreign int
	Capped  int
	Bad     int
}

// Read calls yield with every address the filters admit, in the order the
// dataset holds them.
//
// The order matters and it is deliberately not shuffled. A part is one source at
// one revision, so reading in order hands the frontier a source at a time, and a
// frontier fed one source at a time still spreads across that source's hosts
// because a source is not sorted by host either. Shuffling 16 million addresses
// would mean holding 16 million addresses, and the point of reading a column
// rather than a dataset is not to.
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

// parts is the parquet files under [Pages.dir], in path order, which for a
// published dataset is the order they were written in.
func (p Pages) parts(ctx context.Context, repo string) ([]store.Stored, error) {
	files, err := (&store.Pusher{Repo: repo, Token: p.Token, API: p.API}).List(ctx, p.dir())
	if err != nil {
		return nil, err
	}
	out := make([]store.Stored, 0, len(files))
	for _, f := range files {
		if f.Parquet() {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
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
