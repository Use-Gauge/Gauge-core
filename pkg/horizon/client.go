// Package horizon reads Stellar mainnet liquidity-pool and liquidity-position
// state from Horizon.
//
// # Why hand-rolled JSON rather than the Stellar Go SDK
//
// Gauge reaches exactly two Horizon endpoints: the pool listing and the
// accounts-filtered-by-pool listing. The SDK brings a large dependency tree to
// cover an API surface Gauge does not use, and — more importantly — its
// generated structs type monetary fields as strings that callers are expected
// to parse themselves anyway. Parsing them straight into decimal.Decimal here
// removes the intermediate step where a float could be introduced by accident.
//
// # Why every monetary field is decimal.Decimal
//
// Horizon reports amounts as decimal strings with seven places, which is exactly
// how Stellar stores them: signed 64-bit integers of 1e-7 units. Those values
// are exact. Parsing "223984091.6909237" into a float64 is not — it lands on the
// nearest representable double and silently stops being the ledger's number.
// For a fraud score that error is irrelevant; for a figure presented as a
// position's profit and loss it is the whole ballgame. So the exact type is
// used from the first parse, and no float appears anywhere in this package.
package horizon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// DefaultURL is SDF's public mainnet Horizon instance.
const DefaultURL = "https://horizon.stellar.org"

// Client queries Horizon. The zero value is usable and talks to mainnet.
type Client struct {
	// URL overrides the Horizon instance. Empty means DefaultURL.
	URL string

	// HTTPClient overrides the HTTP client. Nil means a client with a 30s
	// timeout.
	HTTPClient *http.Client

	// Logger is the structured logger for upstream calls. Nil means
	// slog.Default().
	Logger *slog.Logger

	// MaxRetries bounds retries of a single request against the transient
	// failures described on ErrUnavailable. Zero means DefaultMaxRetries.
	// Negative disables retrying, which is what the tests want.
	MaxRetries int
}

// DefaultMaxRetries is deliberately small. Horizon's 503 on the accounts
// endpoint clears on the immediate next attempt in observation; a long retry
// ladder would disguise a genuine outage as slowness.
const DefaultMaxRetries = 4

func (c *Client) baseURL() string {
	if c.URL != "" {
		return strings.TrimRight(c.URL, "/")
	}
	return DefaultURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func (c *Client) maxRetries() int {
	switch {
	case c.MaxRetries < 0:
		return 0
	case c.MaxRetries == 0:
		return DefaultMaxRetries
	default:
		return c.MaxRetries
	}
}

// ErrUnavailable reports that Horizon refused a request in a way that is worth
// trying again: HTTP 429 (rate limited) or 503 (service unavailable).
//
// The two are kept in one type because the caller's remedy is identical — wait
// and ask again — but they are distinguished from every other failure because
// the remedy for those is not. A 400 means the request is wrong and no amount
// of waiting fixes it. Collapsing the two would turn a permanent bug into an
// infinite retry loop.
//
// The 503 case is not hypothetical. GET /accounts?liquidity_pool=<id> returned
// 503 on first call and 200 on the immediate retry, repeatedly, during the
// survey of 2026-09-05. It is the normal behaviour of that endpoint under load,
// not an outage.
type ErrUnavailable struct {
	Endpoint string
	Status   int

	// RetryAfter is what Horizon's Retry-After header asked for. Zero when the
	// header was absent or unparseable — reported rather than guessed.
	RetryAfter time.Duration
}

func (e *ErrUnavailable) Error() string {
	msg := fmt.Sprintf("horizon: %s returned %d", e.Endpoint, e.Status)
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf("; retry after %s", e.RetryAfter)
	}
	return msg
}

// retryAfter reads the Retry-After header, which Horizon is free not to send.
// A missing or unparseable value reports zero rather than a guess.
//
// Ported from Wayfare's transport.RetryAfter, which this project's sibling has
// been running against the same upstream for months.
func retryAfter(resp *http.Response) time.Duration {
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// sanitize strips the query string from a URL for logging. Horizon URLs carry
// no secrets today, but a logged URL that grows one later is a leak nobody
// notices, and the endpoint path is the only part worth reading in a log line.
func sanitize(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "?"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// backoff returns the wait before attempt n (0-indexed), honouring Horizon's
// own Retry-After when it sent one.
//
// The schedule is fixed rather than exponential-with-jitter. The failure being
// retried clears within a second in observation, so the elaborate version would
// add latency without adding reliability, and a fixed ladder is one that can be
// reasoned about from the log timestamps.
func backoff(attempt int, asked time.Duration) time.Duration {
	if asked > 0 {
		return asked
	}
	schedule := []time.Duration{
		250 * time.Millisecond,
		1 * time.Second,
		3 * time.Second,
		8 * time.Second,
	}
	if attempt >= len(schedule) {
		return schedule[len(schedule)-1]
	}
	return schedule[attempt]
}

// getJSON fetches url and decodes the body into out, retrying the transient
// statuses described on ErrUnavailable.
//
// It reports the number of HTTP requests actually made, including failed
// attempts. The manifest of an ingest run records that count, because "how many
// requests did this survey cost" is a question the survey should be able to
// answer about itself.
func (c *Client) getJSON(ctx context.Context, raw string, out any) (requests int, err error) {
	max := c.maxRetries()

	for attempt := 0; ; attempt++ {
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if rerr != nil {
			return requests, fmt.Errorf("horizon: build request: %w", rerr)
		}
		req.Header.Set("Accept", "application/json")

		resp, derr := c.httpClient().Do(req)
		requests++
		if derr != nil {
			return requests, fmt.Errorf("horizon: get %s: %w", sanitize(raw), derr)
		}

		switch {
		case resp.StatusCode == http.StatusOK:
			derr := json.NewDecoder(resp.Body).Decode(out)
			resp.Body.Close()
			if derr != nil {
				return requests, fmt.Errorf("horizon: decode %s: %w", sanitize(raw), derr)
			}
			return requests, nil

		case resp.StatusCode == http.StatusTooManyRequests,
			resp.StatusCode == http.StatusServiceUnavailable:
			unavail := &ErrUnavailable{
				Endpoint:   sanitize(raw),
				Status:     resp.StatusCode,
				RetryAfter: retryAfter(resp),
			}
			resp.Body.Close()

			if attempt >= max {
				return requests, unavail
			}
			wait := backoff(attempt, unavail.RetryAfter)
			c.logger().Debug("horizon retrying",
				"endpoint", unavail.Endpoint,
				"status", unavail.Status,
				"attempt", attempt+1,
				"wait", wait)

			select {
			case <-ctx.Done():
				return requests, ctx.Err()
			case <-time.After(wait):
			}

		default:
			resp.Body.Close()
			return requests, fmt.Errorf("horizon: %s returned %d", sanitize(raw), resp.StatusCode)
		}
	}
}

// ErrNoMorePages is returned by page-walking helpers when Horizon's next link
// leads nowhere new.
var ErrNoMorePages = errors.New("horizon: no more pages")

// mustDecimal parses a Horizon amount string.
//
// An unparseable amount is an error rather than a zero. Zero is a meaningful
// value here — 82 pools in the 2026-09-05 sample genuinely hold nothing — so
// silently substituting it for a parse failure would put a fabricated number
// into a position's accounting, which is precisely what this project refuses to
// do.
func parseAmount(field, s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, fmt.Errorf("horizon: %s is empty", field)
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, fmt.Errorf("horizon: %s %q: %w", field, s, err)
	}
	return d, nil
}

// rebase rewrites a Horizon pagination link so it points at this client's
// configured host.
//
// Horizon's `next` links are absolute and name the host that served the
// request — which, for a response recorded from mainnet and replayed by a test
// server, is `https://horizon.stellar.org`. Following them verbatim means the
// URL field is honoured for the first page and quietly ignored for every page
// after it.
//
// This was found by running the suite in a network namespace with no route
// out: the tests served page one from a local httptest server and then dialled
// live mainnet for page two. They had been passing because the machine
// happened to have connectivity. In production the same bug makes `-horizon`
// a lie past the first page, and points a run at SDF's instance no matter what
// the operator asked for.
//
// Only the path and query are taken from the link. Anything else in it is the
// upstream's opinion about where this client should go next, which is not a
// question the upstream gets to answer.
func (c *Client) rebase(href string) (string, error) {
	if href == "" {
		return "", nil
	}
	u, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("horizon: unusable next link %q: %w", href, err)
	}
	return c.baseURL() + u.EscapedPath() + "?" + u.RawQuery, nil
}
