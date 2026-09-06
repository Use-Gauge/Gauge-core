"""IL is checked against values that exist independently of this code.

The canonical table below is the one quoted throughout the AMM literature and
by Uniswap's own documentation: a 1.25x price move costs 0.6%, 1.5x costs 2.0%,
2x costs 5.7%, 3x costs 13.4%, 4x costs 20.0%, 5x costs 25.5%. If this
implementation disagrees with those, the implementation is wrong.
"""

from decimal import Decimal as D

import pytest

from gauge_metrics.exact import ZERO
from gauge_metrics.impermanent_loss import impermanent_loss, price_ratio

# (price ratio, published IL as a percentage, tolerance in percentage points)
CANONICAL = [
    ("1", "0.00", "0.005"),
    ("1.25", "0.62", "0.01"),
    ("1.5", "2.02", "0.01"),
    ("2", "5.72", "0.01"),
    ("3", "13.40", "0.01"),
    ("4", "20.00", "0.005"),
    ("5", "25.46", "0.01"),
]


@pytest.mark.parametrize("r,expected_pct,tol", CANONICAL)
def test_matches_published_values(r, expected_pct, tol):
    got = impermanent_loss(D(r)) * 100
    assert abs(got - -D(expected_pct)) <= D(tol), f"IL({r}) = {got}%"


def test_no_price_move_is_exactly_zero():
    # Not "close to zero". The formula gives 2*1/(1+1) - 1 = 0 exactly, and a
    # decimal implementation must reproduce that with no residue.
    assert impermanent_loss(D(1)) == ZERO


def test_four_x_is_exactly_minus_one_fifth():
    # IL(4) = 2*sqrt(4)/(1+4) - 1 = 4/5 - 1 = -1/5, an exact rational. It is the
    # one point on the curve where an implementation error cannot hide behind
    # rounding.
    assert impermanent_loss(D(4)) == D("-0.2")


@pytest.mark.parametrize("r", ["1.5", "2", "4", "10", "100"])
def test_symmetric_in_r_and_one_over_r(r):
    """A doubling and a halving cost the same. IL(r) == IL(1/r)."""
    up = impermanent_loss(D(r))
    down = impermanent_loss(1 / D(r))
    assert abs(up - down) < D("1e-40"), f"IL({r})={up} vs IL(1/{r})={down}"


@pytest.mark.parametrize("r", ["0.01", "0.5", "1", "2", "50"])
def test_never_positive(r):
    """IL is a loss or nothing. It is never a gain, at any ratio."""
    assert impermanent_loss(D(r)) <= ZERO


def test_monotonic_away_from_one():
    """Loss grows as price moves further from entry, in both directions."""
    rising = [impermanent_loss(D(r)) for r in ["1", "1.5", "2", "3", "4", "5"]]
    assert rising == sorted(rising, reverse=True)
    falling = [impermanent_loss(D(r)) for r in ["1", "0.75", "0.5", "0.33", "0.25"]]
    assert falling == sorted(falling, reverse=True)


@pytest.mark.parametrize("bad", ["0", "-1"])
def test_rejects_non_positive_ratio(bad):
    with pytest.raises(ValueError):
        impermanent_loss(D(bad))


def test_price_ratio_from_pool_composition():
    # Entry 1000/1000 (price 1), now 500/2000 (price 4). r = 4.
    r = price_ratio(D(1000), D(1000), D(500), D(2000))
    assert r == D(4)
    assert impermanent_loss(r) == D("-0.2")


def test_price_ratio_is_none_for_a_drained_pool():
    # 428 pools in the census hold nothing. They have no price, and reporting
    # one would be fabricating it.
    assert price_ratio(D(1000), D(1000), ZERO, ZERO) is None
    assert price_ratio(ZERO, D(1000), D(500), D(2000)) is None
