"""Impermanent loss for a constant-product AMM position.

    IL(r) = 2*sqrt(r)/(1+r) - 1

where ``r`` is the price ratio ``P_now / P_entry`` of asset A denominated in
asset B.

Where the formula comes from
----------------------------
A constant-product pool holds reserves ``x`` and ``y`` with ``x*y = k``. The
pool price of A in B is ``P = y/x``. Substituting ``y = P*x`` into ``x*y = k``
gives ``x = sqrt(k/P)`` and ``y = sqrt(k*P)``.

A position owning the whole pool is therefore worth, denominated in B:

    V_pool(P) = y + P*x = sqrt(k*P) + P*sqrt(k/P) = 2*sqrt(k*P)

Had the same holder simply kept the tokens they deposited at price ``P0`` —
``x0 = sqrt(k/P0)`` and ``y0 = sqrt(k*P0)`` — they would hold:

    V_hold(P) = y0 + P*x0 = sqrt(k*P0) + P*sqrt(k/P0)

The ratio, with ``r = P/P0``, and ``sqrt(k)`` and ``sqrt(P0)`` cancelling:

    V_pool/V_hold = 2*sqrt(k*P) / (sqrt(k*P0) + P*sqrt(k/P0))
                  = 2*sqrt(r) / (1 + r)

and IL is that ratio minus one — the fraction of value given up by providing
liquidity rather than holding, before fees.

Properties worth knowing before trusting a number this produces
---------------------------------------------------------------
* ``IL(1) = 0`` exactly. No price move, no loss.
* ``IL(r) <= 0`` for all ``r > 0``. It is never a gain. If this function ever
  returns a positive number, something is wrong upstream, and it asserts as
  much rather than reporting a windfall.
* ``IL(r) = IL(1/r)``. A halving and a doubling cost the same. This symmetry is
  a useful check on any implementation and is tested.
* It is *impermanent* only in the sense that it reverses if the price returns.
  Realised at exit, it is permanent, and Gauge reports realised outcomes.
* Fees are not in this formula. A position can have negative IL and still be
  profitable. Net P&L is computed separately and is the number that answers
  "did this lose money".
"""

from __future__ import annotations

from decimal import Decimal

from .exact import ONE, TWO, ZERO, sqrt


def impermanent_loss(price_ratio: Decimal) -> Decimal:
    """IL for a price ratio ``r = P_now / P_entry``.

    Returns a signed fraction: ``-0.0572...`` means the position is worth 5.72%
    less than simply holding the deposited tokens would have been.
    """
    r = price_ratio
    if r <= ZERO:
        raise ValueError(f"price ratio must be positive, got {r}")

    # Written out rather than reduced or delegated. The visibility of the
    # arithmetic is the point; a reader must be able to check this line against
    # the derivation above without trusting a library.
    il = TWO * sqrt(r) / (ONE + r) - ONE

    # IL is a loss or nothing. A positive result means the caller handed us a
    # ratio that did not come from a price, and reporting it as a gain would be
    # worse than failing.
    assert il <= ZERO, f"IL({r}) = {il} is positive, which is impossible"
    return il


def price_ratio(
    entry_reserve_a: Decimal,
    entry_reserve_b: Decimal,
    now_reserve_a: Decimal,
    now_reserve_b: Decimal,
) -> Decimal | None:
    """Price ratio of A in B, from pool composition at two moments.

    The pool price of A in B is ``reserve_b / reserve_a``, so

        r = (b_now / a_now) / (b_entry / a_entry)

    Composition is used rather than an external price feed because it is what
    the ledger records. It is also what determines IL: the pool rebalances
    against its own price, not against anyone's mark.

    Returns ``None`` when any reserve is zero — a drained pool has no price, and
    the survey found 428 pools in that state.
    """
    if ZERO in (entry_reserve_a, entry_reserve_b, now_reserve_a, now_reserve_b):
        return None
    return (now_reserve_b / now_reserve_a) / (entry_reserve_b / entry_reserve_a)
