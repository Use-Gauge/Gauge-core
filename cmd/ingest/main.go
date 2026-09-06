// Command ingest reads Stellar mainnet liquidity-pool and liquidity-position
// state from Horizon and writes it to a run directory.
//
// The pool census is cheap: one request per 200 pools. Position attribution is
// not — it costs one request per eligible pool against an endpoint that returns
// 503 under load — so it is opt-in and gated, and the manifest records exactly
// which pools were attributed and which were not.
//
//	ingest -out data/runs                 # census only
//	ingest -out data/runs -holders        # census plus position attribution
//	ingest -out data/runs -max-pages 2    # a small sample, for smoke testing
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Use-Gauge/Gauge-core/pkg/census"
	"github.com/Use-Gauge/Gauge-core/pkg/horizon"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		horizonURL = flag.String("horizon", horizon.DefaultURL, "Horizon instance to read from")
		out        = flag.String("out", "data/runs", "parent directory for the run")
		maxPages   = flag.Int("max-pages", 0, "stop after N pages of pools (0 = the whole listing)")
		holders    = flag.Bool("holders", false, "also attribute positions per pool (slow, one request per eligible pool)")
		verbose    = flag.Bool("v", false, "log every retry")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	// Ctrl-C must still write a manifest. A run abandoned halfway with no
	// manifest is indistinguishable from a corrupt one, and a partial census
	// that says how partial it is remains useful.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	started := time.Now().UTC()
	dir := filepath.Join(*out, started.Format("20060102T150405Z"))
	w, err := census.NewWriter(dir)
	if err != nil {
		return err
	}

	client := &horizon.Client{URL: *horizonURL, Logger: log}
	m := census.Manifest{
		Tool:       "cmd/ingest",
		HorizonURL: *horizonURL,
		StartedAt:  started,
		MaxPages:   *maxPages,
	}

	log.Info("census starting", "horizon", *horizonURL, "run", dir)

	// eligible collects the pools that qualify for attribution during the
	// census walk, so the holder pass does not need to re-read pools.jsonl.
	var eligible []string
	var pools int

	pages, requests, err := client.ListPools(ctx, *maxPages, func(page []horizon.Pool) error {
		if err := w.WritePools(page); err != nil {
			return err
		}
		for _, p := range page {
			pools++
			if p.LastModifiedLedger > m.LedgerHigh {
				m.LedgerHigh = p.LastModifiedLedger
			}
			if m.LedgerLow == 0 || p.LastModifiedLedger < m.LedgerLow {
				m.LedgerLow = p.LastModifiedLedger
			}
			if attributable(p) {
				eligible = append(eligible, p.ID)
			}
		}
		if pools%2000 == 0 {
			log.Info("census progress", "pools", pools, "eligible", len(eligible))
		}
		return nil
	})
	m.Pools, m.Pages, m.Requests = pools, pages, requests

	// A cancelled or failed census still gets a manifest describing how far it
	// got, then reports the failure.
	if err != nil {
		m.FinishedAt = time.Now().UTC()
		if cerr := w.Close(m); cerr != nil {
			log.Error("writing manifest for a failed run", "err", cerr)
		}
		return fmt.Errorf("census: %w", err)
	}

	log.Info("census complete",
		"pools", pools, "pages", pages, "requests", requests,
		"eligible_for_attribution", len(eligible))

	if *holders {
		stats, herr := attribute(ctx, client, w, log, eligible, pools)
		m.Holders = stats
		if herr != nil {
			m.FinishedAt = time.Now().UTC()
			if cerr := w.Close(m); cerr != nil {
				log.Error("writing manifest for a failed run", "err", cerr)
			}
			return fmt.Errorf("attribution: %w", herr)
		}
	}

	m.FinishedAt = time.Now().UTC()
	if err := w.Close(m); err != nil {
		return err
	}
	fmt.Println(dir)
	return nil
}

// gate is the attribution predicate, in words, for the manifest.
const gate = "total_trustlines > 1 OR either reserve non-zero"

// attributable decides whether a pool is worth an attribution request.
//
// 86.1% of the pools in the 2026-09-05 sample had exactly one trustline, and a
// single-holder pool holding nothing has no position worth measuring: the sole
// holder owns 100% of nothing. Spending a request on each of those — against
// the endpoint that already returns 503 under load — would multiply the run's
// cost and its failure surface for rows that carry no information.
//
// A single-holder pool that does hold reserves is still attributed, because
// "one account has an undiversified position of real size" is exactly the kind
// of thing this project exists to measure.
func attributable(p horizon.Pool) bool {
	return p.TotalTrustlines > 1 || !p.IsDrained()
}

func attribute(
	ctx context.Context,
	client *horizon.Client,
	w *census.Writer,
	log *slog.Logger,
	eligible []string,
	totalPools int,
) (*census.HolderStats, error) {
	stats := &census.HolderStats{
		Gate:          gate,
		PoolsEligible: len(eligible),
		PoolsSkipped:  totalPools - len(eligible),
		PoolsFailed:   []string{},
	}

	// One account's record names every pool it holds shares in — the recorded
	// fixture has an account in 167 of them. Writing a position the first time
	// it is seen and skipping it thereafter means the run discovers far more
	// positions than it makes requests for, and never writes the same position
	// twice from two different pools' holder lists.
	seenAccount := make(map[string]bool)

	log.Info("attribution starting", "eligible", len(eligible), "skipped", stats.PoolsSkipped, "gate", gate)

	for i, poolID := range eligible {
		if ctx.Err() != nil {
			log.Warn("attribution interrupted", "completed", i, "remaining", len(eligible)-i)
			// The pools never reached are recorded as failures rather than
			// quietly omitted: an interrupted run must not read as a complete
			// one.
			stats.PoolsFailed = append(stats.PoolsFailed, eligible[i:]...)
			return stats, nil
		}

		accounts, reqs, err := client.PoolHolders(ctx, poolID)
		stats.Requests += reqs
		if err != nil {
			// A pool that cannot be fetched is recorded and the run continues.
			// Aborting a multi-thousand-request census because one endpoint
			// blinked would make the census impossible to complete.
			log.Warn("pool not attributed", "pool", poolID, "err", err)
			stats.PoolsFailed = append(stats.PoolsFailed, poolID)
			continue
		}
		stats.PoolsAttributed++

		var fresh []horizon.PoolShare
		for _, a := range accounts {
			if seenAccount[a.ID] {
				continue
			}
			seenAccount[a.ID] = true
			fresh = append(fresh, a.Shares...)
		}
		if len(fresh) > 0 {
			if err := w.WritePositions(fresh); err != nil {
				return stats, err
			}
			stats.Positions += len(fresh)
		}
		stats.Accounts = len(seenAccount)

		if (i+1)%250 == 0 {
			log.Info("attribution progress",
				"pools", i+1, "of", len(eligible),
				"positions", stats.Positions, "accounts", stats.Accounts,
				"failed", len(stats.PoolsFailed))
		}
	}

	log.Info("attribution complete",
		"attributed", stats.PoolsAttributed,
		"failed", len(stats.PoolsFailed),
		"positions", stats.Positions,
		"accounts", stats.Accounts,
		"requests", stats.Requests)
	return stats, nil
}
