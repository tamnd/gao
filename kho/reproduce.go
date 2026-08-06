package kho

// Rebuilding a snapshot's bytes and checking they come out the same.
//
// The release has to be reproducible by somebody who does not trust us, and the
// claim breaks into two pieces that are almost always run together and are not
// the same claim. The first is that the pipeline computes the same documents
// from the same inputs. The second is that writing those documents produces the
// same bytes. Only the second is checkable from a snapshot alone, and it is the
// one that has to hold before the first can be tested at all: if reading a shard
// and writing it back does not give the same file, then a stage rerun that
// disagrees is indistinguishable from an encoder that disagrees, and the whole
// exercise stops meaning anything.
//
// So this checks the encoder, and it says so. It rebuilds every shard from the
// documents that shard holds, byte for byte, in one streaming pass with no
// temporary file, and reports the first offset where the rebuild and the
// recording disagree. The documents are the same by construction, which is the
// point: a difference here is this build of gao, not the corpus.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/tamnd/gao/doc"
)

// Errors a rebuild can fail with.
var (
	// ErrNotReproducible is returned when a shard's bytes do not come back.
	ErrNotReproducible = errors.New("kho: the snapshot did not rebuild to the same bytes")

	// ErrStageDisagrees is returned when a registered stage check finds a
	// document that is not what that stage would produce.
	ErrStageDisagrees = errors.New("kho: a document is not what its stage produces")
)

// Encoders are the modules whose versions decide what bytes come out. They are
// reported with every rebuild because the first question after a mismatch is
// what differs between the two machines, and the answer is almost always one of
// these rather than anything in gao.
var Encoders = []string{
	"github.com/klauspost/compress",
	"github.com/zeebo/blake3",
	"github.com/parquet-go/parquet-go",
}

// Env is what a rebuild ran on. It is recorded rather than assumed because byte
// identity is a claim about a build and not about a program, and a report that
// does not say which build made it cannot be compared with one from another box.
type Env struct {
	Go   string
	OS   string
	Arch string

	// Modules is the version of each of [Encoders] this binary was built
	// against, or "unknown" for a binary built without module information.
	Modules map[string]string
}

// Environment returns what this binary is.
func Environment() Env {
	e := Env{
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Modules: map[string]string{},
	}
	for _, name := range Encoders {
		e.Modules[name] = "unknown"
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return e
	}
	for _, dep := range info.Deps {
		if _, want := e.Modules[dep.Path]; want {
			e.Modules[dep.Path] = dep.Version
		}
	}
	return e
}

// String renders the environment on one line.
func (e Env) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s/%s", e.Go, e.OS, e.Arch)
	names := make([]string, 0, len(e.Modules))
	for name := range e.Modules {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, " %s@%s", filepath.Base(name), e.Modules[name])
	}
	return b.String()
}

// A Rebuild is one shard rebuilt and compared.
type Rebuild struct {
	Name      string
	Documents int

	// Want and Got are the recorded hash and the hash of the rebuild, and Bytes
	// and Rebuilt are the two sizes. All four are reported even when they agree,
	// because a report that only speaks up on failure is a report nobody can
	// tell apart from one that did not run.
	Want, Got doc.Hash
	Bytes     int64
	Rebuilt   int64
	Same      bool

	// Diff is the byte offset where the rebuild first differs from the
	// recording, or -1 if it does not. Frame is which frame of the segment that
	// offset falls in, or -1 if the offset is past the last frame, which is
	// where the index and the trailer live.
	Diff  int64
	Frame int
}

// A StageCheck re-runs one stage against a document a snapshot holds and reports
// whether the document is what that stage produces.
//
// This is what can be checked about a stage without its inputs. Normalizing an
// already normalized document, or classifying an already accepted one, is the
// stage applied to its own output, and a stage that is a function has to leave
// its output alone. It does not prove the stage ran on the right inputs. It does
// catch a snapshot cleaned by a different version of the stage than the manifest
// names, which is the failure that otherwise surfaces as a model that trains
// badly for no visible reason.
type StageCheck func(d *doc.Document) error

// checks holds the registered stage checks, by stage name without its version.
var checks = map[string]StageCheck{}

// RegisterStageCheck registers a check for a stage.
//
// kho does not know what any stage does and must not: the cleaning stages import
// kho, so kho importing them would be a cycle. Registration is how a binary that
// has both says so.
func RegisterStageCheck(stage string, c StageCheck) { checks[stage] = c }

// A StageStatus is what a rebuild was able to say about one recorded stage.
type StageStatus struct {
	Name       string
	ConfigHash doc.Hash

	// Checked is how many documents were put back through the stage, and
	// Disagreed is how many came back different. Ran is false when no check is
	// registered for the stage, which is most of them: an ingest stage cannot be
	// re-run without the network and a deduplication stage cannot be re-run
	// against one document at a time.
	Ran       bool
	Checked   int64
	Disagreed int64

	// Sample names up to a few documents that disagreed, so the next step is
	// opening one rather than searching for one.
	Sample []doc.Hash

	// Why says why the stage was not re-run, when it was not.
	Why string
}

// ReproduceReport is a whole snapshot rebuilt.
type ReproduceReport struct {
	Snapshot string
	Env      Env
	Shards   []Rebuild
	Stages   []StageStatus

	Same      int
	Different int
	Documents int64
}

// reproduceOptions is the settled configuration for [Reproduce].
type reproduceOptions struct {
	progress func(Rebuild)
	frame    int
	stop     bool
}

// ReproduceOption configures [Reproduce].
type ReproduceOption func(*reproduceOptions)

// ReproduceProgress calls f as each shard finishes, so a command can show what
// it is doing during the several minutes a full rebuild takes.
func ReproduceProgress(f func(Rebuild)) ReproduceOption {
	return func(o *reproduceOptions) { o.progress = f }
}

// ReproduceFrameBytes sets the frame size the rebuild writes at.
//
// It has to match the size the snapshot was written at or nothing will line up,
// so this exists for the snapshot written with a non-default size rather than
// for tuning. The default is [DefaultFrameBytes], which is what every shard gao
// writes uses.
func ReproduceFrameBytes(n int) ReproduceOption {
	return func(o *reproduceOptions) { o.frame = n }
}

// ReproduceStopEarly returns after the first shard that does not rebuild.
//
// The default is to keep going, because one differing shard and every shard
// differing are different diagnoses: the first is a corrupted file and the
// second is a different build of gao, and stopping at the first makes them look
// the same.
func ReproduceStopEarly() ReproduceOption {
	return func(o *reproduceOptions) { o.stop = true }
}

// Reproduce rebuilds every shard of a snapshot from the documents it holds and
// checks the bytes against what the manifest recorded.
//
// The snapshot is verified first. There is no sense asking whether bytes rebuild
// to what a manifest says when nothing has established that the manifest is the
// one that was signed, and a rebuild that skipped the check would report a clean
// result for a snapshot somebody had swapped underneath it.
//
// The report is returned even on failure, because the first thing anybody asks
// is which shard and at which byte.
func Reproduce(dir string, opts ...ReproduceOption) (*ReproduceReport, error) {
	cfg := reproduceOptions{frame: DefaultFrameBytes}
	for _, opt := range opts {
		opt(&cfg)
	}

	if _, err := Verify(dir, Quick()); err != nil {
		return nil, err
	}
	m, err := ReadManifest(dir)
	if err != nil {
		return nil, err
	}

	report := &ReproduceReport{Snapshot: m.Snapshot, Env: Environment()}
	report.Stages = make([]StageStatus, len(m.Stages))
	for i, s := range m.Stages {
		report.Stages[i] = StageStatus{Name: s.Name, ConfigHash: s.ConfigHash}
		if _, ok := checks[stageName(s.Name)]; ok {
			report.Stages[i].Ran = true
		} else {
			report.Stages[i].Why = "no check is registered for this stage in this binary"
		}
	}

	for _, s := range m.Shards {
		rb, err := rebuildShard(filepath.Join(dir, s.Name), s, cfg.frame, func(d *doc.Document) {
			for i := range report.Stages {
				st := &report.Stages[i]
				if !st.Ran {
					continue
				}
				st.Checked++
				if err := checks[stageName(st.Name)](d); err != nil {
					st.Disagreed++
					if len(st.Sample) < 5 {
						st.Sample = append(st.Sample, d.DocID)
					}
				}
			}
		})
		if err != nil {
			return report, fmt.Errorf("%s: %w", s.Name, err)
		}
		report.Shards = append(report.Shards, rb)
		report.Documents += int64(rb.Documents)
		if rb.Same {
			report.Same++
		} else {
			report.Different++
		}
		if cfg.progress != nil {
			cfg.progress(rb)
		}
		if !rb.Same && cfg.stop {
			break
		}
	}

	if report.Different > 0 {
		return report, fmt.Errorf("%w: %d of %d shards", ErrNotReproducible, report.Different, len(report.Shards))
	}
	for _, st := range report.Stages {
		if st.Disagreed > 0 {
			return report, fmt.Errorf("%w: %s, %d of %d documents", ErrStageDisagrees, st.Name, st.Disagreed, st.Checked)
		}
	}
	return report, nil
}

// rebuildShard reads a shard and writes it back, comparing as it goes.
//
// Nothing is written to disk. A rebuilt shard is half a gigabyte and the box
// this has to run on has five, so the rebuild goes into a [mirror] that checks
// each byte against the recording as it is produced and keeps only the offset
// where they first disagreed.
func rebuildShard(path string, want Shard, frame int, each func(*doc.Document)) (Rebuild, error) {
	rb := Rebuild{Name: want.Name, Want: want.Hash, Bytes: want.Bytes, Diff: -1, Frame: -1}

	seg, err := Open[*doc.Document](path)
	if err != nil {
		return rb, err
	}
	defer func() { _ = seg.Close() }()

	src, err := os.Open(path)
	if err != nil {
		return rb, err
	}
	defer func() { _ = src.Close() }()

	mir := &mirror{src: src, diff: -1}
	// Unvalidated because the contract was checked when the document went in.
	// Re-checking it here would turn a rebuild into an admission decision, and a
	// document already in a sealed snapshot is not up for admission.
	w, err := NewWriter[*doc.Document](mir, FrameBytes(frame), Unvalidated())
	if err != nil {
		return rb, err
	}
	for d, err := range seg.All() {
		if err != nil {
			return rb, err
		}
		if each != nil {
			each(d)
		}
		if err := w.Append(d); err != nil {
			return rb, err
		}
		rb.Documents++
	}
	if err := w.Close(); err != nil {
		return rb, err
	}

	rb.Got, rb.Rebuilt = w.Hash(), mir.off
	rb.Same = rb.Got == rb.Want && rb.Rebuilt == rb.Bytes
	if rb.Same {
		return rb, nil
	}

	rb.Diff = mir.diff
	if rb.Diff < 0 {
		// Every byte written matched and the hashes still disagree, so the
		// rebuild is a prefix of the recording or the recording is a prefix of
		// it. Either way they part company at the end of the shorter one.
		rb.Diff = min(rb.Rebuilt, rb.Bytes)
	}
	rb.Frame = frameAt(seg.Index(), rb.Diff)
	return rb, nil
}

// frameAt returns which frame a byte offset falls in, or -1 for an offset past
// the last frame, which is the index and the trailer rather than any document.
func frameAt(idx Index, off int64) int {
	for i, f := range idx.Frames {
		if off >= f.Offset && off < f.Offset+f.Bytes {
			return i
		}
	}
	return -1
}

// stageName is the part of name@semver before the version. A check is
// registered per stage rather than per version, because the check is the current
// version of the stage and the whole question is whether the snapshot agrees
// with it.
func stageName(s string) string {
	name, _, _ := strings.Cut(s, "@")
	return name
}

// mirror is an [io.Writer] that checks what is written to it against a reader
// and remembers the first offset where they disagree.
//
// Writing continues after a disagreement rather than stopping, because the
// caller wants the whole rebuild's hash and its length as well as the first bad
// offset, and a writer that returned an error would deny it both.
type mirror struct {
	src io.Reader
	off int64
	// diff is the first offset where the two disagreed, or -1.
	diff int64
	buf  []byte
	// short is set once the reader has run out, so the comparison stops asking
	// it for more.
	short bool
}

func (m *mirror) Write(p []byte) (int, error) {
	if m.diff < 0 && !m.short {
		if cap(m.buf) < len(p) {
			m.buf = make([]byte, len(p))
		}
		b := m.buf[:len(p)]
		n, err := io.ReadFull(m.src, b)
		if n < len(p) {
			m.short = true
		}
		if i := firstDiff(p[:n], b[:n]); i >= 0 {
			m.diff = m.off + int64(i)
		} else if m.short {
			m.diff = m.off + int64(n)
		}
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, err
		}
	}
	m.off += int64(len(p))
	return len(p), nil
}

// firstDiff returns the index of the first byte at which a and b differ, or -1.
func firstDiff(a, b []byte) int {
	if bytes.Equal(a, b) {
		return -1
	}
	for i := range a {
		if a[i] != b[i] {
			return i
		}
	}
	return len(a)
}
