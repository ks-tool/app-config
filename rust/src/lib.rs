//! An application's configuration, read from three places, a later one over an
//! earlier: a file, the environment, the command line. It is the Rust side of
//! `github.com/ks-tool/app-config` and keeps its naming rule, so that an application
//! in Go and one in Rust are configured the same way.
//!
//! A configuration is a struct that derives `serde::Deserialize`. Every setting has
//! one name in three spellings. Its **key** in a file is the field's name as serde
//! has it (`cert_file`, or `certFile` under `rename_all = "camelCase"`), nested
//! under the fields it lies in. Its **variable** is the path of keys joined by an
//! underscore, each spelled in capitals with underscores, under the application's
//! prefix: `tls.certFile` of `sfs-mount` is `SFS_MOUNT_TLS_CERT_FILE`, the name of
//! the second item of a list `PARTITIONS_1_NAME`. Its **flag**, where the command
//! line has one, is the same path in kebab case, `--tls-cert-file`.
//!
//! ```
//! use serde::Deserialize;
//!
//! #[derive(Deserialize)]
//! #[serde(rename_all = "camelCase")]
//! struct Tls { cert_file: String }
//!
//! #[derive(Deserialize)]
//! struct Config { listen: String, tls: Option<Tls>, #[serde(default)] namenodes: Vec<String> }
//!
//! let cfg: Config = appconfig::Loader::new("sfs-mount")
//!     .kv([("listen", ":7070"), ("tls_certFile", "/etc/tls.crt")]) // what a file gives
//!     .env_from([("SFS_MOUNT_NAMENODES".to_string(), "a:1,b:1".to_string())])
//!     .load()
//!     .unwrap();
//! assert_eq!(cfg.listen, ":7070");
//! assert_eq!(cfg.tls.unwrap().cert_file, "/etc/tls.crt");
//! assert_eq!(cfg.namenodes, ["a:1", "b:1"]);
//! ```
//!
//! What differs from the Go library, on purpose. The prefix is the environment's
//! alone: a file's keys and the flags are the application's own and carry none
//! (a `.env` file may carry it, and it is taken off). A file is read strictly — a
//! key no field answers to is an error that names it — unless [`Loader::strict`]
//! says otherwise; the environment is never held to that, a stray variable under
//! the prefix being nobody's mistake. An optional section is an `Option` of a
//! struct, and is there when any key lies under it.

mod de;
pub mod duration;
mod file;

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;
use std::path::Path;

pub use file::Decoder;

/// What a load ends in when it does not end well.
#[derive(Debug)]
pub enum Error {
    /// The file could not be read.
    Io(String, std::io::Error),
    /// The file is not in the format its extension names, or the extension names none known.
    Format(String, String),
    /// A value is not of the kind its field takes, or a field that has to be set is not.
    Value(String),
    /// Keys of the file that no field answers to.
    Unknown(String, Vec<String>),
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Error::Io(path, e) => write!(f, "{path}: {e}"),
            Error::Format(path, e) => write!(f, "{path}: {e}"),
            Error::Value(e) => write!(f, "{e}"),
            Error::Unknown(path, keys) => {
                write!(f, "{path}: no setting answers to {}", keys.join(", "))
            }
        }
    }
}

impl std::error::Error for Error {}

impl serde::de::Error for Error {
    fn custom<T: fmt::Display>(msg: T) -> Self {
        Error::Value(msg.to_string())
    }
}

/// The prefix of the variables of the application called `app`, its binary's name:
/// `sfs-mount` reads `SFS_MOUNT_…`.
pub fn prefix(app: &str) -> String {
    let mut p = variable(app);
    p.push('_');
    p
}

/// The variable of a key, without the application's prefix: the path of keys as a
/// file's tree is flattened, `tls_certFile`, or a flag, `tls-cert-file`, spelled
/// `TLS_CERT_FILE`. It is the rule of the Go side, letter for letter.
pub fn variable(key: &str) -> String {
    let r: Vec<char> = key.chars().collect();
    let mut out = String::with_capacity(key.len() + 4);
    for (i, &c) in r.iter().enumerate() {
        if c == '-' || c == '.' {
            out.push('_');
            continue;
        }
        if c.is_uppercase() && i > 0 && r[i - 1] != '_' && r[i - 1] != '-' && r[i - 1] != '.' {
            let prev = r[i - 1];
            let next_lower = r.get(i + 1).is_some_and(|n| n.is_lowercase());
            if prev.is_lowercase() || prev.is_ascii_digit() || (prev.is_uppercase() && next_lower) {
                out.push('_');
            }
        }
        out.extend(c.to_uppercase());
    }
    out
}

/// Gathers the sources, a later one over an earlier, and reads them in one pass.
pub struct Loader {
    prefix: String,
    kv: BTreeMap<String, String>,
    from_file: BTreeMap<String, String>, // variable -> the file it came from
    strict: bool,
}

impl Loader {
    /// A loader for the application called `app`; its variables are under [`prefix`]`(app)`.
    pub fn new(app: &str) -> Self {
        Loader {
            prefix: prefix(app),
            kv: BTreeMap::new(),
            from_file: BTreeMap::new(),
            strict: true,
        }
    }

    /// Whether a key of a file that no field answers to is an error. It is, unless told otherwise.
    pub fn strict(mut self, yes: bool) -> Self {
        self.strict = yes;
        self
    }

    /// The file at `path`; its format follows its extension: `.env`, `.json`, and with
    /// the features of those names `.toml` and `.yaml`/`.yml`.
    pub fn file(self, path: impl AsRef<Path>) -> Result<Self, Error> {
        let path = path.as_ref();
        let ext = path
            .extension()
            .and_then(|e| e.to_str())
            .unwrap_or("")
            .to_ascii_lowercase();
        let Some(decoder) = file::decoder(&ext) else {
            return Err(Error::Format(
                path.display().to_string(),
                format!("no decoder for a .{ext} file"),
            ));
        };
        self.file_with(path, decoder)
    }

    /// The file at `path`, when there is a path: an application whose file is optional.
    pub fn file_if(self, path: Option<impl AsRef<Path>>) -> Result<Self, Error> {
        match path {
            Some(p) => self.file(p),
            None => Ok(self),
        }
    }

    /// The file at `path`, read by a decoder of one's own: the text to a tree.
    pub fn file_with(mut self, path: impl AsRef<Path>, decoder: Decoder) -> Result<Self, Error> {
        let name = path.as_ref().display().to_string();
        let text = std::fs::read_to_string(&path).map_err(|e| Error::Io(name.clone(), e))?;
        let tree = decoder(&text).map_err(|e| Error::Format(name.clone(), e))?;
        let mut flat = BTreeMap::new();
        file::flatten("", &tree, &mut flat);
        for (k, v) in flat {
            // A .env file may spell its keys as the environment does, prefix and all.
            let k = k
                .strip_prefix(&self.prefix)
                .map(str::to_string)
                .unwrap_or(k);
            self.from_file.insert(k.clone(), name.clone());
            self.kv.insert(k, v);
        }
        Ok(self)
    }

    /// The process environment: the variables under the application's prefix, and no other.
    pub fn env(self) -> Self {
        self.env_from(std::env::vars())
    }

    /// An environment given as pairs — what [`Loader::env`] does with the process's own.
    pub fn env_from(mut self, vars: impl IntoIterator<Item = (String, String)>) -> Self {
        for (k, v) in vars {
            if let Some(k) = k.strip_prefix(&self.prefix) {
                self.from_file.remove(k);
                self.kv.insert(k.to_string(), v);
            }
        }
        self
    }

    /// A ready set of keys and values, the keys as a file would have them
    /// (`tls_certFile`, `tls.certFile`): the way in for a source of one's own.
    pub fn kv<K: AsRef<str>, V: Into<String>>(
        mut self,
        pairs: impl IntoIterator<Item = (K, V)>,
    ) -> Self {
        for (k, v) in pairs {
            let k = variable(k.as_ref());
            self.from_file.remove(&k);
            self.kv.insert(k, v.into());
        }
        self
    }

    /// The arguments that were given on the command line — not the defaults clap filled
    /// in — each under the variable of its id: `--tls-cert-file` is `TLS_CERT_FILE`.
    /// A flag that takes several values gives a list.
    #[cfg(feature = "clap")]
    pub fn flags(mut self, matches: &clap::ArgMatches) -> Self {
        use clap::parser::ValueSource;
        for id in matches.ids() {
            let id = id.as_str();
            if matches.value_source(id) != Some(ValueSource::CommandLine) {
                continue;
            }
            let raw: Vec<String> = matches
                .get_raw(id)
                .map(|vs| vs.map(|v| v.to_string_lossy().into_owned()).collect())
                .unwrap_or_default();
            let value = if raw.is_empty() {
                // A flag that takes no value: it is there, or it is counted.
                match matches.try_get_one::<bool>(id) {
                    Ok(Some(b)) => b.to_string(),
                    _ => "true".to_string(),
                }
            } else {
                raw.join(",")
            };
            let k = variable(id);
            self.from_file.remove(&k);
            self.kv.insert(k, value);
        }
        self
    }

    /// Reads what was gathered into a `T`.
    pub fn load<T: serde::de::DeserializeOwned>(self) -> Result<T, Error> {
        let seen = std::cell::RefCell::new(BTreeSet::new());
        let value = T::deserialize(de::Node::root(&self.kv, &seen))?;
        if self.strict {
            let seen = seen.into_inner();
            let mut by_file: BTreeMap<&str, Vec<String>> = BTreeMap::new();
            for (k, file) in &self.from_file {
                if !seen.contains(k) {
                    by_file.entry(file).or_default().push(k.clone());
                }
            }
            if let Some((file, keys)) = by_file.into_iter().next() {
                return Err(Error::Unknown(file.to_string(), keys));
            }
        }
        Ok(value)
    }
}
