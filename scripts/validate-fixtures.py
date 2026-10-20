#!/usr/bin/env python3
"""Validate every fixtures/*/fixture.json.

Two layers, both required:

  1. The structural rules of fixtures/schema/dnp3-fixture.schema.json. They
     are checked with the `jsonschema` package when it is installed; CI installs
     it, so a missing package is an error there (--require-schema).
  2. The cross-field rules a JSON Schema cannot express: unique indexes per
     point type, values that fit the chosen variation, outputs whose target
     exists, frozen counters that have a counter to freeze.

The adapters apply the same rules when they load a fixture; this script makes
a broken fixture fail before any image is built.

Usage: validate-fixtures.py [--require-schema] [fixture.json ...]
"""

import json
import struct
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SCHEMA = ROOT / "fixtures" / "schema" / "dnp3-fixture.schema.json"

TYPES = ["binaryInputs", "doubleBitInputs", "binaryOutputs", "counters", "frozenCounters",
         "analogInputs", "analogOutputs", "octetStrings"]

# Static variations whose value field is 16 bits wide, per point type.
SIXTEEN_BIT = {
    "counters": {2, 6},
    "frozenCounters": {2, 6, 10},
    "analogInputs": {2, 4},
    "analogOutputs": {2},
}
# Static variations that carry an integer (not floating point) value.
INTEGER_ANALOG = {"analogInputs": {1, 2, 3, 4}, "analogOutputs": {1, 2}}
DEFAULT_STATIC = {"counters": 1, "frozenCounters": 1, "analogInputs": 1, "analogOutputs": 1}
# Event variations whose value field is 16 bits wide.
SIXTEEN_BIT_EVENT = {
    "counters": {2, 6},
    "frozenCounters": {2, 6},
    "analogInputs": {2, 4},
    "analogOutputs": {2, 4},
}
INTEGER_ANALOG_EVENT = {"analogInputs": {1, 2, 3, 4}, "analogOutputs": {1, 2, 3, 4}}
DEFAULT_EVENT = {"counters": 1, "frozenCounters": 1, "analogInputs": 1, "analogOutputs": 1}
# Static variations without a flags octet: such a point is reported ONLINE.
NO_FLAGS = {"binaryInputs": {1}, "counters": {5, 6}, "frozenCounters": {9, 10}, "analogInputs": {3, 4}}


def is_float32(v):
    try:
        return struct.unpack("<f", struct.pack("<f", v))[0] == v
    except (OverflowError, struct.error):
        return False


def value_errors(kind, p):
    errors = []
    where = f"{kind}[{p.get('index')}]"
    v = p.get("value")
    if not isinstance(v, (int, float)) or isinstance(v, bool):
        return errors
    s = p.get("staticVariation", DEFAULT_STATIC.get(kind))
    e = p.get("eventVariation", DEFAULT_EVENT.get(kind))
    if kind in ("counters", "frozenCounters"):
        if (s in SIXTEEN_BIT[kind] or (p.get("class", 0) and e in SIXTEEN_BIT_EVENT[kind])) and v > 65535:
            errors.append(f"{where}: value {v} does not fit a 16-bit variation")
        return errors
    if kind in ("analogInputs", "analogOutputs"):
        if s in INTEGER_ANALOG[kind] or (p.get("class", 0) and e in INTEGER_ANALOG_EVENT[kind]):
            if v != int(v):
                errors.append(f"{where}: value {v} is not an integer, but a variation of the point is")
        if s in SIXTEEN_BIT[kind] or (p.get("class", 0) and e in SIXTEEN_BIT_EVENT[kind]):
            if not -32768 <= v <= 32767:
                errors.append(f"{where}: value {v} does not fit a 16-bit variation")
        elif not -2147483648 <= v <= 2147483647:
            errors.append(f"{where}: value {v} does not fit a 32-bit variation")
        if not is_float32(float(v)):
            errors.append(f"{where}: value {v} is not exactly representable in single precision")
        for bound in ("min", "max"):
            if bound in p and not is_float32(float(p[bound])):
                errors.append(f"{where}: {bound} {p[bound]} is not exactly representable in single precision")
        if "min" in p and "max" in p and p["min"] > p["max"]:
            errors.append(f"{where}: min is larger than max")
        if ("min" in p and v < p["min"]) or ("max" in p and v > p["max"]):
            errors.append(f"{where}: value {v} is outside [min, max]")
    return errors


def semantic_errors(fx):
    errors = []
    indexes = {}
    for kind in TYPES:
        seen = set()
        for p in fx.get(kind, []):
            i = p.get("index")
            if i in seen:
                errors.append(f"{kind}[{i}]: duplicate index")
            seen.add(i)
            errors.extend(value_errors(kind, p))
            if p.get("staticVariation") in NO_FLAGS.get(kind, set()) and p.get("flags", ["ONLINE"]) != ["ONLINE"]:
                errors.append(f"{kind}[{i}]: static variation {p['staticVariation']} has no flags, so flags must be [\"ONLINE\"]")
        indexes[kind] = seen
    for kind, target_kind in (("binaryOutputs", "binaryInputs"), ("analogOutputs", "analogInputs")):
        targets = {}
        for p in fx.get(kind, []):
            t = p.get("target")
            if t is None:
                continue
            if t not in indexes[target_kind]:
                errors.append(f"{kind}[{p.get('index')}]: target {t} is not one of the {target_kind}")
            elif t in targets:
                errors.append(f"{kind}[{p.get('index')}]: target {t} is also the target of output {targets[t]}")
            targets[t] = p.get("index")
    for p in fx.get("frozenCounters", []):
        if p.get("index") not in indexes["counters"]:
            errors.append(f"frozenCounters[{p.get('index')}]: there is no counter with this index to freeze")
    files = [f.get("name") for f in fx.get("files", [])]
    dirs = list(fx.get("directories", []))
    for n in set(files):
        if files.count(n) > 1:
            errors.append(f"files: {n} is listed twice")
    for n in set(dirs):
        if dirs.count(n) > 1:
            errors.append(f"directories: {n} is listed twice")
    parents = {n.rsplit("/", 1)[0] for n in files + dirs if isinstance(n, str)}
    implied = set(dirs)
    for p in parents:
        while p:
            implied.add(p)
            p = p.rsplit("/", 1)[0]
    for n in files:
        if n in implied:
            errors.append(f"files: {n} is also a directory")
    out = fx.get("outstation", {})
    if out.get("address") is not None and out.get("address") == out.get("masterAddress"):
        errors.append("outstation: address and masterAddress are equal")
    return errors


def main(argv):
    require_schema = "--require-schema" in argv
    paths = [Path(a) for a in argv if not a.startswith("--")]
    if not paths:
        paths = sorted((ROOT / "fixtures").glob("*/fixture.json"))
    if not paths:
        print("no fixtures found", file=sys.stderr)
        return 1

    validator = None
    try:
        import jsonschema

        schema = json.loads(SCHEMA.read_text())
        jsonschema.Draft202012Validator.check_schema(schema)
        validator = jsonschema.Draft202012Validator(schema)
    except ImportError:
        if require_schema:
            print("the jsonschema package is required (pip install jsonschema)", file=sys.stderr)
            return 1
        print("note: jsonschema not installed, schema layer skipped (semantic rules still checked)", file=sys.stderr)

    failed = False
    for path in paths:
        errors = []
        try:
            fx = json.loads(path.read_text())
        except (OSError, ValueError) as e:
            errors.append(str(e))
            fx = None
        if fx is not None:
            if validator is not None:
                for e in sorted(validator.iter_errors(fx), key=lambda e: list(e.absolute_path)):
                    where = "/".join(str(x) for x in e.absolute_path) or "(root)"
                    errors.append(f"{where}: {e.message}")
            if isinstance(fx, dict):
                errors.extend(semantic_errors(fx))
                if path.parent.name != fx.get("name"):
                    errors.append(f"name {fx.get('name')!r} does not match directory {path.parent.name!r}")
        rel = path.relative_to(ROOT) if path.is_relative_to(ROOT) else path
        if errors:
            failed = True
            print(f"FAIL {rel}")
            for e in errors:
                print(f"     {e}")
        else:
            print(f"ok   {rel}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
