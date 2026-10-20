// SPDX-License-Identifier: MIT

//! Output, names of the contract and the command line.

use serde_json::{json, Value};
use std::collections::{BTreeMap, BTreeSet};
use std::io::Write;
use std::sync::Mutex;

pub const ADAPTER: &str = "stepfunc";
pub const SCHEMA_VERSION: &str = "1.0";
pub const DEFAULT_FIXTURE: &str = "/fixtures/baseline/fixture.json";
pub const DEFAULT_READY_FILE: &str = "/run/dnp3-interop/ready";

pub const EXIT_OK: i32 = 0;
pub const EXIT_FAILED: i32 = 1;
pub const EXIT_USAGE: i32 = 2;
pub const EXIT_CONNECT_FAILED: i32 = 3;
pub const EXIT_FIXTURE_INVALID: i32 = 4;

static OUT: Mutex<()> = Mutex::new(());

/// Prints one JSON document on one line of stdout.
pub fn emit(doc: &Value) {
    let _guard = OUT.lock().unwrap_or_else(|e| e.into_inner());
    let mut out = std::io::stdout().lock();
    let _ = writeln!(out, "{doc}");
    let _ = out.flush();
}

/// Diagnostics on stderr.
#[macro_export]
macro_rules! logf {
    ($($arg:tt)*) => {
        eprintln!("[stepfunc] {}", format!($($arg)*))
    };
}

pub fn now_ms() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis() as u64)
        .unwrap_or(0)
}

/// YYYY-MM-DDTHH:MM:SS.mmmZ
pub fn format_time(ms: u64) -> String {
    let secs = (ms / 1000) as i64;
    let days = secs.div_euclid(86_400);
    let rem = secs.rem_euclid(86_400);
    // Civil date from days since 1970-01-01 (H. Hinnant's algorithm).
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146_096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = if m <= 2 { y + 1 } else { y };
    format!(
        "{:04}-{:02}-{:02}T{:02}:{:02}:{:02}.{:03}Z",
        y,
        m,
        d,
        rem / 3600,
        (rem % 3600) / 60,
        rem % 60,
        ms % 1000
    )
}

/// A number with no fractional part is printed as an integer, so that every
/// adapter prints the same text for it.
pub fn number(v: f64) -> Value {
    if v.is_finite() && v == v.floor() && v.abs() < 9_007_199_254_740_992.0 {
        json!(v as i64)
    } else {
        json!(v)
    }
}

pub fn iin_names(iin1: u8, iin2: u8) -> Value {
    const NAMES: [&str; 16] = [
        "BROADCAST",
        "CLASS_1_EVENTS",
        "CLASS_2_EVENTS",
        "CLASS_3_EVENTS",
        "NEED_TIME",
        "LOCAL_CONTROL",
        "DEVICE_TROUBLE",
        "DEVICE_RESTART",
        "NO_FUNC_CODE_SUPPORT",
        "OBJECT_UNKNOWN",
        "PARAMETER_ERROR",
        "EVENT_BUFFER_OVERFLOW",
        "ALREADY_EXECUTING",
        "CONFIG_CORRUPT",
        "RESERVED_2",
        "RESERVED_1",
    ];
    let bits = (iin1 as u16) | ((iin2 as u16) << 8);
    Value::Array(
        (0..16)
            .filter(|i| bits & (1 << i) != 0)
            .map(|i| json!(NAMES[i]))
            .collect(),
    )
}

const COMMON_FLAGS: [&str; 5] = [
    "ONLINE",
    "RESTART",
    "COMM_LOST",
    "REMOTE_FORCED",
    "LOCAL_FORCED",
];

#[derive(PartialEq, Clone, Copy)]
enum Family {
    Binary,
    Counter,
    Analog,
}

fn family_of_group(group: u8) -> Family {
    match group {
        20..=23 => Family::Counter,
        30 | 31 | 32 | 33 | 40 | 42 => Family::Analog,
        _ => Family::Binary,
    }
}

fn high_flag(family: Family, bit: u8, outputs: bool) -> Option<&'static str> {
    match family {
        Family::Counter => Some(if bit == 5 {
            "ROLLOVER"
        } else {
            "DISCONTINUITY"
        }),
        Family::Analog => Some(if bit == 5 {
            "OVER_RANGE"
        } else {
            "REFERENCE_ERR"
        }),
        Family::Binary => (bit == 5 && !outputs).then_some("CHATTER_FILTER"),
    }
}

pub fn flag_names(group: u8, flags: u8) -> Value {
    let mut out: Vec<Value> = (0..5)
        .filter(|b| flags & (1 << b) != 0)
        .map(|b| json!(COMMON_FLAGS[b]))
        .collect();
    let family = family_of_group(group);
    let outputs = group == 10 || group == 11;
    for bit in 5..7u8 {
        if flags & (1 << bit) == 0 {
            continue;
        }
        if family == Family::Binary && (group == 3 || group == 4) && bit == 6 {
            continue; // a state bit of the double-bit types
        }
        if let Some(name) = high_flag(family, bit, outputs) {
            out.push(json!(name));
        }
    }
    Value::Array(out)
}

/// Bit of a flag name for a point type of the fixture.
pub fn flag_bit(point_type: &str, name: &str) -> Option<u8> {
    if let Some(bit) = COMMON_FLAGS.iter().position(|f| *f == name) {
        return Some(bit as u8);
    }
    let family = match point_type {
        "counters" | "frozenCounters" => Family::Counter,
        "analogInputs" | "analogOutputs" => Family::Analog,
        _ => Family::Binary,
    };
    let outputs = point_type == "binaryOutputs";
    (5..7u8).find(|&bit| {
        // ROLLOVER is obsolete and not accepted in fixtures.
        !(family == Family::Counter && bit == 5) && high_flag(family, bit, outputs) == Some(name)
    })
}

pub fn command_status_name(status: u8) -> &'static str {
    const NAMES: [&str; 19] = [
        "SUCCESS",
        "TIMEOUT",
        "NO_SELECT",
        "FORMAT_ERROR",
        "NOT_SUPPORTED",
        "ALREADY_ACTIVE",
        "HARDWARE_ERROR",
        "LOCAL",
        "TOO_MANY_OPS",
        "NOT_AUTHORIZED",
        "AUTOMATION_INHIBIT",
        "PROCESSING_LIMITED",
        "OUT_OF_RANGE",
        "DOWNSTREAM_LOCAL",
        "ALREADY_COMPLETE",
        "BLOCKED",
        "CANCELLED",
        "BLOCKED_OTHER_MASTER",
        "DOWNSTREAM_FAIL",
    ];
    match status {
        s if (s as usize) < NAMES.len() => NAMES[s as usize],
        126 => "NON_PARTICIPATING",
        _ => "UNDEFINED",
    }
}

pub fn to_hex(data: &[u8]) -> String {
    data.iter().map(|b| format!("{b:02x}")).collect()
}

/// Thrown by argument parsing: exit 2 with the message on stderr.
pub struct UsageError(pub String);

pub fn usage<T>(message: impl Into<String>) -> Result<T, UsageError> {
    Err(UsageError(message.into()))
}

/// Command line: "--name value" flags and bare "--name" switches.
pub struct Args {
    values: BTreeMap<String, String>,
}

impl Args {
    pub fn parse(argv: &[String], flags: &[&str], switches: &[&str]) -> Result<Self, UsageError> {
        let flags: BTreeSet<&str> = flags.iter().copied().collect();
        let switches: BTreeSet<&str> = switches.iter().copied().collect();
        let mut values = BTreeMap::new();
        let mut i = 0;
        while i < argv.len() {
            let arg = &argv[i];
            let Some(name) = arg.strip_prefix("--") else {
                return usage(format!("unexpected argument '{arg}'"));
            };
            if switches.contains(name) {
                values.insert(name.to_string(), "true".to_string());
            } else if flags.contains(name) {
                i += 1;
                let Some(v) = argv.get(i) else {
                    return usage(format!("--{name} needs a value"));
                };
                values.insert(name.to_string(), v.clone());
            } else {
                return usage(format!("unknown flag --{name}"));
            }
            i += 1;
        }
        Ok(Self { values })
    }

    pub fn has(&self, name: &str) -> bool {
        self.values.contains_key(name)
    }

    pub fn str(&self, name: &str, default: &str) -> String {
        self.values
            .get(name)
            .cloned()
            .unwrap_or_else(|| default.to_string())
    }

    pub fn required(&self, name: &str) -> Result<String, UsageError> {
        match self.values.get(name) {
            Some(v) => Ok(v.clone()),
            None => usage(format!("--{name} is required")),
        }
    }

    pub fn integer(&self, name: &str, default: i64, min: i64, max: i64) -> Result<i64, UsageError> {
        match self.values.get(name) {
            None => Ok(default),
            Some(v) => match v.parse::<i64>() {
                Ok(n) if n >= min && n <= max => Ok(n),
                _ => usage(format!("--{name} must be an integer in {min}..{max}")),
            },
        }
    }

    pub fn required_integer(&self, name: &str, min: i64, max: i64) -> Result<i64, UsageError> {
        self.required(name)?;
        self.integer(name, 0, min, max)
    }

    pub fn number(&self, name: &str) -> Result<f64, UsageError> {
        match self.required(name)?.parse::<f64>() {
            Ok(v) if v.is_finite() => Ok(v),
            _ => usage(format!("--{name} must be a number")),
        }
    }

    pub fn classes(&self, name: &str, default: &str, min: u8) -> Result<Vec<u8>, UsageError> {
        let mut out = Vec::new();
        for item in self.str(name, default).split(',') {
            match item.parse::<u8>() {
                Ok(c) if item.len() == 1 && c >= min && c <= 3 => out.push(c),
                _ => {
                    return usage(format!(
                        "--{name} is a comma-separated list of classes {min}..3"
                    ))
                }
            }
        }
        Ok(out)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn times() {
        assert_eq!(format_time(0), "1970-01-01T00:00:00.000Z");
        assert_eq!(format_time(1_760_000_000_123), "2025-10-09T08:53:20.123Z");
        assert_eq!(format_time(951_782_400_000), "2000-02-29T00:00:00.000Z");
    }

    #[test]
    fn flags() {
        assert_eq!(flag_names(30, 0x21), json!(["ONLINE", "OVER_RANGE"]));
        assert_eq!(flag_names(1, 0x81), json!(["ONLINE"]));
        assert_eq!(flag_bit("counters", "DISCONTINUITY"), Some(6));
        assert_eq!(flag_bit("counters", "ROLLOVER"), None);
        assert_eq!(flag_bit("binaryOutputs", "CHATTER_FILTER"), None);
    }
}
