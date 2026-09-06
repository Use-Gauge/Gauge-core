"""Realised volatility and drawdown over a series of pool observations.

This is the one module in Gauge where floats are permitted, and the boundary is
drawn deliberately.

Where the line sits
-------------------
Realised volatility is the standard deviation of log returns. A logarithm has no
exact decimal representation, so the computation is irrational by nature and
demanding exactness of it would be theatre. More to the point, the *input* is
already an estimate: a volatility computed from whatever observation times a
census happened to sample is a statistic about a sample, and float64's sixteen
digits are some ten orders of magnitude finer than the sampling error.

So: the price series arrives as Decimal, is converted to float at one clearly
marked place inside ``realised_volatility``, and the result is a float that is
never presented as money. Drawdown, by contrast, is a *ratio of two prices* and
stays exact — nothing about it requires a transcendental function, so nothing
about it gets a float.

CI enforces the no-float rule on Go's ``pkg/`` and ``cmd/``. This module is the
statistical layer the rule always exempted, and it is the only Python module in
Gauge permitted to convert.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from decimal import Decimal

from .exact import ONE, ZERO, ratio

# Stellar closes a ledger about every 5 seconds.
LEDGERS_PER_YEAR = 365 * 24 * 60 * 60 // 5


@dataclass(frozen=True)
class Drawdown:
    """Largest peak-to-trough decline in a series, as a negative fraction."""

    max_drawdown: Decimal | None
    peak: Decimal | None
    trough: Decimal | None


def max_drawdown(series: list[Decimal]) -> Drawdown:
    """Largest peak-to-trough decline, exactly.

        drawdown_t = value_t / max(value_0..t) - 1
        max_drawdown = min over t

    Kept in exact arithmetic because it is a ratio of two observed values and
    needs no transcendental function. The running peak must be tracked forward:
    taking ``min/max - 1`` over the whole series would report a decline that
    never happened if the trough precedes the peak.
    """
    if len(series) < 2:
        return Drawdown(None, None, None)

    peak = series[0]
    worst = ZERO
    worst_peak = peak
    worst_trough = peak

    for value in series[1:]:
        if value > peak:
            peak = value
            continue
        d = ratio(value, peak)
        if d is None:
            continue
        d -= ONE
        if d < worst:
            worst, worst_peak, worst_trough = d, peak, value

    if worst == ZERO:
        # Monotonically non-decreasing: a real zero drawdown, not a missing one.
        return Drawdown(ZERO, peak, peak)
    return Drawdown(worst, worst_peak, worst_trough)


def realised_volatility(
    series: list[Decimal], *, ledgers_between: int | None = None
) -> float | None:
    """Standard deviation of log returns, annualised if a spacing is given.

    Returns a ``float``, and the type is the documentation: this is a statistic,
    not an amount, and must never be formatted as money.

    ``None`` when there are fewer than three observations — two points give one
    return and a sample standard deviation of zero, which would report a
    volatile pool as perfectly calm.
    """
    if len(series) < 3:
        return None

    # The one permitted conversion in Gauge's Python, marked as such.
    values = [float(v) for v in series if v > ZERO]
    if len(values) < 3:
        return None

    returns = [
        math.log(values[i] / values[i - 1])
        for i in range(1, len(values))
        if values[i - 1] > 0
    ]
    if len(returns) < 2:
        return None

    mean = sum(returns) / len(returns)
    # Sample variance, Bessel-corrected: these are a sample of the pool's
    # returns, not its whole history.
    variance = sum((r - mean) ** 2 for r in returns) / (len(returns) - 1)
    vol = math.sqrt(variance)

    if ledgers_between and ledgers_between > 0:
        periods_per_year = LEDGERS_PER_YEAR / ledgers_between
        vol *= math.sqrt(periods_per_year)
    return vol


def price_series(observations: list[tuple[Decimal, Decimal]]) -> list[Decimal]:
    """Pool price of A in B from a series of ``(reserve_a, reserve_b)`` pairs.

    Observations with an empty reserve are dropped rather than treated as zero
    price: a drained pool has no price, and 428 pools in the census are drained.
    """
    out = []
    for a, b in observations:
        p = ratio(b, a)
        if p is not None and p > ZERO:
            out.append(p)
    return out
