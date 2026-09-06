// Package census writes an ingest run to disk as newline-delimited JSON plus a
// manifest.
//
// # Why runs are snapshotted rather than streamed into metrics
//
// A metric computed directly from a live fetch is not reproducible: Horizon's
// answer changes between ledgers, so the number cannot be checked later and two
// people running the same command get different results with no way to tell
// whether the code or the chain moved. Every figure Gauge publishes has to be
// traceable to bytes that still exist.
//
// So ingestion writes a run, and everything downstream reads a run. The
// manifest records what was asked, what came back, and what did not — including
// the failures, which are the part a summary is most tempted to drop.
package census

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Use-Gauge/Gauge-core/pkg/horizon"
)

// Manifest describes one ingest run. It is written last, so its presence in a
// run directory means the run finished.
type Manifest struct {
	// Tool identifies what produced the run, so a document citing it can be
	// re-derived after the code has moved on.
	Tool       string `json:"tool"`
	HorizonURL string `json:"horizon_url"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	DurationS  int64     `json:"duration_seconds"`

	// Pools counts what was written to pools.jsonl.
	Pools int `json:"pools"`

	// Pages and Requests differ whenever a request was retried. Both are
	// recorded because "what did this run cost" is a question the run should
	// answer about itself, and page count alone hides retries.
	Pages    int `json:"pages"`
	Requests int `json:"requests"`

	// MaxPages echoes the cap that was applied. Zero means the walk ran to the
	// end of the listing; any other value means the census is a prefix of the
	// population and must not be described as complete.
	MaxPages int `json:"max_pages"`

	// LedgerHigh is the highest last_modified_ledger seen. It is the closest
	// thing to an "as of" for a run assembled from thousands of separate
	// requests over several minutes — the state is not a single instant, and
	// saying so is more honest than stamping one timestamp on it.
	LedgerHigh int `json:"ledger_high"`
	LedgerLow  int `json:"ledger_low"`

	// Holders records the attribution pass. Nil when it did not run.
	Holders *HolderStats `json:"holders,omitempty"`

	// Trades records the price-history pass. Nil when it did not run.
	Trades *TradeStats `json:"trades,omitempty"`
}

// HolderStats describes the position-attribution pass.
type HolderStats struct {
	// Gate is the predicate that decided which pools were attributed, written
	// out in words. Attribution is the expensive, fragile part of a run, so a
	// reader must be able to see what was skipped without reading the code
	// that skipped it.
	Gate string `json:"gate"`

	PoolsEligible   int `json:"pools_eligible"`
	PoolsAttributed int `json:"pools_attributed"`
	PoolsSkipped    int `json:"pools_skipped"`

	// PoolsFailed lists pools that were eligible but could not be fetched
	// after retries, by ID. Enumerated rather than counted: a failure that is
	// only a number cannot be investigated or retried, and a census with
	// unexplained holes is worse than one with labelled holes.
	PoolsFailed []string `json:"pools_failed"`

	Positions int `json:"positions"`
	Accounts  int `json:"accounts"`
	Requests  int `json:"requests"`

	// PoolsReread counts pools whose state was re-read at the moment their
	// holders were fetched, into pools_at_attribution.jsonl.
	//
	// A census assembled over hours is not a snapshot: the run of 2026-09-06
	// read pool rows in its first minute and some holder lists 412 minutes
	// later, and five pools moved in between. Reconciliation and any
	// share-of-pool arithmetic must use the contemporaneous row, not the
	// census row. Where this is lower than PoolsAttributed, the difference is
	// pools whose re-read failed and which therefore only have census-era
	// state.
	PoolsReread int `json:"pools_reread"`
}

// TradeStats describes the price-history pass.
type TradeStats struct {
	// MaxPages bounds each pool's walk. Recorded because it determines how far
	// back the series reaches, and a volatility figure means nothing without
	// knowing the window it covers.
	MaxPages int `json:"max_pages_per_pool"`

	Pools       int      `json:"pools"`
	Trades      int      `json:"trades"`
	Requests    int      `json:"requests"`
	PoolsFailed []string `json:"pools_failed"`

	// Earliest and Latest bound the observed window across all pools.
	Earliest string `json:"earliest_close_time"`
	Latest   string `json:"latest_close_time"`
}

// Writer accumulates a run in a directory.
type Writer struct {
	dir      string
	pools    *os.File
	poolsEnc *json.Encoder
	posts    *os.File
	postsEnc *json.Encoder
	atAttr   *os.File
	atAttrEn *json.Encoder
	trades   *os.File
	tradesEn *json.Encoder
}

// NewWriter creates the run directory and opens its files.
func NewWriter(dir string) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("census: create run dir: %w", err)
	}
	pf, err := os.Create(filepath.Join(dir, "pools.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("census: create pools.jsonl: %w", err)
	}
	return &Writer{dir: dir, pools: pf, poolsEnc: json.NewEncoder(pf)}, nil
}

// Dir reports the run directory.
func (w *Writer) Dir() string { return w.dir }

// WritePools appends pools to pools.jsonl.
func (w *Writer) WritePools(pools []horizon.Pool) error {
	for _, p := range pools {
		if err := w.poolsEnc.Encode(p); err != nil {
			return fmt.Errorf("census: write pool %s: %w", p.ID, err)
		}
	}
	return nil
}

// WritePositions appends positions to positions.jsonl, creating it on first
// use. The file is absent from runs that did no attribution, rather than
// present and empty — an empty file and a pass that never ran are different
// facts and should not look alike on disk.
func (w *Writer) WritePositions(shares []horizon.PoolShare) error {
	if w.posts == nil {
		f, err := os.Create(filepath.Join(w.dir, "positions.jsonl"))
		if err != nil {
			return fmt.Errorf("census: create positions.jsonl: %w", err)
		}
		w.posts = f
		w.postsEnc = json.NewEncoder(f)
	}
	for _, s := range shares {
		if err := w.postsEnc.Encode(s); err != nil {
			return fmt.Errorf("census: write position %s/%s: %w", s.AccountID, s.PoolID, err)
		}
	}
	return nil
}

// WritePoolsAtAttribution appends pool state as read at attribution time to
// pools_at_attribution.jsonl.
//
// Kept in a separate file from pools.jsonl rather than overwriting it, because
// the two are different measurements and the gap between them is itself data:
// comparing the files shows exactly which pools moved during a run and by how
// much.
func (w *Writer) WritePoolsAtAttribution(pools []horizon.Pool) error {
	if w.atAttr == nil {
		f, err := os.Create(filepath.Join(w.dir, "pools_at_attribution.jsonl"))
		if err != nil {
			return fmt.Errorf("census: create pools_at_attribution.jsonl: %w", err)
		}
		w.atAttr = f
		w.atAttrEn = json.NewEncoder(f)
	}
	for _, p := range pools {
		if err := w.atAttrEn.Encode(p); err != nil {
			return fmt.Errorf("census: write attribution-time pool %s: %w", p.ID, err)
		}
	}
	return nil
}

// WriteTrades appends price observations to trades.jsonl.
//
// A census gives one price per pool — a single point, from which no variance
// and no drawdown can be computed. Trades are where the series comes from.
func (w *Writer) WriteTrades(trades []horizon.Trade) error {
	if w.trades == nil {
		f, err := os.Create(filepath.Join(w.dir, "trades.jsonl"))
		if err != nil {
			return fmt.Errorf("census: create trades.jsonl: %w", err)
		}
		w.trades = f
		w.tradesEn = json.NewEncoder(f)
	}
	for _, t := range trades {
		if err := w.tradesEn.Encode(t); err != nil {
			return fmt.Errorf("census: write trade %s: %w", t.ID, err)
		}
	}
	return nil
}

// Close writes the manifest and closes the run's files. A run directory without
// manifest.json is an interrupted run, and downstream readers should treat it
// as such.
func (w *Writer) Close(m Manifest) error {
	if w.pools != nil {
		if err := w.pools.Close(); err != nil {
			return fmt.Errorf("census: close pools.jsonl: %w", err)
		}
	}
	if w.posts != nil {
		if err := w.posts.Close(); err != nil {
			return fmt.Errorf("census: close positions.jsonl: %w", err)
		}
	}
	if w.atAttr != nil {
		if err := w.atAttr.Close(); err != nil {
			return fmt.Errorf("census: close pools_at_attribution.jsonl: %w", err)
		}
	}
	if w.trades != nil {
		if err := w.trades.Close(); err != nil {
			return fmt.Errorf("census: close trades.jsonl: %w", err)
		}
	}

	m.DurationS = int64(m.FinishedAt.Sub(m.StartedAt) / time.Second)

	// PoolsFailed is enumerated in the manifest, and a nil slice would encode
	// as null. An explicit empty list says "the attribution pass ran and
	// nothing failed"; null says nothing at all.
	if m.Holders != nil && m.Holders.PoolsFailed == nil {
		m.Holders.PoolsFailed = []string{}
	}
	if m.Trades != nil && m.Trades.PoolsFailed == nil {
		m.Trades.PoolsFailed = []string{}
	}

	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("census: encode manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(w.dir, "manifest.json"), append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("census: write manifest: %w", err)
	}
	return nil
}
