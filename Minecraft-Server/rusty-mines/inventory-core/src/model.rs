use crate::wire::{decode_stack, put_int, read_stack, Reader, Result, Stack};
use serde_json::Value;
use std::{collections::BTreeSet, sync::OnceLock};

pub const PLAYER_SLOTS: usize = 46;
pub const STORAGE_SLOTS: usize = 27;

fn registry() -> Result<&'static Value> {
    static DATA: OnceLock<Result<Value>> = OnceLock::new();
    DATA.get_or_init(|| {
        serde_json::from_str(include_str!("../../assets/inventory-26.3.json"))
            .map_err(|_| "invalid item metadata")
    })
    .as_ref()
    .map_err(|error| *error)
}
pub fn item_id(name: &str) -> Result<i32> {
    registry()?["items"]
        .as_array()
        .ok_or("invalid item registry")?
        .iter()
        .find(|entry| entry["name"] == name)
        .and_then(|entry| entry["id"].as_i64())
        .map(|id| id as i32)
        .ok_or("unknown item name")
}
pub fn maximum(item: i32) -> Result<u8> {
    let entry = registry()?["items"]
        .as_array()
        .ok_or("invalid item registry")?
        .iter()
        .find(|entry| entry["id"] == item)
        .ok_or("unknown item ID")?;
    let count = entry["defaults"]["minecraft:max_stack_size"]
        .as_u64()
        .ok_or("missing stack-size default")?;
    u8::try_from(count).map_err(|_| "invalid stack-size default")
}
pub fn permitted(stack: &Stack) -> Result<()> {
    if stack.count == 0 {
        return if *stack == Stack::empty() {
            Ok(())
        } else {
            Err("noncanonical empty stack")
        };
    }
    // Full wire coverage is separate from the intentionally small gameplay item
    // policy. Component effects, arbitrary containers and entity drops are absent.
    if !stack.added.is_empty() || !stack.removed.is_empty() {
        return Err("component modifications are not permitted by inventory policy");
    }
    let names = [
        "minecraft:stone",
        "minecraft:oak_log",
        "minecraft:oak_planks",
        "minecraft:stick",
        "minecraft:crafting_table",
        "minecraft:diamond_sword",
    ];
    if !names
        .iter()
        .any(|name| item_id(name).ok() == Some(stack.item))
    {
        return Err("item is outside inventory gameplay policy");
    }
    if stack.count > maximum(stack.item)? {
        return Err("stack exceeds item maximum");
    }
    Ok(())
}
pub fn encode_slots(slots: &[Stack]) -> Vec<u8> {
    let mut bytes = Vec::new();
    put_int(&mut bytes, slots.len() as i32);
    for slot in slots {
        bytes.extend(slot.trusted_bytes());
    }
    bytes
}
pub fn decode_slots(bytes: &[u8], expected: usize) -> Result<Vec<Stack>> {
    let mut reader = Reader::new(bytes)?;
    if reader.length(expected)? != expected {
        return Err("incorrect inventory slot count");
    }
    let mut slots = Vec::new();
    for _ in 0..expected {
        let stack = read_stack(&mut reader, false)?;
        permitted(&stack)?;
        slots.push(stack);
    }
    if !reader.bytes.is_empty() {
        return Err("trailing inventory snapshot bytes");
    }
    Ok(slots)
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct State {
    pub slots: Vec<Stack>,
    pub cursor: Stack,
    pub selected: u8,
    pub revision: i32,
    pub drag_kind: i8,
    pub drag_slots: Vec<u8>,
}
impl Default for State {
    fn default() -> Self {
        Self {
            slots: vec![Stack::empty(); PLAYER_SLOTS],
            cursor: Stack::empty(),
            selected: 0,
            revision: 0,
            drag_kind: -1,
            drag_slots: Vec::new(),
        }
    }
}
impl State {
    fn validate(&self) -> Result<()> {
        if self.slots.len() != PLAYER_SLOTS || self.selected > 8 || self.revision < 0 {
            return Err("invalid inventory state");
        }
        for slot in &self.slots {
            permitted(slot)?;
        }
        if !(-1..=1).contains(&self.drag_kind)
            || self.drag_slots.len() > STORAGE_SLOTS + 36
            || self
                .drag_slots
                .iter()
                .copied()
                .collect::<BTreeSet<_>>()
                .len()
                != self.drag_slots.len()
            || (self.drag_kind == -1 && !self.drag_slots.is_empty())
        {
            return Err("invalid inventory drag state");
        }
        permitted(&self.cursor)
    }
    fn advance(&mut self) -> Result<()> {
        self.revision = self
            .revision
            .checked_add(1)
            .ok_or("inventory revision exhausted")?;
        Ok(())
    }
    pub fn select(&mut self, selected: u8) -> Result<()> {
        if selected > 8 {
            return Err("invalid held slot");
        }
        self.selected = selected;
        Ok(())
    }
    pub fn creative(
        &mut self,
        slot: i16,
        stack: Stack,
        expected: i32,
        allowed: bool,
    ) -> Result<()> {
        self.validate()?;
        if !allowed {
            return Err("Creative inventory permission required");
        }
        if expected != self.revision {
            return Err("stale inventory revision");
        }
        if !(9..=45).contains(&slot) {
            return Err("Creative slot is outside supported inventory");
        }
        permitted(&stack)?;
        let mut next = self.clone();
        next.slots[slot as usize] = stack;
        next.drag_kind = -1;
        next.drag_slots.clear();
        next.advance()?;
        *self = next;
        Ok(())
    }
    pub fn click(
        &mut self,
        storage: Option<&mut Vec<Stack>>,
        slot: i16,
        button: i8,
        mode: i32,
        expected: i32,
        creative_allowed: bool,
    ) -> Result<()> {
        self.validate()?;
        if let Some(slots) = storage.as_ref() {
            for stack in slots.iter() {
                permitted(stack)?;
            }
        }
        if expected != self.revision {
            return Err("stale inventory revision");
        }
        if mode == 4 || (slot == -999 && mode != 5) {
            return Err("item drops are disabled");
        }
        if self.slots.len() != PLAYER_SLOTS
            || storage
                .as_ref()
                .is_some_and(|slots| slots.len() != STORAGE_SLOTS)
        {
            return Err("invalid inventory dimensions");
        }
        let mut next = self.clone();
        let mut store = storage.as_deref().cloned();
        let count = if store.is_some() {
            STORAGE_SLOTS + 36
        } else {
            PLAYER_SLOTS
        };
        let slot = if slot == -999 && mode == 5 && (button & 3 == 0 || button & 3 == 2) {
            9
        } else {
            if slot < 0 || slot as usize >= count {
                return Err("invalid inventory click slot");
            }
            slot as usize
        };
        if store.is_none() && (5..=8).contains(&slot) {
            return Err("armor menu actions are unsupported");
        }
        if store.is_none() && slot == 0 {
            if mode != 0 || !(0..=1).contains(&button) {
                return Err("unsupported crafting-output click");
            }
            next.craft()?;
        } else {
            let mut menu = if let Some(ref store) = store {
                let mut slots = store.clone();
                slots.extend(next.slots[9..45].iter().cloned());
                slots
            } else {
                next.slots.clone()
            };
            match mode {
                0 => {
                    if !(0..=1).contains(&button) {
                        return Err("invalid pickup button");
                    }
                    let target = &mut menu[slot];
                    if next.cursor.count == 0 {
                        let amount = if button == 0 {
                            target.count
                        } else {
                            target.count.div_ceil(2)
                        };
                        next.cursor = take(target, amount);
                    } else if target.count == 0 || same(target, &next.cursor) {
                        let amount = if button == 0 { next.cursor.count } else { 1 };
                        transfer(&mut next.cursor, target, amount)?;
                    } else {
                        std::mem::swap(target, &mut next.cursor);
                    }
                }
                1 => {
                    if !(0..=1).contains(&button) {
                        return Err("invalid shift-click button");
                    }
                    let targets: Vec<usize> = if store.is_some() {
                        if slot < STORAGE_SLOTS {
                            (STORAGE_SLOTS..count).collect()
                        } else {
                            (0..STORAGE_SLOTS).collect()
                        }
                    } else if (9..36).contains(&slot) {
                        (36..45).collect()
                    } else {
                        (9..36).collect()
                    };
                    distribute(&mut menu, slot, &targets)?;
                }
                2 => {
                    if !(0..=8).contains(&button) && button != 40 {
                        return Err("invalid hotbar swap");
                    }
                    let hotbar = if button == 40 {
                        if store.is_some() {
                            return Err("storage offhand swap is unsupported");
                        }
                        45
                    } else if store.is_some() {
                        STORAGE_SLOTS + 27 + button as usize
                    } else {
                        36 + button as usize
                    };
                    menu.swap(slot, hotbar);
                }
                3 => {
                    if !creative_allowed || button != 2 {
                        return Err("Creative clone permission required");
                    }
                    next.cursor = menu[slot].clone();
                    if next.cursor.count != 0 {
                        next.cursor.count = maximum(next.cursor.item)?;
                    }
                }
                5 => {
                    let kind = button >> 2;
                    let stage = button & 3;
                    if !(0..=1).contains(&kind) {
                        return Err("unsupported drag kind");
                    }
                    match stage {
                        0 => {
                            next.drag_kind = kind;
                            next.drag_slots.clear();
                        }
                        1 => {
                            if next.drag_kind != kind {
                                return Err("drag phase mismatch");
                            }
                            if next.drag_slots.len() >= count {
                                return Err("too many drag slots");
                            }
                            if !next.drag_slots.contains(&(slot as u8)) {
                                next.drag_slots.push(slot as u8);
                            }
                        }
                        2 => {
                            if next.drag_kind != kind || next.drag_slots.is_empty() {
                                return Err("drag phase mismatch");
                            }
                            if next.drag_slots.iter().any(|index| {
                                *index as usize >= count
                                    || (store.is_none() && (*index == 0 || (5..=8).contains(index)))
                            }) {
                                return Err("drag slots do not belong to this menu");
                            }
                            let amount = if kind == 1 {
                                1
                            } else {
                                next.cursor.count / next.drag_slots.len() as u8
                            };
                            for index in &next.drag_slots {
                                let target = &mut menu[*index as usize];
                                if target.count == 0 || same(target, &next.cursor) {
                                    transfer(&mut next.cursor, target, amount)?;
                                }
                            }
                            next.drag_kind = -1;
                            next.drag_slots.clear();
                        }
                        _ => return Err("invalid drag stage"),
                    }
                }
                6 => {
                    if button != 0 || next.cursor.count == 0 {
                        return Err("invalid collect click");
                    }
                    for (index, target) in menu.iter_mut().enumerate() {
                        if store.is_none() && index < 9 {
                            continue;
                        }
                        if same(target, &next.cursor) {
                            let amount = target.count;
                            transfer(target, &mut next.cursor, amount)?;
                        }
                    }
                }
                _ => return Err("unsupported inventory click mode"),
            }
            if let Some(ref mut store) = store {
                store.clone_from_slice(&menu[..STORAGE_SLOTS]);
                next.slots[9..45].clone_from_slice(&menu[STORAGE_SLOTS..]);
            } else {
                next.slots = menu;
            }
        }
        if mode != 5 {
            next.drag_kind = -1;
            next.drag_slots.clear();
        }
        next.refresh_crafting()?;
        next.advance()?;
        if let (Some(destination), Some(store)) = (storage, store) {
            *destination = store;
        }
        *self = next;
        Ok(())
    }
    pub fn reconcile(&mut self) -> Result<()> {
        self.validate()?;
        let mut next = self.clone();
        for index in 9..45 {
            let amount = next.cursor.count;
            transfer(&mut next.cursor, &mut next.slots[index], amount)?;
        }
        if next.cursor.count != 0 {
            return Err("inventory full; cursor retained durably");
        }
        next.drag_kind = -1;
        next.drag_slots.clear();
        next.refresh_crafting()?;
        next.advance()?;
        *self = next;
        Ok(())
    }
    fn recipe(&self) -> Result<Option<(Stack, Vec<usize>)>> {
        self.validate()?;
        let log = item_id("minecraft:oak_log")?;
        let planks = item_id("minecraft:oak_planks")?;
        let grid = &self.slots[1..5];
        let occupied: Vec<usize> = grid
            .iter()
            .enumerate()
            .filter(|(_, slot)| slot.count != 0)
            .map(|(i, _)| i + 1)
            .collect();
        let result = if occupied.len() == 1 && grid[occupied[0] - 1].item == log {
            Some((planks, 4, occupied))
        } else if occupied.len() == 4 && grid.iter().all(|slot| slot.item == planks) {
            Some((item_id("minecraft:crafting_table")?, 1, occupied))
        } else if (occupied == vec![1, 3] || occupied == vec![2, 4])
            && occupied
                .iter()
                .all(|index| self.slots[*index].item == planks)
        {
            Some((item_id("minecraft:stick")?, 4, occupied))
        } else {
            None
        };
        Ok(result.map(|(item, count, inputs)| {
            (
                Stack {
                    item,
                    count,
                    added: Vec::new(),
                    removed: Vec::new(),
                },
                inputs,
            )
        }))
    }
    pub fn refresh_crafting(&mut self) -> Result<()> {
        self.slots[0] = self
            .recipe()?
            .map(|(output, _)| output)
            .unwrap_or_else(Stack::empty);
        Ok(())
    }
    pub fn craft(&mut self) -> Result<()> {
        let (mut output, inputs) = self.recipe()?.ok_or("no supported recipe matches")?;
        if self.cursor.count != 0
            && (!same(&self.cursor, &output)
                || u16::from(self.cursor.count) + u16::from(output.count)
                    > u16::from(maximum(output.item)?))
        {
            return Err("crafting cursor cannot accept output");
        }
        let amount = output.count;
        transfer(&mut output, &mut self.cursor, amount)?;
        for index in inputs {
            take(&mut self.slots[index], 1);
        }
        self.refresh_crafting()
    }
}
fn same(a: &Stack, b: &Stack) -> bool {
    a.count != 0 && b.count != 0 && a.item == b.item && a.added == b.added && a.removed == b.removed
}
fn take(stack: &mut Stack, amount: u8) -> Stack {
    let amount = amount.min(stack.count);
    if amount == 0 {
        return Stack::empty();
    }
    let mut result = stack.clone();
    result.count = amount;
    stack.count -= amount;
    if stack.count == 0 {
        *stack = Stack::empty();
    }
    result
}
fn transfer(source: &mut Stack, target: &mut Stack, amount: u8) -> Result<()> {
    if source.count == 0 || (target.count != 0 && !same(source, target)) {
        return Ok(());
    }
    let space = maximum(source.item)?.saturating_sub(target.count);
    let amount = amount.min(space).min(source.count);
    let moved = take(source, amount);
    if target.count == 0 {
        *target = moved;
    } else {
        target.count += moved.count;
    }
    Ok(())
}
fn distribute(menu: &mut [Stack], source: usize, targets: &[usize]) -> Result<()> {
    let mut stack = std::mem::replace(&mut menu[source], Stack::empty());
    for empty in [false, true] {
        for target in targets {
            if *target != source && (menu[*target].count == 0) == empty {
                let amount = stack.count;
                transfer(&mut stack, &mut menu[*target], amount)?;
            }
        }
    }
    menu[source] = stack;
    Ok(())
}

#[derive(Debug, PartialEq, Eq)]
pub struct HashedStack {
    pub item: i32,
    pub count: u8,
    pub added: Vec<(i32, i32)>,
    pub removed: Vec<i32>,
}
pub fn read_hashed(reader: &mut Reader<'_>) -> Result<Option<HashedStack>> {
    reader.charge(1)?;
    match reader.take(1)?[0] {
        0 => return Ok(None),
        1 => {}
        _ => return Err("invalid hashed Slot option"),
    }
    let item = reader.int()?;
    if !(1..1658).contains(&item) {
        return Err("unknown hashed item");
    }
    let count = reader.length(99)? as u8;
    if count == 0 {
        return Err("nonempty hashed Slot has zero count");
    }
    let added_count = reader.length(122)?;
    reader.charge(added_count)?;
    let mut ids = BTreeSet::new();
    let mut added = Vec::new();
    for _ in 0..added_count {
        let id = reader.int()?;
        if !(0..122).contains(&id) || !ids.insert(id) {
            return Err("invalid hashed component");
        }
        let hash = i32::from_be_bytes(reader.take(4)?.try_into().map_err(|_| "hash")?);
        added.push((id, hash));
    }
    let removed_count = reader.length(122)?;
    reader.charge(removed_count)?;
    let mut removed = Vec::new();
    for _ in 0..removed_count {
        let id = reader.int()?;
        if !(0..122).contains(&id) || !ids.insert(id) {
            return Err("invalid removed hashed component");
        }
        removed.push(id);
    }
    Ok(Some(HashedStack {
        item,
        count,
        added,
        removed,
    }))
}
pub fn hash_matches_default(evidence: &Option<HashedStack>, stack: &Stack) -> bool {
    // HashedStack hashes the explicit component PATCH, never inherited defaults.
    // Gameplay policy admits default stacks only, so its patch maps are empty.
    match evidence {
        None => stack.count == 0,
        Some(value) => {
            value.item == stack.item
                && value.count == stack.count
                && value.added.is_empty()
                && value.removed.is_empty()
                && stack.added.is_empty()
                && stack.removed.is_empty()
        }
    }
}
pub fn validated_default(bytes: &[u8]) -> Result<Stack> {
    let stack = decode_stack(bytes, false)?;
    permitted(&stack)?;
    Ok(stack)
}

#[cfg(test)]
mod tests {
    use super::*;
    fn stack(name: &str, count: u8) -> Stack {
        Stack {
            item: item_id(name).unwrap(),
            count,
            added: Vec::new(),
            removed: Vec::new(),
        }
    }
    fn hashed_payload(item: i32, count: i32, added: &[(i32, i32)], removed: &[i32]) -> Vec<u8> {
        let mut bytes = vec![1];
        put_int(&mut bytes, item);
        put_int(&mut bytes, count);
        put_int(&mut bytes, added.len() as i32);
        for (id, hash) in added {
            put_int(&mut bytes, *id);
            bytes.extend(hash.to_be_bytes());
        }
        put_int(&mut bytes, removed.len() as i32);
        for id in removed {
            put_int(&mut bytes, *id);
        }
        bytes
    }
    #[test]
    fn hashed_slot_validation_and_default_patch_semantics() {
        let mut empty = Reader::new(&[0]).unwrap();
        assert_eq!(read_hashed(&mut empty).unwrap(), None);
        assert!(empty.bytes.is_empty());

        let bytes = hashed_payload(1, 1, &[], &[]);
        let mut reader = Reader::new(&bytes).unwrap();
        let evidence = read_hashed(&mut reader).unwrap();
        assert!(reader.bytes.is_empty());
        let default = stack("minecraft:stone", 1);
        assert!(hash_matches_default(&evidence, &default));
        assert!(hash_matches_default(&None, &Stack::empty()));
        assert!(!hash_matches_default(&None, &default));
        assert!(!hash_matches_default(&evidence, &Stack::empty()));

        let mut wrong_count = default.clone();
        wrong_count.count = 2;
        assert!(!hash_matches_default(&evidence, &wrong_count));
        let patched = HashedStack {
            item: default.item,
            count: default.count,
            added: vec![(3, 0x1234_5678)],
            removed: Vec::new(),
        };
        assert!(!hash_matches_default(&Some(patched), &default));
        let mut patched_stack = default.clone();
        patched_stack.removed.push(3);
        assert!(!hash_matches_default(&evidence, &patched_stack));
    }
    #[test]
    fn hashed_slot_rejects_malformed_patch_fields() {
        let mut truncated_hash = vec![1, 1, 1, 1, 0];
        truncated_hash.extend([0, 0]);
        let duplicate_added = hashed_payload(1, 1, &[(2, 1), (2, 2)], &[]);
        let duplicate_across_patch = hashed_payload(1, 1, &[(2, 1)], &[2]);
        let invalid_added_id = hashed_payload(1, 1, &[(122, 1)], &[]);
        let invalid_removed_id = hashed_payload(1, 1, &[], &[122]);
        let zero_count = hashed_payload(1, 0, &[], &[]);
        let invalid_item = hashed_payload(0, 1, &[], &[]);
        for bytes in [
            vec![2],
            invalid_item,
            zero_count,
            invalid_added_id,
            invalid_removed_id,
            duplicate_added,
            duplicate_across_patch,
            truncated_hash,
        ] {
            let mut reader = Reader::new(&bytes).unwrap();
            assert!(read_hashed(&mut reader).is_err(), "accepted {bytes:?}");
        }
    }
    #[test]
    fn hashed_slots_share_the_packet_element_budget() {
        let one = hashed_payload(1, 1, &(0..122).map(|id| (id, id)).collect::<Vec<_>>(), &[]);
        let mut bytes = Vec::new();
        for _ in 0..34 {
            bytes.extend_from_slice(&one);
        }
        let mut reader = Reader::new(&bytes).unwrap();
        let mut decoded = 0;
        while !reader.bytes.is_empty() {
            if read_hashed(&mut reader).is_err() {
                break;
            }
            decoded += 1;
        }
        assert!(decoded < 34, "component budget was not enforced");
        assert!(!reader.bytes.is_empty());
    }
    #[test]
    fn pickup_shift_swap_and_collect_preserve_items() {
        let mut state = State::default();
        state
            .creative(9, stack("minecraft:stone", 32), 0, true)
            .unwrap();
        state.click(None, 9, 1, 0, 1, false).unwrap();
        assert_eq!(state.cursor.count, 16);
        assert_eq!(state.slots[9].count, 16);
        state.click(None, 10, 0, 0, 2, false).unwrap();
        assert_eq!(state.slots[10].count, 16);
        state.click(None, 10, 0, 1, 3, false).unwrap();
        assert_eq!(state.slots[36].count, 16);
        assert_eq!(state.slots.iter().map(|s| s.count as u32).sum::<u32>(), 32);
    }
    #[test]
    fn crafting_consumes_once_and_stale_or_denied_writes_are_atomic() {
        let mut state = State::default();
        state.slots[1] = stack("minecraft:oak_log", 2);
        state.refresh_crafting().unwrap();
        state.click(None, 0, 0, 0, 0, false).unwrap();
        assert_eq!(state.cursor, stack("minecraft:oak_planks", 4));
        assert_eq!(state.slots[1].count, 1);
        let before = state.clone();
        assert!(state.click(None, 0, 0, 0, 0, false).is_err());
        assert_eq!(state, before);
        assert!(state
            .creative(36, stack("minecraft:stone", 1), 1, false)
            .is_err());
        assert_eq!(state, before);
        assert!(state.click(None, 1, 0, 4, 1, true).is_err());
        assert_eq!(state, before);
    }
    #[test]
    fn storage_transfer_and_cursor_reconciliation() {
        let mut state = State::default();
        let mut storage = vec![Stack::empty(); 27];
        storage[0] = stack("minecraft:stone", 64);
        state.click(Some(&mut storage), 0, 0, 1, 0, false).unwrap();
        assert_eq!(storage[0].count, 0);
        assert_eq!(state.slots[9].count, 64);
        state.click(None, 9, 0, 0, 1, false).unwrap();
        state.reconcile().unwrap();
        assert_eq!(state.cursor.count, 0);
        assert_eq!(state.slots[9].count, 64);
    }
    #[test]
    fn drag_and_encoded_snapshot_are_bounded() {
        let mut state = State {
            cursor: stack("minecraft:stone", 10),
            ..State::default()
        };
        for (slot, button, revision) in [(9, 0, 0), (9, 1, 1), (10, 1, 2), (10, 2, 3)] {
            state.click(None, slot, button, 5, revision, false).unwrap();
        }
        assert_eq!(state.slots[9].count, 5);
        assert_eq!(state.slots[10].count, 5);
        assert_eq!(state.cursor.count, 0);
        assert_eq!(
            decode_slots(&encode_slots(&state.slots), 46).unwrap(),
            state.slots
        );
    }
    #[test]
    fn revision_exhaustion_and_full_cursor_close_are_atomic() {
        let mut state = State {
            revision: i32::MAX,
            ..State::default()
        };
        let mut storage = vec![Stack::empty(); STORAGE_SLOTS];
        storage[0] = stack("minecraft:stone", 64);
        let before = state.clone();
        let store_before = storage.clone();
        assert!(state
            .click(Some(&mut storage), 0, 0, 1, i32::MAX, false)
            .is_err());
        assert_eq!(state, before);
        assert_eq!(storage, store_before);
        assert!(state
            .creative(9, stack("minecraft:stone", 1), i32::MAX, true)
            .is_err());
        assert_eq!(state, before);
        state = State::default();
        for index in 9..45 {
            state.slots[index] = stack("minecraft:stone", 64);
        }
        state.cursor = stack("minecraft:stone", 3);
        let before = state.clone();
        assert!(state.reconcile().is_err());
        assert_eq!(state, before);
    }
    #[test]
    fn invalid_dimensions_and_cross_menu_drag_are_rejected() {
        let mut state = State::default();
        state.slots.clear();
        assert!(state
            .creative(9, stack("minecraft:stone", 1), 0, true)
            .is_err());
        assert!(state.reconcile().is_err());
        assert!(state.refresh_crafting().is_err());
        state = State::default();
        state.cursor = stack("minecraft:stone", 12);
        let mut storage = vec![Stack::empty(); STORAGE_SLOTS];
        state
            .click(Some(&mut storage), -999, 0, 5, 0, false)
            .unwrap();
        state.click(Some(&mut storage), 62, 1, 5, 1, false).unwrap();
        let before = state.clone();
        assert!(state.click(None, -999, 2, 5, 2, false).is_err());
        assert_eq!(state, before);
    }
    #[test]
    fn bounded_action_sequences_conserve_default_items() {
        let mut state = State::default();
        let mut storage = vec![Stack::empty(); STORAGE_SLOTS];
        state.slots[9] = stack("minecraft:stone", 64);
        storage[0] = stack("minecraft:stone", 17);
        let total = |state: &State, storage: &[Stack]| -> u32 {
            state
                .slots
                .iter()
                .chain(storage)
                .map(|slot| u32::from(slot.count))
                .sum::<u32>()
                + u32::from(state.cursor.count)
        };
        let mut seed = 123456789u32;
        for _ in 0..1024 {
            seed = seed.wrapping_mul(1664525).wrapping_add(1013904223);
            let mode = [0, 1, 2, 5, 6][(seed as usize >> 16) % 5];
            let button = if mode == 2 {
                (seed % 9) as i8
            } else if mode == 5 {
                (seed % 7) as i8
            } else {
                (seed % 2) as i8
            };
            let before = state.clone();
            let storage_before = storage.clone();
            let revision = state.revision;
            if state
                .click(
                    Some(&mut storage),
                    (seed % 63) as i16,
                    button,
                    mode,
                    revision,
                    false,
                )
                .is_err()
            {
                assert_eq!(state, before);
                assert_eq!(storage, storage_before);
            }
            assert_eq!(total(&state, &storage), 81);
            assert!(state
                .slots
                .iter()
                .chain(&storage)
                .chain(std::iter::once(&state.cursor))
                .all(|slot| permitted(slot).is_ok()));
        }
    }
}
