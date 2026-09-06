"""Sample test: for positions whose balance moved inside Horizon's retention
window, does the account's in-window effect history fully explain the balance?

If sum(shares_received) - sum(shares_redeemed) over the window equals the
current balance, the position began at zero inside the window and its entry
basis is completely recoverable. If it does not, the position predates the
window and only a partial history exists.
"""
import json, random, urllib.request, sys
from decimal import Decimal
from collections import defaultdict

ELDER = 57993841
D = 'data/runs/20260906T063725Z'
SAMPLE = int(sys.argv[1]) if len(sys.argv) > 1 else 60

pools = {}
for l in open(D + '/pools.jsonl'):
    p = json.loads(l); pools[p['id']] = p
scope = {p['id'] for p in pools.values()
         for r in p['reserves']
         if r['asset'] == 'native' and Decimal(r['amount']) >= 1000 and p['total_trustlines'] > 1}

cands = []
for l in open(D + '/positions.jsonl'):
    s = json.loads(l)
    if s['pool_id'] in scope and Decimal(s['shares']) > 0 and s['last_modified_ledger'] >= ELDER:
        cands.append(s)

random.seed(20260906)
sample = random.sample(cands, min(SAMPLE, len(cands)))
print(f"candidates: {len(cands)}   sampling: {len(sample)}\n")

def get(u):
    for _ in range(4):
        try:
            return json.load(urllib.request.urlopen(u, timeout=30))
        except Exception:
            pass
    return None

complete = partial = nodata = 0
for i, s in enumerate(sample, 1):
    acct, pid = s['account_id'], s['pool_id']
    bal = Decimal(s['shares'])
    url = f"https://horizon.stellar.org/accounts/{acct}/effects?limit=200&order=desc"
    net = Decimal(0); seen = 0; pages = 0; reached_elder = False
    while url and pages < 25:
        d = get(url)
        if d is None: break
        recs = d['_embedded']['records']
        if not recs: break
        pages += 1
        for r in recs:
            lp = r.get('liquidity_pool') or {}
            if lp.get('id') != pid: continue
            if r['type'] == 'liquidity_pool_deposited':
                net += Decimal(r.get('shares_received', '0')); seen += 1
            elif r['type'] == 'liquidity_pool_withdrew':
                net -= Decimal(r.get('shares_redeemed', '0')); seen += 1
        url = d['_links']['next']['href']
    if seen == 0:
        nodata += 1; verdict = "no in-window events"
    elif net == bal:
        complete += 1; verdict = "COMPLETE (opened in-window)"
    else:
        partial += 1; verdict = f"partial (net {net} vs balance {bal})"
    if i <= 12 or verdict.startswith("COMPLETE"):
        print(f"  {i:>3}. {acct[:10]}… {pid[:10]}… events={seen:<3} {verdict}")

n = len(sample)
print(f"\n=== result over {n} sampled in-window positions ===")
print(f"  entry basis COMPLETE (position opened inside the window): {complete} ({Decimal(complete)*100/n:.1f}%)")
print(f"  partial history only (predates the window):               {partial} ({Decimal(partial)*100/n:.1f}%)")
print(f"  no liquidity events found in 25 pages:                    {nodata} ({Decimal(nodata)*100/n:.1f}%)")
