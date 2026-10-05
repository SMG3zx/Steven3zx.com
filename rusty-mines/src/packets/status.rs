use crate::data_type::VarInt;
use crate::packets::write_frame;
use std::io::{self, Write};

pub(crate) fn validate_request(data: &[u8]) -> io::Result<()> {
    if !data.is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "status request must have no fields",
        ));
    }
    Ok(())
}

pub(crate) fn write_response(writer: &mut impl Write) -> io::Result<()> {
    let json = serde_json::json!({
        "version": {"name": "Rusty Mines 26.3 (offline; static world)", "protocol": 777},
        "players": {"max": 0, "online": 0, "sample": []},
        "description": {"text": "Rusty Mines"}
    })
    .to_string();
    let mut data = VarInt::new(json.len() as i32).encode();
    data.extend_from_slice(json.as_bytes());
    write_frame(writer, 0x00, &data)
}

pub(crate) fn write_pong(writer: &mut impl Write, data: &[u8]) -> io::Result<()> {
    if data.len() != 8 {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "ping must contain exactly eight bytes",
        ));
    }
    write_frame(writer, 0x01, data)
}

#[cfg(test)]
pub(crate) use tests::assert_response;

#[cfg(test)]
mod tests {
    use super::*;
    use crate::packets::parse_packet;

    pub(crate) fn assert_response(bytes: &[u8]) -> usize {
        let (packet, consumed) = parse_packet(bytes, None, false).unwrap().unwrap();
        assert_eq!(packet.id, 0);
        let (length, prefix) = VarInt::decode(&packet.data).unwrap();
        assert_eq!(length.value() as usize, packet.data.len() - prefix);
        let json: serde_json::Value = serde_json::from_slice(&packet.data[prefix..]).unwrap();
        assert_eq!(
            json,
            serde_json::json!({
                "version": {"name": "Rusty Mines 26.3 (offline; static world)", "protocol": 777},
                "players": {"max": 0, "online": 0, "sample": []},
                "description": {"text": "Rusty Mines"}
            })
        );
        consumed
    }

    #[test]
    fn request_has_no_fields() {
        assert!(validate_request(&[]).is_ok());
        assert!(validate_request(&[0]).is_err());
    }

    #[test]
    fn response_has_framed_minecraft_string() {
        let mut output = Vec::new();
        write_response(&mut output).unwrap();
        assert_eq!(assert_response(&output), output.len());
    }

    #[test]
    fn pong_preserves_all_bits_and_rejects_wrong_lengths() {
        let payload = [0xff, 0x80, 0, 1, 2, 3, 4, 5];
        let mut output = Vec::new();
        write_pong(&mut output, &payload).unwrap();
        assert_eq!(output, [vec![9, 1], payload.to_vec()].concat());
        for length in [0, 7, 9] {
            let mut output = Vec::new();
            assert!(write_pong(&mut output, &vec![0; length]).is_err());
            assert!(output.is_empty());
        }
    }
}
