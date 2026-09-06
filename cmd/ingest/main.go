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

	"github.com/shopspring/decimal"

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
		minNative  = flag.String("min-native", "0", "with -holders, only attribute pools holding at least this much XLM on a native leg")
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

	minNativeDec, err := decimal.NewFromString(*minNative)
	if err != nil {
		return fmt.Errorf("-min-native %q is not a decimal: %w", *minNative, err)
	}

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
			if attributable(p, minNativeDec) {
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
		stats, herr := attribute(ctx, client, w, log, eligible, pools, gateDescription(minNativeDec))
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

// gate describes the attribution predicate in words, for the manifest.
//
// A reader must be able to see what a run skipped without reading the code that
// skipped it, so the threshold is interpolated rather than described in the
// abstract.
func gateDescription(minNative decimal.Decimal) string {
	if minNative.IsZero() {
		return "total_trustlines > 1"
	}
	return "total_trustlines > 1 AND a native leg holding at least " + minNative.String() + " XLM"
}

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
func attributable(p horizon.Pool, minNative decimal.Decimal) bool {
	if p.TotalTrustlines <= 1 {
		return false
	}
	if minNative.IsZero() {
		return true
	}
	// The survey's in-scope filter, available as a flag because a full
	// attribution pass takes about seven hours and the population it is really
	// after is 208 pools. Restricting to those is minutes.
	//
	// XLM is the only denominator the ledger offers for cross-pool comparison,
	// so a pool without a native leg cannot pass a value threshold at all and
	// is excluded. That is a real limit of the filter, not a property of the
	// pools: a pool holding substantial value in two assets Gauge cannot price
	// is invisible here, which is why the survey calls 208 a floor.
	for _, r := range p.Reserves {
		if r.IsNative() && r.Amount.GreaterThanOrEqual(minNative) {
			return true
		}
	}
	return false
}

func attribute(
	ctx context.Context,
	client *horizon.Client,
	w *census.Writer,
	log *slog.Logger,
	eligible []string,
	totalPools int,
	gate string,
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

	// The pool row is re-read alongside its holders and written to
	// pools_at_attribution.jsonl. The census pass recorded these pools up to
	// several hours earlier, and on the run of 2026-09-06 five of them had
	// taken a deposit in between — their holder balances summed to more than
	// the total_shares on record. Both reads were correct; they described
	// different instants. A position's denominator has to come from the same
	// moment as its numerator, so it is fetched at that moment.
	var reread int

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

		// Fetched before the holder list rather than after, so that any
		// deposit landing mid-pool-walk shows up as holders exceeding shares
		// — a direction the reconciliation already knows how to read — rather
		// than the reverse, which would look like missing holders.
		p, preqs, perr := client.Pool(ctx, poolID)
		stats.Requests += preqs
		if perr == nil {
			if werr := w.WritePoolsAtAttribution([]horizon.Pool{p}); werr != nil {
				return stats, werr
			}
			reread++
		} else {
			log.Warn("pool state not re-read at attribution time", "pool", poolID, "err", perr)
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

	stats.PoolsReread = reread

	log.Info("attribution complete",
		"attributed", stats.PoolsAttributed,
		"reread", reread,
		"failed", len(stats.PoolsFailed),
		"positions", stats.Positions,
		"accounts", stats.Accounts,
		"requests", stats.Requests)
	return stats, nil
}
