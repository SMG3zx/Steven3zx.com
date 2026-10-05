#[cfg_attr(not(test), derive(spacetimedb::SpacetimeType))]
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum IdentityKind {
    Offline,
    Verified,
}

#[cfg_attr(not(test), derive(spacetimedb::SpacetimeType))]
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum SessionPhase {
    Login,
    Configuration,
    Play,
}

pub const WORLD_BORDER: f64 = 29_999_984.0;
pub const WORLD_BORDER_BLOCK: i32 = 29_999_984;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum WorldGameMode {
    Survival,
    Creative,
    Adventure,
    Spectator,
}

pub fn supported_block_state(state: u32) -> bool {
    matches!(state, 0 | 1 | 9 | 88)
}

pub fn block_action_allowed(
    mode: WorldGameMode,
    expected: u32,
    new: u32,
) -> Result<(), &'static str> {
    if !supported_block_state(expected) || !supported_block_state(new) {
        return Err("unsupported block state");
    }
    if !matches!(mode, WorldGameMode::Survival | WorldGameMode::Creative) {
        return Err("game mode cannot edit blocks");
    }
    if mode == WorldGameMode::Survival && expected == 88 && new == 0 {
        return Err("protected block cannot be removed");
    }
    Ok(())
}

pub fn valid_block_position(x: i32, y: i32, z: i32) -> bool {
    x.abs() <= WORLD_BORDER_BLOCK && z.abs() <= WORLD_BORDER_BLOCK && (-64..=319).contains(&y)
}

pub fn valid_world_pose(x: f64, y: f64, z: f64, yaw: f32, pitch: f32) -> bool {
    x.is_finite()
        && y.is_finite()
        && z.is_finite()
        && yaw.is_finite()
        && pitch.is_finite()
        && x.abs() <= WORLD_BORDER
        && z.abs() <= WORLD_BORDER
        && (-64.0..=320.0).contains(&y)
        && (-360.0..=360.0).contains(&yaw)
        && (-90.0..=90.0).contains(&pitch)
}

pub fn admin_allowed<T: PartialEq>(sender: T, stored: bool, bootstrap: Option<T>) -> bool {
    stored || bootstrap == Some(sender)
}

pub fn offline_player_uuid(name: &str) -> Result<u128, String> {
    if name.is_empty()
        || name.len() > 16
        || !name.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'_')
    {
        return Err("Username must be 1-16 ASCII letters, digits, or underscores".into());
    }
    let mut bytes = md5::compute(format!("OfflinePlayer:{name}")).0;
    bytes[6] = (bytes[6] & 0x0f) | 0x30;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    Ok(u128::from_be_bytes(bytes))
}

pub fn owns_connection<I: PartialEq, C: PartialEq>(
    owner: I,
    owner_connection: C,
    sender: I,
    connection: C,
) -> bool {
    owner == sender && owner_connection == connection
}

pub fn session_durations_micros(
    now: i64,
    connected_at: i64,
    last_activity: i64,
    lease_micros: i64,
) -> (u64, i64) {
    let age = now.saturating_sub(connected_at).max(0) as u64;
    let remaining = last_activity
        .saturating_add(lease_micros)
        .saturating_sub(now);
    (age, remaining)
}

pub fn require_live_lease(now: i64, last_activity: i64, lease_micros: i64) -> Result<(), String> {
    let expiry = last_activity.checked_add(lease_micros);
    if lease_micros <= 0 || !expiry.is_some_and(|expiry| now < expiry) {
        return Err("Session lease has expired or is invalid".into());
    }
    Ok(())
}

pub fn wire_entity_id(id: u64) -> Result<i32, String> {
    i32::try_from(id)
        .ok()
        .filter(|id| *id > 0)
        .ok_or_else(|| "Entity ID space exhausted or invalid".into())
}

pub fn advance_phase(phase: &SessionPhase) -> Result<SessionPhase, String> {
    match phase {
        SessionPhase::Login | SessionPhase::Configuration => Ok(SessionPhase::Configuration),
        SessionPhase::Play => Err("Play cannot transition back to Configuration".into()),
    }
}

pub fn enter_play_phase(phase: &SessionPhase) -> Result<SessionPhase, String> {
    match phase {
        SessionPhase::Configuration | SessionPhase::Play => Ok(SessionPhase::Play),
        SessionPhase::Login => Err("Play requires a Configuration session".into()),
    }
}

pub fn accept_offline_profile(kind: &IdentityKind) -> Result<(), String> {
    match kind {
        IdentityKind::Offline => Ok(()),
        IdentityKind::Verified => Err("Offline login cannot modify a verified profile".into()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn java_offline_identity_and_validation() {
        assert_eq!(
            offline_player_uuid("Notch").unwrap(),
            0xb50ad385829d3141a2167e7d7539ba7f
        );
        assert_ne!(
            offline_player_uuid("Notch").unwrap(),
            offline_player_uuid("notch").unwrap()
        );
        for name in ["", "a b", "é", "abcdefghijklmnopq", "a\0"] {
            assert!(offline_player_uuid(name).is_err());
        }
    }
    #[test]
    fn administrator_bootstrap_is_not_open_registration() {
        assert!(!admin_allowed(2, false, None));
        assert!(!admin_allowed(2, false, Some(1)));
        assert!(admin_allowed(1, false, Some(1)));
        assert!(admin_allowed(1, true, None));
    }
    #[test]
    fn ownership_requires_identity_and_connection() {
        assert!(owns_connection(3, 4, 3, 4));
        assert!(!owns_connection(3, 4, 5, 4));
        assert!(!owns_connection(3, 4, 3, 5));
    }

    #[test]
    fn world_pose_bounds_match_the_flat_world_border() {
        assert!(valid_world_pose(
            -WORLD_BORDER,
            -64.0,
            WORLD_BORDER,
            360.0,
            -90.0
        ));
        assert!(valid_world_pose(0.0, 320.0, 0.0, 0.0, 0.0));
        assert!(!valid_world_pose(WORLD_BORDER + 1.0, 65.0, 0.0, 0.0, 0.0));
        assert!(!valid_world_pose(0.0, 320.001, 0.0, 0.0, 0.0));
        assert!(!valid_world_pose(f64::NAN, 65.0, 0.0, 0.0, 0.0));
        assert!(!valid_world_pose(0.0, 65.0, 0.0, 0.0, f32::INFINITY));
    }

    #[test]
    fn block_actions_enforce_supported_states_positions_and_modes() {
        use WorldGameMode::*;
        assert_eq!(block_action_allowed(Survival, 1, 0), Ok(()));
        assert_eq!(block_action_allowed(Creative, 0, 9), Ok(()));
        assert!(block_action_allowed(Adventure, 1, 0).is_err());
        assert!(block_action_allowed(Spectator, 0, 1).is_err());
        assert!(block_action_allowed(Survival, 88, 0).is_err());
        assert!(block_action_allowed(Survival, 1, u32::MAX).is_err());
        assert!(valid_block_position(
            -WORLD_BORDER_BLOCK,
            319,
            WORLD_BORDER_BLOCK
        ));
        assert!(!valid_block_position(WORLD_BORDER_BLOCK + 1, 64, 0));
        assert!(!valid_block_position(0, 320, 0));
    }

    #[test]
    fn session_diagnostics_clamp_future_timestamps_and_keep_expired_leases_negative() {
        assert_eq!(session_durations_micros(100, 40, 85, 30), (60, 15));
        assert_eq!(session_durations_micros(100, 40, 69, 30), (60, -1));
        assert_eq!(session_durations_micros(100, 120, 100, 30), (0, 30));
    }
    #[test]
    fn wire_entity_ids_reject_zero_and_exhaustion_without_narrowing() {
        for id in [1, 127, 128, i32::MAX as u64] {
            assert_eq!(u64::try_from(wire_entity_id(id).unwrap()).unwrap(), id);
        }
        for id in [0, i32::MAX as u64 + 1, u32::MAX as u64, u64::MAX] {
            assert!(wire_entity_id(id).is_err());
        }
    }

    #[test]
    fn expired_leases_cannot_be_renewed_at_the_boundary_or_after_it() {
        for (last, lease) in [(0, 30), (-100, 30), (i64::MAX - 30, 30)] {
            let expiry = last + lease;
            assert!(require_live_lease(expiry - 1, last, lease).is_ok());
            assert!(require_live_lease(expiry, last, lease).is_err());
            if expiry < i64::MAX {
                assert!(require_live_lease(expiry + 1, last, lease).is_err());
            }
        }
        assert!(require_live_lease(0, i64::MAX, 30).is_err());
        assert!(require_live_lease(0, 0, 0).is_err());
        assert!(require_live_lease(0, 0, -1).is_err());
    }

    #[test]
    fn phase_transitions_are_explicit_and_retry_safe() {
        assert_eq!(
            advance_phase(&SessionPhase::Login).unwrap(),
            SessionPhase::Configuration
        );
        assert_eq!(
            advance_phase(&SessionPhase::Configuration).unwrap(),
            SessionPhase::Configuration
        );
        assert!(advance_phase(&SessionPhase::Play).is_err());
        assert!(enter_play_phase(&SessionPhase::Login).is_err());
        assert_eq!(
            enter_play_phase(&SessionPhase::Configuration).unwrap(),
            SessionPhase::Play
        );
        assert_eq!(
            enter_play_phase(&SessionPhase::Play).unwrap(),
            SessionPhase::Play
        );
    }
    #[test]
    fn offline_login_cannot_overwrite_verified_identity() {
        assert!(accept_offline_profile(&IdentityKind::Offline).is_ok());
        assert!(accept_offline_profile(&IdentityKind::Verified).is_err());
    }
}
