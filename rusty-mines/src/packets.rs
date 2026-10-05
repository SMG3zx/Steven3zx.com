mod handshake;
pub use rusty_mines::{configuration, login, play};
pub(crate) mod status;

use crate::data_type::VarInt;
use std::io::{self, Write};

/// Write one uncompressed frame without retaining an outgoing queue.
pub(crate) fn write_frame(writer: &mut impl Write, id: i32, data: &[u8]) -> io::Result<()> {
    let id = VarInt::new(id).encode();
    let length = id
        .len()
        .checked_add(data.len())
        .filter(|&n| n <= 0x1f_ffff)
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "outbound frame too large"))?;
    writer.write_all(&VarInt::new(length as i32).encode())?;
    writer.write_all(&id)?;
    writer.write_all(data)
}

use flate2::{Decompress, FlushDecompress, Status};

pub(crate) use crate::packets::handshake::HandshakePacket;

#[derive(Debug, PartialEq, Eq)]
pub struct Packet {
    pub id: i32,
    pub data: Vec<u8>,
}

#[derive(Debug, PartialEq, Eq)]
pub enum PacketError {
    InvalidVarInt,
    EmptyPacket,
    InvalidPacketId,
    InvalidDataLength,
    UncompressedPacketTooLarge,
    BelowCompressionThreshold,
    InvalidCompressedData,
    AllocationFailed,
}

pub fn parse_packet(
    input: &[u8],
    compression_threshold: Option<i32>,
    serverbound: bool,
) -> Result<Option<(Packet, usize)>, PacketError> {
    const MAX_SERVERBOUND_BODY: usize = 8_388_608;

    // The outer length uses at most three bytes, allowing up to 2^21 - 1.
    let Some((length, length_bytes)) = read_varint(input, 3)? else {
        return Ok(None);
    };

    let frame_end = length_bytes + length as usize;

    if input.len() < frame_end {
        return Ok(None);
    }

    let frame = &input[length_bytes..frame_end];
    let threshold = compression_threshold.filter(|&value| value >= 0);

    let mut body = match threshold {
        None => copy_bytes(frame)?,
        Some(threshold) => {
            let (data_length, header_bytes) =
                read_varint(frame, 5)?.ok_or(PacketError::InvalidDataLength)?;

            if data_length < 0 {
                return Err(PacketError::InvalidDataLength);
            }

            let payload = &frame[header_bytes..];

            if data_length == 0 {
                // Vanilla accepts uncompressed bodies even above threshold.
                copy_bytes(payload)?
            } else {
                let expected_length = data_length as usize;

                if serverbound && expected_length > MAX_SERVERBOUND_BODY {
                    return Err(PacketError::UncompressedPacketTooLarge);
                }

                if serverbound && data_length < threshold {
                    return Err(PacketError::BelowCompressionThreshold);
                }

                let mut decoded = Vec::new();
                decoded
                    .try_reserve_exact(expected_length + 1)
                    .map_err(|_| PacketError::AllocationFailed)?;

                let mut decoder = Decompress::new(true); // zlib wrapper

                let status = decoder
                    .decompress_vec(payload, &mut decoded, FlushDecompress::Finish)
                    .map_err(|_| PacketError::InvalidCompressedData)?;

                if status != Status::StreamEnd
                    || decoder.total_in() != payload.len() as u64
                    || decoded.len() != expected_length
                {
                    return Err(PacketError::InvalidCompressedData);
                }

                decoded
            }
        }
    };
    if serverbound && body.len() > MAX_SERVERBOUND_BODY {
        return Err(PacketError::UncompressedPacketTooLarge);
    }

    if body.is_empty() {
        return Err(PacketError::EmptyPacket);
    }

    let (id, id_bytes) = read_varint(&body, 5)?.ok_or(PacketError::InvalidPacketId)?;

    if id < 0 {
        return Err(PacketError::InvalidPacketId);
    }

    body.drain(..id_bytes);

    Ok(Some((Packet { id, data: body }, frame_end)))
}

fn copy_bytes(bytes: &[u8]) -> Result<Vec<u8>, PacketError> {
    let mut result = Vec::new();
    result
        .try_reserve_exact(bytes.len())
        .map_err(|_| PacketError::AllocationFailed)?;
    result.extend_from_slice(bytes);
    Ok(result)
}

fn read_varint(input: &[u8], max_bytes: usize) -> Result<Option<(i32, usize)>, PacketError> {
    let mut value = 0u32;

    for index in 0..max_bytes {
        let Some(&byte) = input.get(index) else {
            return Ok(None);
        };

        let payload = byte & 0x7f;

        if index == 4 && payload > 0x0f {
            return Err(PacketError::InvalidVarInt);
        }

        value |= u32::from(payload) << (index * 7);

        if byte & 0x80 == 0 {
            return Ok(Some((value as i32, index + 1)));
        }
    }

    Err(PacketError::InvalidVarInt)
}
