"""Exact arithmetic for Gauge's accounting path.

Every monetary quantity in Gauge is a ``decimal.Decimal``. This module holds the
few operations that need more care than ``+``/``-``/``*``/``/`` and the context
that governs all of them.

Why this exists at all
----------------------
Stellar stores amounts as signed 64-bit integers of 1e-7 units, and Horizon
serialises them as decimal strings. Those values are exact. The census of
2026-09-06 measured what happens when they are not treated as such: of 119,499
monetary values, **4,610 (3.86%) do not survive a float64 round trip** — the
largest share supply in the population, 873148035084.8922752, comes back as
873148035084.8923. Nineteen significant digits into a type that holds sixteen.

Floats are permitted in Gauge's statistical layer, where the inputs are already
estimates and the error is orders of magnitude below the noise. They are not
permitted anywhere a number is presented as money.
"""

from __future__ import annotations

from decimal import Decimal, getcontext, localcontext

# Stellar's own resolution: 1e-7, seven decimal places.
STROOP = Decimal("0.0000001")
SCALE = 7

# Working precision for intermediate results.
#
# 50 digits is far more than the data needs — the widest value seen is 19
# significant digits — and the headroom matters for the one operation here that
# is genuinely irrational. A square root has no exact decimal representation, so
# it is computed to this precision and the error is bounded at 1e-50 relative,
# which is roughly 30 orders of magnitude below a stroop. That is not exactness
# and this module does not claim it is; it is an error too small to reach the
# seventh decimal place of any figure Gauge reports.
PRECISION = 50
getcontext().prec = PRECISION

ZERO = Decimal(0)
ONE = Decimal(1)
TWO = Decimal(2)


def dec(value: str | int | Decimal) -> Decimal:
    """Parse an exact value.

    Deliberately refuses ``float``. Accepting one would let a value that has
    already lost precision enter the accounting path looking respectable, which
    is the exact failure this module exists to prevent — by the time a float
    reaches here the damage is done and no amount of Decimal arithmetic undoes
    it.
    """
    if isinstance(value, float):
        raise TypeError(
            f"refusing to build a Decimal from the float {value!r}: "
            "the value has already lost precision. Pass the original string."
        )
    return Decimal(value)


def sqrt(value: Decimal) -> Decimal:
    """Square root, to PRECISION significant digits.

    ``Decimal.sqrt`` is correctly rounded to the context precision, which is the
    best any finite representation can do for an irrational result.
    """
    if value < ZERO:
        raise ValueError(f"sqrt of a negative amount: {value}")
    with localcontext() as ctx:
        ctx.prec = PRECISION
        return value.sqrt()


def quantise(value: Decimal, places: int = SCALE) -> Decimal:
    """Round to a fixed number of decimal places for display.

    Used only at the presentation boundary. Rounding earlier would compound
    across a calculation, and rounding a ratio to seven places before
    multiplying it by a reserve is how a P&L acquires an error nobody can trace.
    """
    exp = Decimal(1).scaleb(-places)
    return value.quantize(exp)


def ratio(numerator: Decimal, denominator: Decimal) -> Decimal | None:
    """Divide, returning ``None`` rather than raising on a zero denominator.

    ``None`` means "this quantity is not defined for this position", which Gauge
    reports as unavailable. Returning zero instead would be a fabricated number,
    and the whole point of the survey was to establish that unavailable and zero
    are different answers — 428 pools in the census genuinely hold zero.
    """
    if denominator == ZERO:
        return None
    return numerator / denominator
