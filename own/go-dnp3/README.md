# dnp3-interop-otfabric

[go-dnp3](https://github.com/otfabric/go-dnp3), OT Fabric's own DNP3 stack,
behind the [container contract](../../docs/CONTAINER_CONTRACT.md), so that
other stacks can test against it. Anything that uses a
`dnp3-interop-<adapter>` image can use this one in its place. Unlike those, it
is not a reference: its tags are go-dnp3 versions, and no harness release or
`manifest.json` contains it.

**For testing only.** It runs an unauthenticated DNP3 outstation unless
`--sa-*` or `--tls` options are given. Do not expose it to a network you do not
control. Old images stay available, so check
[go-dnp3 advisories](https://github.com/otfabric/go-dnp3/security/advisories)
for the version you pull.

**Not an independent implementation.** The image declares
`upstream.independentEngine: false`, and `results.json` records
`independent: false`. A result against it says nothing independent about
go-dnp3 when OT Fabric produces it. A result you publish, naming the
implementation and version, is independent evidence, and we would like to hear
about it.

## Use

```sh
docker run -d --name go-dnp3 -p 127.0.0.1:20000:20000 ghcr.io/otfabric/dnp3-interop-otfabric:<tag> server
until docker exec go-dnp3 test -f /run/dnp3-interop/ready; do sleep 0.1; done
```

`<tag>` is a go-dnp3 version such as `v1.6.0`, or `latest`, the newest one
published. A rebuild of a version replaces its tag, so pin the digest if you need
an exact build. The digest is in the summary of the
[workflow run](https://github.com/otfabric/dnp3-interop/actions/workflows/own-go-dnp3.yml),
and `docker run --rm <image> version` prints the versions.

| | |
|---|---|
| Upstream | go-dnp3, the latest release when the image was built |
| Licence | MIT; every module's licence is in the image under `/licenses` |
| Adapter name | `otfabric` (the name in documents, as the schema allows) |
| Language | Go |
| Protocol engine | OT Fabric's own |

## Why it is apart from the other adapters

`REQUIREMENTS.md` R1: nothing under `adapters/`, `conformance/`, `scripts/`,
`tests/` or the top-level `Makefile` knows a library under test, and CI enforces
that. This directory and `.github/workflows/own-go-dnp3.yml` are the one
exception. The scripts take the image through `ADAPTER_IMAGE_otfabric`.

No release loop can form (`otfabric/engineering` ADR-0012). This image is built
by hand, from the latest go-dnp3 release, and nothing is committed: this
repository has no pin of go-dnp3 to bump. No release or required check of
go-dnp3 needs the image, and go-dnp3 commits no pin of it. It is not in a
harness release or `manifest.json`.

## Publishing

Run the **Own stack (go-dnp3)** workflow by hand, from `main`. It finds the
latest go-dnp3 release and the latest harness release, and prints them. It
builds the image, runs the smoke test, the cross-stack self-test on the
baseline and the dnp3py fixture, and the feature self-test against opendnp3,
DSC go-dnp3 and dnp3py on both architectures, then publishes
`:<go-dnp3 version>` and `:latest`. Run from a branch, it stops before
publishing. Nothing runs it automatically, so after a go-dnp3 release the image
stays on the old version until someone does.

```sh
make -f own/go-dnp3/Makefile image smoke interop features    # from the repository root
```

## What it cannot do

Every feature of the contract (1.5) is declared `true`, with these limits in
what the capabilities list:

- Secure authentication: no AES-GMAC (MAC algorithm 6), so `macAlgorithms`
  leaves out `aes-gmac` and `--sa-mac aes-gmac` is a usage error. No
  aggressive mode. One user per outstation.
- Frozen event policy: `on-change` only. The library raises a frozen counter
  event when the frozen value or its flags change, and has no setting for it;
  `--frozen-event-policy always` is a usage error.

Where the library hands an application nothing, the adapter derives the event
from the library's metrics callbacks (`dnp3.Metrics`): `link-status`,
`broadcast`, `auth` and `auth-error`, and the connection events of a dialling
outstation. The `broadcast` event matches a fragment to the frames that carried
it by sequence number and function code, since a fragment's metrics do not say
its destination address.

The client follows the contract with library calls that do a little more or
less than one operation:

- `operate-multi` sends what `client.Operate` sends: consecutive controls of
  one object type in one header, not one header per control.
- `freeze --no-ack --at` (FREEZE AT TIME NO ACK) and the `manage` functions
  15..19 go through `client.Request`; the library has no call of their own.
  `manage --function activate-config` uses `client.ActivateConfig`, and the
  outstation answers it through `server.ActivateConfigHandler`.
- `--no-confirm` and `monitor --no-unsolicited-confirm` (contract 1.5) are the
  options `client.WithSolicitedConfirm(false)` and
  `client.WithUnsolicitedConfirm(false)`. The library hands a repeated
  unsolicited response's events to the handler once, so `unsolicitedFragments`
  is counted from the metrics of the received fragments.
- `operate --mode operate` and `--operate-delay-ms` (contract 1.5) send the
  SELECT and the OPERATE as separate `client.Request` calls, since
  `client.Operate` sends both in one turn; the status is read from the echo.
- A refused control is reported `rejected` from the IIN of the response, which
  `client.Operate` does not return: the adapter takes it from the metrics of
  the response.
- `read` of an attribute set reads the set with a range header, since
  `ReadAttributeSet` returns a map, without the order of the objects.
- Variations without flags (g1v1, g10v1, g20v5, ...) are reported with flags
  `ONLINE`, as the other masters do; the library decodes them with flags 0.
- `--transport udp` binds the local port itself: `client.UDP` cannot.

Three differences from the reference outstations are permitted, and the
checks accept them:

- While its time is unsynchronized (`needTime` in the fixture, until a master
  writes the time), a class poll returns binary input events with relative
  time under an unsynchronized CTO (g2v3 after g51v2), not in the point's event
  variation. This is on purpose: g2v2 cannot say that its time is
  unsynchronized.
- The security statistics carry the master's address as their association;
  DSC go-dnp3 uses 0.
- A read of every attribute of a set (g0v254) returns them from the highest
  variation down.

See [COMPATIBILITY.md](../../docs/COMPATIBILITY.md#ot-fabric-go-dnp3-permitted-differences).
