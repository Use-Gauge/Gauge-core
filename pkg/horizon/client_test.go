package horizon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// The fixtures are real Horizon responses, recorded from mainnet on 2026-09-06
// and committed unmodified. Hand-writing them would defeat the point: these
// tests exist to prove Gauge reads what Horizon actually sends, and a fixture
// written to match the parser proves only that the author was consistent.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// serveFixture stands up a server returning one fixture for every request. The
// client under test never reaches the network: a test that silently fell
// through to live Horizon would pass or fail on someone else's uptime.
func serveFixture(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve the fixture once, then an empty page, so page walks terminate
		// instead of following the fixture's own next link forever.
		if r.URL.Query().Get("cursor") != "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"_links":{},"_embedded":{"records":[]}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListPoolsReadsRecordedPage(t *testing.T) {
	srv := serveFixture(t, fixture(t, "pools_page.json"))
	c := &Client{URL: srv.URL, MaxRetries: -1}

	var got []Pool
	pages, requests, err := c.ListPools(context.Background(), 1, func(p []Pool) error {
		got = append(got, p...)
		return nil
	})
	if err != nil {
		t.Fatalf("ListPools: %v", err)
	}
	if pages != 1 || requests != 1 {
		t.Errorf("pages=%d requests=%d, want 1 and 1", pages, requests)
	}
	if len(got) != 3 {
		t.Fatalf("got %d pools, want 3", len(got))
	}

	for _, p := range got {
		if p.ID == "" {
			t.Error("pool with empty ID")
		}
		if p.Type != "constant_product" {
			t.Errorf("pool %s type %q", p.ID, p.Type)
		}
		if p.Reserves[0].Asset == "" || p.Reserves[1].Asset == "" {
			t.Errorf("pool %s has an unnamed reserve", p.ID)
		}
	}
}

// The invariant this project is built on: an amount that came out of Horizon
// parses to exactly the value Horizon sent. Not "close to", not "within an
// epsilon of" — equal. A float64 round trip fails this, which is the entire
// reason decimal.Decimal is a dependency.
//
// Note what is asserted and what is not. The test compares *values*, not
// strings, because the two are not the same claim. Horizon always sends seven
// decimal places ("2477.8894960") and shopspring/decimal's String() normalises
// trailing zeros away ("2477.889496"). Roughly 30% of real reserve amounts
// differ textually from the wire for that reason, with no difference in value.
//
// An earlier version of this test compared strings and passed, because the
// three pools in the fixture happened to have no trailing zeros. It was
// asserting something stronger than is true and would have failed the next time
// anyone re-recorded fixtures. The format difference is pinned separately, in
// TestDecimalStringNormalisesTrailingZeros, so it is documented behaviour
// rather than a trap.
func TestAmountsRoundTripExactly(t *testing.T) {
	raw := fixture(t, "pools_page.json")

	var wire struct {
		Embedded struct {
			Records []struct {
				ID          string `json:"id"`
				TotalShares string `json:"total_shares"`
				Reserves    []struct {
					Amount string `json:"amount"`
				} `json:"reserves"`
			} `json:"records"`
		} `json:"_embedded"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	srv := serveFixture(t, raw)
	c := &Client{URL: srv.URL, MaxRetries: -1}

	var got []Pool
	if _, _, err := c.ListPools(context.Background(), 1, func(p []Pool) error {
		got = append(got, p...)
		return nil
	}); err != nil {
		t.Fatalf("ListPools: %v", err)
	}

	for i, p := range got {
		w := wire.Embedded.Records[i]

		wantShares, err := decimal.NewFromString(w.TotalShares)
		if err != nil {
			t.Fatalf("fixture total_shares %q is not a decimal: %v", w.TotalShares, err)
		}
		if !p.TotalShares.Equal(wantShares) {
			t.Errorf("pool %s total_shares: parsed %s, Horizon sent %s",
				p.ID, p.TotalShares, wantShares)
		}
		for j, r := range p.Reserves {
			want, err := decimal.NewFromString(w.Reserves[j].Amount)
			if err != nil {
				t.Fatalf("fixture reserve %q is not a decimal: %v", w.Reserves[j].Amount, err)
			}
			if !r.Amount.Equal(want) {
				t.Errorf("pool %s reserve %d: parsed %s, Horizon sent %s",
					p.ID, j, r.Amount, want)
			}
		}
	}
}

// Horizon sends every amount at seven decimal places; decimal.Decimal prints
// the normalised form. The values are equal and no precision is lost, but the
// strings differ, and anything that diffs a run file against a live Horizon
// response textually will report changes that did not happen.
//
// Pinned as a test rather than left as folklore, because the failure it causes
// is a confusing one: a reconciliation or a fixture refresh appears to show the
// chain moving when nothing moved at all.
func TestDecimalStringNormalisesTrailingZeros(t *testing.T) {
	cases := []struct{ wire, printed string }{
		{"2477.8894960", "2477.889496"},
		{"5.0000000", "5"},
		{"20000.0000000", "20000"},
		{"0.0000000", "0"},
		{"11718.7500000", "11718.75"},
		{"5494.2144063", "5494.2144063"}, // already minimal, unchanged
	}
	for _, tc := range cases {
		d, err := decimal.NewFromString(tc.wire)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.wire, err)
		}
		if got := d.String(); got != tc.printed {
			t.Errorf("decimal(%q).String() = %q, want %q", tc.wire, got, tc.printed)
		}
		// The value is unchanged, which is the part that matters.
		if !d.Equal(decimal.RequireFromString(tc.wire)) {
			t.Errorf("value changed for %q", tc.wire)
		}
	}
}

func TestPoolHoldersHarvestsEveryPosition(t *testing.T) {
	srv := serveFixture(t, fixture(t, "holders_page.json"))
	c := &Client{URL: srv.URL, MaxRetries: -1}

	accounts, requests, err := c.PoolHolders(context.Background(), "poolid")
	if err != nil {
		t.Fatalf("PoolHolders: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}
	// Two requests for one page of holders: Horizon always sends a next link,
	// so the walk only learns it has finished by fetching a page and finding it
	// empty. That second request is the cost of termination, not a bug.
	if requests != 2 {
		t.Errorf("requests=%d, want 2 (the page, then the empty page that ends the walk)", requests)
	}

	// The recorded account holds positions in many pools, not just the one
	// queried. Harvesting all of them is what makes the census affordable, so
	// a regression that returned only the queried pool must fail here.
	a := accounts[0]
	if len(a.Shares) < 2 {
		t.Fatalf("harvested %d positions from an account known to hold many", len(a.Shares))
	}
	t.Logf("one request yielded %d positions across %d pools", len(a.Shares), len(a.Shares))

	for _, s := range a.Shares {
		if s.AccountID != a.ID {
			t.Errorf("position attributed to %q, want %q", s.AccountID, a.ID)
		}
		if s.PoolID == "" {
			t.Error("position with no pool ID")
		}
		if s.Shares.IsNegative() {
			t.Errorf("negative share balance %s in pool %s", s.Shares, s.PoolID)
		}
	}
}

// Measured behaviour, not a hypothetical: the holders endpoint returned 503 on
// three consecutive attempts before answering on the fourth while these very
// fixtures were being recorded. A client that gives up on the first 503 cannot
// complete a census.
func TestRetriesThrough503(t *testing.T) {
	body := fixture(t, "pools_page.json")
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	c := &Client{URL: srv.URL, HTTPClient: &http.Client{Timeout: 5 * time.Second}}

	var seen int
	_, requests, err := c.ListPools(context.Background(), 1, func(p []Pool) error {
		seen += len(p)
		return nil
	})
	if err != nil {
		t.Fatalf("ListPools should have retried through the 503s: %v", err)
	}
	if seen != 3 {
		t.Errorf("got %d pools, want 3", seen)
	}
	// Four requests for one page of data — the count the manifest must record,
	// so that a run's true cost is visible rather than its page count.
	if requests != 4 {
		t.Errorf("requests=%d, want 4 (three 503s then a 200)", requests)
	}
}

func TestGivesUpAfterMaxRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	c := &Client{URL: srv.URL, MaxRetries: 2}
	_, requests, err := c.ListPools(context.Background(), 1, func([]Pool) error { return nil })
	if err == nil {
		t.Fatal("expected an error from a permanently unavailable endpoint")
	}
	var unavail *ErrUnavailable
	if !errors.As(err, &unavail) {
		t.Fatalf("error %v is not *ErrUnavailable; callers cannot distinguish transient from permanent", err)
	}
	if unavail.Status != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", unavail.Status)
	}
	if requests != 3 {
		t.Errorf("requests=%d, want 3 (initial attempt plus MaxRetries=2)", requests)
	}
}

// A 400 is a bug in the request, not a blip. Retrying it would loop on a
// permanent failure, so it must surface immediately and as something other than
// ErrUnavailable.
func TestDoesNotRetryClientErrors(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	c := &Client{URL: srv.URL}
	if _, _, err := c.ListPools(context.Background(), 1, func([]Pool) error { return nil }); err == nil {
		t.Fatal("expected an error on 400")
	}
	if calls != 1 {
		t.Errorf("server saw %d calls, want 1 — a 400 must not be retried", calls)
	}
}

func TestRetryAfterHeaderParsing(t *testing.T) {
	cases := []struct {
		header string
		want   time.Duration
	}{
		{"", 0},
		{"5", 5 * time.Second},
		{"0", 0},
		{"not-a-duration", 0},
		{"-3", 0},
	}
	for _, tc := range cases {
		resp := &http.Response{Header: http.Header{}}
		if tc.header != "" {
			resp.Header.Set("Retry-After", tc.header)
		}
		if got := retryAfter(resp); got != tc.want {
			t.Errorf("retryAfter(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestSanitizeDropsQuery(t *testing.T) {
	got := sanitize("https://horizon.stellar.org/accounts?liquidity_pool=abc&limit=100")
	want := "https://horizon.stellar.org/accounts"
	if got != want {
		t.Errorf("sanitize = %q, want %q", got, want)
	}
}

// A malformed amount must be an error, never a silent zero: 82 pools in the
// 2026-09-05 sample legitimately hold zero, so substituting zero for a parse
// failure would make a broken record indistinguishable from a real empty pool.
func TestUnparseableAmountIsAnError(t *testing.T) {
	for _, bad := range []string{"", "abc", "1.2.3"} {
		if _, err := parseAmount("test", bad); err == nil {
			t.Errorf("parseAmount(%q) returned no error", bad)
		}
	}
}

func TestPoolShapeIsNotGuessed(t *testing.T) {
	// A pool with one reserve is a shape this code has never seen. Taking the
	// first two anyway would invent a census row.
	r := rawPool{ID: "x", TotalShares: "1", TotalTrustlines: "1"}
	if _, err := r.toPool(); err == nil {
		t.Error("a pool with 0 reserves was accepted")
	}
}

// Regression: Horizon's next links are absolute and name whichever host served
// the request. Following them verbatim honours the client's URL for page one
// and silently ignores it thereafter.
//
// This is pinned by a unit test as well as by the offline CI job, because the
// offline job proves the suite does not reach the network while this proves
// *why*. A future change that reintroduces the bug should fail with a message
// that names the cause.
func TestNextLinksAreRebasedOntoConfiguredHost(t *testing.T) {
	c := &Client{URL: "http://127.0.0.1:8000"}

	got, err := c.rebase("https://horizon.stellar.org/accounts?cursor=GABC&limit=100&order=asc")
	if err != nil {
		t.Fatalf("rebase: %v", err)
	}
	want := "http://127.0.0.1:8000/accounts?cursor=GABC&limit=100&order=asc"
	if got != want {
		t.Errorf("rebase sent the walk to %q, want %q", got, want)
	}

	if got, err := c.rebase(""); err != nil || got != "" {
		t.Errorf("rebase(\"\") = %q, %v; want \"\", nil", got, err)
	}
}

// The page walk must issue every request to the configured host, not just the
// first. The fixture's own next link points at mainnet, so a client that does
// not rebase will miss this server on the second call.
func TestPageWalkNeverLeavesTheConfiguredHost(t *testing.T) {
	body := fixture(t, "holders_page.json")
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") != "" {
			_, _ = w.Write([]byte(`{"_links":{},"_embedded":{"records":[]}}`))
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	c := &Client{URL: srv.URL, MaxRetries: -1}
	_, requests, err := c.PoolHolders(context.Background(), "poolid")
	if err != nil {
		t.Fatalf("PoolHolders: %v", err)
	}
	if hits != requests {
		t.Errorf("client made %d requests but this server saw %d — %d went elsewhere",
			requests, hits, requests-hits)
	}
}
