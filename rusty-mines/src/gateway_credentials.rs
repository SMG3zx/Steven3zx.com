//! Gateway bearer credentials never belong in project configuration or logs.
use std::{
    fs,
    io::{self, Read, Write},
    path::{Path, PathBuf},
};

pub(crate) fn path(host: &str, database: &str) -> io::Result<PathBuf> {
    let dirs = directories::ProjectDirs::from("", "", "rusty-mines")
        .ok_or_else(|| io::Error::other("Cannot determine user configuration directory"))?;
    let key = md5::compute(format!("{host}\0{database}"));
    Ok(dirs
        .config_dir()
        .join("gateway-credentials")
        .join(format!("{key:x}"))
        .join("token"))
}

fn reject_link(path: &Path) -> io::Result<()> {
    match fs::symlink_metadata(path) {
        Ok(metadata) if metadata.file_type().is_symlink() => {
            Err(io::Error::other("Credential path must not be a symlink"))
        }
        Ok(_) => Ok(()),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(error),
    }
}

fn protect_directory(directory: &Path) -> io::Result<()> {
    reject_link(directory)?;
    fs::create_dir_all(directory)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(directory, fs::Permissions::from_mode(0o700))?;
    }
    #[cfg(windows)]
    {
        use std::{os::windows::process::CommandExt, process::Command};
        let user = Command::new("whoami.exe")
            .args(["/user", "/fo", "csv", "/nh"])
            .creation_flags(0x08000000)
            .output()?;
        if !user.status.success() {
            return Err(io::Error::other(
                "Cannot determine gateway credential owner SID",
            ));
        }
        let output = String::from_utf8_lossy(&user.stdout);
        let sid = output
            .split(['"', ',', '\r', '\n'])
            .find(|part| part.starts_with("S-1-"))
            .ok_or_else(|| io::Error::other("Cannot parse credential owner SID"))?;
        let acl = Command::new("icacls.exe")
            .arg(directory)
            .args(["/inheritance:r", "/grant:r"])
            .arg(format!("*{sid}:(OI)(CI)F"))
            .creation_flags(0x08000000)
            .output()?;
        if !acl.status.success() {
            return Err(io::Error::other(
                "Cannot protect gateway credential directory ACL",
            ));
        }
    }
    Ok(())
}

pub(crate) fn load(path: &Path) -> io::Result<Option<String>> {
    if let Some(directory) = path.parent() {
        reject_link(directory)?;
    }
    reject_link(path)?;
    let file = match fs::File::open(path) {
        Ok(file) => file,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    };
    if !file.metadata()?.is_file() || file.metadata()?.len() > 16384 {
        return Err(io::Error::other("Invalid gateway credential file"));
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if file.metadata()?.permissions().mode() & 0o077 != 0 {
            return Err(io::Error::other(
                "Gateway token permissions must be private (0600)",
            ));
        }
    }
    let mut token = String::new();
    file.take(16385).read_to_string(&mut token)?;
    if token.is_empty() || token.len() > 16384 || token.chars().any(char::is_whitespace) {
        return Err(io::Error::other(
            "Invalid gateway token; refusing anonymous fallback",
        ));
    }
    Ok(Some(token))
}

pub(crate) fn save(path: &Path, token: &str) -> io::Result<()> {
    if token.is_empty() || token.len() > 16384 || token.chars().any(char::is_whitespace) {
        return Err(io::Error::other("Invalid gateway token"));
    }
    let directory = path
        .parent()
        .ok_or_else(|| io::Error::other("Token path has no parent"))?;
    protect_directory(directory)?;
    if let Some(existing) = load(path)? {
        return if existing == token {
            Ok(())
        } else {
            Err(io::Error::other(
                "Gateway credential already exists with a different identity; refusing overwrite",
            ))
        };
    }
    let mut file = tempfile::NamedTempFile::new_in(directory)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        file.as_file()
            .set_permissions(fs::Permissions::from_mode(0o600))?;
    }
    file.write_all(token.as_bytes())?;
    file.as_file().sync_all()?;
    match file.persist_noclobber(path) {
        Ok(_) => Ok(()),
        Err(error) if error.error.kind() == io::ErrorKind::AlreadyExists => {
            if load(path)?.as_deref() == Some(token) {
                Ok(())
            } else {
                Err(io::Error::other(
                    "Concurrent gateway credential creation; refusing overwrite",
                ))
            }
        }
        Err(error) => Err(error.error),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn roundtrip_no_overwrite_and_no_anonymous_fallback() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("private").join("token");
        assert_eq!(load(&path).unwrap(), None);
        save(&path, "test-token").unwrap();
        assert_eq!(load(&path).unwrap().as_deref(), Some("test-token"));
        save(&path, "test-token").unwrap();
        assert!(save(&path, "different").is_err());
        assert_eq!(load(&path).unwrap().as_deref(), Some("test-token"));
        fs::write(&path, "").unwrap();
        assert!(load(&path).is_err());
    }
    #[test]
    fn rejects_invalid_tokens_without_creating_file() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("token");
        for token in ["", "secret\n", "a b"] {
            assert!(save(&path, token).is_err());
            assert!(!path.exists());
        }
    }
    #[test]
    fn concurrent_first_start_never_overwrites_winning_token() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("private").join("token");
        let barrier = std::sync::Arc::new(std::sync::Barrier::new(2));
        let workers: Vec<_> = ["synthetic-a", "synthetic-b"]
            .into_iter()
            .map(|token| {
                let path = path.clone();
                let barrier = barrier.clone();
                std::thread::spawn(move || {
                    barrier.wait();
                    (token, save(&path, token).is_ok())
                })
            })
            .collect();
        let results: Vec<_> = workers
            .into_iter()
            .map(|worker| worker.join().unwrap())
            .collect();
        assert_eq!(results.iter().filter(|(_, saved)| *saved).count(), 1);
        let winner = results.iter().find(|(_, saved)| *saved).unwrap().0;
        assert_eq!(load(&path).unwrap().as_deref(), Some(winner));
    }

    #[test]
    fn malformed_and_oversized_files_do_not_fall_back() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("private").join("token");
        save(&path, "synthetic").unwrap();
        fs::write(&path, [0xff]).unwrap();
        assert!(load(&path).is_err());
        fs::write(&path, vec![b'a'; 16385]).unwrap();
        assert!(load(&path).is_err());
    }

    #[cfg(unix)]
    #[test]
    fn symlinks_and_public_permissions_are_rejected() {
        use std::os::unix::fs::{symlink, PermissionsExt};
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("private").join("token");
        save(&path, "synthetic").unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o644)).unwrap();
        assert!(load(&path).is_err());
        fs::set_permissions(&path, fs::Permissions::from_mode(0o600)).unwrap();
        let link = temp.path().join("linked-token");
        symlink(&path, &link).unwrap();
        assert!(load(&link).is_err());
        assert!(save(&link, "synthetic").is_err());
        let directory_link = temp.path().join("linked-directory");
        symlink(path.parent().unwrap(), &directory_link).unwrap();
        assert!(load(&directory_link.join("token")).is_err());
    }

    #[test]
    fn credentials_are_scoped_to_host_and_database() {
        assert_ne!(
            path("http://localhost:3000", "a").unwrap(),
            path("http://localhost:3000", "b").unwrap()
        );
        assert_ne!(
            path("http://localhost:3000", "a").unwrap(),
            path("https://maincloud.spacetimedb.com", "a").unwrap()
        );
    }
}
