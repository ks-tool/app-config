use std::time::Duration;

use serde::Deserialize;

#[derive(Debug, Deserialize)]
struct Grant {
    principal: String,
    #[serde(default)]
    read: bool,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Partition {
    name: String,
    #[serde(default, rename = "sizeMB")]
    size_mb: u64,
    #[serde(default)]
    grants: Vec<Grant>,
}

#[derive(Debug, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
struct Tls {
    cert_file: String,
    #[serde(default)]
    key_file: String,
}

#[derive(Debug, Deserialize, PartialEq)]
#[serde(rename_all = "lowercase")]
enum Level {
    Info,
    Debug,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Config {
    id: String,
    #[serde(default)]
    listen: String,
    tls: Option<Tls>,
    kerberos: Option<Tls>,
    #[serde(default)]
    volume_servers: Vec<String>,
    #[serde(default, rename = "volumeSizeLimitMB")]
    volume_size_limit_mb: u64,
    #[serde(default)]
    garbage_threshold: f64,
    #[serde(default)]
    partitions: Vec<Partition>,
    #[serde(default, deserialize_with = "appconfig::duration::option::deserialize")]
    vacuum: Option<Duration>,
    #[serde(default)]
    verbose: bool,
    level: Option<Level>,
}

const FILE: &str = r#"{
  "id": "nn-1",
  "listen": ":19333",
  "tls": {"certFile": "/etc/app/tls.crt"},
  "volumeServers": ["10.0.0.21:8080", "10.0.0.22:8080"],
  "volumeSizeLimitMB": 1024,
  "garbageThreshold": 0.3,
  "partitions": [
    {"name": "oci", "sizeMB": 100, "grants": [{"principal": "CN=registry", "read": true}, {"principal": "CN=mirror"}]},
    {"name": "maven"}
  ],
  "vacuum": "1h30m",
  "level": "debug"
}"#;

fn write(name: &str, text: &str) -> std::path::PathBuf {
    let dir = std::env::temp_dir().join(format!(
        "appconfig-{}-{}",
        std::process::id(),
        name.replace('.', "-")
    ));
    std::fs::create_dir_all(&dir).unwrap();
    let path = dir.join(name);
    std::fs::write(&path, text).unwrap();
    path
}

fn vars(pairs: &[(&str, &str)]) -> Vec<(String, String)> {
    pairs
        .iter()
        .map(|(k, v)| (k.to_string(), v.to_string()))
        .collect()
}

/// The rule of the Go side, letter for letter: its table, and the spellings a flag and
/// a dotted path add.
#[test]
fn a_key_is_spelled_as_a_variable() {
    for (key, want) in [
        ("id", "ID"),
        ("tls_certFile", "TLS_CERT_FILE"),
        ("volumeSizeLimitMB", "VOLUME_SIZE_LIMIT_MB"),
        ("kerberos_krb5Conf", "KERBEROS_KRB5_CONF"),
        ("tls_caFile", "TLS_CA_FILE"),
        ("raftListen", "RAFT_LISTEN"),
        (
            "partitions_list_0_grants_1_principal",
            "PARTITIONS_LIST_0_GRANTS_1_PRINCIPAL",
        ),
        ("openbao_kvMount", "OPENBAO_KV_MOUNT"),
        ("tls-cert-file", "TLS_CERT_FILE"),
        ("tls.certFile", "TLS_CERT_FILE"),
        ("cert_file", "CERT_FILE"),
    ] {
        assert_eq!(appconfig::variable(key), want, "{key}");
    }
    assert_eq!(appconfig::prefix("sfs-mount"), "SFS_MOUNT_");
}

/// A file is read whole — lists of structs in lists of structs, a list of strings, a
/// fraction, a duration, a word that is one of a few — and the environment is over it,
/// under the application's prefix and no other.
#[test]
fn the_file_then_the_environment() {
    let cfg: Config = appconfig::Loader::new("app")
        .file(write("app.json", FILE))
        .unwrap()
        .env_from(vars(&[
            ("APP_ID", "from-the-environment"),
            ("APP_PARTITIONS_0_GRANTS_1_PRINCIPAL", "CN=other"),
            ("APP_TLS_KEY_FILE", "/from/the/environment.key"),
            ("ID", "a stray variable of the same name"),
            ("LISTEN", "and another"),
        ]))
        .load()
        .unwrap();

    assert_eq!(cfg.id, "from-the-environment");
    assert_eq!(cfg.listen, ":19333");
    assert_eq!(
        cfg.tls,
        Some(Tls {
            cert_file: "/etc/app/tls.crt".into(),
            key_file: "/from/the/environment.key".into()
        })
    );
    assert_eq!(
        cfg.kerberos, None,
        "a section nothing lies under is not there"
    );
    assert_eq!(cfg.volume_servers, ["10.0.0.21:8080", "10.0.0.22:8080"]);
    assert_eq!(
        (cfg.volume_size_limit_mb, cfg.garbage_threshold),
        (1024, 0.3)
    );
    assert_eq!(cfg.vacuum, Some(Duration::from_secs(5400)));
    assert_eq!(cfg.level, Some(Level::Debug));
    let p = &cfg.partitions;
    assert_eq!(
        (
            p.len(),
            p[0].name.as_str(),
            p[0].size_mb,
            p[1].name.as_str()
        ),
        (2, "oci", 100, "maven")
    );
    assert!(p[0].grants[0].read && !p[0].grants[1].read);
    assert_eq!(p[0].grants[1].principal, "CN=other");
    assert!(p[1].grants.is_empty());
}

/// A flag that was given is over the environment and the file; one clap filled in by
/// its default is not, or no file could ever say otherwise.
#[cfg(feature = "clap")]
#[test]
fn a_flag_over_both() {
    use clap::{Arg, ArgAction, Command};

    let cmd = || {
        Command::new("app")
            .arg(Arg::new("listen").long("listen").default_value(":1"))
            .arg(Arg::new("id").long("id"))
            .arg(Arg::new("tls-cert-file").long("tls-cert-file"))
            .arg(
                Arg::new("volume-servers")
                    .long("volume-servers")
                    .value_delimiter(',')
                    .action(ArgAction::Append),
            )
            .arg(
                Arg::new("verbose")
                    .long("verbose")
                    .action(ArgAction::SetTrue),
            )
    };
    let env = vars(&[("APP_ID", "from-the-environment")]);

    let m = cmd().get_matches_from([
        "app",
        "--id",
        "from-the-flag",
        "--tls-cert-file",
        "/flag.crt",
        "--volume-servers",
        "a:1,b:1",
        "--verbose",
    ]);
    let cfg: Config = appconfig::Loader::new("app")
        .file(write("flags.json", FILE))
        .unwrap()
        .env_from(env.clone())
        .flags(&m)
        .load()
        .unwrap();
    assert_eq!(cfg.id, "from-the-flag");
    assert_eq!(
        cfg.listen, ":19333",
        "clap's own default is not over the file"
    );
    assert_eq!(cfg.tls.unwrap().cert_file, "/flag.crt");
    assert_eq!(cfg.volume_servers, ["a:1", "b:1"]);
    assert!(cfg.verbose);

    let m = cmd().get_matches_from(["app"]);
    let cfg: Config = appconfig::Loader::new("app")
        .file(write("noflags.json", FILE))
        .unwrap()
        .env_from(env)
        .flags(&m)
        .load()
        .unwrap();
    assert_eq!(
        (cfg.id.as_str(), cfg.verbose),
        ("from-the-environment", false)
    );
}

/// A key of a file that no field answers to is an error that names it and the file; the
/// environment is not held to that; and a loader told not to be strict lets it pass.
#[test]
fn a_file_is_read_strictly() {
    let path = write(
        "typo.json",
        r#"{"id": "nn-1", "garbageTreshold": 0.3, "tls": {"certFile": "a", "keyfile": "b"}}"#,
    );
    let err = appconfig::Loader::new("app")
        .file(&path)
        .unwrap()
        .load::<Config>()
        .unwrap_err()
        .to_string();
    assert!(
        err.contains("GARBAGE_TRESHOLD")
            && err.contains("TLS_KEYFILE")
            && err.contains("typo.json"),
        "{err}"
    );

    let lax: Config = appconfig::Loader::new("app")
        .strict(false)
        .file(&path)
        .unwrap()
        .load()
        .unwrap();
    assert_eq!(lax.id, "nn-1");

    let stray: Config = appconfig::Loader::new("app")
        .kv([("id", "x")])
        .env_from(vars(&[("APP_NOBODYS", "1")]))
        .load()
        .unwrap();
    assert_eq!(stray.id, "x");
}

/// What goes wrong says where: a value of the wrong kind names its variable, a setting
/// that has to be there its field, a file its path.
#[test]
fn what_goes_wrong_says_where() {
    let err = appconfig::Loader::new("app")
        .kv([("id", "x"), ("volumeSizeLimitMB", "plenty")])
        .load::<Config>()
        .unwrap_err()
        .to_string();
    assert!(
        err.contains("VOLUME_SIZE_LIMIT_MB") && err.contains("plenty"),
        "{err}"
    );

    let err = appconfig::Loader::new("app")
        .kv([("listen", ":1")])
        .load::<Config>()
        .unwrap_err()
        .to_string();
    assert!(err.contains("id"), "{err}");

    let err = appconfig::Loader::new("app")
        .kv([("id", "x"), ("vacuum", "soon")])
        .load::<Config>()
        .unwrap_err()
        .to_string();
    assert!(err.contains("soon"), "{err}");

    assert!(appconfig::Loader::new("app")
        .file("/no/such/app.json")
        .is_err());
    assert!(
        appconfig::Loader::new("app")
            .file(write("app.ini", "id=x"))
            .is_err(),
        "a format nobody decodes"
    );
}

/// A .env file is the environment written down: its keys may carry the prefix.
#[test]
fn a_dotenv_file() {
    let path = write("app.env", "# the namenode\nexport APP_ID=nn-1\nLISTEN=\":19333\"\nTLS_CERT_FILE='/etc/tls.crt'\nVOLUME_SERVERS=a:1, b:1\n");
    let cfg: Config = appconfig::Loader::new("app")
        .file(path)
        .unwrap()
        .load()
        .unwrap();
    assert_eq!((cfg.id.as_str(), cfg.listen.as_str()), ("nn-1", ":19333"));
    assert_eq!(cfg.tls.unwrap().cert_file, "/etc/tls.crt");
    assert_eq!(cfg.volume_servers, ["a:1", "b:1"]);
}

#[test]
fn durations_as_go_writes_them() {
    use appconfig::duration::parse;
    assert_eq!(parse("90s").unwrap(), Duration::from_secs(90));
    assert_eq!(parse("1h30m").unwrap(), Duration::from_secs(5400));
    assert_eq!(parse("1.5h").unwrap(), Duration::from_secs(5400));
    assert_eq!(parse("250ms").unwrap(), Duration::from_millis(250));
    assert_eq!(parse("0").unwrap(), Duration::ZERO);
    assert!(parse("soon").is_err() && parse("15").is_err() && parse("").is_err());
}

#[cfg(feature = "toml")]
#[test]
fn a_toml_file() {
    let path = write("app.toml", "id = \"nn-1\"\nvolumeServers = [\"a:1\", \"b:1\"]\n\n[tls]\ncertFile = \"/etc/tls.crt\"\n\n[[partitions]]\nname = \"oci\"\nsizeMB = 7\n");
    let cfg: Config = appconfig::Loader::new("app")
        .file(path)
        .unwrap()
        .load()
        .unwrap();
    assert_eq!(
        (
            cfg.id.as_str(),
            cfg.partitions[0].size_mb,
            cfg.volume_servers.len()
        ),
        ("nn-1", 7, 2)
    );
}

#[cfg(feature = "yaml")]
#[test]
fn a_yaml_file() {
    let path = write("app.yaml", "id: nn-1\ntls:\n  certFile: /etc/tls.crt\npartitions:\n  - name: oci\n    grants:\n      - principal: CN=registry\n        read: true\nvacuum: 15m\n");
    let cfg: Config = appconfig::Loader::new("app")
        .file(path)
        .unwrap()
        .load()
        .unwrap();
    assert_eq!(
        (
            cfg.id.as_str(),
            cfg.partitions[0].grants[0].read,
            cfg.vacuum
        ),
        ("nn-1", true, Some(Duration::from_secs(900)))
    );
}
