package main

import (
	"fmt"
	"io"
	// Aliased because this package has a function called net, in graft.go, and
	// a file scoped import name collides with a package scoped declaration.
	gonet "net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"time"
)

// Profiling a crawl while it runs.
//
// The crawl's throughput ceiling has been argued about from goroutine dumps,
// which say where goroutines are parked but not how long they waited to get
// there or how much of the wait was one lock. A dump taken at 130 pages a
// second showed 1,098 goroutines inside the frontier and the conclusion drawn
// was that the frontier's spill was the ceiling. That may be right. It was an
// inference from a snapshot rather than a measurement, and the change made on
// the strength of it could not be checked afterwards by the same method,
// because a dump of the fixed version also shows goroutines in the frontier.
//
// The block and mutex profiles answer the question directly: which call site
// waited, and for how long in total. That turns "the spill looks bad" into a
// number that goes down or does not.
//
// Both profiles are off by default in the runtime because both cost something
// on every lock operation, which is why this is a flag rather than always on.

// profileRates are the sampling rates used when -pprof is given.
//
// The block profiler takes a nanosecond threshold and the mutex profiler takes
// a one-in-n fraction. These are the aggressive settings: every blocking event
// and every contention event. On a crawler that is thousands of lock
// operations a second, and it is the right trade for a diagnostic run, because
// a sampled profile of a contention problem is how a contention problem gets
// missed. A run with -pprof is a run being measured rather than a run being
// counted, and the throughput it reports should not be quoted as the box's
// number.
const (
	blockRate = 1
	mutexRate = 1
)

// serveProfiles starts the pprof endpoints on addr and turns on the two
// profilers the crawl actually needs.
//
// It returns as soon as the listener is open, so a caller that gets no error
// knows the address is live rather than hoping it will be. The server is left
// running for the life of the process: a profile server that shut down cleanly
// at the end of a run would be a profile server that is gone at exactly the
// moment somebody wants the final heap.
//
// The address is echoed with the paths worth knowing, because the failure mode
// of a profiling flag is somebody enabling it, getting no output, and
// concluding the crawl is not the problem.
func serveProfiles(w io.Writer, addr string) error {
	ln, err := gonet.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("gao crawl: serving profiles on %s: %w", addr, err)
	}

	runtime.SetBlockProfileRate(blockRate)
	runtime.SetMutexProfileFraction(mutexRate)

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// ReadHeaderTimeout because a profile server with no timeouts is a
	// goroutine leak waiting for a half open connection, and this one runs
	// inside a process that is holding a great deal of memory. The write
	// timeout is deliberately generous: a 30 second CPU profile is a 30 second
	// response, and the default would cut it off.
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Minute,
	}
	go func() { _ = srv.Serve(ln) }()

	fmt.Fprintf(w, "profiles on http://%s/debug/pprof/\n", ln.Addr())
	fmt.Fprintf(w, "  contention  go tool pprof http://%s/debug/pprof/mutex\n", ln.Addr())
	fmt.Fprintf(w, "  waiting     go tool pprof http://%s/debug/pprof/block\n", ln.Addr())
	fmt.Fprintf(w, "  cpu         go tool pprof http://%s/debug/pprof/profile?seconds=30\n", ln.Addr())
	fmt.Fprintf(w, "  goroutines  curl http://%s/debug/pprof/goroutine?debug=2\n", ln.Addr())
	return nil
}
