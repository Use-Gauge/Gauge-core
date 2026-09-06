"""Position accounting, including the cases where it must refuse to answer."""

from decimal import Decimal as D

from gauge_metrics.position import claim, fee_yield, net_pnl, sqrt_k_per_share

# A pool at 1000/1000 whose price of A in B has doubled. Constant product holds
# k = 1e6, so reserves become sqrt(k/P), sqrt(k*P) at P = 2.
K_1E6_AT_PRICE_2_A = D("707.1067811865475244008443621048")
K_1E6_AT_PRICE_2_B = D("1414.2135623730950488016887242097")


def test_claim_is_proportional():
    c = claim(D(100), D(1000), D(500), D(2000))
    assert c.pool_fraction == D("0.1")
    assert c.reserve_a == D(50)
    assert c.reserve_b == D(200)


def test_claim_is_none_when_no_shares_exist():
    assert claim(D(0), D(0), D(1), D(1)) is None


def test_pnl_against_holding_equals_impermanent_loss_without_fees():
    """The strongest cross-check available.

    Net P&L versus holding and impermanent loss are derived and implemented
    independently. With no fees earned they must agree exactly, because that is
    what IL *is*. If these two ever diverge, one of the formulas is wrong.
    """
    p = net_pnl(
        shares=D(100),
        total_shares=D(1000),
        reserve_a=K_1E6_AT_PRICE_2_A,
        reserve_b=K_1E6_AT_PRICE_2_B,
        entry_reserve_a=D(1000),
        entry_reserve_b=D(1000),
        entry_shares=D(100),
        entry_total_shares=D(1000),
    )
    assert abs(p.net_fraction - p.impermanent_loss) < D("1e-25")
    # And both land on the published 5.72% for a 2x move.
    assert abs(p.net_fraction * 100 - D("-5.7191")) < D("0.001")


def test_no_entry_basis_yields_none_not_zero():
    """82% of in-scope positions are in this state. It must not read as break-even."""
    p = net_pnl(
        shares=D(100),
        total_shares=D(1000),
        reserve_a=D(500),
        reserve_b=D(2000),
    )
    assert p.value_now == D(400)  # 10% of (500 A at price 4) + 200 B
    assert p.net is None
    assert p.net_fraction is None
    assert p.impermanent_loss is None
    assert p.fee_yield is None


def test_fee_yield_is_zero_when_k_per_share_is_unchanged():
    """A pure price move earns nothing: sqrt(k)/shares is price-independent."""
    entry = sqrt_k_per_share(D(1000), D(1000), D(1000))
    now = sqrt_k_per_share(K_1E6_AT_PRICE_2_A, K_1E6_AT_PRICE_2_B, D(1000))
    assert abs(fee_yield(entry, now)) < D("1e-25")


def test_fee_yield_detects_retained_fees():
    """Fees stay in the reserves, so k rises for a fixed share supply."""
    entry = sqrt_k_per_share(D(1000), D(1000), D(1000))
    # k grows 1%: reserves each up 0.5% at unchanged price.
    now = sqrt_k_per_share(D("1005"), D("1005"), D(1000))
    y = fee_yield(entry, now)
    assert abs(y - D("0.005")) < D("1e-25")
