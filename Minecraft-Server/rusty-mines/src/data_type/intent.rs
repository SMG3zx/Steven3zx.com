use super::VarInt;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Intent {
    Status,
    Login,
    Transfer,
}

impl TryFrom<VarInt> for Intent {
    type Error = &'static str;

    fn try_from(value: VarInt) -> Result<Self, Self::Error> {
        match value.value() {
            1 => Ok(Self::Status),
            2 => Ok(Self::Login),
            3 => Ok(Self::Transfer),
            _ => Err("invalid handshake intent"),
        }
    }
}
