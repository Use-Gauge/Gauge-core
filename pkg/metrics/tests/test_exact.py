"""The float ban, enforced in Python.

Go's pkg/ and cmd/ are guarded by a CI grep. Python cannot be guarded the same
way — the statistical layer legitimately uses floats — so the boundary is
enforced at the type level instead: dec() refuses a float outright.
"""

from decimal import Decimal as D

import pytest

from gauge_metrics.exact import PRECISION, dec, quantise, ratio, sqrt


def test_dec_refuses_floats():
    # By the time a float arrives the precision is already gone; accepting it
    # would launder a damaged value into the accounting path.
    with pytest.raises(TypeError, match="already lost precision"):
        dec(0.1)


def test_dec_accepts_strings_and_ints():
    assert dec("0.1") == D("0.1")
    assert dec(7) == D(7)
    assert dec(D("1.5")) == D("1.5")


def test_the_value_that_float64_cannot_hold():
    """The largest share supply in the census of 2026-09-06.

    Nineteen significant digits. float64 holds about sixteen and returns
    873148035084.8923. This is the concrete case behind the whole decimal rule.
    """
    wire = "873148035084.8922752"
    exact = dec(wire)
    assert str(exact) == wire
    assert exact != D(repr(float(wire)))
    assert D(repr(float(wire))) == D("873148035084.8923")


def test_sqrt_is_accurate_to_working_precision():
    two = sqrt(D(2))
    # Squaring must return to 2 within the working precision, not merely close.
    assert abs(two * two - D(2)) < D(10) ** -(PRECISION - 2)


def test_sqrt_of_perfect_squares_is_exact():
    for n, root in [(4, 2), (9, 3), (144, 12), (10000, 100)]:
        assert sqrt(D(n)) == D(root)


def test_sqrt_rejects_negatives():
    with pytest.raises(ValueError):
        sqrt(D(-1))


def test_ratio_returns_none_not_zero_on_zero_denominator():
    # Unavailable and zero are different answers. 428 census pools hold a real
    # zero; a division by zero is not one of them.
    assert ratio(D(1), D(0)) is None
    assert ratio(D(0), D(1)) == D(0)


def test_quantise_is_display_only():
    assert quantise(D("1.23456789")) == D("1.2345679")
    assert quantise(D("1.23456789"), 2) == D("1.23")
