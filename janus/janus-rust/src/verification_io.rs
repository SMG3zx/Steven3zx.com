//! Bounded file input shared by local verification binaries.

use std::fs;
use std::io::Read;

const CHUNK_BYTES: usize = 8 * 1024;

/// Reads UTF-8 text while enforcing a caller-provided byte limit.
///
/// # Errors
///
/// Returns a formatted error when the file cannot be read, exceeds the limit,
/// or is not valid UTF-8.
pub fn read_bounded_text(path: &str, max_bytes: usize) -> Result<String, String> {
    let mut file = fs::File::open(path).map_err(|error| format!("{path}: {error}"))?;
    let mut bytes = Vec::from([]);
    let max_chunks = (max_bytes / CHUNK_BYTES) + 1;
    for _ in 0..=max_chunks {
        let mut chunk = [0_u8; CHUNK_BYTES];
        let bytes_read = file
            .read(&mut chunk)
            .map_err(|error| format!("{path}: {error}"))?;
        if bytes_read == 0 {
            return String::from_utf8(bytes)
                .map_err(|error| format!("{path}: invalid UTF-8: {error}"));
        }
        if bytes.len() + bytes_read > max_bytes {
            return Err(format!("{path}: input exceeds {max_bytes} bytes"));
        }
        bytes.extend_from_slice(&chunk[..bytes_read]);
    }
    Err(format!("{path}: input exceeds {max_bytes} bytes"))
}
