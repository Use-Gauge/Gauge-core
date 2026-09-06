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
const gate = "total_trustlines > 1"

// attributable decides whether a pool is worth an attribution request.
//
// The first version of this gate — more than one trustline OR either reserve
// non-zero — admitted 39,405 of the 39,833 pools in the census of 2026-09-06.
// It was honest and it was useless: nearly every single-holder pool does hold
// reserves, so the disjunction let almost the whole population through and the
// run still cost one request per pool against the endpoint that returns 503
// under load.
//
// The gate is now trustlines alone, on a stronger argument than cost. In a pool
// with exactly one trustline the position is already fully determined by the
// census: one account holds 100% of the shares, so its claim is the entire
// reserve of both assets. Fetching the holder list adds the account's public
// key and nothing else — no share balance that was not already implied, no
// composition that was not already known. 34,402 pools (86.37%) are in that
// state, and spending 34,402 requests on an unreliable endpoint to learn 34,402
// account IDs is not a trade this census should make.
//
// Where there are two or more trustlines the split between holders is genuinely
// unknown and cannot be derived from pool state at all. Those 5,431 pools are
// the only ones where the request buys information.
//
// Single-holder pools are not abandoned: an account fetched for one pool
// reports every pool it holds shares in, so many of them arrive anyway as a
// side effect. The manifest records how many.
func attributable(p horizon.Pool) bool {
	return p.TotalTrustlines > 1
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
