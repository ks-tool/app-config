//! A duration as Go writes one — `90s`, `1h30m`, `1.5h`, `250ms` — so that a setting
//! reads the same in a file of either side.
//!
//! ```
//! #[derive(serde::Deserialize)]
//! struct Config {
//!     #[serde(deserialize_with = "appconfig::duration::deserialize")]
//!     vacuum: std::time::Duration,
//! }
//! let cfg: Config = appconfig::Loader::new("app").kv([("vacuum", "1h30m")]).load().unwrap();
//! assert_eq!(cfg.vacuum.as_secs(), 5400);
//! ```

use std::time::Duration;

use serde::de::{Deserialize, Deserializer, Error};

/// Parses a duration in Go's notation: numbers, each with its unit, one after another.
pub fn parse(text: &str) -> Result<Duration, String> {
    let s = text.trim();
    if s == "0" {
        return Ok(Duration::ZERO);
    }
    if s.is_empty() {
        return Err("a duration is a number and a unit, like 90s or 1h30m".to_string());
    }
    let mut total = 0f64;
    let mut rest = s;
    while !rest.is_empty() {
        let digits = rest
            .find(|c: char| !(c.is_ascii_digit() || c == '.'))
            .unwrap_or(rest.len());
        let (number, tail) = rest.split_at(digits);
        let unit_len = tail
            .find(|c: char| c.is_ascii_digit() || c == '.')
            .unwrap_or(tail.len());
        let (unit, next) = tail.split_at(unit_len);
        let n: f64 = number
            .parse()
            .map_err(|_| format!("{text:?} is no duration: {number:?} is no number"))?;
        let seconds = match unit {
            "ns" => 1e-9,
            "us" | "µs" | "μs" => 1e-6,
            "ms" => 1e-3,
            "s" => 1.0,
            "m" => 60.0,
            "h" => 3600.0,
            _ => {
                return Err(format!(
                    "{text:?} is no duration: {unit:?} is no unit (ns, us, ms, s, m, h)"
                ))
            }
        };
        total += n * seconds;
        rest = next;
    }
    Ok(Duration::from_secs_f64(total))
}

/// For `#[serde(deserialize_with = "appconfig::duration::deserialize")]`.
pub fn deserialize<'de, D: Deserializer<'de>>(d: D) -> Result<Duration, D::Error> {
    parse(&String::deserialize(d)?).map_err(D::Error::custom)
}

/// The same, for a setting that may be absent: `Option<Duration>` with `#[serde(default)]`.
pub mod option {
    use super::*;

    pub fn deserialize<'de, D: Deserializer<'de>>(d: D) -> Result<Option<Duration>, D::Error> {
        match Option::<String>::deserialize(d)? {
            Some(s) => parse(&s).map(Some).map_err(D::Error::custom),
            None => Ok(None),
        }
    }
}
