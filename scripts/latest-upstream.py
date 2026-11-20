#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Compares the upstream pin of one adapter with the latest upstream release.

    scripts/latest-upstream.py <adapter>

Prints key=value lines (the format of $GITHUB_OUTPUT):

    pin=<version in the adapter Dockerfile>
    latest=<latest stable upstream release>
    latest_sha=<its commit, for an adapter that is pinned by commit>
    update=yes|no
    source=<where the latest release was looked up>

Nothing is modified. Pre-releases and yanked releases are never "latest".
"""

import json
import re
import subprocess
import sys
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
AGENT = "dnp3-interop latest-upstream (https://github.com/otfabric/dnp3-interop)"


def fetch(url):
    if not url.startswith("https://"):
        raise ValueError(f"not an https URL: {url}")
    request = urllib.request.Request(url, headers={"User-Agent": AGENT, "Accept": "application/json, */*"})
    # B310: https only, checked above.
    with urllib.request.urlopen(request, timeout=30) as response:  # nosec B310
        return response.read().decode()


def key(version):
    """Sort key of a stable version; None for anything else (pre-releases)."""
    match = re.fullmatch(r"v?(\d+(?:\.\d+)*)", version)
    return tuple(int(part) for part in match.group(1).split(".")) if match else None


def newest(versions):
    stable = [v for v in versions if key(v) is not None]
    if not stable:
        raise SystemExit("no stable release found upstream")
    return max(stable, key=key)


def git_tags(repository):
    """Tags of repository as {tag: commit}; an annotated tag gives its commit."""
    out = subprocess.check_output(["git", "ls-remote", "--tags", repository], text=True)
    commits = {}
    for line in out.splitlines():
        sha, ref = line.split()
        tag = ref.removeprefix("refs/tags/")
        if tag.endswith("^{}"):
            commits[tag[:-3]] = sha
        else:
            commits.setdefault(tag, sha)
    return commits


def opendnp3():
    # The repository is archived (2022); a new tag is not expected, and an
    # untagged commit is not a release.
    tags = git_tags("https://github.com/dnp3/opendnp3.git")
    latest = newest(tags)
    return latest, tags[latest], "git tags of dnp3/opendnp3 (archived)"


def dscsystems():
    versions = fetch("https://proxy.golang.org/github.com/dscsystems/go-dnp3/@v/list").split()
    return newest(versions), "", "Go module proxy"


def dnp3py():
    releases = json.loads(fetch("https://pypi.org/pypi/dnp3py/json"))["releases"]
    live = [v for v, files in releases.items() if files and not all(f.get("yanked") for f in files)]
    return newest(live), "", "PyPI"


def cbsigma():
    # The module path has capitals, which the proxy escapes as !c!b-!sigma.
    versions = fetch("https://proxy.golang.org/github.com/!c!b-!sigma/dnp3/@v/list").split()
    return newest(versions), "", "Go module proxy"


def nfmdnp3():
    releases = json.loads(fetch("https://pypi.org/pypi/nfm-dnp3/json"))["releases"]
    live = [v for v, files in releases.items() if files and not all(f.get("yanked") for f in files)]
    return newest(live), "", "PyPI"


def dnp3c():
    # A test tool on Maven Central (io.stepfunc:dnp3c); the git tag of the
    # release gives its commit, which versions.yaml records.
    metadata = fetch("https://repo1.maven.org/maven2/io/stepfunc/dnp3c/maven-metadata.xml")
    match = re.search(r"<release>([^<]+)</release>", metadata)
    if not match:
        raise SystemExit("maven-metadata.xml of io.stepfunc:dnp3c names no release")
    latest = match.group(1).strip()
    return latest, git_tags("https://github.com/stepfunc/dnp3c.git").get(latest, ""), "Maven Central"


RESOLVERS = {
    "opendnp3": opendnp3,
    "dscsystems": dscsystems,
    "dnp3py": dnp3py,
    "cbsigma": cbsigma,
    "nfmdnp3": nfmdnp3,
    "dnp3c": dnp3c,
}

# Where each pin is: the adapter's ARG UPSTREAM_VERSION, unless listed here.
PINS = {
    "dnp3c": ("conformance/dnp3c/Dockerfile", "DNP3C_VERSION"),
}


def main():
    if len(sys.argv) != 2 or sys.argv[1] not in RESOLVERS:
        raise SystemExit(f"usage: latest-upstream.py {'|'.join(RESOLVERS)}")
    adapter = sys.argv[1]
    path, arg = PINS.get(adapter, (f"adapters/{adapter}/Dockerfile", "UPSTREAM_VERSION"))
    dockerfile = (ROOT / path).read_text()
    match = re.search(rf"^ARG {arg}=(.+)$", dockerfile, re.MULTILINE)
    if not match:
        raise SystemExit(f"{path} has no ARG {arg}=<pin>")
    pin = match.group(1).strip()
    latest, latest_sha, source = RESOLVERS[adapter]()
    print(f"pin={pin}")
    print(f"latest={latest}")
    print(f"latest_sha={latest_sha}")
    print(f"update={'yes' if key(latest) != key(pin) else 'no'}")
    print(f"source={source}")


if __name__ == "__main__":
    main()
