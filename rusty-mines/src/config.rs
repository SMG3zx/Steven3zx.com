use std::{
    env, fs,
    io::{self, Write},
    net::SocketAddr,
    path::{Path, PathBuf},
};

use directories::ProjectDirs;
use reedline::{DefaultPrompt, DefaultPromptSegment, Reedline, Signal};
use serde::{Deserialize, Serialize};

const CONFIG_FILE: &str = "rusty-mines.toml";
const PATH_FILE: &str = "config-path";

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Config {
    pub host: String,
    pub database: String,
    pub listen: SocketAddr,
}

/// Load the working-directory config, a remembered selection, or the conventional
/// config, in that order. Only prompt when none of those locations is configured.
pub fn load_or_setup(editor: &mut Reedline) -> io::Result<(Config, PathBuf)> {
    let host_override = env::var("SPACETIMEDB_HOST")
        .map(Some)
        .or_else(|error| match error {
            env::VarError::NotPresent => Ok(None),
            env::VarError::NotUnicode(_) => Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "SPACETIMEDB_HOST must be valid Unicode",
            )),
        })?;
    if let Some(host) = &host_override {
        validate_host(host).map_err(|error| {
            io::Error::new(
                io::ErrorKind::InvalidInput,
                format!("Invalid SPACETIMEDB_HOST: {error}"),
            )
        })?;
    }
    let (mut config, path) = load_or_setup_config(editor, host_override.as_deref())?;
    if let Some(host) = host_override {
        config.host = host;
        println!("Using SPACETIMEDB_HOST: {}", config.host);
    }
    Ok((config, path))
}

fn load_or_setup_config(
    editor: &mut Reedline,
    host_override: Option<&str>,
) -> io::Result<(Config, PathBuf)> {
    let cwd = env::current_dir()?;
    let dirs = ProjectDirs::from("", "", "rusty-mines").ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::NotFound,
            "Cannot determine the user config directory",
        )
    })?;
    let config_dir = dirs.config_dir();
    let conventional = config_dir.join(CONFIG_FILE);
    let marker = config_dir.join(PATH_FILE);

    if let Some(path) = discover_path(&cwd, &conventional, &marker)? {
        return Ok((read_config(&path)?, path));
    }

    println!("I don't see a config file. Where would you like it to reside?");
    let location = ask(
        editor,
        format!(
            "Config location [{}] (here, directory, or file): ",
            conventional.display()
        ),
    )?;
    let path = resolve_path(&location, &cwd, &conventional);
    println!("Using configuration at {}", path.display());
    let config = if exists(&path)? {
        read_config(&path)?
    } else {
        let host_default = host_override.unwrap_or("http://127.0.0.1:3000");
        let host = loop {
            let input = ask(
                editor,
                format!("SPACETIMEDB_HOST (HTTP(S) address) [{host_default}]: "),
            )?;
            let host = default_input(&input, host_default);
            match validate_host(&host) {
                Ok(()) => break host,
                Err(error) => eprintln!("Invalid host: {error}"),
            }
        };
        let database = loop {
            let database = ask(editor, "Database name: ".into())?.trim().to_owned();
            if !database.is_empty() {
                break database;
            }
            eprintln!("Database name must not be empty.");
        };
        let listen = loop {
            let input = ask(editor, "Listen socket address [127.0.0.1:25565]: ".into())?;
            match default_input(&input, "127.0.0.1:25565").parse::<SocketAddr>() {
                Ok(listen) => break listen,
                Err(error) => eprintln!("Invalid listen socket address: {error}"),
            }
        };
        let config = Config {
            host,
            database,
            listen,
        };
        create_config(&path, &config)?;
        println!("Configuration saved.");
        config
    };

    // Store an absolute path so the selection is independent of the next launch's cwd.
    let selected = path.to_str().ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::InvalidInput,
            "Config path is not valid UTF-8; cannot remember it",
        )
    })?;
    if selected.contains(['\n', '\r']) {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "Config path cannot contain a newline",
        ));
    }
    fs::create_dir_all(config_dir)
        .map_err(|error| path_error("create config directory", config_dir, error))?;
    fs::write(&marker, selected).map_err(|error| {
        path_error(
            "remember config selection (config remains at the selected path)",
            &marker,
            error,
        )
    })?;
    Ok((config, path))
}

fn ask(editor: &mut Reedline, question: String) -> io::Result<String> {
    let prompt = DefaultPrompt::new(
        DefaultPromptSegment::Basic(question),
        DefaultPromptSegment::Empty,
    );
    match editor.read_line(&prompt)? {
        Signal::Success(line) => Ok(line),
        Signal::CtrlC | Signal::CtrlD => Err(io::Error::new(
            io::ErrorKind::Interrupted,
            "Configuration setup aborted",
        )),
    }
}

fn default_input(input: &str, default: &str) -> String {
    let input = input.trim();
    if input.is_empty() {
        default.to_owned()
    } else {
        input.to_owned()
    }
}

// Existing files are used directly. Otherwise, extensionless paths and paths
// ending in a separator denote directories. Quotes support shell-style pasted paths.
fn resolve_path(input: &str, cwd: &Path, conventional: &Path) -> PathBuf {
    let input = input.trim();
    if input.is_empty() {
        return conventional.to_path_buf();
    }
    let input = if input.len() >= 2
        && ((input.starts_with('"') && input.ends_with('"'))
            || (input.starts_with('\'') && input.ends_with('\'')))
    {
        &input[1..input.len() - 1]
    } else {
        input
    };
    if input == "here" {
        return cwd.join(CONFIG_FILE);
    }
    let path = PathBuf::from(input);
    let path = if path.is_absolute() {
        path
    } else {
        cwd.join(path)
    };
    if !path.is_file()
        && (path.is_dir() || path.extension().is_none() || input.ends_with(['/', '\\']))
    {
        path.join(CONFIG_FILE)
    } else {
        path
    }
}

fn exists(path: &Path) -> io::Result<bool> {
    path.try_exists()
        .map_err(|error| path_error("inspect", path, error))
}

fn discover_path(cwd: &Path, conventional: &Path, marker: &Path) -> io::Result<Option<PathBuf>> {
    let local = cwd.join(CONFIG_FILE);
    if exists(&local)? {
        return Ok(Some(local));
    }
    if exists(marker)? {
        let saved = fs::read_to_string(marker)
            .map_err(|error| path_error("read saved config path", marker, error))?;
        let saved = saved.trim_end_matches(['\r', '\n']);
        let path = PathBuf::from(saved);
        if saved.is_empty() || !path.is_absolute() {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                format!(
                    "Saved config path in '{}' must be a nonempty absolute path",
                    marker.display()
                ),
            ));
        }
        // A stale selection is an error, not permission to silently create a new config.
        return Ok(Some(path));
    }
    if exists(conventional)? {
        return Ok(Some(conventional.to_path_buf()));
    }
    Ok(None)
}

fn validate_host(host: &str) -> Result<(), &'static str> {
    let authority = host
        .strip_prefix("http://")
        .or_else(|| host.strip_prefix("https://"))
        .ok_or("expected an http:// or https:// URL")?;
    if host.chars().any(|c| c.is_whitespace() || c.is_control()) {
        return Err("URL must not contain whitespace or control characters");
    }
    let authority = authority.split(['/', '?', '#']).next().unwrap_or_default();
    if authority.is_empty() || authority.contains('@') {
        return Err("URL must contain a host without credentials");
    }
    let (name, port) = if let Some(rest) = authority.strip_prefix('[') {
        let (ip, suffix) = rest.split_once(']').ok_or("invalid bracketed IPv6 host")?;
        ip.parse::<std::net::Ipv6Addr>()
            .map_err(|_| "invalid IPv6 host")?;
        let port = if suffix.is_empty() {
            None
        } else {
            Some(suffix.strip_prefix(':').ok_or("invalid host suffix")?)
        };
        (ip, port)
    } else {
        let (name, port) = match authority.split_once(':') {
            Some((name, port)) => (name, Some(port)),
            None => (authority, None),
        };
        if name.is_empty()
            || !name
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '-'))
        {
            return Err("invalid hostname (IPv6 addresses must be bracketed)");
        }
        if name
            .trim_end_matches('.')
            .split('.')
            .any(|label| label.is_empty() || label.starts_with('-') || label.ends_with('-'))
        {
            return Err("invalid hostname label");
        }
        (name, port)
    };
    if name.is_empty() {
        return Err("URL must contain a host");
    }
    if let Some(port) = port {
        if port.is_empty()
            || !port.bytes().all(|c| c.is_ascii_digit())
            || port.parse::<u16>().is_err()
        {
            return Err("invalid port (expected 0 through 65535)");
        }
    }
    Ok(())
}

fn validate_config(config: &Config) -> io::Result<()> {
    if config.database.trim().is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "Database name must not be empty",
        ));
    }
    validate_host(&config.host).map_err(|error| {
        io::Error::new(io::ErrorKind::InvalidData, format!("Invalid host: {error}"))
    })
}

fn read_config(path: &Path) -> io::Result<Config> {
    let contents =
        fs::read_to_string(path).map_err(|error| path_error("read config", path, error))?;
    let config: Config = toml::from_str(&contents).map_err(|error| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            format!("Invalid config '{}': {error}", path.display()),
        )
    })?;
    validate_config(&config).map_err(|error| path_error("validate config", path, error))?;
    Ok(config)
}

pub fn edit_settings(editor: &mut Reedline, path: &Path) -> io::Result<()> {
    loop {
        let saved = read_config(path)?;
        println!(
            "Saved settings ({}):\n  1. host     = {}\n  2. database = {}\n  3. listen   = {}",
            path.display(),
            saved.host,
            saved.database,
            saved.listen
        );
        if let Ok(host) = env::var("SPACETIMEDB_HOST") {
            println!("SPACETIMEDB_HOST overrides the saved host: {host}");
        }
        let choice = ask(
            editor,
            "Which item? [1-3, host, database, listen, or done]: ".into(),
        )?;
        let field = match choice.trim() {
            "" | "done" | "cancel" => return Ok(()),
            "1" | "host" | "SPACETIMEDB_HOST" => "host",
            "2" | "database" => "database",
            "3" | "listen" => "listen",
            _ => {
                println!("Choose host, database, listen, or done.");
                continue;
            }
        };
        loop {
            let value = ask(
                editor,
                format!("New {field} (Enter to keep current value): "),
            )?;
            if value.trim().is_empty() {
                break;
            }
            match update_setting(&saved, field, &value) {
                Ok(updated) => {
                    save_setting(path, field, &updated)?;
                    println!("Saved {field}. Restart the server to apply changes.");
                    if field == "host" && env::var_os("SPACETIMEDB_HOST").is_some() {
                        println!("Update or unset SPACETIMEDB_HOST to use the saved host.");
                    }
                    break;
                }
                Err(error) => println!("Invalid {field}: {error}"),
            }
        }
    }
}

fn update_setting(config: &Config, field: &str, value: &str) -> io::Result<Config> {
    let mut updated = config.clone();
    let value = value.trim();
    match field {
        "host" => updated.host = value.into(),
        "database" => updated.database = value.into(),
        "listen" => {
            updated.listen = value
                .parse()
                .map_err(|error| io::Error::new(io::ErrorKind::InvalidInput, error))?
        }
        _ => {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "Unknown setting",
            ))
        }
    }
    validate_config(&updated)?;
    Ok(updated)
}

fn save_setting(path: &Path, field: &str, config: &Config) -> io::Result<()> {
    // Edit only the selected key, retaining comments and other configuration fields.
    let contents = fs::read_to_string(path)?;
    let mut document = contents
        .parse::<toml_edit::DocumentMut>()
        .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
    let value = match field {
        "host" => config.host.clone(),
        "database" => config.database.clone(),
        "listen" => config.listen.to_string(),
        _ => {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "Unknown setting",
            ))
        }
    };
    let mut replacement = toml_edit::Value::from(value);
    if let Some(previous) = document[field].as_value() {
        *replacement.decor_mut() = previous.decor().clone();
    }
    document[field] = toml_edit::Item::Value(replacement);
    let parent = path.parent().ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::InvalidInput,
            "Config needs a parent directory",
        )
    })?;
    let mut file = tempfile::NamedTempFile::new_in(parent)?;
    file.write_all(document.to_string().as_bytes())?;
    file.as_file().sync_all()?;
    file.persist(path)
        .map_err(|error| path_error("save config", path, error.error))?;
    Ok(())
}

fn create_config(path: &Path, config: &Config) -> io::Result<()> {
    validate_config(config)?;
    let contents = toml::to_string_pretty(config)
        .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)
            .map_err(|error| path_error("create config parent", parent, error))?;
    }
    let mut file = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(path)
        .map_err(|error| path_error("create config without overwriting", path, error))?;
    file.write_all(contents.as_bytes())
        .and_then(|()| file.sync_all())
        .map_err(|error| path_error("write config", path, error))
}

fn path_error(action: &str, path: &Path, error: io::Error) -> io::Error {
    io::Error::new(
        error.kind(),
        format!("Could not {action} '{}': {error}", path.display()),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sample() -> Config {
        Config {
            host: "https://example.com:3000".into(),
            database: "mines".into(),
            listen: "127.0.0.1:25565".parse().unwrap(),
        }
    }

    #[test]
    fn resolves_default_here_directories_and_files_with_spaces() {
        let cwd = env::current_dir().unwrap();
        let conventional = cwd.join("conventional").join(CONFIG_FILE);
        assert_eq!(resolve_path("", &cwd, &conventional), conventional);
        assert_eq!(
            resolve_path("here", &cwd, &conventional),
            cwd.join(CONFIG_FILE)
        );
        assert_eq!(
            resolve_path("my config", &cwd, &conventional),
            cwd.join("my config").join(CONFIG_FILE)
        );
        assert_eq!(
            resolve_path("my config/custom.toml", &cwd, &conventional),
            cwd.join("my config/custom.toml")
        );
        assert_eq!(
            resolve_path("\"my config/custom.toml\"", &cwd, &conventional),
            cwd.join("my config/custom.toml")
        );
        assert_eq!(
            resolve_path(conventional.to_str().unwrap(), &cwd, &conventional),
            conventional
        );
        assert_eq!(
            resolve_path(cwd.to_str().unwrap(), &cwd, &conventional),
            cwd.join(CONFIG_FILE)
        );
    }

    #[test]
    fn validates_config_and_http_hosts() {
        assert!(validate_config(&sample()).is_ok());
        for host in [
            "http://localhost:3000",
            "https://example.com/",
            "http://[::1]:3000",
        ] {
            assert!(validate_host(host).is_ok(), "{host}");
        }
        for host in [
            "",
            "ftp://example.com",
            "https://",
            "http:// bad",
            "http://:3000",
            "http://host:abc",
            "http://host:65536",
            "http://[bad]",
            "http://a..b",
        ] {
            assert!(validate_host(host).is_err(), "{host}");
        }
        let mut config = sample();
        config.database = " \t".into();
        assert!(validate_config(&config).is_err());
        config.database = "mines".into();
        config.host = "ws://localhost".into();
        assert!(validate_config(&config).is_err());
    }

    #[test]
    fn edits_validate_and_preserve_other_settings() {
        let original = sample();
        let updated = update_setting(&original, "database", " new-mines ").unwrap();
        assert_eq!(updated.database, "new-mines");
        assert_eq!(updated.host, original.host);
        assert_eq!(updated.listen, original.listen);
        for (field, value) in [
            ("host", "bad"),
            ("database", " "),
            ("listen", "bad"),
            ("unknown", "value"),
        ] {
            assert!(update_setting(&original, field, value).is_err());
        }
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(CONFIG_FILE);
        fs::write(
            &path,
            format!(
                "# My server\n{}extra = true\n",
                toml::to_string(&original).unwrap()
            ),
        )
        .unwrap();
        save_setting(&path, "database", &updated).unwrap();
        assert_eq!(read_config(&path).unwrap(), updated);
        let contents = fs::read_to_string(&path).unwrap();
        assert!(contents.contains("# My server"));
        assert!(contents.contains("extra = true"));
    }

    #[test]
    fn config_toml_round_trip_and_invalid_socket() {
        let config = sample();
        let encoded = toml::to_string_pretty(&config).unwrap();
        assert_eq!(toml::from_str::<Config>(&encoded).unwrap(), config);
        let mut ipv6 = config;
        ipv6.listen = "[::1]:25565".parse().unwrap();
        assert_eq!(
            toml::from_str::<Config>(&toml::to_string(&ipv6).unwrap()).unwrap(),
            ipv6
        );
        assert!(toml::from_str::<Config>(
            "host = 'http://localhost'\ndatabase = 'mines'\nlisten = 'localhost:25565'\n"
        )
        .is_err());
    }
}
