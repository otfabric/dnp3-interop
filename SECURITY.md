# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in `dnp3-interop`, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email **security@otfabric.com**, or open a private advisory on GitHub (the [Security](https://github.com/otfabric/dnp3-interop/security) tab → **Advisories** → **Report a vulnerability**), with a description, steps to reproduce and the affected image tag or digest.

Vulnerabilities in the bundled implementations themselves (opendnp3, Step Function I/O dnp3, dnp3py, DSC Systems go-dnp3, the Triangle MicroWorks library) belong upstream; report them there, and tell us so that the pin can be moved.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Older releases | No; pull the latest images |

## Security Considerations

### These are test fixtures, not products

The images run **unauthenticated, unencrypted DNP3 outstations** that accept polls, controls, restarts and time writes from anyone who can reach them, and clients that send commands to whatever address they are given. That is their purpose.

- **Never attach a server to a plant, control or other production network**
- **Never point a client at a real outstation**: `operate`, `restart`, `time-sync` and the other operations act on what they are sent to
- `compose.yaml` and the `run-*` Makefile targets publish ports on `127.0.0.1` only. Keep it that way; the self-tests use private Docker networks and publish nothing
- There is no TLS and no secure authentication in contract 1.0; when they are added, the keys and certificates are test material baked into the images and published with them, and protect nothing

### Image hardening

- Processes run as a non-root user (uid 10001)
- Base images are pinned by digest; upstream sources by commit or exact version ([versions.yaml](versions.yaml))
- The runtime images contain the adapter, its runtime libraries and the fixtures; build tools stay in the build stages
- The images are rebuilt, not patched: a base image or upstream fix arrives with a new release

### Supply chain

- Upstream sources are fetched during the image build at pinned versions: opendnp3 from GitHub (verified against the pinned commit hash), Step Function I/O dnp3 from crates.io (checksums in `Cargo.lock`), dnp3py from PyPI (hash-pinned `requirements.txt`) and DSC Systems go-dnp3 through the Go module proxy (checksums in `go.sum`). The Triangle MicroWorks source is supplied by the user at build time and verified against the SHA-256 recorded in `versions.yaml`
- Base images are pinned by digest. CI and the release workflow pull them from `mirror.gcr.io/library`, a mirror of Docker Hub's official images, because anonymous Docker Hub pulls are rate limited; the digest makes the source irrelevant to the content (`BASE_REGISTRY`, default `docker.io/library`). The build tooling images (BuildKit, QEMU handlers, shellcheck) come from the same mirror by tag (`TOOLS_REGISTRY`)
- Consumers should pin images by digest, taken from the `manifest.json` of a release
