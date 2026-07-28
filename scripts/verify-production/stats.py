#!/usr/bin/env python3
"""stats.py — the statistical core of the production verification suite.

Pure Python 3 standard library: no numpy, no scipy, no pip. The suite has to run
on whatever box a user just installed Appitools on, so it may not assume anything
beyond `python3`.

It exists because a benchmark that reports a mean is not evidence. Every number
this suite publishes goes through here:

  * percentiles       — p50/p90/p95/p99/max from the RAW sample, never averaged
                        across runs (averaging percentiles is a classic error:
                        the mean of per-run p99s is not the p99 of the pooled
                        sample). We pool the raw observations and re-percentile.
  * bootstrap CI      — a distribution-free 95% CI for the median, so a headline
                        number carries its uncertainty.
  * Mann-Whitney U    — the non-parametric A/B test used for every before/after
                        tuning claim. Latency is right-skewed and not normal, so
                        a t-test is the wrong instrument.
  * a verdict gate    — "significant" alone is not enough on a noisy VPS. A change
                        counts as real only if it is BOTH statistically
                        significant (p < 0.05) AND practically material (the
                        median moved by more than max(0.5 ms, 3%)). That is the
                        same gate `make bench-protocol` uses, so verdicts from
                        this suite and from the engine's own bench protocol mean
                        the same thing.

Subcommands (all read JSON on stdin or from a file, write JSON to stdout):

  summarize   one sample  -> {n, min, p50, p90, p95, p99, max, mean, stdev, cv,
                              ci95_median}
  compare     two samples -> {verdict, p_value, delta_ms, delta_pct, ...}

Sample input is either a bare JSON array of numbers, or {"values": [...]}.
"""

import argparse
import json
import math
import statistics
import sys
from bisect import bisect_left

# ── deterministic RNG ────────────────────────────────────────────────────────
# The bootstrap must be REPRODUCIBLE: re-running the analysis on the same raw
# data has to yield the same CI, or "the CI moved" becomes indistinguishable
# from "the code changed". A fixed-seed LCG (no global random state to disturb)
# gives that for free.
class _LCG:
    def __init__(self, seed=20260728):
        self.s = seed & 0xFFFFFFFF

    def next_int(self, n):
        # Numerical Recipes LCG — plenty for resampling indices.
        self.s = (1664525 * self.s + 1013904223) & 0xFFFFFFFF
        return self.s % n


def percentile(sorted_vals, q):
    """Linear-interpolated percentile of an ALREADY SORTED list. q in [0,100]."""
    if not sorted_vals:
        return None
    if len(sorted_vals) == 1:
        return float(sorted_vals[0])
    k = (len(sorted_vals) - 1) * (q / 100.0)
    lo = math.floor(k)
    hi = math.ceil(k)
    if lo == hi:
        return float(sorted_vals[int(k)])
    return float(sorted_vals[lo] * (hi - k) + sorted_vals[hi] * (k - lo))


def bootstrap_ci_median(vals, iters=2000, alpha=0.05):
    """Percentile-bootstrap 95% CI for the median. Distribution-free."""
    n = len(vals)
    if n < 3:
        return None
    rng = _LCG()
    meds = []
    for _ in range(iters):
        sample = [vals[rng.next_int(n)] for _ in range(n)]
        sample.sort()
        meds.append(percentile(sample, 50))
    meds.sort()
    return [
        round(percentile(meds, 100 * alpha / 2), 4),
        round(percentile(meds, 100 * (1 - alpha / 2)), 4),
    ]


def mann_whitney_u(a, b):
    """Two-sided Mann-Whitney U with tie correction (normal approximation).

    Returns (U, p_value). The normal approximation is accurate for n>~10 per
    group, which every comparison in this suite satisfies by construction (we
    compare thousands of individual request latencies, not a handful of run
    aggregates).
    """
    na, nb = len(a), len(b)
    if na == 0 or nb == 0:
        return None, None

    combined = sorted([(v, 0) for v in a] + [(v, 1) for v in b])
    # Rank with ties averaged.
    ranks = [0.0] * len(combined)
    i = 0
    tie_correction = 0.0
    while i < len(combined):
        j = i
        while j + 1 < len(combined) and combined[j + 1][0] == combined[i][0]:
            j += 1
        avg_rank = (i + j) / 2.0 + 1.0
        for k in range(i, j + 1):
            ranks[k] = avg_rank
        t = j - i + 1
        if t > 1:
            tie_correction += t ** 3 - t
        i = j + 1

    rank_sum_a = sum(r for r, (_, g) in zip(ranks, combined) if g == 0)
    u_a = rank_sum_a - na * (na + 1) / 2.0
    u_b = na * nb - u_a
    u = min(u_a, u_b)

    mu = na * nb / 2.0
    n = na + nb
    sigma_sq = (na * nb / 12.0) * ((n + 1) - tie_correction / (n * (n - 1)))
    if sigma_sq <= 0:
        return u, 1.0
    sigma = math.sqrt(sigma_sq)
    # Continuity correction.
    z = (abs(u - mu) - 0.5) / sigma
    p = 2.0 * (1.0 - _norm_cdf(z))
    return u, max(0.0, min(1.0, p))


def _norm_cdf(z):
    return 0.5 * (1.0 + math.erf(z / math.sqrt(2.0)))


def summarize(vals):
    vals = [float(v) for v in vals if v is not None]
    if not vals:
        return {"n": 0}
    s = sorted(vals)
    mean = statistics.fmean(s)
    stdev = statistics.pstdev(s) if len(s) > 1 else 0.0
    return {
        "n": len(s),
        "min": round(s[0], 4),
        "p50": round(percentile(s, 50), 4),
        "p90": round(percentile(s, 90), 4),
        "p95": round(percentile(s, 95), 4),
        "p99": round(percentile(s, 99), 4),
        "max": round(s[-1], 4),
        "mean": round(mean, 4),
        "stdev": round(stdev, 4),
        "cv": round(stdev / mean, 4) if mean else None,
        "ci95_median": bootstrap_ci_median(s),
    }


# Practical-significance floor. Below this, a "statistically significant" move on
# a shared VPS is noise we refuse to sell as an improvement.
MIN_ABS_DELTA_MS = 0.5
MIN_REL_DELTA = 0.03


def compare(baseline, candidate, label_a="baseline", label_b="candidate"):
    """A/B two raw latency samples. Positive delta = candidate is SLOWER."""
    sa, sb = summarize(baseline), summarize(candidate)
    if not sa.get("n") or not sb.get("n"):
        return {"verdict": "no_data", "a": sa, "b": sb}

    u, p = mann_whitney_u(baseline, candidate)
    delta = sb["p50"] - sa["p50"]
    delta_pct = (delta / sa["p50"]) if sa["p50"] else 0.0

    material = abs(delta) >= MIN_ABS_DELTA_MS or abs(delta_pct) >= MIN_REL_DELTA
    significant = p is not None and p < 0.05

    if significant and material:
        verdict = "regression" if delta > 0 else "improvement"
    elif significant and not material:
        verdict = "no_change"  # detectable but too small to matter
    else:
        verdict = "no_change"

    return {
        "verdict": verdict,
        "label_a": label_a,
        "label_b": label_b,
        "p_value": round(p, 6) if p is not None else None,
        "u": u,
        "significant": significant,
        "material": material,
        "delta_p50_ms": round(delta, 4),
        "delta_pct": round(delta_pct * 100, 2),
        "gate": f"|delta| >= max({MIN_ABS_DELTA_MS}ms, {int(MIN_REL_DELTA*100)}%)",
        "a": sa,
        "b": sb,
    }


def _load(path):
    raw = sys.stdin.read() if path in (None, "-") else open(path).read()
    data = json.loads(raw)
    if isinstance(data, dict):
        data = data.get("values", data.get("latencies", []))
    return [float(v) for v in data]


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    sub = ap.add_subparsers(dest="cmd", required=True)

    s = sub.add_parser("summarize", help="percentiles + bootstrap CI of one sample")
    s.add_argument("--file", default="-")

    c = sub.add_parser("compare", help="Mann-Whitney A/B of two samples")
    c.add_argument("--a", required=True)
    c.add_argument("--b", required=True)
    c.add_argument("--label-a", default="baseline")
    c.add_argument("--label-b", default="candidate")

    args = ap.parse_args()
    if args.cmd == "summarize":
        print(json.dumps(summarize(_load(args.file)), indent=2))
    else:
        print(json.dumps(
            compare(_load(args.a), _load(args.b), args.label_a, args.label_b),
            indent=2))


if __name__ == "__main__":
    main()
