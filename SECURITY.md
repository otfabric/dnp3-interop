# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in `dnp3-interop`, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email **security@otfabric.io**, or open a private advisory on GitHub (the [Security](https://github.com/otfabric/dnp3-interop/security) tab → **Advisories** → **Report a vulnerability**), with a description, steps to reproduce and the affected image tag or digest.

Vulnerabilities in the bundled implementations themselves (opendnp3, dnp3py, DSC Systems go-dnp3, cB-Sigma dnp3, nfm-dnp3) belong upstream; report them there, and tell us so that the pin can be moved.

## Supported Versions

| Version | Supported |
|---------|-----------|
| v0.12.x (latest, v0.12.0) | Yes |
| < v0.12 | No; pull the latest images (and build the dnp3c image locally) |

## Security Considerations

### These are test fixtures, not products

The images run **DNP3 outstations, unauthenticated and unencrypted by default,** that accept polls, controls, restarts and time writes from anyone who can reach them, and clients that send commands to whatever address they are given. That is their purpose.

- **Never attach a server to a plant, control or other production network**
- **Never point a client at a real outstation**: `operate`, `restart`, `time-sync` and the other operations act on what they are sent to
- `compose.yaml` and the `run-*` Makefile targets publish ports on `127.0.0.1` only. Keep it that way; the self-tests use private Docker networks and publish nothing
- TLS and secure authentication (contract 1.1) are optional. The self-tests make throw-away certificates per run (`scripts/test-pki.sh`); keys passed with `--tls-key` or `--sa-update-key` are test material and protect nothing. The images contain no certificates or keys

### Image hardening

- Processes run as a non-root user (uid 10001)
- Base images are pinned by digest; upstream sources by commit or exact version; Debian packages by version, from a fixed snapshot of the Debian archive ([versions.yaml](versions.yaml))
- The runtime images contain the adapter, its runtime libraries and the fixtures; build tools stay in the build stages
- The images are rebuilt, not patched: a base image or upstream fix arrives with a new release

### Supply chain

- Upstream sources are fetched during the image build at pinned versions: opendnp3 from GitHub (verified against the pinned commit hash), dnp3py and nfm-dnp3 from PyPI (hash-pinned `requirements.txt`), DSC Systems go-dnp3 and cB-Sigma dnp3 through the Go module proxy (checksums in `go.sum`).
- CI runs static checks on every push: govulncheck on the three Go modules with the Go of the images, hadolint on every Dockerfile, shellcheck on every shell script, ruff and bandit on the Python files. The tools are pinned by version and digest, their Python packages by hash.
- Base images are pinned by digest. CI and the release workflow pull them from `mirror.gcr.io/library`, a mirror of Docker Hub's official images, because anonymous Docker Hub pulls are rate limited; the digest makes the source irrelevant to the content (`BASE_REGISTRY`, default `docker.io/library`). The build tooling images (BuildKit, QEMU handlers) come from the same mirror by tag, the linters and test tools by version and digest (`TOOLS_REGISTRY`)
- Consumers should pin the published images by digest, taken from the `manifest.json` of a release.
- Every release is scanned with Trivy (pinned by digest) after publishing, and every published image gets a CycloneDX SBOM per architecture; `vulnerabilities.md` and `sboms.tar.gz` are attached to the release. Findings are recorded, not gated on: the images are test fixtures, never deployed. Findings with a fix available are taken up in the next release by moving the base image digest or the package; the rest disappear when upstream ships a fix. Trivy sees the Debian packages, the Python packages of the dnp3py and nfmdnp3 images and the Go modules and Go standard library of the dscsystems and cbsigma binaries, not what is statically compiled into the C++ binary (their versions are in `versions.yaml` and the release's `manifest.json`)
