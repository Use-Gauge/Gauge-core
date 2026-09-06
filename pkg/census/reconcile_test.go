package census

import (
	"testing"

	"github.com/Use-Gauge/Gauge-core/pkg/horizon"
	"github.com/shopspring/decimal"
)

func pool(id string, shares string, trustlines int) horizon.Pool {
	return horizon.Pool{
		ID:              id,
		TotalShares:     decimal.RequireFromString(shares),
		TotalTrustlines: trustlines,
	}
}

func share(pool, acct, amount string) horizon.PoolShare {
	return horizon.PoolShare{
		PoolID:    pool,
		AccountID: acct,
		Shares:    decimal.RequireFromString(amount),
	}
}

func TestReconcileAcceptsAnExactSum(t *testing.T) {
	pools := []horizon.Pool{pool("p", "5494.2144063", 2)}
	positions := []horizon.PoolShare{
		share("p", "a", "5000.0000001"),
		share("p", "b", "494.2144062"),
	}
	r := Reconcile(pools, positions)
	if r.Checked != 1 || r.Exact != 1 {
		t.Errorf("checked=%d exact=%d, want 1 and 1", r.Checked, r.Exact)
	}
}

// The check must be exact equality, not a tolerance. Both sides are integers of
// 1e-7 units, so a one-stroop difference is a real disagreement between two
// Horizon endpoints and not a rounding artefact to be waved through.
func TestReconcileRejectsAOneStroopDifference(t *testing.T) {
	pools := []horizon.Pool{pool("p", "5494.2144063", 2)}
	positions := []horizon.PoolShare{
		share("p", "a", "5000.0000001"),
		share("p", "b", "494.2144061"), // one stroop short
	}
	r := Reconcile(pools, positions)
	if r.Exact != 0 {
		t.Fatal("a one-stroop shortfall was accepted as exact")
	}
	if len(r.Mismatches) != 1 {
		t.Fatalf("got %d mismatches, want 1", len(r.Mismatches))
	}
	want := decimal.RequireFromString("-0.0000001")
	if !r.Mismatches[0].Difference.Equal(want) {
		t.Errorf("difference %s, want %s", r.Mismatches[0].Difference, want)
	}
}

// A pool whose holder set is incomplete cannot reconcile and must be skipped
// rather than counted as a failure. Positions arrive from two sources: pools
// that were deliberately attributed, and pools discovered incidentally through
// some other pool's holder list. Only the first kind is complete.
func TestReconcileSkipsIncompleteHolderSets(t *testing.T) {
	pools := []horizon.Pool{pool("p", "100", 5)}
	positions := []horizon.PoolShare{share("p", "a", "40")}

	r := Reconcile(pools, positions)
	if r.Checked != 0 {
		t.Errorf("checked=%d, want 0 — a partial holder set is not evidence of anything", r.Checked)
	}
	if len(r.Mismatches) != 0 {
		t.Error("a partial holder set was reported as a mismatch")
	}
}

// Large supplies are exactly where a float implementation would fail, so the
// reconciliation is exercised on a value that a float64 cannot hold.
func TestReconcileHandlesSuppliesBeyondFloat64Precision(t *testing.T) {
	// The largest share supply in the census of 2026-09-06. Nineteen
	// significant digits; float64 holds about sixteen and returns
	// 873148035084.8923.
	const supply = "873148035084.8922752"

	pools := []horizon.Pool{pool("p", supply, 2)}
	positions := []horizon.PoolShare{
		share("p", "a", "873148035084.8922751"),
		share("p", "b", "0.0000001"),
	}
	r := Reconcile(pools, positions)
	if r.Exact != 1 {
		t.Errorf("exact=%d, want 1: %+v", r.Exact, r.Mismatches)
	}
}
