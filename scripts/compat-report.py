#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Writes a compatibility report from the documents the self-tests kept.

    scripts/compat-report.py <reports directory> [--title TEXT]

Run the self-tests with REPORT_DIR set first (make documents does), so that
the directory holds the capability documents (capabilities-*.json) and the
outcome of every check (results-*.tsv: smoke per adapter, interop per server
adapter, and the agreement across pairings). Prints Markdown on stdout:

  - the adapters, their upstreams and roles
  - totals of the smoke, cross-stack and contract 1.1 feature self-tests
  - a client x server matrix of passed, failed, known-bug and skipped checks
  - every failure, every known upstream bug hit, the skips by reason
  - the declared features per adapter

The report is about the reference stacks; it says nothing about a library
that consumes the images.
"""

import json
import re
import sys
from collections import Counter, defaultdict
from pathlib import Path


def read_results(path):
    rows = []
    for line in path.read_text().splitlines():
        if "\t" in line:
            outcome, label = line.split("\t", 1)
            rows.append((outcome, label))
    return rows


PAIR = re.compile(r"^([a-z0-9]+)->([a-z0-9]+)")


def main(argv):
    args = [a for a in argv if not a.startswith("--")]
    title = "Compatibility report"
    if "--title" in argv:
        title = argv[argv.index("--title") + 1]
        args.remove(title)
    if len(args) != 1:
        print(__doc__, file=sys.stderr)
        return 2
    reports = Path(args[0])

    caps = {}
    for f in sorted(reports.glob("capabilities-*.json")):
        doc = json.loads(f.read_text())
        caps[doc["adapter"]] = doc
    if not caps:
        print(f"no capability documents in {reports}", file=sys.stderr)
        return 1
    adapters = list(caps)
    servers = [a for a in adapters if caps[a]["roles"]["server"]]
    clients = [a for a in adapters if caps[a]["roles"]["client"]]

    smoke = {}
    for a in adapters:
        f = reports / f"results-smoke-{a}.tsv"
        smoke[a] = Counter(o for o, _ in read_results(f)) if f.exists() else None

    interop = []
    for f in sorted(reports.glob("results-interop-*.tsv")):
        interop.extend(read_results(f))
    features = []
    for f in sorted(reports.glob("results-features-*.tsv")):
        features.extend(read_results(f))
    if not interop:
        print(f"no cross-stack results in {reports}", file=sys.stderr)
        return 1

    matrix = defaultdict(Counter)
    failures, known, skips = [], defaultdict(list), Counter()
    for outcome, label in interop + features:
        m = PAIR.match(label)
        if m:
            matrix[(m.group(1), m.group(2))][outcome] += 1
        if outcome == "fail":
            failures.append(label)
        elif outcome == "known":
            what, _, bug = label.partition(" -- ")
            known[bug].append(what)
        elif outcome == "skip":
            reason = re.search(r"\(needs ([^)]*)\)", label)
            skips[f"needs {reason.group(1)}" if reason else re.sub(r"^[a-z0-9]+->[a-z0-9]+: ", "", label)] += 1

    totals = Counter(o for o, _ in interop)
    ftotals = Counter(o for o, _ in features)
    out = [f"# {title}", ""]
    out.append("Generated from the documents of the smoke and cross-stack self-tests of the")
    out.append("reference images. It is about the reference stacks and says nothing about a")
    out.append("library that consumes the images.")
    out.append("")
    out.append("## Adapters")
    out.append("")
    out.append("| Adapter | Upstream | Version | Licence | Server | Client | Image version |")
    out.append("|---------|----------|---------|---------|:------:|:------:|---------------|")
    for a in adapters:
        c = caps[a]
        u = c["upstream"]
        yes = lambda b: "yes" if b else "no"
        out.append(f"| `{a}` | {u['name']} | {u['version']} | {u['license']} | {yes(c['roles']['server'])} "
                   f"| {yes(c['roles']['client'])} | {c['adapterVersion']} |")
    out.append("")
    out.append("## Totals")
    out.append("")
    out.append("| Suite | Passed | Failed | Known upstream bugs | Skipped |")
    out.append("|-------|-------:|-------:|--------------------:|--------:|")
    for a in adapters:
        s = smoke[a]
        if s is None:
            out.append(f"| smoke `{a}` | not run | | | |")
        else:
            out.append(f"| smoke `{a}` | {s['ok']} | {s['fail']} | | |")
    out.append(f"| cross-stack | {totals['ok']} | {totals['fail']} | {totals['known']} | {totals['skip']} |")
    if features:
        out.append(f"| contract 1.1 features (TLS, UDP, serial, files, SAv5) | {ftotals['ok']} | {ftotals['fail']} "
                   f"| {ftotals['known']} | {ftotals['skip']} |")
    out.append("")
    out.append("## Pairings")
    out.append("")
    out.append("Checks per client (rows) and server (columns), cross-stack and contract 1.1 features together:")
    out.append("passed / failed / known upstream bug / skipped.")
    out.append("The agreement of documents across pairings is counted for the pairing it names first.")
    out.append("")
    out.append("| Client \\ Server | " + " | ".join(f"`{s}`" for s in servers) + " |")
    out.append("|---|" + "|".join(":-:" for _ in servers) + "|")
    for c in clients:
        cells = []
        for s in servers:
            m = matrix.get((c, s))
            cells.append(f"{m['ok']} / {m['fail']} / {m['known']} / {m['skip']}" if m else "—")
        out.append(f"| `{c}` | " + " | ".join(cells) + " |")
    out.append("")
    out.append("## Failures")
    out.append("")
    out.extend([f"- {f}" for f in failures] or ["None."])
    out.append("")
    out.append("## Known upstream bugs hit")
    out.append("")
    if known:
        for bug, where in sorted(known.items()):
            out.append(f"- **{bug}** ({len(where)}): " + "; ".join(sorted(set(where))))
    else:
        out.append("None.")
    out.append("")
    out.append("## Skipped checks by reason")
    out.append("")
    if skips:
        out.append("| Reason | Checks |")
        out.append("|--------|-------:|")
        for reason, n in skips.most_common():
            out.append(f"| {reason} | {n} |")
    else:
        out.append("None.")
    out.append("")
    out.append("## Declared features")
    out.append("")
    features = sorted({f for c in caps.values() for f in c["features"]})
    out.append("| Feature | " + " | ".join(f"`{a}`" for a in adapters) + " |")
    out.append("|---|" + "|".join(":-:" for _ in adapters) + "|")
    for f in features:
        out.append(f"| `{f}` | " + " | ".join("yes" if caps[a]["features"].get(f) else "no" for a in adapters) + " |")
    print("\n".join(out))
    return 1 if totals["fail"] or ftotals["fail"] or any(s and s["fail"] for s in smoke.values()) else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
