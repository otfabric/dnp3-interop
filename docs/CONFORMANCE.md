# The dnp3c runner

The image `dnp3-interop-dnp3c` runs the DNP Users Group's *DNP3 IED
Certification Procedure* against an outstation, using Step Function I/O's
[dnp3c](https://github.com/stepfunc/dnp3c). dnp3c implements the procedure
independently, in two revisions:

- 3.1 rev 1, subset levels 1 to 3;
- 2.7, subset level 2.

dnp3c acts as the master. Some steps cannot be done over DNP3: restarting
the device, changing its configuration, generating events, checking that an
output operated. For those, the runner's integration plugin calls the
device under test (DUT) on a control port. The DUT does these steps and
checks what dnp3c read against what it holds.

The DUT is any outstation that implements the control protocol below; this
repository has none. go-dnp3 has one, built on its library.

The results show how the device answers the procedure. They are not a
certification: only the DNP Users Group certifies devices.

## Licence

dnp3c is licensed under the GNU General Public License v3.0 only. The
integration plugin and runner in `conformance/dnp3c/` link it, so they are
GPL-3.0-only too ([conformance/LICENSE](../conformance/LICENSE)). The image
build fetches dnp3c 0.2.0 and its dependencies from Maven Central, with
strict checksums.

The image is **built locally and not published**, like the stepfunc image.
GPL-3.0 does allow publishing it, with the corresponding source, but that is
a separate decision. A DUT talks to the runner over TCP and is not affected
by its licence. See
[THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md#image-dnp3-interop-dnp3c).

## Building and running

```bash
make image-dnp3c          # dnp3-interop-dnp3c:dev
```

Mount a directory with the DUT's dnp3c configuration (`dnp3c -write`
lists every option) at `/work`. dnp3c writes its results there, under
`results/<revision>/L<level>/`, with `report.html`. The arguments are:

1. the revision;
2. the configuration file;
3. the DUT's control address (`host:port`);
4. optionally, procedures or sections, with `all` the default.

```bash
docker run --rm --network host --user "$(id -u):$(id -g)" -v "$PWD:/work" \
  dnp3-interop-dnp3c:dev 3.1r1 device.ini 127.0.0.1:20099
```

Use `--network host` on Linux. Docker Desktop has no host network, so
there, set `tcp.device.address = host.docker.internal` in the configuration,
pass `--add-host=host.docker.internal:host-gateway` and give the control
address as `host.docker.internal:20099`.

The runner runs each procedure on its own, so a failure does not stop the
run, and writes the report at the end. It exits with 1 when a procedure
failed or was excluded, and with 0 otherwise; procedures that pass with
warnings still exit 0.

## The control protocol

The DUT listens on a TCP port. The plugin opens one connection for the
run and sends one request at a time. A request is one line of JSON. The
reply is a series of lines, each a key, a space and a value, ended by a
line `end`:

| Line | Meaning |
|------|---------|
| `log <text>` | written to the procedure's log, as what the DUT did or checked |
| `index <n>`, `flags <n>` | the answer of `setFlagAbnormal` |
| `error <text>` | the step failed with this message |

A request has `op`, the name of the dnp3c `IntegrationPlugin` method,
and its arguments:

| Field | Type | Carries |
|-------|------|---------|
| `kind` | string | the dnp3c point kind: `binary input`, `double-bit binary input`, `counter`, `frozen counter`, `analog input`, `binary output status`, `analog output status` |
| `class` | number | 1, 2 or 3; 0 for classes 1, 2 and 3 together |
| `n` | number | a count, timeout (ms), address or fragment size, by method |
| `index`, `indices` | number, numbers | point indices |
| `value`, `flag`, `bool`, `limit` | number, number, boolean, number | the method's value; no `limit` means unlimited |
| `expect` | string | the `EventExpectation`: `LatestBatch`, `RemainingLatestBatch`, `AllGeneratedBatches`, `CurrentValuesWithOptionalEarlier`, `ReturnedEventsOnly` |
| `points`, `points2` | objects | the points dnp3c parsed (see below); `points2` is the second list of the methods that compare two responses |
| `attrs` | objects | device attributes: `v` (variation), `type` (`string` or `other`), `s` (the value, or its description) |
| `expected`, `actual`, `sentMs`, `eventMs`, `delayMs` | number | the arguments of the checks of the same names |

A point has these fields:

- `kind`: as above, plus `frozen analog input`;
- `i`: its index;
- `v`: its value, a number (0 or 1 for binary points, 0 to 3 for double-bit ones);
- `f`: its flags octet, if reported;
- `t`: its time in milliseconds since 1970, if reported;
- `rel`: true for a relative-time event;
- `event`: true for an event.

**`startProcedure` resets the DUT**, so that every procedure starts from a
known state; `endProcedure` follows each one. Configuration changes are
offline configuration. dnp3c restarts the device (`cyclePower`) after
changing one, and they may take effect then. `cyclePower` returns once the
device accepts connections again. The DUT answers every other check
immediately; no check is deferred.
