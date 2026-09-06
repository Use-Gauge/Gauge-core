package horizon

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
)

// The trades fixture is 20 real trades recorded from the largest pool on
// mainnet (native/USDC) on 2026-09-06.
//
// The check that matters is not that a price parses, but that it points the
// right way. Horizon reports every trade from the taker's side, so `price` is
// counter/base and base flips between trades in the same pool. A series built
// without normalising alternates between p and 1/p — for this pool, between
// 0.185 and 5.4 — and reports a fictitious volatility of several hundred
// percent from a market that did not move.
//
// So the test asserts the normalised price agrees with the pool's own reserve
// ratio, which is an independent measurement of the same quantity.
func TestTradePricesAreNormalisedToTheReserveRatio(t *testing.T) {
	srv := serveFixture(t, fixture(t, "trades_page.json"))
	c := &Client{URL: srv.URL, MaxRetries: -1}

	// Reserves of the same pool at the same time: 12,286,463.14 XLM against
	// 2,295,481.11 USDC. Asset A is native, so the price of A in B is
	// USDC per XLM, about 0.1868.
	const assetA = "native"
	reserveA := decimal.RequireFromString("12286463.1416566")
	reserveB := decimal.RequireFromString("2295481.1143391")
	poolPrice := reserveB.Div(reserveA)

	trades, _, err := c.PoolTrades(context.Background(), "poolid", assetA, 1)
	if err != nil {
		t.Fatalf("PoolTrades: %v", err)
	}
	if len(trades) == 0 {
		t.Fatal("no trades parsed from the fixture")
	}

	// A trade moves the price, so exact equality is not expected. Being within
	// 20% of the reserve ratio is: the inverted form would be off by a factor
	// of about 29.
	lo := poolPrice.Mul(decimal.RequireFromString("0.8"))
	hi := poolPrice.Mul(decimal.RequireFromString("1.2"))

	for _, tr := range trades {
		if tr.PriceAInB.LessThan(lo) || tr.PriceAInB.GreaterThan(hi) {
			t.Errorf("trade %s price %s is outside [%s, %s] - likely inverted (pool ratio %s)",
				tr.ID, tr.PriceAInB, lo, hi, poolPrice)
		}
		if tr.CloseTime == "" {
			t.Errorf("trade %s has no close time; a price with no time is not a series", tr.ID)
		}
	}
	t.Logf("%d trades, all within 20%% of the pool's reserve ratio %s", len(trades), poolPrice)
}

// Inverting is the whole job, so it is also tested directly against a
// constructed pair rather than only through the fixture.
func TestTradePriceInvertsWhenAssetAIsTheCounterSide(t *testing.T) {
	raw := rawTrade{
		ID:            "t",
		BaseAssetType: "credit_alphanum4",
		BaseAssetCode: "USDC",
		// price = counter/base = 4
		Price: struct {
			N string `json:"n"`
			D string `json:"d"`
		}{N: "4", D: "1"},
	}

	// Asset A is the base side: price is already A-in-B.
	got, err := raw.toTrade("p", "USDC:")
	if err != nil {
		t.Fatalf("toTrade: %v", err)
	}
	if !got.PriceAInB.Equal(decimal.NewFromInt(4)) {
		t.Errorf("base-side price = %s, want 4", got.PriceAInB)
	}

	// Asset A is the counter side: price must invert to 1/4.
	got, err = raw.toTrade("p", "native")
	if err != nil {
		t.Fatalf("toTrade: %v", err)
	}
	if !got.PriceAInB.Equal(decimal.RequireFromString("0.25")) {
		t.Errorf("counter-side price = %s, want 0.25", got.PriceAInB)
	}
}

func TestTradeRejectsAZeroPriceComponent(t *testing.T) {
	raw := rawTrade{ID: "t", BaseAssetType: "native"}
	raw.Price.N, raw.Price.D = "0", "1"
	if _, err := raw.toTrade("p", "native"); err == nil {
		t.Error("a zero price was accepted; it would invert to a division by zero")
	}
}
