package harvest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOneHostAlwaysGetsTheSamePool(t *testing.T) {
	t.Parallel()

	f := NewFleet(TransportOptions{})
	first := f.shard("vnexpress.net")
	for range 100 {
		if got := f.shard("vnexpress.net"); got != first {
			t.Fatalf("vnexpress.net landed on shard %d and then %d, so its keep-alive is lost", first, got)
		}
	}
}

func TestTheHostsAreSpreadOverThePools(t *testing.T) {
	t.Parallel()

	f := NewFleet(TransportOptions{Shards: 8})
	seen := map[int]bool{}
	for _, h := range []string{
		"vnexpress.net", "tuoitre.vn", "thanhnien.vn", "dantri.com.vn",
		"kenh14.vn", "vietnamnet.vn", "cafef.vn", "zingnews.vn",
		"nhandan.vn", "laodong.vn", "baophapluat.vn", "tienphong.vn",
	} {
		seen[f.shard(h)] = true
	}
	// Twelve hosts over eight pools will not hit all eight, and a hash that put
	// them all in one is the failure worth catching.
	if len(seen) < 4 {
		t.Fatalf("twelve hosts landed on %d of 8 pools, which is not a spread", len(seen))
	}
}

func TestAFleetIsBuiltWithTheDefaults(t *testing.T) {
	t.Parallel()

	f := NewFleet(TransportOptions{})
	if got := f.Shards(); got != DefaultShards {
		t.Fatalf("built %d pools and the default is %d", got, DefaultShards)
	}
	for i, tr := range f.shards {
		if tr.ResponseHeaderTimeout != DefaultHeaderTimeout {
			t.Fatalf("pool %d gives a server %v to start answering and the rule is %v",
				i, tr.ResponseHeaderTimeout, DefaultHeaderTimeout)
		}
		if tr.IdleConnTimeout != DefaultIdleTimeout {
			t.Fatalf("pool %d keeps an unused connection for %v and the rule is %v",
				i, tr.IdleConnTimeout, DefaultIdleTimeout)
		}
	}
}

func TestTheDeadlinesCanBeSet(t *testing.T) {
	t.Parallel()

	f := NewFleet(TransportOptions{Shards: 2, Header: time.Second, TLS: 2 * time.Second})
	if f.Shards() != 2 {
		t.Fatalf("asked for 2 pools and got %d", f.Shards())
	}
	if f.shards[0].ResponseHeaderTimeout != time.Second {
		t.Fatalf("asked for a one second header deadline and got %v", f.shards[0].ResponseHeaderTimeout)
	}
	if f.shards[0].TLSHandshakeTimeout != 2*time.Second {
		t.Fatalf("asked for a two second handshake deadline and got %v", f.shards[0].TLSHandshakeTimeout)
	}
}

func TestASlowHeaderIsGivenUpOnWithoutWaitingOutTheRequest(t *testing.T) {
	t.Parallel()

	// A server that takes the connection and then says nothing, which on the
	// seed crawl is a parked domain or a load balancer with nothing behind it.
	quiet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer quiet.Close()

	c := NewClient(TransportOptions{Header: 100 * time.Millisecond}, 10*time.Second)
	at := time.Now()
	resp, err := c.Get(quiet.URL)
	took := time.Since(at)

	if err == nil {
		// Unreachable while the server holds its header back, and closed anyway
		// because a test that leaks a body on a path it does not expect to take
		// is a test that hides the leak.
		_ = resp.Body.Close()
		t.Fatal("a server that sent no header answered anyway")
	}
	if took > 2*time.Second {
		t.Fatalf("waited %v for a header that was never coming, and the deadline was 100ms", took)
	}
}

func TestAPageThatArrivesIsFetched(t *testing.T) {
	t.Parallel()

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>xin chao</body></html>"))
	}))
	defer site.Close()

	c := NewClient(TransportOptions{}, 0)
	resp, err := c.Get(site.URL)
	if err != nil {
		t.Fatalf("an ordinary page did not arrive: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an ordinary page came back %s", resp.Status)
	}
}

func TestARedirectIsHandedBackRatherThanFollowed(t *testing.T) {
	t.Parallel()

	var asked []string
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/somewhere-else", http.StatusMovedPermanently)
			return
		}
		_, _ = w.Write([]byte("<html><body>xin chao</body></html>"))
	}))
	defer site.Close()

	c := NewClient(TransportOptions{}, 0)
	resp, err := c.Get(site.URL + "/")
	if err != nil {
		t.Fatalf("the redirect did not come back: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("got %s, and a redirect belongs back in the frontier where robots.txt is checked", resp.Status)
	}
	if len(asked) != 1 {
		t.Fatalf("the client made %d requests and the crawl allowed for one", len(asked))
	}
}

func TestClosingIdleConnectionsIsSafeOnEveryPool(t *testing.T) {
	t.Parallel()

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer site.Close()

	f := NewFleet(TransportOptions{Shards: 4})
	c := &http.Client{Transport: f}
	resp, err := c.Get(site.URL)
	if err != nil {
		t.Fatalf("the fetch failed: %v", err)
	}
	_ = resp.Body.Close()

	f.CloseIdle()
	// Still usable afterwards, since a crawl closes idle connections between
	// parts and then carries on.
	resp, err = c.Get(site.URL)
	if err != nil {
		t.Fatalf("the fetch after closing idle connections failed: %v", err)
	}
	_ = resp.Body.Close()
}
