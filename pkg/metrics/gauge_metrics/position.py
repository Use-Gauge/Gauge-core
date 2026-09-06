"""Position-level accounting: claim, fee yield, and net P&L.

Everything here is exact decimal arithmetic. These are the figures presented as
money, so none of them may pass through a float.

The entry-basis problem, stated up front
-----------------------------------------
Three of these quantities — impermanent loss, net P&L and drawdown from entry —
need to know what the position cost. The ledger records that in
``liquidity_pool_deposited`` effects, and Horizon retains only 365 days of
history (``history_elder_ledger`` 57,993,841 as of 2026-09-06).

The survey measured the consequence: **81.91% of positions in Gauge's in-scope
population last changed before that boundary**, so their establishing deposit is
provably outside the window and unrecoverable at any price.

Every function here that needs an entry basis therefore takes an optional one
and returns ``None`` without it. ``None`` means "not computable from the ledger",
which is a different answer from zero and is reported as such. Guessing a cost
basis would produce a plausible P&L for four positions in five, and plausible is
the failure mode this project is built to avoid.
"""

from __future__ import annotations

from dataclasses import dataclass
from decimal import Decimal

from .exact import ONE, ZERO, ratio, sqrt
from .impermanent_loss import impermanent_loss


@dataclass(frozen=True)
class Claim:
    """What a share balance is currently worth, in pool tokens."""

    reserve_a: Decimal
    reserve_b: Decimal
    pool_fraction: Decimal


def claim(
    shares: Decimal,
    total_shares: Decimal,
    reserve_a: Decimal,
    reserve_b: Decimal,
) -> Claim | None:
    """A holder's claim on the pool's reserves.

    A pool-share balance is a proportional claim, so the position's holdings are
    ``shares/total_shares`` of each reserve. That fraction is exact under
    decimal division at the module's working precision.

    Both the shares and the reserves must come from the *same instant*. The
    census of 2026-09-06 measured why: reading a pool row and its holder list
    412 minutes apart left five pools whose holder balances summed to more than
    the recorded supply, because deposits landed in between. ``cmd/ingest`` now
    re-reads the pool alongside its holders into ``pools_at_attribution.jsonl``,
    and that is the file this function's inputs should come from.
    """
    fraction = ratio(shares, total_shares)
    if fraction is None:
        return None
    return Claim(
        reserve_a=fraction * reserve_a,
        reserve_b=fraction * reserve_b,
        pool_fraction=fraction,
    )


def value_in_b(claim_: Claim, price_a_in_b: Decimal) -> Decimal:
    """Value the claim in units of asset B.

    B is chosen as the numeraire rather than USD because the ledger has no USD.
    The survey is explicit that no on-chain price exists, so every value figure
    Gauge reports is denominated in one of the pool's own assets and says so.
    """
    return claim_.reserve_b + price_a_in_b * claim_.reserve_a


def pool_price_a_in_b(reserve_a: Decimal, reserve_b: Decimal) -> Decimal | None:
    """The pool's own price of A in B: ``reserve_b / reserve_a``."""
    return ratio(reserve_b, reserve_a)


def fee_yield(
    entry_reserves_per_share: Decimal,
    now_reserves_per_share: Decimal,
) -> Decimal | None:
    """Growth in the constant-product invariant per share.

        fee_yield = (k_now/shares_now) / (k_entry/shares_entry) - 1

    Fees in a constant-product AMM are not paid out; they are retained in the
    reserves, which raises ``k`` for a fixed share supply. So the value accruing
    to a share is exactly the growth of ``sqrt(k)/shares``, and that growth is
    the fee yield.

    The caller supplies ``sqrt(k)/shares`` at each end rather than raw reserves,
    because ``sqrt(k)`` is the price-independent measure of pool size: it is
    unchanged by a swap that only moves price, and changes only when fees are
    added or liquidity is deposited or withdrawn.

    Returns ``None`` without an entry basis, which is the common case.
    """
    r = ratio(now_reserves_per_share, entry_reserves_per_share)
    if r is None:
        return None
    return r - ONE


def sqrt_k_per_share(
    reserve_a: Decimal, reserve_b: Decimal, total_shares: Decimal
) -> Decimal | None:
    """``sqrt(reserve_a * reserve_b) / total_shares``.

    The per-share size of the pool, independent of price. Used as the fee-yield
    basis.
    """
    if total_shares == ZERO:
        return None
    return sqrt(reserve_a * reserve_b) / total_shares


@dataclass(frozen=True)
class PnL:
    """Net outcome for a position, all figures denominated in asset B.

    ``None`` on any field means the ledger does not support computing it, not
    that it is zero.
    """

    value_now: Decimal
    value_if_held: Decimal | None
    net: Decimal | None
    net_fraction: Decimal | None
    impermanent_loss: Decimal | None
    fee_yield: Decimal | None


def net_pnl(
    *,
    shares: Decimal,
    total_shares: Decimal,
    reserve_a: Decimal,
    reserve_b: Decimal,
    entry_reserve_a: Decimal | None = None,
    entry_reserve_b: Decimal | None = None,
    entry_shares: Decimal | None = None,
    entry_total_shares: Decimal | None = None,
) -> PnL | None:
    """Net position P&L against holding the deposited tokens.

    The comparison is deliberately against *holding*, not against zero. A
    position that fell 10% in a market that fell 30% did not lose money by
    providing liquidity; it lost money by being in that asset. Gauge measures the
    former, which is the decision the position holder actually made.

    Without an entry basis only ``value_now`` is returned and every other field
    is ``None``.
    """
    now = claim(shares, total_shares, reserve_a, reserve_b)
    if now is None:
        return None
    price_now = pool_price_a_in_b(reserve_a, reserve_b)
    if price_now is None:
        return None
    value_now = value_in_b(now, price_now)

    have_entry = None not in (
        entry_reserve_a,
        entry_reserve_b,
        entry_shares,
        entry_total_shares,
    )
    if not have_entry:
        return PnL(value_now, None, None, None, None, None)

    entry = claim(entry_shares, entry_total_shares, entry_reserve_a, entry_reserve_b)
    if entry is None:
        return PnL(value_now, None, None, None, None, None)

    # What the deposited tokens would be worth now if simply held.
    value_if_held = value_in_b(entry, price_now)

    net = value_now - value_if_held
    net_fraction = ratio(net, value_if_held)

    r = None
    entry_price = pool_price_a_in_b(entry_reserve_a, entry_reserve_b)
    if entry_price is not None and entry_price > ZERO:
        r = price_now / entry_price
    il = impermanent_loss(r) if r is not None and r > ZERO else None

    fy = None
    now_k = sqrt_k_per_share(reserve_a, reserve_b, total_shares)
    entry_k = sqrt_k_per_share(entry_reserve_a, entry_reserve_b, entry_total_shares)
    if now_k is not None and entry_k is not None:
        fy = fee_yield(entry_k, now_k)

    return PnL(value_now, value_if_held, net, net_fraction, il, fy)
