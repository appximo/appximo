#!/usr/bin/env python3
"""report.py — turn a results directory into ONE readable verdict.

The suite writes a lot of JSON. This collapses it into a Markdown report that a
human can act on: what the stack costs, what it sustains, what breaks it, and
whether the numbers are trustworthy.

  python3 report.py --dir verify-results/20260728T150000Z --out report.md

Design rule: every number carries the condition that produced it. A latency
without "which cache arm, which rate, measured from where, with a CDN in front or
not" is not a result, it is a rumour — so each table repeats those conditions
even when it makes the table wider.
"""

import argparse
import glob
import json
import os
import sys


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception:
        return None


def find(d, pattern):
    return sorted(glob.glob(os.path.join(d, pattern), recursive=True))


def fmt(v, unit="", nd=2):
    if v is None:
        return "—"
    if isinstance(v, (int, float)):
        return f"{v:.{nd}f}{unit}"
    return str(v)


def section_footprint(d, out):
    # dict.fromkeys, not a set: the two globs overlap (the second subsumes the
    # first), and a plain set would also scramble the order the snapshots are
    # reported in — idle must come before under-load to read as a progression.
    files = list(dict.fromkeys(find(d, "footprint*.json") + find(d, "**/footprint*.json")))
    snaps = [(os.path.basename(f), load(f)) for f in files]
    snaps = [(n, s) for n, s in snaps if isinstance(s, dict) and s.get("services")]
    if not snaps:
        return
    out.append("## 1. Footprint — what the stack costs at rest and under load\n")
    out.append("PSS (proportional set size) is used, not RSS: RSS double-counts the shared")
    out.append("pages every PostgreSQL backend maps, which is how PostgreSQL gets reported")
    out.append("as several times larger than it is. `anon` is memory the process really")
    out.append("owns; the remainder is mapped binary/library page cache the kernel reclaims")
    out.append("under pressure — the number that matters for \"does this fit in 1 GB\".\n")
    out.append("| state | service | PSS MiB | anon MiB | procs | fds | CPU %core |")
    out.append("|---|---|---:|---:|---:|---:|---:|")
    for name, s in snaps:
        label = s.get("label", name)
        for svc, v in sorted(s.get("services", {}).items(), key=lambda kv: -kv[1].get("pss_mib", 0)):
            cpu = (v.get("cpu") or {}).get("pct_one_core")
            out.append(f"| {label} | {svc} | {fmt(v.get('pss_mib'),'',1)} | "
                       f"{fmt(v.get('anon_mib'),'',1)} | {v.get('processes','—')} | "
                       f"{v.get('open_fds','—')} | {fmt(cpu,'',1)} |")
        sysm = s.get("system", {})
        out.append(f"| {label} | **TOTAL STACK** | **{fmt(s.get('stack_pss_mib'),'',1)}** | "
                   f"**{fmt(s.get('stack_anon_mib'),'',1)}** | | | |")
        out.append(f"| {label} | _system_ | total {fmt(sysm.get('mem_total_mib'),'',0)} | "
                   f"available {fmt(sysm.get('mem_available_mib'),'',0)} | | | used {sysm.get('pct_used')}% |")
    out.append("")


def section_load(d, out):
    # NOTE the exclusion: "<x>-raw-pooled.json" is a bare ARRAY of latencies, not
    # a result object. Globbing "*-pooled.json" happily matches it and then every
    # .get() blows up — filter by shape, not just by name.
    # Excluded:
    #  * "-raw-pooled.json" — a bare ARRAY of latencies, not a result object.
    #  * anything under fp-load/ — that load exists only to give the under-load
    #    footprint something to measure; reporting it as a scenario would put a
    #    deliberately-unrepresentative run in the capacity table.
    # This table is "one row per scenario/arm at the headline rate". Three kinds of
    # pooled file must stay OUT of it or it stops meaning that:
    #   * "-raw-pooled.json"  — a bare ARRAY of latencies, not a result object
    #   * fp-load/            — load that exists only to give the under-load
    #                           footprint something to measure
    #   * ladder/ and tuning/ — rungs of the capacity ladder and the before/after
    #                           arms of a tuning A/B. Both are reported in their
    #                           OWN sections; listing them here would show the
    #                           same scenario three times with no explanation of
    #                           why the numbers differ.
    skip = ("/fp-load/", "/ladder/", "/tuning/", "/superseded")
    files = [f for f in find(d, "**/*-pooled.json")
             if not f.endswith("-raw-pooled.json") and not any(k in f for k in skip)]
    rows = []
    for f in files:
        s = load(f)
        if not isinstance(s, dict) or not s.get("per_run"):
            continue
        t = s.get("totals", {})
        stats_f = f.replace("-pooled.json", "-stats.json")
        st = load(stats_f) or {}
        rows.append((s, t, st))
    if not rows:
        return
    out.append("## 2. Load — what the production surface sustains\n")
    out.append("Percentiles come from the POOLED raw sample of every measurement run, not")
    out.append("from averaging each run's percentile (the mean of per-run p99s is not the")
    out.append("p99). `cache` says whether the engine's 5 s response cache was allowed to")
    out.append("serve — the bypassed arm is the floor, where every request reaches Postgres.\n")
    out.append("| scenario | rate | cache | n | p50 ms | p90 | p95 | p99 | max | err | achieved rps | run-to-run CV |")
    out.append("|---|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
    for s, t, st in sorted(rows, key=lambda r: (r[0]["scenario"], r[0]["rate_requested"])):
        cache = "bypassed" if s.get("cache_bust") else "on"
        err = t.get("error_rate_max")
        rows_p = s.get("per_run") or [{}]
        lat = st if st.get("n") else (rows_p[0].get("latency_ms") or {})
        out.append(
            f"| {s['scenario']} | {s['rate_requested']} | {cache} | {lat.get('n','—')} | "
            f"{fmt(lat.get('p50'))} | {fmt(lat.get('p90'))} | {fmt(lat.get('p95'))} | "
            f"{fmt(lat.get('p99'))} | {fmt(lat.get('max'))} | "
            f"{fmt((err or 0)*100,'%',2)} | {fmt(t.get('rps_achieved_mean'),'',0)} | "
            f"{fmt((t.get('p50_cv') or 0)*100,'%',1)} |")
    conds = rows[0][0]
    notes = []
    if conds.get("cdn_detected"):
        if conds.get("origin_ip"):
            notes.append(f"A CDN (`{conds['cdn_detected']}`) fronts this domain; the numbers above "
                         f"were measured against the ORIGIN (`{conds['origin_ip']}`), i.e. the product itself.")
        else:
            notes.append(f"**A CDN (`{conds['cdn_detected']}`) fronts this domain and was NOT bypassed** — "
                         f"these numbers include the CDN's hop and TLS, not just this stack.")
    if conds.get("colocated_loader"):
        notes.append("**The load generator ran on the server itself**, so these numbers include "
                     "loader/server CPU contention and understate the stack.")
    for n in notes:
        out.append(f"\n> {n}")
    out.append("")

    lad = find(d, "ladder-*.json")
    if lad:
        out.append("### Capacity — where the knee is\n")
        out.append("| scenario | levels driven | highest clean | knee |")
        out.append("|---|---|---:|---:|")
        for f in lad:
            s = load(f)
            if not s:
                continue
            out.append(f"| {s['scenario']} | {' '.join(s['levels'])} | "
                       f"{s.get('highest_clean_rps') or '—'} | {s.get('knee_rps') or 'not reached'} |")
        out.append("\nSaturation = median p95 > 100 ms, or error rate > 1%, or the offered rate")
        out.append("could not be delivered. \"not reached\" means the ladder ran out of levels")
        out.append("before the server ran out of capacity.\n")


def section_layers(d, out):
    f = os.path.join(d, "tls-overhead.json")
    s = load(f)
    if not s:
        return
    out.append("## 3. What the production layers cost\n")
    a, b = s["a"], s["b"]
    out.append(f"| path | p50 ms | p95 ms | n |")
    out.append("|---|---:|---:|---:|")
    out.append(f"| {s['label_a']} | {fmt(a['p50'])} | {fmt(a['p95'])} | {a['n']} |")
    out.append(f"| {s['label_b']} | {fmt(b['p50'])} | {fmt(b['p95'])} | {b['n']} |")
    out.append(f"\n**Overhead of TLS + the reverse proxy: {s['delta_p50_ms']:+.2f} ms p50 "
               f"({s['delta_pct']:+.1f}%)**, Mann-Whitney p={s['p_value']}.\n")


def section_chaos(d, out):
    files = find(d, "chaos/*.json")
    cases = [load(f) for f in files]
    cases = [c for c in cases if isinstance(c, dict) and c.get("part") == "C-chaos"]
    if not cases:
        return
    out.append("## 6. Resilience — what breaks, and what the user loses\n")
    out.append("Each row: a fault injected while a probe kept requesting at a fixed rate.")
    out.append("\"Requests lost\" is measured, not inferred — the probe records every outcome,")
    out.append("including connection refused and timeouts.\n")
    out.append("| fault | requests lost | codes the user saw | time to 1st error | outage | recovered by itself |")
    out.append("|---|---:|---|---:|---:|---|")
    order = ["engine-kill", "caddy-kill", "postgres-stop", "pool-exhaust",
             "memory-pressure", "deploy-update", "reboot"]
    cases.sort(key=lambda c: order.index(c["case"]) if c["case"] in order else 99)
    for c in cases:
        if c.get("skipped"):
            out.append(f"| {c['case']} | — | — | — | — | _skipped: {c.get('skip_reason','')}_ |")
            continue
        codes = ", ".join(f"{k}×{v}" for k, v in (c.get("failure_codes") or {}).items()) or "none"
        out.append(
            f"| {c['case']} | {c['requests_failed_after_fault']} / {c['requests_after_fault']} | {codes} | "
            f"{fmt(c.get('time_to_first_error_s'),'s')} | {fmt(c.get('outage_s'),'s')} | "
            f"{'yes' if c.get('recovered') else '**NO**'} |")
    out.append("")


def section_scale(d, out):
    files = find(d, "scale/*.json") + find(d, "**/scale-*.json")
    entries = [(os.path.basename(f), load(f)) for f in files]
    entries = [(n, s) for n, s in entries if isinstance(s, dict)]
    if not entries:
        return
    out.append("## 4. Scale — behaviour as the table grows\n")
    out.append("| dataset | scenario | rate | p50 ms | p95 | p99 | err |")
    out.append("|---|---|---:|---:|---:|---:|---:|")
    for n, s in entries:
        lat = s.get("latency_ms", {})
        out.append(f"| {s.get('dataset','—')} | {s.get('scenario','—')} | {s.get('rate_requested','—')} | "
                   f"{fmt(lat.get('p50'))} | {fmt(lat.get('p95'))} | {fmt(lat.get('p99'))} | "
                   f"{fmt((s.get('error_rate') or 0)*100,'%',2)} |")
    out.append("")


def section_restgql(d, out):
    f = os.path.join(d, "rest-vs-graphql.json")
    s = load(f)
    if not s:
        return
    out.append("## 5. REST vs GraphQL — the same data, both ways\n")
    out.append("| shape | round trips per view | p50 ms | p95 ms | bytes/request |")
    out.append("|---|---:|---:|---:|---:|")
    for arm in s.get("arms", []):
        out.append(f"| {arm['name']} | {arm.get('round_trips','—')} | {fmt(arm.get('p50'))} | "
                   f"{fmt(arm.get('p95'))} | {arm.get('bytes_per_request','—')} |")
    if s.get("verdict"):
        out.append(f"\n{s['verdict']}\n")


def section_tuning(d, out):
    files = find(d, "tuning/*.json")
    entries = [load(f) for f in files]
    entries = [e for e in entries if isinstance(e, dict)]
    if not entries:
        return
    out.append("## 7. Tuning — what was changed and whether it actually helped\n")
    out.append("A change is kept only if it is BOTH statistically significant (Mann-Whitney")
    out.append("p < 0.05) AND practically material (median moved more than max(0.5 ms, 3%)).")
    out.append("\"no_change\" means the tuning did nothing measurable and was NOT adopted.\n")
    out.append("| change | before p50 | after p50 | delta | p | verdict |")
    out.append("|---|---:|---:|---:|---:|---|")
    for e in entries:
        cmp_ = e.get("comparison") or e
        a, b = cmp_.get("a", {}), cmp_.get("b", {})
        out.append(f"| {e.get('name', cmp_.get('label_b','—'))} | {fmt(a.get('p50'))} | {fmt(b.get('p50'))} | "
                   f"{fmt(cmp_.get('delta_p50_ms'),' ms')} ({fmt(cmp_.get('delta_pct'),'%',1)}) | "
                   f"{cmp_.get('p_value','—')} | {cmp_.get('verdict','—')} |")
    out.append("")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dir", required=True)
    ap.add_argument("--out", default="")
    ap.add_argument("--title", default="Appximo — production verification report")
    args = ap.parse_args()

    d = args.dir
    if not os.path.isdir(d):
        sys.exit(f"no such results directory: {d}")

    meta = load(os.path.join(d, "run-meta.json")) or {}
    out = [f"# {args.title}\n"]
    if meta:
        out.append("| | |")
        out.append("|---|---|")
        for k in ("started_utc", "target", "origin_ip", "cdn_detected", "tenant",
                  "server", "loader", "dataset", "suite_commit"):
            if meta.get(k):
                out.append(f"| {k.replace('_',' ')} | `{meta[k]}` |")
        out.append("")

    section_footprint(d, out)
    section_load(d, out)
    section_layers(d, out)
    section_scale(d, out)
    section_restgql(d, out)
    section_chaos(d, out)
    section_tuning(d, out)

    out.append("---\n")
    out.append("Generated by `scripts/verify-production/report.py`. Raw JSON for every number")
    out.append("in this report is in the same directory — nothing here is hand-written.\n")

    text = "\n".join(out)
    if args.out:
        with open(args.out, "w") as fh:
            fh.write(text)
        print(f"wrote {args.out}")
    else:
        print(text)


if __name__ == "__main__":
    main()
