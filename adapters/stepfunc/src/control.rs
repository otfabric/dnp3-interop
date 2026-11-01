// SPDX-License-Identifier: MIT

//! The counter-increment command (contract 1.4): asks the running server, on
//! its control socket, to add to a counter. The exchange on the socket, one
//! line of JSON each way, is private to this adapter.

use crate::logf;
use crate::util::*;

use serde_json::{json, Value};
use std::io::{BufRead, BufReader, Write};
use std::os::unix::net::UnixStream;
use std::time::{Duration, Instant};

pub fn run(argv: &[String]) -> i32 {
    let start = Instant::now();
    let parsed = (|| -> Result<_, UsageError> {
        let args = Args::parse(argv, &["index", "by", "control-socket", "timeout-ms"], &[])?;
        Ok((
            args.required_integer("index", 0, 65535)?,
            args.integer("by", 1, 1, 4_294_967_295)?,
            args.integer("timeout-ms", 5000, 1, 3_600_000)?,
            args.str("control-socket", DEFAULT_CONTROL_SOCKET),
        ))
    })();
    let (index, by, timeout, socket) = match parsed {
        Ok(p) => p,
        Err(e) => {
            logf!("counter-increment: {}", e.0);
            return EXIT_USAGE;
        }
    };
    let finish = |connected: bool, value: Value, error: Option<(&str, String)>, code: i32| {
        let ok = error.is_none();
        let error = error.map(|(c, m)| json!({"code": c, "message": m}));
        emit(&json!({
            "schemaVersion": SCHEMA_VERSION, "adapter": ADAPTER, "operation": "counter-increment",
            "ok": ok, "error": error, "connected": connected, "iin": null, "objects": [],
            "index": index, "value": value, "elapsedMs": start.elapsed().as_millis() as u64,
        }));
        code
    };
    let stream = match UnixStream::connect(&socket) {
        Ok(s) => s,
        Err(e) => {
            return finish(
                false,
                Value::Null,
                Some(("connect-failed", e.to_string())),
                EXIT_CONNECT_FAILED,
            )
        }
    };
    let exchange = || -> Result<u64, String> {
        let wait = Some(Duration::from_millis(timeout as u64));
        stream.set_read_timeout(wait).map_err(|e| e.to_string())?;
        stream.set_write_timeout(wait).map_err(|e| e.to_string())?;
        let request = json!({"operation": "counter-increment", "index": index, "by": by});
        (&stream)
            .write_all(format!("{request}\n").as_bytes())
            .map_err(|e| e.to_string())?;
        let mut line = String::new();
        BufReader::new(&stream)
            .read_line(&mut line)
            .map_err(|e| e.to_string())?;
        let reply: Value = serde_json::from_str(&line).map_err(|e| e.to_string())?;
        match reply["value"].as_u64() {
            Some(value) if reply["ok"] == true => Ok(value),
            _ => Err(reply["message"].as_str().unwrap_or("no answer").to_string()),
        }
    };
    match exchange() {
        Ok(value) => finish(true, json!(value), None, EXIT_OK),
        Err(message) => finish(true, Value::Null, Some(("failed", message)), EXIT_FAILED),
    }
}
