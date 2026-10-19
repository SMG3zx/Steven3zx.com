use crate::data_type::{Intent, MCString, VarInt};

#[derive(Debug)]
pub struct HandshakePacket {
    pub(crate) protocol_version: VarInt,
    pub(crate) server_address: MCString<255>,
    pub(crate) server_port: u16,
    pub(crate) intent: Intent,
}

impl HandshakePacket {
    /// `data` contains the fields only: no frame length or packet ID.
    pub fn decode(data: &[u8]) -> Result<Self, &'static str> {
        let mut remaining = data;

        let protocol_version = read_varint(&mut remaining)?;
        let address_length = read_varint(&mut remaining)?.value();

        if !(0..=255 * 3).contains(&address_length) {
            return Err("invalid server address byte length");
        }

        let address_bytes = take(&mut remaining, address_length as usize)?;
        let address = std::str::from_utf8(address_bytes)
            .map_err(|_| "server address contains invalid UTF-8")?;

        let server_address = MCString::<255>::try_new(address.to_owned())?;

        let port_bytes = take(&mut remaining, 2)?;
        let server_port = u16::from_be_bytes([port_bytes[0], port_bytes[1]]);

        let intent = Intent::try_from(read_varint(&mut remaining)?)?;

        if !remaining.is_empty() {
            return Err("unexpected trailing handshake bytes");
        }

        Ok(Self {
            protocol_version,
            server_address,
            server_port,
            intent,
        })
    }
}

fn read_varint(input: &mut &[u8]) -> Result<VarInt, &'static str> {
    let (value, consumed) =
        VarInt::decode(input).map_err(|_| "invalid or truncated handshake VarInt")?;

    *input = &input[consumed..];
    Ok(value)
}

fn take<'a>(input: &mut &'a [u8], length: usize) -> Result<&'a [u8], &'static str> {
    if input.len() < length {
        return Err("truncated handshake");
    }

    let (value, remaining) = input.split_at(length);
    *input = remaining;
    Ok(value)
}
