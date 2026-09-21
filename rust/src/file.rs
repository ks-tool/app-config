//! A file as a tree, and a tree as the flat set of keys the loader reads.

use std::collections::BTreeMap;

use serde_json::Value;

use crate::variable;

/// Reads the text of a file into a tree. The error is what went wrong, in words.
pub type Decoder = fn(&str) -> Result<Value, String>;

/// The decoder of files with the extension `ext`, lower-cased and without its dot.
pub(crate) fn decoder(ext: &str) -> Option<Decoder> {
    match ext {
        "env" => Some(dotenv),
        "json" => Some(|text| serde_json::from_str(text).map_err(|e| e.to_string())),
        #[cfg(feature = "toml")]
        "toml" => Some(|text| {
            let v: toml::Value = toml::from_str(text).map_err(|e| e.to_string())?;
            serde_json::to_value(v).map_err(|e| e.to_string())
        }),
        #[cfg(feature = "yaml")]
        "yaml" | "yml" => Some(|text| serde_yaml::from_str(text).map_err(|e| e.to_string())),
        _ => None,
    }
}

/// `KEY=value` a line; blank lines and `#` comments pass, `export ` is taken off, and
/// a value in quotes loses them.
fn dotenv(text: &str) -> Result<Value, String> {
    let mut map = serde_json::Map::new();
    for (n, line) in text.lines().enumerate() {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let line = line.strip_prefix("export ").unwrap_or(line);
        let Some((k, v)) = line.split_once('=') else {
            return Err(format!("line {}: no `=` in it", n + 1));
        };
        let v = v.trim();
        let v = v
            .strip_prefix('"')
            .and_then(|s| s.strip_suffix('"'))
            .or_else(|| v.strip_prefix('\'').and_then(|s| s.strip_suffix('\'')))
            .unwrap_or(v);
        map.insert(k.trim().to_string(), Value::String(v.to_string()));
    }
    Ok(Value::Object(map))
}

/// Flattens a tree into variables: keys joined by `_`, a list of trees by its indices
/// (`SRV_0_PORT`), a list of plain values joined by commas.
pub(crate) fn flatten(path: &str, v: &Value, out: &mut BTreeMap<String, String>) {
    let join = |k: &str| {
        if path.is_empty() {
            variable(k)
        } else {
            format!("{path}_{}", variable(k))
        }
    };
    match v {
        Value::Object(map) => {
            for (k, val) in map {
                flatten(&join(k), val, out);
            }
        }
        Value::Array(items) => {
            let mut plain = Vec::new();
            for (i, item) in items.iter().enumerate() {
                match item {
                    Value::Object(_) | Value::Array(_) => flatten(&join(&i.to_string()), item, out),
                    other => plain.push(scalar(other)),
                }
            }
            if !plain.is_empty() || items.is_empty() {
                out.insert(path.to_string(), plain.join(","));
            }
        }
        other => {
            out.insert(path.to_string(), scalar(other));
        }
    }
}

fn scalar(v: &Value) -> String {
    match v {
        Value::Null => String::new(),
        Value::String(s) => s.clone(),
        other => other.to_string(),
    }
}
