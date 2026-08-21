package harvest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// A nameServer is a real DNS server on a real UDP socket, so the tests below
// count queries that went over the wire rather than calls to a stub.
//
// That distinction is the whole point of them. What the cache has to do is stop
// packets leaving the box, and a fake resolver would prove that the cache
// returns the right strings while saying nothing about whether it asked.
type nameServer struct {
	conn *net.UDPConn

	mu    sync.Mutex
	zone  map[string][]net.IP
	asked map[string]int
}

// newNameServer starts one and returns it. It stops when the test ends.
func newNameServer(t *testing.T, zone map[string][]net.IP) *nameServer {
	t.Helper()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("could not start a name server: %v", err)
	}
	s := &nameServer{conn: conn, zone: zone, asked: map[string]int{}}
	go s.serve()
	t.Cleanup(func() { _ = conn.Close() })
	return s
}

func (s *nameServer) serve() {
	buf := make([]byte, 1500)
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		reply, err := s.answer(buf[:n])
		if err != nil {
			continue
		}
		_, _ = s.conn.WriteToUDP(reply, from)
	}
}

// answer parses one query and builds the response for it.
func (s *nameServer) answer(query []byte) ([]byte, error) {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil {
		return nil, err
	}
	q, err := p.Question()
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(q.Name.String(), ".")

	s.mu.Lock()
	ips, known := s.zone[name]
	// Only the A query is counted. Go asks for A and AAAA together, so
	// counting both would double every number in these tests for no gain.
	if q.Type == dnsmessage.TypeA {
		s.asked[name]++
	}
	s.mu.Unlock()

	code := dnsmessage.RCodeSuccess
	if !known {
		code = dnsmessage.RCodeNameError
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID: h.ID, Response: true, Authoritative: true, RCode: code,
	})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(q); err != nil {
		return nil, err
	}
	if err := b.StartAnswers(); err != nil {
		return nil, err
	}
	if known && q.Type == dnsmessage.TypeA {
		for _, ip := range ips {
			v4 := ip.To4()
			if v4 == nil {
				continue
			}
			err := b.AResource(dnsmessage.ResourceHeader{
				Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60,
			}, dnsmessage.AResource{A: [4]byte(v4)})
			if err != nil {
				return nil, err
			}
		}
	}
	return b.Finish()
}

// resolver returns a [net.Resolver] that asks this server and nothing else.
func (s *nameServer) resolver() *net.Resolver {
	addr := s.conn.LocalAddr().String()
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
}

// queries is how many A queries this server has had for a name.
func (s *nameServer) queries(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked[name]
}

func TestASecondLookupOfAHostDoesNotReachTheResolver(t *testing.T) {
	t.Parallel()

	dns := newNameServer(t, map[string][]net.IP{
		"vnexpress.net": {net.IPv4(203, 0, 113, 7)},
	})
	n := NewNames(NameOptions{Resolver: dns.resolver()})

	for i := range 20 {
		addrs, err := n.Lookup(t.Context(), "vnexpress.net")
		if err != nil {
			t.Fatalf("lookup %d failed: %v", i, err)
		}
		if len(addrs) != 1 || addrs[0] != "203.0.113.7" {
			t.Fatalf("lookup %d gave %v", i, addrs)
		}
	}
	if got := dns.queries("vnexpress.net"); got != 1 {
		t.Fatalf("twenty lookups put %d queries on the wire and the cache is meant to make it one", got)
	}
	if s := n.Stats(); s.Hits != 19 || s.Misses != 1 {
		t.Fatalf("%d hits and %d misses out of twenty lookups", s.Hits, s.Misses)
	}
}

func TestManyGoroutinesWantingOneHostAskOnce(t *testing.T) {
	t.Parallel()

	// The server holds every answer back until it has seen the first query, so
	// that all two hundred callers are inside the cache at once. Without
	// coalescing they each start their own query and the count is two hundred.
	slow := make(chan struct{})
	dns := newNameServer(t, map[string][]net.IP{
		"tuoitre.vn": {net.IPv4(203, 0, 113, 8)},
	})
	go func() {
		for dns.queries("tuoitre.vn") == 0 {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		close(slow)
	}()

	n := NewNames(NameOptions{Resolver: dns.resolver()})
	var wg sync.WaitGroup
	var bad atomic.Int64
	for range 200 {
		wg.Go(func() {
			addrs, err := n.Lookup(t.Context(), "tuoitre.vn")
			if err != nil || len(addrs) != 1 {
				bad.Add(1)
			}
		})
	}
	wg.Wait()
	<-slow

	if bad.Load() != 0 {
		t.Fatalf("%d of 200 lookups came back wrong", bad.Load())
	}
	if got := dns.queries("tuoitre.vn"); got != 1 {
		t.Fatalf("two hundred goroutines put %d queries on the wire", got)
	}
	s := n.Stats()
	if s.Misses != 1 {
		t.Fatalf("%d of the two hundred went to the resolver", s.Misses)
	}
	if s.Hits+s.Joined != 199 {
		t.Fatalf("%d hits and %d joined, and 199 callers did not ask", s.Hits, s.Joined)
	}
}

func TestAHostThatDoesNotResolveIsNotAskedAboutAgain(t *testing.T) {
	t.Parallel()

	dns := newNameServer(t, map[string][]net.IP{})
	n := NewNames(NameOptions{Resolver: dns.resolver()})

	for i := range 10 {
		if _, err := n.Lookup(t.Context(), "khong-ton-tai.example"); err == nil {
			t.Fatalf("lookup %d of a name that does not exist came back fine", i)
		}
	}
	if got := dns.queries("khong-ton-tai.example"); got != 1 {
		t.Fatalf("ten lookups of a dead host put %d queries on the wire", got)
	}
}

func TestARecordIsAskedAboutAgainOnceItHasGoneStale(t *testing.T) {
	t.Parallel()

	dns := newNameServer(t, map[string][]net.IP{
		"thanhnien.vn": {net.IPv4(203, 0, 113, 9)},
	})
	n := NewNames(NameOptions{Resolver: dns.resolver(), TTL: 20 * time.Millisecond})

	if _, err := n.Lookup(t.Context(), "thanhnien.vn"); err != nil {
		t.Fatalf("the first lookup failed: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := n.Lookup(t.Context(), "thanhnien.vn"); err != nil {
		t.Fatalf("the second lookup failed: %v", err)
	}
	if got := dns.queries("thanhnien.vn"); got != 2 {
		t.Fatalf("a record that had expired was asked about %d times and it should be twice", got)
	}
}

func TestTheCacheStopsGrowing(t *testing.T) {
	t.Parallel()

	zone := map[string][]net.IP{}
	for i := range 500 {
		zone[fmt.Sprintf("host%d.example", i)] = []net.IP{net.IPv4(203, 0, 113, 10)}
	}
	dns := newNameServer(t, zone)
	n := NewNames(NameOptions{Resolver: dns.resolver(), Size: 16})

	for i := range 500 {
		if _, err := n.Lookup(t.Context(), fmt.Sprintf("host%d.example", i)); err != nil {
			t.Fatalf("lookup %d failed: %v", i, err)
		}
	}
	// Two generations of Size, and the bound is what matters rather than which
	// hosts survived.
	if held := n.Stats().Held; held > 32 {
		t.Fatalf("five hundred hosts through a cache sized 16 left %d held", held)
	}
}

func TestAHostStillBeingCrawledSurvivesTheCacheFilling(t *testing.T) {
	t.Parallel()

	zone := map[string][]net.IP{"kenh14.vn": {net.IPv4(203, 0, 113, 11)}}
	for i := range 100 {
		zone[fmt.Sprintf("host%d.example", i)] = []net.IP{net.IPv4(203, 0, 113, 12)}
	}
	dns := newNameServer(t, zone)
	n := NewNames(NameOptions{Resolver: dns.resolver(), Size: 8})

	// The host the crawl is working on, asked for between every other one, the
	// way a busy host actually arrives.
	for i := range 100 {
		if _, err := n.Lookup(t.Context(), "kenh14.vn"); err != nil {
			t.Fatalf("the busy host failed on round %d: %v", i, err)
		}
		if _, err := n.Lookup(t.Context(), fmt.Sprintf("host%d.example", i)); err != nil {
			t.Fatalf("filler %d failed: %v", i, err)
		}
	}
	if got := dns.queries("kenh14.vn"); got != 1 {
		t.Fatalf("the busy host was asked about %d times while a hundred others went past it", got)
	}
}

func TestAFetchGoesThroughTheNameCache(t *testing.T) {
	t.Parallel()

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>xin chao</body></html>"))
	}))
	defer site.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(site.URL, "http://"))
	if err != nil {
		t.Fatalf("could not read the test server's port: %v", err)
	}

	dns := newNameServer(t, map[string][]net.IP{
		"dantri.com.vn": {net.IPv4(127, 0, 0, 1)},
	})
	names := NewNames(NameOptions{Resolver: dns.resolver()})
	c := NewClient(TransportOptions{Names: names}, 0)

	for i := range 10 {
		// A fresh connection every time, which is the case the cache is for:
		// a keep-alive would hide the lookup.
		resp, err := c.Get("http://dantri.com.vn:" + port + "/")
		if err != nil {
			t.Fatalf("fetch %d failed: %v", i, err)
		}
		_ = resp.Body.Close()
		c.Transport.(*Fleet).CloseIdle()
	}
	if got := dns.queries("dantri.com.vn"); got != 1 {
		t.Fatalf("ten fetches over ten connections put %d queries on the wire", got)
	}
}

func TestOnlyTheFirstFewAddressesOfAHostAreTried(t *testing.T) {
	t.Parallel()

	// Six addresses is what a large site behind a round robin answers with, and
	// a dial deadline is five seconds, so a dialer that works through all of
	// them spends half a minute on one URL.
	dns := newNameServer(t, map[string][]net.IP{
		"cafef.vn": {
			net.IPv4(203, 0, 113, 1), net.IPv4(203, 0, 113, 2), net.IPv4(203, 0, 113, 3),
			net.IPv4(203, 0, 113, 4), net.IPv4(203, 0, 113, 5), net.IPv4(203, 0, 113, 6),
		},
	})

	for _, allowed := range []int{1, 2, 4} {
		names := NewNames(NameOptions{Resolver: dns.resolver(), Addrs: allowed})
		var tried atomic.Int64
		dial := names.dialWith(func(context.Context, string, string) (net.Conn, error) {
			tried.Add(1)
			return nil, errors.New("nothing is listening")
		})
		if _, err := dial(t.Context(), "tcp", "cafef.vn:80"); err == nil {
			t.Fatalf("a host where every address refuses came back with a connection")
		}
		if got := tried.Load(); got != int64(allowed) {
			t.Fatalf("allowed %d addresses and dialed %d of the six", allowed, got)
		}
	}
}

func TestEveryAddressOfAHostIsTriedUntilOneAnswers(t *testing.T) {
	t.Parallel()

	// A real listener, because the point of the test is that a connection comes
	// back rather than that a third error does.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	dns := newNameServer(t, map[string][]net.IP{
		"vietnamnet.vn": {net.IPv4(203, 0, 113, 1), net.IPv4(203, 0, 113, 2), net.IPv4(203, 0, 113, 3)},
	})
	names := NewNames(NameOptions{Resolver: dns.resolver(), Addrs: 3})

	// The resolver sorts what it hands back, so the test asks for the third
	// dial to be the one that works rather than for a particular address to be
	// third.
	var tried atomic.Int64
	dial := names.dialWith(func(ctx context.Context, network, _ string) (net.Conn, error) {
		if tried.Add(1) < 3 {
			return nil, errors.New("nothing is listening")
		}
		var d net.Dialer
		return d.DialContext(ctx, network, ln.Addr().String())
	})
	conn, err := dial(t.Context(), "tcp", "vietnamnet.vn:80")
	if err != nil {
		t.Fatalf("two addresses refused and the third was never reached: %v", err)
	}
	_ = conn.Close()
	if got := tried.Load(); got != 3 {
		t.Fatalf("the dialer stopped after %d of three addresses", got)
	}
}

func TestAURLThatCarriesAnAddressIsDialedWithoutALookup(t *testing.T) {
	t.Parallel()

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer site.Close()

	names := NewNames(NameOptions{Resolver: newNameServer(t, nil).resolver()})
	c := NewClient(TransportOptions{Names: names}, 0)
	resp, err := c.Get(site.URL)
	if err != nil {
		t.Fatalf("a fetch of an address failed: %v", err)
	}
	_ = resp.Body.Close()

	if s := names.Stats(); s.Hits+s.Misses != 0 {
		t.Fatalf("an address was looked up anyway: %d hits and %d misses", s.Hits, s.Misses)
	}
}
