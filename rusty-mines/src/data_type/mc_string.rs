#[derive(Clone, Debug, Eq, PartialEq)]
pub struct MCString<const N: usize>(String);

impl<const N: usize> MCString<N> {
    pub fn try_new(value: String) -> Result<Self, &'static str> {
        if N > 32_767 {
            return Err("Minecraft string limit exceeds 32767");
        }

        if value.len() > N * 3 {
            return Err("Minecraft string exceeds its byte limit");
        }

        if value.encode_utf16().count() > N {
            return Err("Minecraft string exceeds its UTF-16 limit");
        }

        Ok(Self(value))
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }
}
