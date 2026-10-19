// SPDX-License-Identifier: MIT

//! Loads a fixture and applies the rules of fixtures/schema/dnp3-fixture.schema.json
//! and of docs/FIXTURES.md ("cross-field rules"), as scripts/validate-fixtures.py does.

use crate::util::flag_bit;
use serde_json::{Map, Value};
use std::collections::{BTreeMap, BTreeSet};

pub const TYPES: [&str; 8] = [
    "binaryInputs",
    "doubleBitInputs",
    "binaryOutputs",
    "counters",
    "frozenCounters",
    "analogInputs",
    "analogOutputs",
    "octetStrings",
];

#[derive(Clone, Debug, Default)]
pub struct Point {
    pub index: u16,
    /// numeric types; booleans as 0/1; the double-bit state
    pub value: f64,
    pub octets: Vec<u8>,
    pub flags: u8,
    /// 0..3
    pub class: u8,
    pub svar: u8,
    pub evar: u8,
    pub target: Option<u16>,
    pub select_required: bool,
    pub min: Option<f64>,
    pub max: Option<f64>,
}

#[derive(Clone, Debug, Default)]
pub struct Fixture {
    pub name: String,
    pub address: u16,
    pub master_address: u16,
    pub unsolicited: bool,
    pub need_time: bool,
    pub cold_restart_delay_ms: Option<u16>,
    pub warm_restart_delay_ms: Option<u16>,
    pub points: BTreeMap<&'static str, Vec<Point>>,
}

impl Fixture {
    pub fn points(&self, kind: &str) -> &[Point] {
        self.points.get(kind).map(|v| v.as_slice()).unwrap_or(&[])
    }

    pub fn find(&self, kind: &str, index: u16) -> Option<&Point> {
        self.points(kind).iter().find(|p| p.index == index)
    }
}

struct Rule {
    svars: &'static [u8],
    evars: &'static [u8],
    sdefault: u8,
    edefault: u8,
}

fn rule(kind: &str) -> Rule {
    match kind {
        "binaryInputs" => Rule {
            svars: &[1, 2],
            evars: &[1, 2],
            sdefault: 2,
            edefault: 2,
        },
        "doubleBitInputs" => Rule {
            svars: &[2],
            evars: &[1, 2],
            sdefault: 2,
            edefault: 2,
        },
        "binaryOutputs" => Rule {
            svars: &[2],
            evars: &[1, 2],
            sdefault: 2,
            edefault: 2,
        },
        "counters" => Rule {
            svars: &[1, 2, 5, 6],
            evars: &[1, 2, 5, 6],
            sdefault: 1,
            edefault: 1,
        },
        "frozenCounters" => Rule {
            svars: &[1, 2, 5, 6, 9, 10],
            evars: &[1, 2, 5, 6],
            sdefault: 1,
            edefault: 1,
        },
        "analogInputs" => Rule {
            svars: &[1, 2, 3, 4, 5, 6],
            evars: &[1, 2, 3, 4, 5, 6, 7, 8],
            sdefault: 1,
            edefault: 1,
        },
        "analogOutputs" => Rule {
            svars: &[1, 2, 3, 4],
            evars: &[1, 2, 3, 4, 5, 6, 7, 8],
            sdefault: 1,
            edefault: 1,
        },
        _ => Rule {
            svars: &[],
            evars: &[],
            sdefault: 0,
            edefault: 0,
        },
    }
}

fn int(v: &Value) -> Option<i64> {
    v.as_i64()
}

fn is_float32(v: f64) -> bool {
    (v as f32) as f64 == v
}

fn check_point(kind: &'static str, p: &Value, position: usize, errors: &mut Vec<String>) -> Point {
    let r = rule(kind);
    let mut out = Point {
        flags: 0x01,
        svar: r.sdefault,
        evar: r.edefault,
        ..Default::default()
    };
    let mut where_ = format!("{kind}[{position}]");
    let Some(obj) = p.as_object() else {
        errors.push(format!("{where_}: not an object"));
        return out;
    };
    let mut allowed: Vec<&str> = vec!["index", "value", "class"];
    if kind != "octetStrings" {
        allowed.extend(["flags", "staticVariation", "eventVariation"]);
    }
    if kind == "binaryOutputs" || kind == "analogOutputs" {
        allowed.extend(["target", "selectRequired"]);
    }
    if kind == "analogOutputs" {
        allowed.extend(["min", "max"]);
    }
    for key in obj.keys() {
        if !allowed.contains(&key.as_str()) {
            errors.push(format!("{where_}: unknown field '{key}'"));
        }
    }

    match obj.get("index").and_then(int) {
        Some(i) if (0..=65535).contains(&i) => out.index = i as u16,
        _ => {
            errors.push(format!("{where_}: index must be an integer in 0..65535"));
            return out;
        }
    }
    where_ = format!("{kind}[{}]", out.index);

    if let Some(c) = obj.get("class") {
        match int(c) {
            Some(c) if (0..=3).contains(&c) => out.class = c as u8,
            _ => errors.push(format!("{where_}: class must be 0..3")),
        }
    }
    if let Some(v) = obj.get("staticVariation") {
        match int(v) {
            Some(v) if r.svars.contains(&(v as u8)) && (0..=255).contains(&v) => out.svar = v as u8,
            _ => errors.push(format!(
                "{where_}: staticVariation is not one of the allowed variations"
            )),
        }
    }
    if let Some(v) = obj.get("eventVariation") {
        match int(v) {
            Some(v) if r.evars.contains(&(v as u8)) && (0..=255).contains(&v) => out.evar = v as u8,
            _ => errors.push(format!(
                "{where_}: eventVariation is not one of the allowed variations"
            )),
        }
    }
    if let Some(flags) = obj.get("flags") {
        out.flags = 0;
        match flags.as_array() {
            None => errors.push(format!("{where_}: flags must be an array")),
            Some(list) => {
                for f in list {
                    match f.as_str().and_then(|n| flag_bit(kind, n)) {
                        None => {
                            errors.push(format!("{where_}: flag {f} is not allowed for {kind}"))
                        }
                        Some(bit) if out.flags & (1 << bit) != 0 => {
                            errors.push(format!("{where_}: flag {f} is repeated"))
                        }
                        Some(bit) => out.flags |= 1 << bit,
                    }
                }
            }
        }
    }
    // A static variation without flags reports the point ONLINE.
    let no_flags = matches!(
        (kind, out.svar),
        ("binaryInputs", 1)
            | ("counters", 5 | 6)
            | ("frozenCounters", 9 | 10)
            | ("analogInputs", 3 | 4)
    );
    if no_flags && out.flags != 0x01 {
        errors.push(format!(
            "{where_}: static variation {} has no flags, so flags must be [\"ONLINE\"]",
            out.svar
        ));
    }
    if let Some(v) = obj.get("selectRequired") {
        match v.as_bool() {
            Some(b) => out.select_required = b,
            None => errors.push(format!("{where_}: selectRequired must be a boolean")),
        }
    }
    if let Some(v) = obj.get("target") {
        match int(v) {
            Some(t) if (0..=65535).contains(&t) => out.target = Some(t as u16),
            _ => errors.push(format!("{where_}: target must be an index")),
        }
    }
    for bound in ["min", "max"] {
        if let Some(v) = obj.get(bound) {
            match v.as_f64() {
                None => errors.push(format!("{where_}: {bound} must be a number")),
                Some(x) => {
                    if !is_float32(x) {
                        errors.push(format!(
                            "{where_}: {bound} is not exactly representable in single precision"
                        ));
                    }
                    if bound == "min" {
                        out.min = Some(x);
                    } else {
                        out.max = Some(x);
                    }
                }
            }
        }
    }

    let Some(v) = obj.get("value") else {
        errors.push(format!("{where_}: value is required"));
        return out;
    };
    match kind {
        "binaryInputs" | "binaryOutputs" => match v.as_bool() {
            Some(b) => out.value = if b { 1.0 } else { 0.0 },
            None => errors.push(format!("{where_}: value must be a boolean")),
        },
        "doubleBitInputs" => match int(v) {
            Some(d) if (0..=3).contains(&d) => out.value = d as f64,
            _ => errors.push(format!("{where_}: value must be 0..3")),
        },
        "octetStrings" => {
            let ok = v.as_str().is_some_and(|s| {
                !s.is_empty()
                    && s.len() <= 510
                    && s.len() % 2 == 0
                    && s.bytes()
                        .all(|c| c.is_ascii_digit() || (b'a'..=b'f').contains(&c))
            });
            if !ok {
                errors.push(format!(
                    "{where_}: value must be 1..255 octets in lower-case hex"
                ));
            } else {
                let s = v.as_str().unwrap_or_default();
                out.octets = (0..s.len())
                    .step_by(2)
                    .map(|i| u8::from_str_radix(&s[i..i + 2], 16).unwrap_or(0))
                    .collect();
            }
        }
        "counters" | "frozenCounters" => match v.as_u64() {
            Some(c) if c <= 4_294_967_295 => {
                out.value = c as f64;
                let s16: &[u8] = if kind == "counters" {
                    &[2, 6]
                } else {
                    &[2, 6, 10]
                };
                let sixteen =
                    s16.contains(&out.svar) || (out.class != 0 && [2, 6].contains(&out.evar));
                if sixteen && c > 65535 {
                    errors.push(format!("{where_}: value does not fit a 16-bit variation"));
                }
            }
            _ => errors.push(format!(
                "{where_}: value must be an integer in 0..4294967295"
            )),
        },
        _ => {
            // analog inputs and outputs
            let Some(x) = v.as_f64() else {
                errors.push(format!("{where_}: value must be a number"));
                return out;
            };
            out.value = x;
            let input = kind == "analogInputs";
            let sint: &[u8] = if input { &[1, 2, 3, 4] } else { &[1, 2] };
            let s16: &[u8] = if input { &[2, 4] } else { &[2] };
            let has_events = out.class != 0;
            if (sint.contains(&out.svar) || (has_events && [1, 2, 3, 4].contains(&out.evar)))
                && x != x.floor()
            {
                errors.push(format!(
                    "{where_}: value is not an integer, but a variation of the point is"
                ));
            }
            if s16.contains(&out.svar) || (has_events && [2, 4].contains(&out.evar)) {
                if !(-32768.0..=32767.0).contains(&x) {
                    errors.push(format!("{where_}: value does not fit a 16-bit variation"));
                }
            } else if !(-2_147_483_648.0..=2_147_483_647.0).contains(&x) {
                errors.push(format!("{where_}: value does not fit a 32-bit variation"));
            }
            if !is_float32(x) {
                errors.push(format!(
                    "{where_}: value is not exactly representable in single precision"
                ));
            }
            if let (Some(min), Some(max)) = (out.min, out.max) {
                if min > max {
                    errors.push(format!("{where_}: min is larger than max"));
                }
            }
            if out.min.is_some_and(|m| x < m) || out.max.is_some_and(|m| x > m) {
                errors.push(format!("{where_}: value is outside [min, max]"));
            }
        }
    }
    out
}

fn link_address(v: Option<&Value>) -> Option<u16> {
    v.and_then(int)
        .filter(|a| (0..=65519).contains(a))
        .map(|a| a as u16)
}

/// Loads and validates a fixture. Returns the error messages on failure.
pub fn load(text: &str) -> Result<Fixture, String> {
    let fx: Value = serde_json::from_str(text).map_err(|e| format!("not JSON: {e}"))?;
    let Some(top) = fx.as_object() else {
        return Err("not a JSON object".into());
    };
    let mut errors = Vec::new();
    let mut out = Fixture::default();
    for key in top.keys() {
        if !["schemaVersion", "name", "description", "outstation"].contains(&key.as_str())
            && !TYPES.contains(&key.as_str())
        {
            errors.push(format!("unknown field '{key}'"));
        }
    }
    if top.get("schemaVersion").and_then(Value::as_str) != Some("1.0") {
        errors.push("schemaVersion must be \"1.0\"".into());
    }
    let name_ok = |s: &str| {
        let b = s.as_bytes();
        !b.is_empty()
            && (b[0].is_ascii_lowercase() || b[0].is_ascii_digit())
            && b.iter()
                .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || *c == b'-')
    };
    match top.get("name").and_then(Value::as_str) {
        Some(n) if name_ok(n) => out.name = n.to_string(),
        _ => errors.push("name is missing or invalid".into()),
    }
    if top.get("description").is_some_and(|d| !d.is_string()) {
        errors.push("description must be a string".into());
    }

    match top.get("outstation").and_then(Value::as_object) {
        None => errors.push("outstation is required".into()),
        Some(o) => check_outstation(o, &mut out, &mut errors),
    }

    for kind in TYPES {
        let Some(list) = top.get(kind) else { continue };
        let Some(list) = list.as_array() else {
            errors.push(format!("{kind} must be an array"));
            continue;
        };
        let mut seen = BTreeSet::new();
        let mut points = Vec::new();
        for (i, p) in list.iter().enumerate() {
            let point = check_point(kind, p, i, &mut errors);
            if !seen.insert(point.index) {
                errors.push(format!("{kind}[{}]: duplicate index", point.index));
            }
            points.push(point);
        }
        out.points.insert(kind, points);
    }

    for (outputs, inputs) in [
        ("binaryOutputs", "binaryInputs"),
        ("analogOutputs", "analogInputs"),
    ] {
        let mut targets: BTreeMap<u16, u16> = BTreeMap::new();
        for p in out.points(outputs) {
            let Some(t) = p.target else { continue };
            if out.find(inputs, t).is_none() {
                errors.push(format!(
                    "{outputs}[{}]: target {t} is not one of the {inputs}",
                    p.index
                ));
            } else if let Some(other) = targets.get(&t) {
                errors.push(format!(
                    "{outputs}[{}]: target {t} is also the target of output {other}",
                    p.index
                ));
            }
            targets.insert(t, p.index);
        }
    }
    for p in out.points("frozenCounters") {
        if out.find("counters", p.index).is_none() {
            errors.push(format!(
                "frozenCounters[{}]: there is no counter with this index to freeze",
                p.index
            ));
        }
    }

    if errors.is_empty() {
        Ok(out)
    } else {
        Err(errors.join("; "))
    }
}

fn check_outstation(o: &Map<String, Value>, out: &mut Fixture, errors: &mut Vec<String>) {
    let fields = [
        "address",
        "masterAddress",
        "unsolicited",
        "needTime",
        "coldRestartDelayMs",
        "warmRestartDelayMs",
    ];
    for key in o.keys() {
        if !fields.contains(&key.as_str()) {
            errors.push(format!("outstation: unknown field '{key}'"));
        }
    }
    match link_address(o.get("address")) {
        Some(a) => out.address = a,
        None => errors.push("outstation: address must be 0..65519".into()),
    }
    match link_address(o.get("masterAddress")) {
        Some(a) if o.get("address").is_some() && a == out.address => {
            errors.push("outstation: address and masterAddress are equal".into())
        }
        Some(a) => out.master_address = a,
        None => errors.push("outstation: masterAddress must be 0..65519".into()),
    }
    for (key, target) in [
        ("unsolicited", &mut out.unsolicited),
        ("needTime", &mut out.need_time),
    ] {
        if let Some(v) = o.get(key) {
            match v.as_bool() {
                Some(b) => *target = b,
                None => errors.push(format!("outstation: {key} must be a boolean")),
            }
        }
    }
    for (key, target) in [
        ("coldRestartDelayMs", &mut out.cold_restart_delay_ms),
        ("warmRestartDelayMs", &mut out.warm_restart_delay_ms),
    ] {
        if let Some(v) = o.get(key) {
            match int(v) {
                Some(d) if (0..=65535).contains(&d) => *target = Some(d as u16),
                _ => errors.push(format!("outstation: {key} must be 0..65535")),
            }
        }
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn baseline_is_valid() {
        let text = include_str!("../../../fixtures/baseline/fixture.json");
        let fx = super::load(text).expect("baseline");
        assert_eq!(fx.address, 1024);
        assert_eq!(fx.points("binaryInputs").len(), 5);
    }
}
