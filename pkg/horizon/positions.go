package horizon

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
)

// PoolShare is one account's stake in one pool: the unit Gauge actually
// measures.
//
// This is the provider side of an AMM, and it is a different row from anything
// on the trades endpoint. A trade record says someone swapped against the pool;
// this says whose capital the pool is made of.
type PoolShare struct {
	AccountID string `json:"account_id"`
	PoolID    string `json:"pool_id"`

	// Shares is the account's pool-share balance. Its economic claim on the
	// pool is Shares/Pool.TotalShares of each reserve — a ratio that must be
	// computed in exact arithmetic, since it is then multiplied by a reserve
	// to produce a figure presented as money.
	Shares decimal.Decimal `json:"shares"`

	// LastModifiedLedger is when this balance last changed. It is the only
	// activity signal available from state alone: a position untouched for
	// millions of ledgers is a different object from one that moved
	// yesterday, and nothing else in the account record distinguishes them.
	LastModifiedLedger int `json:"last_modified_ledger"`
}

// Account is a Horizon account reduced to what Gauge needs: its pool positions.
//
// The full record is kept out deliberately. Horizon returns the account's
// entire balance list, signers, thresholds and flags; carrying all of that into
// the run files would multiply their size for data no metric reads.
type Account struct {
	ID string `json:"id"`

	// Shares is every pool position this account holds — not only the pool
	// that was queried.
	//
	// This is the single most useful property of the endpoint. Asking about
	// one pool returns accounts whose balance lists name every other pool they
	// are in; the first account fetched during the 2026-09-05 probe held
	// positions in hundreds of pools. Harvesting all of them means the census
	// discovers far more positions than it makes requests for.
	Shares []PoolShare `json:"shares"`
}

type rawAccount struct {
	ID       string `json:"id"`
	Balances []struct {
		Balance            string `json:"balance"`
		AssetType          string `json:"asset_type"`
		LiquidityPoolID    string `json:"liquidity_pool_id"`
		LastModifiedLedger int    `json:"last_modified_ledger"`
	} `json:"balances"`
}

func (r rawAccount) toAccount() (Account, error) {
	a := Account{ID: r.ID}
	for _, b := range r.Balances {
		if b.AssetType != "liquidity_pool_shares" {
			continue
		}
		shares, err := parseAmount("pool share balance", b.Balance)
		if err != nil {
			return a, fmt.Errorf("account %s: %w", r.ID, err)
		}
		a.Shares = append(a.Shares, PoolShare{
			AccountID:          r.ID,
			PoolID:             b.LiquidityPoolID,
			Shares:             shares,
			LastModifiedLedger: b.LastModifiedLedger,
		})
	}
	return a, nil
}

type accountPage struct {
	Links struct {
		Next struct {
			Href string `json:"href"`
		} `json:"next"`
	} `json:"_links"`
	Embedded struct {
		Records []rawAccount `json:"records"`
	} `json:"_embedded"`
}

// AccountPageSize is the page size used for the holder listing.
//
// Smaller than PoolPageSize on purpose. Each account record carries its entire
// balance list, and an account in hundreds of pools produces a large record;
// 200 of those in one response is megabytes, and this is the endpoint already
// observed returning 503 under load.
const AccountPageSize = 100

// PoolHolders returns every account holding shares in poolID, with all of each
// account's pool positions attached.
//
// This endpoint is the only route from Horizon to position-level data, and it
// is the fragile one. During the survey of 2026-09-05 it returned HTTP 503 on a
// first call and 200 on the immediate retry, repeatedly. That is handled in
// getJSON rather than here, but it is why this function exists as a separate,
// individually retryable unit of work instead of being folded into the census
// walk: a pool that cannot be attributed must be recorded as unattributed, not
// abort the run.
func (c *Client) PoolHolders(ctx context.Context, poolID string) (accounts []Account, requests int, err error) {
	next := fmt.Sprintf("%s/accounts?liquidity_pool=%s&limit=%d", c.baseURL(), poolID, AccountPageSize)

	for next != "" {
		var page accountPage
		n, gerr := c.getJSON(ctx, next, &page)
		requests += n
		if gerr != nil {
			return accounts, requests, fmt.Errorf("holders of %s: %w", poolID, gerr)
		}
		if len(page.Embedded.Records) == 0 {
			return accounts, requests, nil
		}
		for _, raw := range page.Embedded.Records {
			a, cerr := raw.toAccount()
			if cerr != nil {
				return accounts, requests, cerr
			}
			accounts = append(accounts, a)
		}
		next, err = c.rebase(page.Links.Next.Href)
		if err != nil {
			return accounts, requests, err
		}
	}
	return accounts, requests, nil
}
