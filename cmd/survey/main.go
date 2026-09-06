// Command survey reads an ingest run and reports the population characteristics
// that docs/data-survey.md is written from.
//
// It computes nothing that resembles a risk metric. Its job is to describe what
// is in the data — how many pools, how concentrated, how much is empty, what is
// missing — so that later metrics are built on a population somebody has
// actually looked at.
//
//	survey data/runs/20260906T063358Z
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Use-Gauge/Gauge-core/pkg/census"
	"github.com/Use-Gauge/Gauge-core/pkg/horizon"
	"github.com/shopspring/decimal"
)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: survey <run-directory>")
		os.Exit(2)
	}
	if err := run(flag.Arg(0)); err != nil {
		fmt.Fprintf(os.Stderr, "survey: %v\n", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	pools, err := readPools(filepath.Join(dir, "pools.jsonl"))
	if err != nil {
		return err
	}
	if len(pools) == 0 {
		return fmt.Errorf("no pools in %s", dir)
	}

	fmt.Printf("run: %s\n", dir)
	fmt.Printf("pools: %d\n\n", len(pools))

	reportUniformity(pools)
	reportHolders(pools)
	reportEmptiness(pools)
	reportAssets(pools)
	reportActivity(pools)
	reportSharePrecision(pools)

	if positions, err := readPositions(filepath.Join(dir, "positions.jsonl")); err == nil {
		reportPositions(pools, positions)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func readPools(path string) ([]horizon.Pool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var pools []horizon.Pool
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		var p horizon.Pool
		if err := json.Unmarshal(sc.Bytes(), &p); err != nil {
			return nil, fmt.Errorf("parse pool: %w", err)
		}
		pools = append(pools, p)
	}
	return pools, sc.Err()
}

func readPositions(path string) ([]horizon.PoolShare, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []horizon.PoolShare
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		var s horizon.PoolShare
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			return nil, fmt.Errorf("parse position: %w", err)
		}
		out = append(out, s)
	}
	return out, sc.Err()
}

func section(name string) { fmt.Printf("== %s ==\n", name) }

func reportUniformity(pools []horizon.Pool) {
	section("uniformity")
	types := map[string]int{}
	fees := map[int]int{}
	for _, p := range pools {
		types[p.Type]++
		fees[p.FeeBP]++
	}
	fmt.Printf("  types: %v\n", types)
	fmt.Printf("  fee_bp: %v\n\n", fees)
}

func reportHolders(pools []horizon.Pool) {
	section("trustline counts (Horizon's holder proxy)")
	counts := make([]int, 0, len(pools))
	dist := map[int]int{}
	total := 0
	for _, p := range pools {
		counts = append(counts, p.TotalTrustlines)
		dist[p.TotalTrustlines]++
		total += p.TotalTrustlines
	}
	sort.Ints(counts)

	fmt.Printf("  min %d  median %d  p90 %d  p99 %d  max %d\n",
		counts[0], counts[len(counts)/2], counts[len(counts)*90/100],
		counts[len(counts)*99/100], counts[len(counts)-1])
	fmt.Printf("  total trustlines across all pools: %d\n", total)
	for _, n := range []int{1, 2, 3} {
		fmt.Printf("  pools with exactly %d: %6d (%5s%%)\n", n, dist[n], pct(dist[n], len(pools)))
	}
	for _, threshold := range []int{2, 5, 10, 100} {
		c := 0
		for _, p := range pools {
			if p.TotalTrustlines >= threshold {
				c++
			}
		}
		fmt.Printf("  pools with >= %3d:  %6d (%5s%%)\n", threshold, c, pct(c, len(pools)))
	}
	fmt.Println()
}

func reportEmptiness(pools []horizon.Pool) {
	section("emptiness")
	var drained, halfEmpty, zeroShares, sharesButNoReserves int
	for _, p := range pools {
		switch {
		case p.IsDrained():
			drained++
		case p.Reserves[0].Amount.IsZero() || p.Reserves[1].Amount.IsZero():
			halfEmpty++
		}
		if p.TotalShares.IsZero() {
			zeroShares++
		}
		if !p.TotalShares.IsZero() && p.IsDrained() {
			sharesButNoReserves++
		}
	}
	fmt.Printf("  both reserves zero:            %6d (%5s%%)\n", drained, pct(drained, len(pools)))
	fmt.Printf("  exactly one reserve zero:      %6d (%5s%%)\n", halfEmpty, pct(halfEmpty, len(pools)))
	fmt.Printf("  zero total_shares:             %6d (%5s%%)\n", zeroShares, pct(zeroShares, len(pools)))
	fmt.Printf("  shares outstanding, no reserves: %4d  <- claims on nothing\n\n", sharesButNoReserves)
}

func reportAssets(pools []horizon.Pool) {
	section("assets")
	assetPools := map[string]int{}
	var withNative int
	pairs := map[string]int{}
	for _, p := range pools {
		if p.Reserves[0].IsNative() || p.Reserves[1].IsNative() {
			withNative++
		}
		for _, r := range p.Reserves {
			assetPools[r.Asset]++
		}
		pairs[p.Pair()]++
	}
	fmt.Printf("  distinct assets: %d\n", len(assetPools))
	fmt.Printf("  distinct pairs:  %d\n", len(pairs))
	fmt.Printf("  pools with an XLM leg: %d (%s%%)\n", withNative, pct(withNative, len(pools)))

	dupes := 0
	for _, n := range pairs {
		if n > 1 {
			dupes += n
		}
	}
	fmt.Printf("  pools sharing a pair with another pool: %d\n", dupes)

	type kv struct {
		asset string
		n     int
	}
	top := make([]kv, 0, len(assetPools))
	for a, n := range assetPools {
		top = append(top, kv{a, n})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n })
	fmt.Println("  most-paired assets:")
	for i := 0; i < 8 && i < len(top); i++ {
		fmt.Printf("    %-58s %5d pools\n", truncate(top[i].asset, 58), top[i].n)
	}
	fmt.Println()
}

func reportActivity(pools []horizon.Pool) {
	section("activity (last_modified_ledger)")
	ledgers := make([]int, 0, len(pools))
	for _, p := range pools {
		ledgers = append(ledgers, p.LastModifiedLedger)
	}
	sort.Ints(ledgers)
	high := ledgers[len(ledgers)-1]
	fmt.Printf("  oldest %d  median %d  newest %d\n", ledgers[0], ledgers[len(ledgers)/2], high)
	fmt.Printf("  span: %d ledgers\n", high-ledgers[0])

	// Roughly 5s per ledger on Stellar, so 17,280 ledgers is about a day.
	// Approximate and labelled as such; the ledger numbers above are exact.
	const perDay = 17280
	for _, days := range []int{1, 7, 30, 90, 365} {
		c := 0
		for _, l := range ledgers {
			if high-l <= days*perDay {
				c++
			}
		}
		fmt.Printf("  touched within ~%3dd: %6d (%5s%%)\n", days, c, pct(c, len(pools)))
	}
	fmt.Println()
}

// reportSharePrecision looks for pools where the share supply is so small that
// a position in them cannot be expressed at Stellar's 7-decimal resolution.
func reportSharePrecision(pools []horizon.Pool) {
	section("share supply")
	one := decimal.NewFromInt(1)
	var tiny, huge int
	big := decimal.RequireFromString("1000000000000")
	supplies := make([]decimal.Decimal, 0, len(pools))
	for _, p := range pools {
		if p.TotalShares.LessThan(one) && !p.TotalShares.IsZero() {
			tiny++
		}
		if p.TotalShares.GreaterThan(big) {
			huge++
		}
		supplies = append(supplies, p.TotalShares)
	}
	sort.Slice(supplies, func(i, j int) bool { return supplies[i].LessThan(supplies[j]) })
	fmt.Printf("  min %s\n  median %s\n  max %s\n",
		supplies[0], supplies[len(supplies)/2], supplies[len(supplies)-1])
	fmt.Printf("  supply < 1 unit:  %d\n", tiny)
	fmt.Printf("  supply > 1e12:    %d\n\n", huge)
}

func reportPositions(pools []horizon.Pool, positions []horizon.PoolShare) {
	section("positions")

	byPool := map[string][]horizon.PoolShare{}
	accounts := map[string]int{}
	var zero int
	for _, s := range positions {
		byPool[s.PoolID] = append(byPool[s.PoolID], s)
		accounts[s.AccountID]++
		if s.Shares.IsZero() {
			zero++
		}
	}
	fmt.Printf("  positions: %d\n", len(positions))
	fmt.Printf("  distinct accounts: %d\n", len(accounts))
	fmt.Printf("  distinct pools covered: %d of %d (%s%%)\n",
		len(byPool), len(pools), pct(len(byPool), len(pools)))
	fmt.Printf("  positions with a zero share balance: %d (%s%%)  <- trustline without a position\n",
		zero, pct(zero, len(positions)))

	held := make([]int, 0, len(accounts))
	for _, n := range accounts {
		held = append(held, n)
	}
	sort.Ints(held)
	fmt.Printf("  positions per account: min %d  median %d  p99 %d  max %d\n",
		held[0], held[len(held)/2], held[len(held)*99/100], held[len(held)-1])

	// The share-supply reconciliation lives in pkg/census so it is tested
	// against constructed cases — including a one-stroop shortfall and a supply
	// too large for a float64 — rather than only against whatever the live
	// network happened to return.
	r := census.Reconcile(pools, positions)
	fmt.Printf("  share reconciliation: %d/%d pools sum exactly to total_shares\n", r.Exact, r.Checked)
	for i, m := range r.Mismatches {
		if i >= 5 {
			fmt.Printf("    ...and %d more\n", len(r.Mismatches)-5)
			break
		}
		fmt.Printf("    mismatch %s: holders sum %s, total_shares %s, diff %s\n",
			m.PoolID[:12], m.HolderSum, m.TotalShares, m.Difference)
	}

	fmt.Println()
}

// pct renders n/total as a percentage string with two decimal places.
//
// It returns a string rather than a number, and computes in decimal rather than
// float, because the no-floats CI job caught the float version and the right
// response to that was to fix the code rather than to carve out an exemption.
//
// The exemption would have been defensible on its own terms — this is a
// percentage of pool counts, not money, and it is only ever printed. But the
// value of an absolute rule is that nobody has to adjudicate cases, and the
// first exemption is what turns "no floats in the accounting path" into "no
// floats except where someone argued otherwise". The conversion cost about
// four lines.
func pct(n, total int) string {
	if total == 0 {
		return "0.00"
	}
	return decimal.NewFromInt(int64(n)).
		Mul(decimal.NewFromInt(100)).
		Div(decimal.NewFromInt(int64(total))).
		StringFixed(2)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
