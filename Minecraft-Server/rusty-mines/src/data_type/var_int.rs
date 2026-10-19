use std::fmt;
#[cfg(test)]
use std::io::{self, Read};

/// A Minecraft protocol VarInt: a two's-complement signed 32-bit integer
/// encoded in one to five bytes.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) struct VarInt(i32);

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum VarIntError {
    Incomplete,
    TooLong,
    Overflow,
    #[cfg(test)]
    Io(io::ErrorKind),
}

impl fmt::Display for VarIntError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Incomplete => write!(f, "incomplete VarInt"),
            Self::TooLong => write!(f, "VarInt exceeds five bytes"),
            Self::Overflow => write!(f, "VarInt overflows a signed 32-bit integer"),
            #[cfg(test)]
            Self::Io(kind) => write!(f, "failed to read VarInt: {kind}"),
        }
    }
}

impl std::error::Error for VarIntError {}

impl VarInt {
    const MAX_BYTES: usize = 5;

    pub(crate) fn new(value: i32) -> Self {
        Self(value)
    }

    pub(crate) fn value(self) -> i32 {
        self.0
    }

    pub(crate) fn encode(self) -> Vec<u8> {
        let mut value = self.0 as u32;
        let mut encoded = Vec::with_capacity(Self::MAX_BYTES);

        loop {
            let mut byte = (value & 0x7f) as u8;
            value >>= 7;

            if value != 0 {
                byte |= 0x80;
            }

            encoded.push(byte);
            if value == 0 {
                return encoded;
            }
        }
    }

    /// Decode one VarInt from the start of `bytes`, returning the value and
    /// number of bytes consumed. Any trailing bytes are left for the caller.
    pub(crate) fn decode(bytes: &[u8]) -> Result<(Self, usize), VarIntError> {
        let mut value = 0u32;

        for (index, &byte) in bytes.iter().take(Self::MAX_BYTES).enumerate() {
            let payload = byte & 0x7f;

            // An i32 has only four payload bits available in byte five.
            if index == Self::MAX_BYTES - 1 && payload > 0x0f {
                return Err(VarIntError::Overflow);
            }

            value |= u32::from(payload) << (index * 7);

            if byte & 0x80 == 0 {
                return Ok((Self::new(value as i32), index + 1));
            }

            // A continuation bit on byte five means the encoding exceeds
            // the maximum VarInt width.
            if index == Self::MAX_BYTES - 1 {
                return Err(VarIntError::TooLong);
            }
        }

        if bytes.len() < Self::MAX_BYTES {
            Err(VarIntError::Incomplete)
        } else {
            Err(VarIntError::TooLong)
        }
    }

    // Production packet handling uses decode to retain incomplete buffered frames.
    #[cfg(test)]
    fn read<R: Read>(reader: &mut R) -> Result<Self, VarIntError> {
        let mut bytes = [0u8; Self::MAX_BYTES];

        for length in 1..=Self::MAX_BYTES {
            reader
                .read_exact(&mut bytes[length - 1..length])
                .map_err(|error| {
                    if error.kind() == io::ErrorKind::UnexpectedEof {
                        VarIntError::Incomplete
                    } else {
                        VarIntError::Io(error.kind())
                    }
                })?;

            match Self::decode(&bytes[..length]) {
                Ok((value, _)) => return Ok(value),
                Err(VarIntError::Incomplete) if length < Self::MAX_BYTES => continue,
                Err(error) => return Err(error),
            }
        }

        Err(VarIntError::TooLong)
    }
}

#[cfg(test)]
mod tests {
    use super::{VarInt, VarIntError};
    use std::io::Cursor;

    #[test]
    fn encodes_protocol_boundaries() {
        assert_eq!(VarInt::new(0).encode(), vec![0]);
        assert_eq!(VarInt::new(127).encode(), vec![0x7f]);
        assert_eq!(VarInt::new(128).encode(), vec![0x80, 0x01]);
        assert_eq!(VarInt::new(-1).encode(), vec![0xff, 0xff, 0xff, 0xff, 0x0f]);
        assert_eq!(
            VarInt::new(i32::MIN).encode(),
            vec![0x80, 0x80, 0x80, 0x80, 0x08]
        );
        assert_eq!(
            VarInt::new(i32::MAX).encode(),
            vec![0xff, 0xff, 0xff, 0xff, 0x07]
        );
    }

    #[test]
    fn decodes_and_reports_consumed_bytes() {
        assert_eq!(VarInt::decode(&[0x80, 0x01]), Ok((VarInt::new(128), 2)));
        assert_eq!(VarInt::new(128).value(), 128);
        assert_eq!(
            VarInt::decode(&[0xff, 0xff, 0xff, 0xff, 0x0f]),
            Ok((VarInt::new(-1), 5))
        );
    }

    #[test]
    fn reads_from_a_stream() {
        let mut input = Cursor::new(VarInt::new(300).encode());
        assert_eq!(VarInt::read(&mut input), Ok(VarInt::new(300)));
    }

    #[test]
    fn reports_stream_errors() {
        struct FailedReader;
        impl std::io::Read for FailedReader {
            fn read(&mut self, _: &mut [u8]) -> std::io::Result<usize> {
                Err(std::io::Error::from(std::io::ErrorKind::ConnectionReset))
            }
        }
        assert_eq!(
            VarInt::read(&mut FailedReader),
            Err(VarIntError::Io(std::io::ErrorKind::ConnectionReset))
        );
        assert_eq!(
            VarInt::read(&mut Cursor::new(vec![0x80])),
            Err(VarIntError::Incomplete)
        );
    }

    #[test]
    fn rejects_invalid_values() {
        assert_eq!(VarInt::decode(&[0x80]), Err(VarIntError::Incomplete));
        assert_eq!(
            VarInt::decode(&[0x80, 0x80, 0x80, 0x80, 0x10]),
            Err(VarIntError::Overflow)
        );
        assert_eq!(
            VarInt::decode(&[0x80, 0x80, 0x80, 0x80, 0x80]),
            Err(VarIntError::TooLong)
        );
    }
}
