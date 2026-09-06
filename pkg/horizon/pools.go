package horizon

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
)

// Reserve is one side of a pool's holdings.
type Reserve struct {
	// Asset is Horizon's canonical form: "native" for XLM, otherwise
	// "CODE:ISSUER".
	Asset string `json:"asset"`

	// Amount is the exact reserve balance. Never a float; see the package
	// comment.
	Amount decimal.Decimal `json:"amount"`
}

// IsNative reports whether this side is XLM.
func (r Reserve) IsNative() bool { return r.Asset == "native" }

// Pool is a Stellar AMM liquidity pool as Horizon reports it.
type Pool struct {
	ID   string `json:"id"`
	Type string `json:"type"`

	// FeeBP is the pool's fee in basis points. Every one of the 8,000 pools
	// sampled on 2026-09-05 reported 30, because the protocol currently
	// permits only that value for constant-product pools. It is carried
	// anyway rather than assumed, so that the survey can state the uniformity
	// as an observation instead of a premise.
	FeeBP int `json:"fee_bp"`

	// TotalShares is the pool's outstanding share supply.
	TotalShares decimal.Decimal `json:"total_shares"`

	// TotalTrustlines is Horizon's count of trustlines to the pool share
	// asset. It is the cheapest available proxy for "how many holders" and it
	// is NOT the same thing: a trustline can exist with a zero balance, so
	// this over-counts economically live positions. Recorded as what it is.
	TotalTrustlines int `json:"total_trustlines"`

	Reserves [2]Reserve `json:"reserves"`

	LastModifiedLedger int    `json:"last_modified_ledger"`
	LastModifiedTime   string `json:"last_modified_time"`
}

// IsDrained reports whether both reserves are exactly zero. Such pools still
// exist as ledger entries and still appear in the listing; they hold nothing.
func (p Pool) IsDrained() bool {
	return p.Reserves[0].Amount.IsZero() && p.Reserves[1].Amount.IsZero()
}

// Pair renders the asset pair for display, e.g. "native/USDC:GA5Z...".
func (p Pool) Pair() string {
	return p.Reserves[0].Asset + "/" + p.Reserves[1].Asset
}

// rawPool mirrors Horizon's wire format, where every amount is a string.
//
// The intermediate type exists so that amounts are converted exactly once, at a
// single place, by parseAmount. Decoding directly into Pool would need custom
// UnmarshalJSON on decimal fields and would scatter the conversion.
type rawPool struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	FeeBP           int    `json:"fee_bp"`
	TotalShares     string `json:"total_shares"`
	TotalTrustlines string `json:"total_trustlines"`
	Reserves        []struct {
		Asset  string `json:"asset"`
		Amount string `json:"amount"`
	} `json:"reserves"`
	LastModifiedLedger int    `json:"last_modified_ledger"`
	LastModifiedTime   string `json:"last_modified_time"`
}

func (r rawPool) toPool() (Pool, error) {
	var p Pool

	// A constant-product pool has exactly two reserves. Anything else is a
	// shape this code has never seen and must not guess at — silently taking
	// the first two would put an invented pool into the census.
	if len(r.Reserves) != 2 {
		return p, fmt.Errorf("horizon: pool %s has %d reserves, expected 2", r.ID, len(r.Reserves))
	}

	shares, err := parseAmount("total_shares", r.TotalShares)
	if err != nil {
		return p, fmt.Errorf("pool %s: %w", r.ID, err)
	}

	// total_trustlines arrives as a JSON string, not a number.
	var trustlines int
	if _, err := fmt.Sscanf(r.TotalTrustlines, "%d", &trustlines); err != nil {
		return p, fmt.Errorf("horizon: pool %s total_trustlines %q: %w", r.ID, r.TotalTrustlines, err)
	}

	p = Pool{
		ID:                 r.ID,
		Type:               r.Type,
		FeeBP:              r.FeeBP,
		TotalShares:        shares,
		TotalTrustlines:    trustlines,
		LastModifiedLedger: r.LastModifiedLedger,
		LastModifiedTime:   r.LastModifiedTime,
	}
	for i, res := range r.Reserves {
		amt, err := parseAmount("reserve amount", res.Amount)
		if err != nil {
			return p, fmt.Errorf("pool %s: %w", r.ID, err)
		}
		p.Reserves[i] = Reserve{Asset: res.Asset, Amount: amt}
	}
	return p, nil
}

// poolPage is one page of Horizon's pool listing.
type poolPage struct {
	Links struct {
		Next struct {
			Href string `json:"href"`
		} `json:"next"`
	} `json:"_links"`
	Embedded struct {
		Records []rawPool `json:"records"`
	} `json:"_embedded"`
}

// PoolPageSize is the largest page Horizon serves. Using the maximum matters:
// the census is thousands of pools, and at the default of 10 it would be
// thousands of round trips instead of dozens.
const PoolPageSize = 200

// ListPools walks the full pool listing, calling visit for each page in order.
//
// It returns the number of pages read and the number of HTTP requests made,
// which differ whenever a request was retried. Both go into the run manifest.
//
// visit may return a non-nil error to stop the walk early; that error is
// returned unchanged.
func (c *Client) ListPools(ctx context.Context, maxPages int, visit func(page []Pool) error) (pages, requests int, err error) {
	next := fmt.Sprintf("%s/liquidity_pools?limit=%d&order=asc", c.baseURL(), PoolPageSize)

	for next != "" {
		if maxPages > 0 && pages >= maxPages {
			return pages, requests, nil
		}

		var page poolPage
		n, err := c.getJSON(ctx, next, &page)
		requests += n
		if err != nil {
			return pages, requests, err
		}

		// An empty page is how Horizon signals the end of the listing: the
		// next link is always present and always resolvable, so following it
		// forever is a real risk. The record count is the terminator.
		if len(page.Embedded.Records) == 0 {
			return pages, requests, nil
		}

		pools := make([]Pool, 0, len(page.Embedded.Records))
		for _, raw := range page.Embedded.Records {
			p, cerr := raw.toPool()
			if cerr != nil {
				return pages, requests, cerr
			}
			pools = append(pools, p)
		}
		pages++

		if verr := visit(pools); verr != nil {
			return pages, requests, verr
		}

		next, err = c.rebase(page.Links.Next.Href)
		if err != nil {
			return pages, requests, err
		}
	}
	return pages, requests, nil
}
