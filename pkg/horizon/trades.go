package horizon

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
)

// Trade is one swap against a pool, reduced to a price observation.
//
// Realised volatility and drawdown need a price series, and a census gives one
// observation per pool — a single point, from which no variance can be computed.
// Trades are where the series comes from: every swap moves a constant-product
// pool's price and Horizon records each one with its close time.
type Trade struct {
	PoolID string `json:"pool_id"`
	ID     string `json:"id"`
	// CloseTime is when the containing ledger closed.
	CloseTime string `json:"close_time"`

	// PriceAInB is the pool price of the pool's first reserve asset denominated
	// in its second, normalised so that every observation in a series points the
	// same way. Horizon reports each trade from the taker's side, so base and
	// counter swap between trades in the same pool; a series built without
	// normalising alternates between p and 1/p and reports enormous fictitious
	// volatility.
	PriceAInB decimal.Decimal `json:"price_a_in_b"`
}

type rawTrade struct {
	ID              string `json:"id"`
	LedgerCloseTime string `json:"ledger_close_time"`
	TradeType       string `json:"trade_type"`
	BaseAmount      string `json:"base_amount"`
	CounterAmount   string `json:"counter_amount"`
	BaseAssetType   string `json:"base_asset_type"`
	BaseAssetCode   string `json:"base_asset_code"`
	BaseAssetIssuer string `json:"base_asset_issuer"`
	Price           struct {
		N string `json:"n"`
		D string `json:"d"`
	} `json:"price"`
}

// asset renders the base side in Horizon's canonical "native" or "CODE:ISSUER"
// form, so it can be compared against a pool's reserve assets.
func (r rawTrade) baseAsset() string {
	if r.BaseAssetType == "native" {
		return "native"
	}
	return r.BaseAssetCode + ":" + r.BaseAssetIssuer
}

// toTrade normalises a trade into a price of assetA denominated in assetB.
//
// Horizon's `price` is counter/base as an exact rational. Using n/d rather than
// dividing the two amount strings keeps the value exactly as the ledger
// expressed it, and both are decimal integers so nothing here needs a float.
func (r rawTrade) toTrade(poolID, assetA string) (Trade, error) {
	n, err := decimal.NewFromString(r.Price.N)
	if err != nil {
		return Trade{}, fmt.Errorf("horizon: trade %s price.n %q: %w", r.ID, r.Price.N, err)
	}
	d, err := decimal.NewFromString(r.Price.D)
	if err != nil {
		return Trade{}, fmt.Errorf("horizon: trade %s price.d %q: %w", r.ID, r.Price.D, err)
	}
	if d.IsZero() || n.IsZero() {
		return Trade{}, fmt.Errorf("horizon: trade %s has a zero price component", r.ID)
	}

	// price = counter/base. If the pool's asset A was the base side, that is
	// already A-in-B. If A was the counter side, invert.
	price := n.Div(d)
	if r.baseAsset() != assetA {
		price = d.Div(n)
	}

	return Trade{
		PoolID:    poolID,
		ID:        r.ID,
		CloseTime: r.LedgerCloseTime,
		PriceAInB: price,
	}, nil
}

type tradePage struct {
	Links struct {
		Next struct {
			Href string `json:"href"`
		} `json:"next"`
	} `json:"_links"`
	Embedded struct {
		Records []rawTrade `json:"records"`
	} `json:"_embedded"`
}

// TradePageSize is the largest page Horizon serves for trades.
const TradePageSize = 200

// PoolTrades returns up to maxPages of a pool's most recent trades, newest
// first, as normalised price observations against assetA.
//
// Bounded by pages rather than exhaustive: the largest pool produced 1,205
// trades in 15 pages covering less than a day, so an unbounded walk of every
// in-scope pool's full history is not a thing a run can finish. A recent window
// is what a realised-volatility figure wants anyway — it is a statement about
// the recent past, and saying which window it covers is part of reporting it.
func (c *Client) PoolTrades(ctx context.Context, poolID, assetA string, maxPages int) (trades []Trade, requests int, err error) {
	next := fmt.Sprintf("%s/liquidity_pools/%s/trades?limit=%d&order=desc",
		c.baseURL(), poolID, TradePageSize)

	for pages := 0; next != "" && (maxPages <= 0 || pages < maxPages); pages++ {
		var page tradePage
		n, gerr := c.getJSON(ctx, next, &page)
		requests += n
		if gerr != nil {
			return trades, requests, fmt.Errorf("trades of %s: %w", poolID, gerr)
		}
		if len(page.Embedded.Records) == 0 {
			return trades, requests, nil
		}
		for _, raw := range page.Embedded.Records {
			t, cerr := raw.toTrade(poolID, assetA)
			if cerr != nil {
				// One malformed trade must not discard a pool's whole series.
				// It is skipped, and skipping is visible because the caller
				// compares the count it asked for against what it received.
				continue
			}
			trades = append(trades, t)
		}
		next, err = c.rebase(page.Links.Next.Href)
		if err != nil {
			return trades, requests, err
		}
	}
	return trades, requests, nil
}
