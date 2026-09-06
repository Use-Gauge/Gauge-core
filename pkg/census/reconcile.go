package census

import (
	"github.com/Use-Gauge/Gauge-core/pkg/horizon"
	"github.com/shopspring/decimal"
)

// Reconciliation is the result of checking holder balances against a pool's
// declared share supply.
//
// This is the survey's strongest integrity check and the cheapest one. Horizon
// reports a pool's total_shares on the pools endpoint and each holder's balance
// on the accounts endpoint. They are separate views assembled from separate
// requests, and for a pool whose full holder set was fetched they must agree
// exactly. If they ever do not, either the ingestion is losing rows or an
// assumption in this project is wrong, and every metric built on the data would
// inherit that doubt silently.
type Reconciliation struct {
	// Checked counts pools where the full holder set was available. A pool
	// discovered incidentally through another pool's holder list has an
	// arbitrary subset of its holders and cannot be reconciled.
	Checked int

	// Exact counts pools where the holder balances summed to total_shares with
	// decimal equality. Not "within a tolerance" — there is no tolerance. Both
	// numbers are exact integers of 1e-7 units and any difference is real.
	Exact int

	// Mismatches names the pools that disagreed, with both figures.
	Mismatches []Mismatch
}

// Mismatch is one pool whose holder balances did not sum to its share supply.
type Mismatch struct {
	PoolID      string          `json:"pool_id"`
	HolderSum   decimal.Decimal `json:"holder_sum"`
	TotalShares decimal.Decimal `json:"total_shares"`
	Difference  decimal.Decimal `json:"difference"`
	Holders     int             `json:"holders"`
}

// Reconcile checks holder balances against declared share supply.
//
// Only pools whose holder count matches the pool's trustline count are checked,
// because only those had their complete holder set fetched.
func Reconcile(pools []horizon.Pool, positions []horizon.PoolShare) Reconciliation {
	byPool := make(map[string][]horizon.PoolShare, len(pools))
	for _, s := range positions {
		byPool[s.PoolID] = append(byPool[s.PoolID], s)
	}

	var r Reconciliation
	for _, p := range pools {
		shares, ok := byPool[p.ID]
		if !ok || len(shares) != p.TotalTrustlines {
			continue
		}
		r.Checked++

		sum := decimal.Zero
		for _, s := range shares {
			sum = sum.Add(s.Shares)
		}
		if sum.Equal(p.TotalShares) {
			r.Exact++
			continue
		}
		r.Mismatches = append(r.Mismatches, Mismatch{
			PoolID:      p.ID,
			HolderSum:   sum,
			TotalShares: p.TotalShares,
			Difference:  sum.Sub(p.TotalShares),
			Holders:     len(shares),
		})
	}
	return r
}
