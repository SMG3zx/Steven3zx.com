//! Private bounded inventories and connection-owned transactional menus.
use crate::foundation::{self, gateway__view, player_session, player_session__view};
use crate::policy::SessionPhase;
use rusty_mines_inventory::{
    model::{self, State},
    wire::{self, Stack},
};
use spacetimedb::{ConnectionId, ReducerContext, SpacetimeType, Table, Uuid, ViewContext};

#[spacetimedb::table(accessor = player_inventory)]
pub struct PlayerInventory {
    #[primary_key]
    pub player_uuid: Uuid,
    pub slots: Vec<u8>,
    pub cursor: Vec<u8>,
    pub selected: u8,
    pub revision: i32,
    pub drag_kind: i8,
    pub drag_slots: Vec<u8>,
}
#[spacetimedb::table(accessor = inventory_permission)]
pub struct InventoryPermission {
    #[primary_key]
    pub player_uuid: Uuid,
    pub creative: bool,
}
#[spacetimedb::table(accessor = inventory_menu)]
pub struct InventoryMenu {
    #[primary_key]
    pub session_uuid: Uuid,
    pub menu_id: i32,
    pub next_menu_id: i32,
    pub storage_uuid: Option<Uuid>,
    #[index(btree)]
    pub storage_key: String,
    pub sequence: u64,
    pub last_request: Vec<u8>,
    pub result_revision: i32,
}
#[spacetimedb::table(accessor = simple_storage)]
pub struct SimpleStorage {
    #[primary_key]
    pub storage_uuid: Uuid,
    pub title: String,
    pub slots: Vec<u8>,
    pub revision: i32,
}
#[spacetimedb::table(accessor = storage_access, index(accessor = by_player, btree(columns = [player_uuid])))]
pub struct StorageAccess {
    #[primary_key]
    pub key: String,
    pub player_uuid: Uuid,
    pub storage_uuid: Uuid,
}

#[derive(SpacetimeType)]
pub struct InventorySnapshot {
    pub session_uuid: Uuid,
    pub owner_connection: ConnectionId,
    pub revision: i32,
    pub selected: u8,
    pub slots: Vec<u8>,
    pub cursor: Vec<u8>,
    pub menu_id: i32,
    pub menu_title: String,
    pub sequence: u64,
    pub creative_allowed: bool,
}
fn access_key(player: Uuid, storage: Uuid) -> String {
    format!("{player}:{storage}")
}
fn owned(
    ctx: &ReducerContext,
    session_uuid: Uuid,
    play: bool,
) -> Result<foundation::PlayerSession, String> {
    let connection = foundation::require_gateway(ctx)?;
    let session = ctx
        .db
        .player_session()
        .session_uuid()
        .find(session_uuid)
        .ok_or("Session not found")?;
    if !foundation::owns_session(&session, ctx.sender(), connection) {
        return Err("Inventory session belongs to another connection".into());
    }
    foundation::require_session_lease(ctx, &session)?;
    if (play && session.phase != SessionPhase::Play)
        || (!play && session.phase == SessionPhase::Login)
    {
        return Err("Inventory phase is not ready".into());
    }
    Ok(session)
}
fn load(row: &PlayerInventory) -> Result<State, String> {
    Ok(State {
        slots: model::decode_slots(&row.slots, 46).map_err(str::to_owned)?,
        cursor: wire::decode_stack(&row.cursor, false).map_err(str::to_owned)?,
        selected: row.selected,
        revision: row.revision,
        drag_kind: row.drag_kind,
        drag_slots: row.drag_slots.clone(),
    })
}
fn save(ctx: &ReducerContext, player: Uuid, state: &State) {
    ctx.db
        .player_inventory()
        .player_uuid()
        .update(PlayerInventory {
            player_uuid: player,
            slots: model::encode_slots(&state.slots),
            cursor: state.cursor.trusted_bytes(),
            selected: state.selected,
            revision: state.revision,
            drag_kind: state.drag_kind,
            drag_slots: state.drag_slots.clone(),
        });
}
// Permission changes must invalidate any optimistic click and drag begun under
// the previous authority. Keep the cursor durable even if every slot is full;
// an administrator's revocation must not depend on room in the inventory.
fn invalidate_actions(ctx: &ReducerContext, player: Uuid) -> Result<(), String> {
    if let Some(mut row) = ctx.db.player_inventory().player_uuid().find(player) {
        row.revision = row
            .revision
            .checked_add(1)
            .ok_or("Inventory revision exhausted")?;
        row.drag_kind = -1;
        row.drag_slots.clear();
        ctx.db.player_inventory().player_uuid().update(row);
    }
    Ok(())
}
#[spacetimedb::reducer]
pub fn initialize_player_inventory(ctx: &ReducerContext, session_uuid: Uuid) -> Result<(), String> {
    let session = owned(ctx, session_uuid, false)?;
    if ctx
        .db
        .player_inventory()
        .player_uuid()
        .find(session.player_uuid)
        .is_none()
    {
        let state = State::default();
        ctx.db.player_inventory().insert(PlayerInventory {
            player_uuid: session.player_uuid,
            slots: model::encode_slots(&state.slots),
            cursor: vec![0],
            selected: 0,
            revision: 0,
            drag_kind: -1,
            drag_slots: Vec::new(),
        });
    }
    if ctx
        .db
        .inventory_menu()
        .session_uuid()
        .find(session_uuid)
        .is_none()
    {
        ctx.db.inventory_menu().insert(InventoryMenu {
            session_uuid,
            menu_id: 0,
            next_menu_id: 1,
            storage_uuid: None,
            storage_key: String::new(),
            sequence: 0,
            last_request: Vec::new(),
            result_revision: 0,
        });
    }
    Ok(())
}
#[spacetimedb::reducer]
pub fn grant_inventory_permission(
    ctx: &ReducerContext,
    player_uuid: Uuid,
    creative: bool,
) -> Result<(), String> {
    foundation::require_admin(ctx)?;
    let previous = ctx
        .db
        .inventory_permission()
        .player_uuid()
        .find(player_uuid)
        .is_some_and(|row| row.creative);
    if previous != creative {
        invalidate_actions(ctx, player_uuid)?;
    }
    let row = InventoryPermission {
        player_uuid,
        creative,
    };
    if ctx
        .db
        .inventory_permission()
        .player_uuid()
        .find(player_uuid)
        .is_some()
    {
        ctx.db.inventory_permission().player_uuid().update(row);
    } else {
        ctx.db.inventory_permission().insert(row);
    }
    Ok(())
}
#[spacetimedb::reducer]
pub fn define_simple_storage(
    ctx: &ReducerContext,
    storage_uuid: Uuid,
    title: String,
) -> Result<(), String> {
    foundation::require_admin(ctx)?;
    if title.is_empty() || title.encode_utf16().count() > 64 {
        return Err("Storage title must contain 1..64 UTF-16 units".into());
    }
    if let Some(mut row) = ctx.db.simple_storage().storage_uuid().find(storage_uuid) {
        row.title = title;
        ctx.db.simple_storage().storage_uuid().update(row);
    } else {
        ctx.db.simple_storage().insert(SimpleStorage {
            storage_uuid,
            title,
            slots: model::encode_slots(&vec![Stack::empty(); 27]),
            revision: 0,
        });
    }
    Ok(())
}
#[spacetimedb::reducer]
pub fn grant_storage_access(
    ctx: &ReducerContext,
    player_uuid: Uuid,
    storage_uuid: Uuid,
    allowed: bool,
) -> Result<(), String> {
    foundation::require_admin(ctx)?;
    if ctx
        .db
        .simple_storage()
        .storage_uuid()
        .find(storage_uuid)
        .is_none()
    {
        return Err("Storage not found".into());
    }
    let key = access_key(player_uuid, storage_uuid);
    if allowed {
        if ctx.db.storage_access().key().find(&key).is_none() {
            ctx.db.storage_access().insert(StorageAccess {
                key,
                player_uuid,
                storage_uuid,
            });
        }
    } else {
        ctx.db.storage_access().key().delete(key);
        for session in ctx.db.player_session().player_uuid().filter(player_uuid) {
            if let Some(mut menu) = ctx
                .db
                .inventory_menu()
                .session_uuid()
                .find(session.session_uuid)
                && menu.storage_uuid == Some(storage_uuid)
            {
                invalidate_actions(ctx, player_uuid)?;
                menu.storage_uuid = None;
                menu.storage_key.clear();
                menu.menu_id = 0;
                ctx.db.inventory_menu().session_uuid().update(menu);
            }
        }
    }
    Ok(())
}
fn request(
    ctx: &ReducerContext,
    session_uuid: Uuid,
    sequence: u64,
    payload: Vec<u8>,
) -> Result<Option<InventoryMenu>, String> {
    if payload.len() > wire::MAX_BYTES || sequence == 0 {
        return Err("Invalid inventory request bounds".into());
    }
    let menu = ctx
        .db
        .inventory_menu()
        .session_uuid()
        .find(session_uuid)
        .ok_or("Inventory not initialized")?;
    if sequence == menu.sequence {
        if payload == menu.last_request {
            return Ok(None);
        }
        return Err("Conflicting inventory request replay".into());
    }
    if menu.sequence.checked_add(1) != Some(sequence) {
        return Err("Inventory request sequence mismatch".into());
    }
    Ok(Some(menu))
}
fn commit_request(
    ctx: &ReducerContext,
    mut menu: InventoryMenu,
    sequence: u64,
    payload: Vec<u8>,
    revision: i32,
) {
    menu.sequence = sequence;
    menu.last_request = payload;
    menu.result_revision = revision;
    ctx.db.inventory_menu().session_uuid().update(menu);
}

// A single bounded command envelope provides exact retry identity for all menu
// actions. The gateway cannot bypass ownership, revisions or item permissions.
#[spacetimedb::reducer]
pub fn apply_inventory_request(
    ctx: &ReducerContext,
    session_uuid: Uuid,
    sequence: u64,
    payload: Vec<u8>,
) -> Result<(), String> {
    let session = owned(ctx, session_uuid, true)?;
    let Some(mut menu) = request(ctx, session_uuid, sequence, payload.clone())? else {
        return Ok(());
    };
    let row = ctx
        .db
        .player_inventory()
        .player_uuid()
        .find(session.player_uuid)
        .ok_or("Inventory not initialized")?;
    let mut state = load(&row)?;
    let creative = ctx
        .db
        .inventory_permission()
        .player_uuid()
        .find(session.player_uuid)
        .is_some_and(|permission| permission.creative);
    let mut reader = wire::Reader::new(&payload).map_err(str::to_owned)?;
    let action = reader.int().map_err(str::to_owned)?;
    let expected = reader.int().map_err(str::to_owned)?;
    if expected != state.revision {
        return Err("Stale inventory revision; resynchronize".into());
    }
    match action {
        0 => {
            let selected = reader.length(8).map_err(str::to_owned)? as u8;
            state.select(selected).map_err(str::to_owned)?;
        }
        1 => {
            let slot = reader.int().map_err(str::to_owned)?;
            let slot = i16::try_from(slot).map_err(|_| "Invalid Creative slot")?;
            let stack = wire::read_stack(&mut reader, false).map_err(str::to_owned)?;
            state
                .creative(slot, stack, expected, creative)
                .map_err(str::to_owned)?;
        }
        2 => {
            let menu_id = reader.int().map_err(str::to_owned)?;
            if menu_id != menu.menu_id {
                return Err("Wrong inventory menu".into());
            }
            let slot = i16::try_from(reader.int().map_err(str::to_owned)?)
                .map_err(|_| "Invalid click slot")?;
            let button = i8::try_from(reader.int().map_err(str::to_owned)?)
                .map_err(|_| "Invalid click button")?;
            let mode = reader.int().map_err(str::to_owned)?;
            let mut storage = match menu.storage_uuid {
                Some(uuid) => {
                    if ctx
                        .db
                        .storage_access()
                        .key()
                        .find(access_key(session.player_uuid, uuid))
                        .is_none()
                    {
                        return Err("Storage access required".into());
                    }
                    Some(
                        ctx.db
                            .simple_storage()
                            .storage_uuid()
                            .find(uuid)
                            .ok_or("Storage not found")?,
                    )
                }
                None => None,
            };
            let mut storage_slots = storage
                .as_ref()
                .map(|row| model::decode_slots(&row.slots, 27))
                .transpose()
                .map_err(str::to_owned)?;
            state
                .click(
                    storage_slots.as_mut(),
                    slot,
                    button,
                    mode,
                    expected,
                    creative,
                )
                .map_err(str::to_owned)?;
            if let (Some(mut row), Some(slots)) = (storage.take(), storage_slots) {
                row.slots = model::encode_slots(&slots);
                row.revision = row
                    .revision
                    .checked_add(1)
                    .ok_or("Storage revision exhausted")?;
                for other in ctx
                    .db
                    .inventory_menu()
                    .storage_key()
                    .filter(&row.storage_uuid.to_string())
                {
                    if other.session_uuid == session_uuid {
                        continue;
                    }
                    if let Some(other_session) = ctx
                        .db
                        .player_session()
                        .session_uuid()
                        .find(other.session_uuid)
                        && let Some(mut inventory) = ctx
                            .db
                            .player_inventory()
                            .player_uuid()
                            .find(other_session.player_uuid)
                    {
                        inventory.revision = inventory
                            .revision
                            .checked_add(1)
                            .ok_or("Inventory revision exhausted")?;
                        inventory.drag_kind = -1;
                        inventory.drag_slots.clear();
                        ctx.db.player_inventory().player_uuid().update(inventory);
                    }
                }
                ctx.db.simple_storage().storage_uuid().update(row);
            }
        }
        3 => {
            let bytes: [u8; 16] = reader
                .take(16)
                .map_err(str::to_owned)?
                .try_into()
                .map_err(|_| "Invalid storage UUID")?;
            let uuid = Uuid::from_u128(u128::from_be_bytes(bytes));
            if ctx
                .db
                .storage_access()
                .key()
                .find(access_key(session.player_uuid, uuid))
                .is_none()
            {
                return Err("Storage access required".into());
            }
            if ctx.db.simple_storage().storage_uuid().find(uuid).is_none() {
                return Err("Storage not found".into());
            }
            if ctx
                .db
                .inventory_menu()
                .storage_key()
                .filter(&uuid.to_string())
                .count()
                >= 128
            {
                return Err("Storage viewer limit reached".into());
            }
            state.reconcile().map_err(str::to_owned)?;
            menu.menu_id = menu.next_menu_id;
            menu.next_menu_id = menu
                .next_menu_id
                .checked_add(1)
                .ok_or("Menu ID exhausted")?;
            menu.storage_uuid = Some(uuid);
            menu.storage_key = uuid.to_string();
        }
        4 => {
            let menu_id = reader.int().map_err(str::to_owned)?;
            if menu_id != menu.menu_id {
                return Err("Wrong closing menu".into());
            }
            state.reconcile().map_err(str::to_owned)?;
            menu.storage_uuid = None;
            menu.storage_key.clear();
            menu.menu_id = 0;
        }
        5 => {
            if menu.menu_id != 0 {
                return Err("Crafting requires player inventory menu".into());
            }
            state
                .click(None, 0, 0, 0, expected, false)
                .map_err(str::to_owned)?;
        }
        6 => {
            if menu.menu_id != 0 {
                return Err("Offhand swap requires player inventory menu".into());
            }
            state
                .click(None, 45, state.selected as i8, 2, expected, false)
                .map_err(str::to_owned)?;
        }
        _ => return Err("Unknown inventory action".into()),
    }
    if !reader.bytes.is_empty() {
        return Err("Trailing inventory request fields".into());
    }
    save(ctx, session.player_uuid, &state);
    commit_request(ctx, menu, sequence, payload, state.revision);
    Ok(())
}
pub(crate) fn remove_session(ctx: &ReducerContext, session: &foundation::PlayerSession) {
    if let Some(row) = ctx
        .db
        .player_inventory()
        .player_uuid()
        .find(session.player_uuid)
        && let Ok(mut state) = load(&row)
    {
        // Cursor remains persistent when inventory is full. A reconnect receives
        // it again; no item is discarded or duplicated during cleanup.
        let _ = state.reconcile();
        state.drag_kind = -1;
        state.drag_slots.clear();
        save(ctx, session.player_uuid, &state);
    }
    ctx.db
        .inventory_menu()
        .session_uuid()
        .delete(session.session_uuid);
}
#[spacetimedb::view(accessor=gateway_inventory_snapshots,public,primary_key=session_uuid)]
pub fn gateway_inventory_snapshots(ctx: &ViewContext) -> Vec<InventorySnapshot> {
    if ctx.db.gateway().identity().find(ctx.sender()).is_none() {
        return Vec::new();
    }
    ctx.db
        .player_session()
        .gateway_identity()
        .filter(ctx.sender())
        .filter_map(|session| {
            let row = ctx
                .db
                .player_inventory()
                .player_uuid()
                .find(session.player_uuid)?;
            let menu = ctx
                .db
                .inventory_menu()
                .session_uuid()
                .find(session.session_uuid)?;
            let mut slots = model::decode_slots(&row.slots, 46).ok()?;
            let mut title = String::new();
            if let Some(uuid) = menu.storage_uuid {
                ctx.db
                    .storage_access()
                    .key()
                    .find(access_key(session.player_uuid, uuid))?;
                let storage = ctx.db.simple_storage().storage_uuid().find(uuid)?;
                let mut storage_slots = model::decode_slots(&storage.slots, 27).ok()?;
                storage_slots.extend(slots[9..45].iter().cloned());
                slots = storage_slots;
                title = storage.title;
            }
            Some(InventorySnapshot {
                session_uuid: session.session_uuid,
                owner_connection: session.owner_connection,
                revision: row.revision,
                selected: row.selected,
                slots: model::encode_slots(&slots),
                cursor: row.cursor,
                menu_id: menu.menu_id,
                menu_title: title,
                sequence: menu.sequence,
                creative_allowed: ctx
                    .db
                    .inventory_permission()
                    .player_uuid()
                    .find(session.player_uuid)
                    .is_some_and(|permission| permission.creative),
            })
        })
        .collect()
}
