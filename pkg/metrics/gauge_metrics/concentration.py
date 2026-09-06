"""Holder concentration within a pool.

    HHI = sum over holders of s_i^2,  where s_i is holder i's share of the pool

The Herfindahl-Hirschman index, applied to pool-share balances rather than
market shares. It runs from ``1/n`` (n equal holders) to ``1`` (one holder owns
everything).

What the data supports, and what it does not
--------------------------------------------
This is genuine account-level attribution, not a coarser proxy, but it is
available only where the *complete* holder set was fetched. Three limits, all
measured in the census of 2026-09-06 and recorded in ``docs/data-survey.md``:

1. **It is degenerate for most of the network.** 34,402 of 39,833 pools (86.37%)
   have a single trustline, where HHI is 1.0 by construction. Reporting that as
   a risk measurement measures the arithmetic, not the market. ``holder_hhi``
   returns ``None`` for single-holder pools rather than a misleading 1.0.

2. **It needs a complete holder set.** Positions arrive two ways: from pools
   deliberately attributed, and from pools discovered incidentally in some other
   account's balance list. The second kind is an arbitrary subset. Computing HHI
   over a subset understates concentration and looks like a real number, so the
   caller must pass ``complete=False`` and get ``None``.

3. **A trustline is not a holder.** 6.58% of positions carry a zero balance —
   a trustline to the pool-share asset with no stake. Those are excluded from
   both the numerator and the holder count; including them would deflate HHI
   with holders who hold nothing.

Where the data *does* support it, it is informative rather than constant: across
the 208 in-scope pools the largest holder's share runs from 0.0516 to 1.0000
with a median of 0.7997.
"""

from __future__ import annotations

from decimal import Decimal

from .exact import ZERO


def holder_hhi(balances: list[Decimal], *, complete: bool) -> Decimal | None:
    """HHI over holder balances.

    ``complete`` asserts that every holder of the pool is present. Pass ``False``
    when the holder set is a subset; the result is ``None``, because a
    concentration computed over an unknown fraction of a pool is not a weaker
    measurement, it is a wrong one.
    """
    if not complete:
        return None

    live = [b for b in balances if b > ZERO]
    if len(live) < 2:
        # One holder, or none. Nothing to be concentrated relative to.
        return None

    total = sum(live, ZERO)
    if total == ZERO:
        return None

    # Written out rather than delegated: sum of squared fractional shares.
    return sum(((b / total) ** 2 for b in live), ZERO)


def top_holder_share(balances: list[Decimal], *, complete: bool) -> Decimal | None:
    """Fraction of the pool held by its largest holder.

    Reported alongside HHI because they answer different questions. HHI is
    lowered by a long tail of small holders; this is not. A pool with one
    account at 95% and forty at 0.125% each has a moderate-sounding HHI and a
    single point of failure.
    """
    if not complete:
        return None
    live = [b for b in balances if b > ZERO]
    if not live:
        return None
    total = sum(live, ZERO)
    if total == ZERO:
        return None
    return max(live) / total
