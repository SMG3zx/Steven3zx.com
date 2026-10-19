use serde_json::{Map, Value};
use std::collections::BTreeSet;
use std::sync::OnceLock;

pub type Result<T> = std::result::Result<T, &'static str>;
pub const MAX_BYTES: usize = 65_536;
const MAX_ELEMENTS: usize = 4096;
const MAX_DEPTH: usize = 32;

fn schemas() -> Result<&'static Value> {
    static SCHEMA: OnceLock<Result<Value>> = OnceLock::new();
    SCHEMA
        .get_or_init(|| {
            let value: Value =
                serde_json::from_str(include_str!("../../assets/inventory-schema-26.3.json"))
                    .map_err(|_| "invalid schema asset")?;
            if value["version"] != "26.3"
                || value["protocol"] != 777
                || value["server_sha1"] != "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c"
            {
                return Err("inventory schema provenance mismatch");
            }
            Ok(value)
        })
        .as_ref()
        .map_err(|error| *error)
}
fn component_schema(id: i32) -> Result<&'static Value> {
    let schema = schemas()?;
    let name = component_name(id)?;
    Ok(&schema["types"]["SlotComponent"][1][1]["type"][1]["fields"][name])
}
fn component_name(id: i32) -> Result<&'static str> {
    schemas()?["types"]["SlotComponentType"][1]["mappings"][id.to_string()]
        .as_str()
        .ok_or("missing component ID")
}

pub struct Reader<'a> {
    pub bytes: &'a [u8],
    budget: usize,
}
impl<'a> Reader<'a> {
    pub fn new(bytes: &'a [u8]) -> Result<Self> {
        if bytes.len() > MAX_BYTES {
            return Err("inventory payload too large");
        }
        Ok(Self {
            bytes,
            budget: MAX_ELEMENTS,
        })
    }
    pub fn take(&mut self, count: usize) -> Result<&'a [u8]> {
        if count > self.bytes.len() {
            return Err("truncated inventory field");
        }
        let (value, rest) = self.bytes.split_at(count);
        self.bytes = rest;
        Ok(value)
    }
    pub fn int(&mut self) -> Result<i32> {
        let mut value = 0u32;
        for shift in 0..5 {
            let byte = self.take(1)?[0];
            if shift == 4 && byte & 0xf0 != 0 {
                return Err("invalid inventory VarInt");
            }
            value |= u32::from(byte & 127) << (shift * 7);
            if byte & 128 == 0 {
                return Ok(value as i32);
            }
        }
        Err("invalid inventory VarInt")
    }
    pub fn length(&mut self, limit: usize) -> Result<usize> {
        let count = self.int()?;
        if count < 0 || count as usize > limit {
            return Err("inventory count out of bounds");
        }
        Ok(count as usize)
    }
    pub(crate) fn charge(&mut self, count: usize) -> Result<()> {
        self.budget = self
            .budget
            .checked_sub(count)
            .ok_or("inventory element budget exceeded")?;
        Ok(())
    }
    fn boolean(&mut self) -> Result<bool> {
        match self.take(1)?[0] {
            0 => Ok(false),
            1 => Ok(true),
            _ => Err("invalid inventory boolean"),
        }
    }
    fn nbt_string(&mut self) -> Result<()> {
        let count =
            u16::from_be_bytes(self.take(2)?.try_into().map_err(|_| "NBT string")?) as usize;
        // NBT uses Java modified UTF-8, including encoded NUL and surrogate pairs.
        let bytes = self.take(count)?;
        let mut index = 0;
        while index < bytes.len() {
            let lead = bytes[index];
            index += 1;
            let extra = if lead & 0x80 == 0 {
                if lead == 0 {
                    return Err("invalid modified UTF-8");
                }
                0
            } else if lead & 0xe0 == 0xc0 {
                1
            } else if lead & 0xf0 == 0xe0 {
                2
            } else {
                return Err("invalid modified UTF-8");
            };
            if index + extra > bytes.len()
                || bytes[index..index + extra]
                    .iter()
                    .any(|byte| byte & 0xc0 != 0x80)
            {
                return Err("invalid modified UTF-8");
            }
            if extra == 1 {
                let code = (u16::from(lead & 31) << 6) | u16::from(bytes[index] & 63);
                if code < 128 && !(lead == 0xc0 && bytes[index] == 0x80) {
                    return Err("overlong modified UTF-8");
                }
            } else if extra == 2 {
                let code = (u16::from(lead & 15) << 12)
                    | (u16::from(bytes[index] & 63) << 6)
                    | u16::from(bytes[index + 1] & 63);
                if code < 2048 {
                    return Err("overlong modified UTF-8");
                }
            }
            index += extra;
        }
        Ok(())
    }
    fn nbt_payload(&mut self, tag: u8, depth: usize) -> Result<()> {
        if depth > MAX_DEPTH {
            return Err("inventory NBT depth exceeded");
        }
        self.charge(1)?;
        match tag {
            1 => {
                self.take(1)?;
            }
            2 => {
                self.take(2)?;
            }
            3 | 5 => {
                self.take(4)?;
            }
            4 | 6 => {
                self.take(8)?;
            }
            7 | 11 | 12 => {
                let count = i32::from_be_bytes(self.take(4)?.try_into().map_err(|_| "NBT array")?);
                if count < 0 {
                    return Err("negative NBT array count");
                }
                let width = if tag == 7 {
                    1
                } else if tag == 11 {
                    4
                } else {
                    8
                };
                self.take(
                    (count as usize)
                        .checked_mul(width)
                        .ok_or("NBT array overflow")?,
                )?;
            }
            8 => self.nbt_string()?,
            9 => {
                let kind = self.take(1)?[0];
                let count = i32::from_be_bytes(self.take(4)?.try_into().map_err(|_| "NBT list")?);
                if count < 0
                    || count as usize > self.budget
                    || kind > 12
                    || (kind == 0 && count != 0)
                {
                    return Err("invalid NBT list");
                }
                for _ in 0..count {
                    self.nbt_payload(kind, depth + 1)?;
                }
            }
            10 => loop {
                let kind = self.take(1)?[0];
                if kind == 0 {
                    break;
                }
                self.nbt_string()?;
                self.nbt_payload(kind, depth + 1)?;
            },
            _ => return Err("invalid inventory NBT tag"),
        }
        Ok(())
    }
    fn reference(scopes: &[Map<String, Value>], name: &str) -> Result<Value> {
        scopes
            .iter()
            .rev()
            .find_map(|scope| scope.get(name))
            .cloned()
            .ok_or("missing schema field reference")
    }
    fn decode(
        &mut self,
        schema: &Value,
        scopes: &mut Vec<Map<String, Value>>,
        depth: usize,
    ) -> Result<Value> {
        if depth > MAX_DEPTH {
            return Err("inventory component depth exceeded");
        }
        self.charge(1)?;
        if let Some(name) = schema.as_str() {
            if name == "Slot" {
                read_stack_at(self, false, depth + 1)?;
                return Ok(Value::Null);
            }
            if name == "ItemStackTemplate" {
                let item = self.int()?;
                let count = self.length(99)? as u8;
                if count == 0 {
                    return Err("empty required nested Slot");
                }
                read_stack_body(self, false, depth + 1, item, count)?;
                return Ok(Value::Null);
            }
            let primitive = match name {
                "void" => Some(Value::Null),
                "varint" => Some(Value::from(self.int()?)),
                "bool" => Some(Value::Bool(self.boolean()?)),
                "i32" => Some(Value::from(i32::from_be_bytes(
                    self.take(4)?.try_into().map_err(|_| "integer")?,
                ))),
                "f32" => {
                    let value = f32::from_be_bytes(self.take(4)?.try_into().map_err(|_| "float")?);
                    if !value.is_finite() {
                        return Err("nonfinite inventory float");
                    }
                    Some(Value::from(value))
                }
                "f64" => {
                    let value = f64::from_be_bytes(self.take(8)?.try_into().map_err(|_| "double")?);
                    if !value.is_finite() {
                        return Err("nonfinite inventory double");
                    }
                    Some(Value::from(value))
                }
                "UUID" => {
                    self.take(16)?;
                    Some(Value::Null)
                }
                "anonymousNbt" | "anonOptionalNbt" => {
                    let kind = self.take(1)?[0];
                    if kind != 0 {
                        self.nbt_payload(kind, depth + 1)?;
                    } else if name != "anonOptionalNbt" {
                        return Err("empty required NBT");
                    }
                    Some(Value::Null)
                }
                _ => None,
            };
            if let Some(value) = primitive {
                return Ok(value);
            }
            let definition = &schemas()?["types"][name];
            if definition.is_null() || definition == "native" {
                return Err("unknown inventory schema type");
            }
            return self.decode(definition, scopes, depth + 1);
        }
        let kind = schema[0]
            .as_str()
            .ok_or("invalid inventory schema operator")?;
        let args = &schema[1];
        match kind {
            "container" => {
                scopes.push(Map::new());
                for field in args.as_array().ok_or("invalid container schema")? {
                    let value = self.decode(&field["type"], scopes, depth + 1)?;
                    let scope = scopes.last_mut().ok_or("missing container")?;
                    if field["anon"] == true {
                        if let Value::Object(fields) = value {
                            scope.extend(fields);
                        }
                    } else {
                        scope.insert(
                            field["name"].as_str().ok_or("missing field name")?.into(),
                            value,
                        );
                    }
                }
                Ok(Value::Object(scopes.pop().ok_or("missing container")?))
            }
            "switch" => {
                let compare =
                    Self::reference(scopes, args["compareTo"].as_str().ok_or("invalid switch")?)?;
                let key = compare
                    .as_str()
                    .map(str::to_owned)
                    .unwrap_or_else(|| compare.to_string());
                let definition = args["fields"].get(&key).or_else(|| args.get("default"));
                match definition {
                    Some(definition) => self.decode(definition, scopes, depth + 1),
                    None => Err("invalid inventory switch value"),
                }
            }
            "mapper" => {
                let key = self.decode(&args["type"], scopes, depth + 1)?.to_string();
                args["mappings"]
                    .get(&key)
                    .cloned()
                    .ok_or("invalid inventory enum")
            }
            "option" => {
                if self.boolean()? {
                    self.decode(args, scopes, depth + 1)
                } else {
                    Ok(Value::Null)
                }
            }
            "array" => {
                let count = if args.get("countType").is_some() {
                    self.length(MAX_ELEMENTS)?
                } else if let Some(count) = args["count"].as_u64() {
                    count as usize
                } else {
                    Self::reference(scopes, args["count"].as_str().ok_or("invalid array count")?)?
                        .as_u64()
                        .ok_or("negative array count")? as usize
                };
                if count > self.budget {
                    return Err("inventory array budget exceeded");
                }
                let mut values = Vec::new();
                for _ in 0..count {
                    values.push(self.decode(&args["type"], scopes, depth + 1)?);
                }
                Ok(Value::Array(values))
            }
            "pstring" => {
                let count = self.length(MAX_BYTES)?;
                let value = std::str::from_utf8(self.take(count)?)
                    .map_err(|_| "invalid inventory UTF-8")?;
                if value.encode_utf16().count() > 32767 {
                    return Err("inventory string too long");
                }
                Ok(Value::String(value.to_owned()))
            }
            "buffer" => {
                let count = self.length(MAX_BYTES)?;
                self.take(count)?;
                Ok(Value::Null)
            }
            "registryEntryHolder" => {
                let id = self.int()?;
                if id < 0 {
                    return Err("negative registry holder");
                }
                if id == 0 {
                    self.decode(&args["otherwise"]["type"], scopes, depth + 1)
                } else {
                    Ok(Value::from(id - 1))
                }
            }
            "registryEntryHolderSet" => {
                let count = self.length(MAX_ELEMENTS + 1)?;
                if count == 0 {
                    self.decode(&args["base"]["type"], scopes, depth + 1)
                } else {
                    self.charge(count - 1)?;
                    for _ in 1..count {
                        if self.int()? < 0 {
                            return Err("negative holder ID");
                        }
                    }
                    Ok(Value::Null)
                }
            }
            "bitfield" => {
                self.take(8)?;
                Ok(Value::Null)
            }
            _ => Err("unimplemented inventory schema operator"),
        }
    }
}

pub fn validate_component(id: i32, bytes: &[u8]) -> Result<Value> {
    if !(0..122).contains(&id) {
        return Err("unknown inventory component ID");
    }
    let definition = component_schema(id)?;
    let mut reader = Reader::new(bytes)?;
    let value = reader.decode(definition, &mut Vec::new(), 0)?;
    if !reader.bytes.is_empty() {
        return Err("trailing inventory component bytes");
    }
    Ok(value)
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Stack {
    pub item: i32,
    pub count: u8,
    pub added: Vec<(i32, Vec<u8>)>,
    pub removed: Vec<i32>,
}
impl Stack {
    pub fn empty() -> Self {
        Self {
            item: 0,
            count: 0,
            added: Vec::new(),
            removed: Vec::new(),
        }
    }
    pub fn trusted_bytes(&self) -> Vec<u8> {
        let mut bytes = Vec::new();
        put_int(&mut bytes, i32::from(self.count));
        if self.count == 0 {
            return bytes;
        }
        put_int(&mut bytes, self.item);
        put_int(&mut bytes, self.added.len() as i32);
        put_int(&mut bytes, self.removed.len() as i32);
        for (id, value) in &self.added {
            put_int(&mut bytes, *id);
            bytes.extend(value);
        }
        for id in &self.removed {
            put_int(&mut bytes, *id);
        }
        bytes
    }
    pub fn untrusted_bytes(&self) -> Vec<u8> {
        let mut bytes = Vec::new();
        put_int(&mut bytes, i32::from(self.count));
        if self.count == 0 {
            return bytes;
        }
        put_int(&mut bytes, self.item);
        put_int(&mut bytes, self.added.len() as i32);
        put_int(&mut bytes, self.removed.len() as i32);
        for (id, value) in &self.added {
            put_int(&mut bytes, *id);
            put_int(&mut bytes, value.len() as i32);
            bytes.extend(value);
        }
        for id in &self.removed {
            put_int(&mut bytes, *id);
        }
        bytes
    }
}
pub fn put_int(bytes: &mut Vec<u8>, value: i32) {
    let mut value = value as u32;
    loop {
        let mut byte = (value & 127) as u8;
        value >>= 7;
        if value != 0 {
            byte |= 128;
        }
        bytes.push(byte);
        if value == 0 {
            break;
        }
    }
}

pub fn read_stack(reader: &mut Reader<'_>, delimited: bool) -> Result<Stack> {
    read_stack_at(reader, delimited, 0)
}
fn read_stack_at(reader: &mut Reader<'_>, delimited: bool, depth: usize) -> Result<Stack> {
    if depth > MAX_DEPTH {
        return Err("inventory Slot depth exceeded");
    }
    reader.charge(1)?;
    let count = reader.length(99)? as u8;
    if count == 0 {
        return Ok(Stack::empty());
    }
    let item = reader.int()?;
    read_stack_body(reader, delimited, depth, item, count)
}
fn read_stack_body(
    reader: &mut Reader<'_>,
    delimited: bool,
    depth: usize,
    item: i32,
    count: u8,
) -> Result<Stack> {
    if depth > MAX_DEPTH {
        return Err("inventory Slot depth exceeded");
    }
    if !(1..1658).contains(&item) {
        return Err("unknown inventory item ID");
    }
    let added_count = reader.length(122)?;
    let removed_count = reader.length(122)?;
    if added_count + removed_count > 122 {
        return Err("too many inventory components");
    }
    reader.charge(added_count + removed_count)?;
    let mut ids = BTreeSet::new();
    let mut added = Vec::new();
    let mut removed = Vec::new();
    for _ in 0..added_count {
        let id = reader.int()?;
        if !(0..122).contains(&id) || !ids.insert(id) {
            return Err("duplicate or unknown inventory component");
        }
        let bytes = if delimited {
            let length = reader.length(MAX_BYTES)?;
            let bytes = reader.take(length)?;
            let mut nested = Reader {
                bytes,
                budget: reader.budget,
            };
            nested.decode(component_schema(id)?, &mut Vec::new(), depth + 1)?;
            if !nested.bytes.is_empty() {
                return Err("trailing inventory component bytes");
            }
            reader.budget = nested.budget;
            bytes.to_vec()
        } else {
            let before = reader.bytes;
            reader.decode(component_schema(id)?, &mut Vec::new(), depth + 1)?;
            before[..before.len() - reader.bytes.len()].to_vec()
        };
        added.push((id, bytes));
    }
    for _ in 0..removed_count {
        let id = reader.int()?;
        if !(0..122).contains(&id) || !ids.insert(id) {
            return Err("duplicate or unknown removed component");
        }
        removed.push(id);
    }
    Ok(Stack {
        item,
        count,
        added,
        removed,
    })
}
pub fn decode_stack(bytes: &[u8], delimited: bool) -> Result<Stack> {
    let mut reader = Reader::new(bytes)?;
    let stack = read_stack(&mut reader, delimited)?;
    if !reader.bytes.is_empty() {
        return Err("trailing Slot bytes");
    }
    Ok(stack)
}

#[cfg(test)]
mod tests {
    use super::*;
    fn hex(value: &str) -> Vec<u8> {
        (0..value.len())
            .step_by(2)
            .map(|i| u8::from_str_radix(&value[i..i + 2], 16).unwrap())
            .collect()
    }
    #[test]
    fn official_slot_and_default_component_fixtures() {
        let fixture: Value =
            serde_json::from_str(include_str!("../../assets/inventory-26.3.json")).unwrap();
        let components = fixture["components"].as_array().unwrap();
        let samples = fixture["component_samples"].as_object().unwrap();
        assert_eq!(components.len(), 122);
        assert_eq!(samples.len(), components.len());
        let mut ids = BTreeSet::new();
        for component in components {
            let id = component["id"].as_i64().unwrap() as i32;
            let name = component["name"].as_str().unwrap();
            let name = name.strip_prefix("minecraft:").unwrap();
            assert_eq!(component_name(id).unwrap(), name, "component ID {id}");
            assert!(ids.insert(id), "duplicate component ID {id}");
        }
        assert_eq!(ids, (0..122).collect());
        for (name, sample) in fixture["slots"].as_object().unwrap() {
            let bytes = hex(sample["hex"].as_str().unwrap());
            let stack =
                decode_stack(&bytes, false).unwrap_or_else(|error| panic!("{name}: {error}"));
            assert_eq!(stack.trusted_bytes(), bytes);
            assert_eq!(decode_stack(&stack.untrusted_bytes(), true).unwrap(), stack);
        }
        let mut failures = Vec::new();
        for (name, sample) in fixture["component_samples"].as_object().unwrap() {
            let id = fixture["components"]
                .as_array()
                .unwrap()
                .iter()
                .find(|entry| entry["name"] == *name)
                .unwrap()["id"]
                .as_i64()
                .unwrap() as i32;
            let bytes = hex(sample["hex"].as_str().unwrap());
            if let Err(error) = validate_component(id, &bytes) {
                failures.push(format!("{name}: {error}"));
            }
            for end in 0..bytes.len() {
                assert!(
                    validate_component(id, &bytes[..end]).is_err(),
                    "{name} accepted truncated prefix {end}"
                );
            }
        }
        assert!(failures.is_empty(), "{}", failures.join("\n"));
    }
    #[test]
    fn rejects_invalid_bounds_and_exact_consumption() {
        for bytes in [
            vec![0, 0],
            vec![100],
            vec![255; 6],
            vec![1, 0, 0, 0],
            vec![1, 1, 1, 0, 122],
            vec![1, 1, 0, 1, 122],
        ] {
            assert!(decode_stack(&bytes, true).is_err());
        }
        assert!(validate_component(3, &[0, 1]).is_err());
        assert!(validate_component(122, &[]).is_err());
        assert!(validate_component(21, &[2]).is_err());
    }
    #[test]
    fn nested_slots_reject_counts_ids_duplicates_and_depth() {
        let fixture: Value =
            serde_json::from_str(include_str!("../../assets/inventory-26.3.json")).unwrap();
        let id = fixture["components"]
            .as_array()
            .unwrap()
            .iter()
            .find(|entry| entry["name"] == "minecraft:container")
            .unwrap()["id"]
            .as_i64()
            .unwrap() as i32;
        // Container is a length-prefixed array of optional trusted Slots.
        for slot in [vec![100], vec![1, 0, 0, 0], vec![1, 1, 0, 2, 3, 3]] {
            let mut bytes = vec![1];
            bytes.extend(slot);
            assert!(validate_component(id, &bytes).is_err());
        }
        let mut slot = Stack::empty().trusted_bytes();
        for _ in 0..MAX_DEPTH + 1 {
            let mut component = vec![1];
            component.extend(slot);
            slot = Stack {
                item: 1,
                count: 1,
                added: vec![(id, component)],
                removed: Vec::new(),
            }
            .trusted_bytes();
        }
        assert!(decode_stack(&slot, false).is_err());
        // Delimited top-level payloads share the same element/depth budget.
        let mut component = vec![1];
        component.extend(slot);
        let bytes = Stack {
            item: 1,
            count: 1,
            added: vec![(id, component)],
            removed: Vec::new(),
        }
        .untrusted_bytes();
        assert!(decode_stack(&bytes, true).is_err());
    }
    #[test]
    fn modified_utf8_rejects_overlong_encodings_and_preserves_java_forms() {
        for bytes in [
            vec![0, 2, 0xc0, 0x81],
            vec![0, 3, 0xe0, 0x80, 0x81],
            vec![0, 1, 0],
        ] {
            assert!(Reader::new(&bytes).unwrap().nbt_string().is_err());
        }
        for bytes in [vec![0, 2, 0xc0, 0x80], vec![0, 3, 0xed, 0xa0, 0x80]] {
            assert!(Reader::new(&bytes).unwrap().nbt_string().is_ok());
        }
    }
    #[test]
    fn nested_nbt_rejects_invalid_tags_lengths_and_element_overflow() {
        for (tag, bytes) in [
            (7, vec![0xff, 0xff, 0xff, 0xff]),    // negative byte-array length
            (11, vec![0xff, 0xff, 0xff, 0xff]),   // negative int-array length
            (12, vec![0xff, 0xff, 0xff, 0xff]),   // negative long-array length
            (9, vec![1, 0xff, 0xff, 0xff, 0xff]), // negative list length
            (9, vec![0, 0, 0, 0, 1]),             // nonempty list of TAG_End
            (13, Vec::new()),                     // unknown tag
        ] {
            let mut reader = Reader::new(&bytes).unwrap();
            assert!(reader.nbt_payload(tag, 0).is_err(), "accepted tag {tag}");
        }

        let mut oversized_array = Reader::new(&[0, 1, 0, 1]).unwrap();
        oversized_array.budget = 0;
        assert!(oversized_array.nbt_payload(7, 0).is_err());

        let mut invalid_compound_child = Reader::new(&[13, 0, 0, 0]).unwrap();
        assert!(invalid_compound_child.nbt_payload(10, 0).is_err());

        let mut invalid_compound_name = Reader::new(&[1, 0, 1, 0]).unwrap();
        assert!(invalid_compound_name.nbt_payload(10, 0).is_err());
    }
}
