//! Immutable packets generated and roundtripped by the pinned official 26.3 codecs.
use crate::play;
use serde::Deserialize;
use std::{collections::BTreeMap, sync::OnceLock};

pub const MAX_RENDER_DISTANCE: i32 = 2;
pub const WORLD_BORDER: i32 = 29_999_984;
pub const MAX_CHUNK_COORD: i32 = WORLD_BORDER / 16;
pub const WORLD_ID: &str = "rusty-mines-flat-26.3-v1";
pub const GENERATOR_VERSION: &str = "deterministic-flat-platform-v1";
pub const PROTOCOL_FINGERPRINT: &str =
    "java-26.3-protocol-777-server-sha1-33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c";

#[derive(Deserialize)]
struct Asset {
    version: String,
    protocol: i32,
    server_sha1: String,
    packets: BTreeMap<String, Entry>,
}
#[derive(Deserialize)]
struct Entry {
    hex: String,
}

/// Metadata for the pinned official asset (see tools/ExtractWorldInitialization.java).
pub const METADATA: Metadata = Metadata {
    world_id: WORLD_ID,
    dimension: "minecraft:overworld",
    generator_version: GENERATOR_VERSION,
    seed: None,
    protocol_fingerprint: PROTOCOL_FINGERPRINT,
    spawn: "8.5, 65, 8.5 (initial position)",
    default_spawn: "8, 65, 8 (advertised; no respawn flow)",
    chunk_radius: 2,
    platform_y: 64,
    height: "y=-64..319",
    mode: "Survival by default; mode is backend-authoritative",
    edit_policy: "Every logged-in player follows their authorized mode policy; Creative and Spectator grants require administrator authority",
    limitations: "Deterministic flat platform with persistent supported block overrides; generalized chunk palettes, terrain physics, and non-void death causes remain incomplete.",
};

pub struct Metadata {
    pub world_id: &'static str,
    pub dimension: &'static str,
    pub generator_version: &'static str,
    pub seed: Option<i64>,
    pub protocol_fingerprint: &'static str,
    pub spawn: &'static str,
    pub default_spawn: &'static str,
    pub chunk_radius: i32,
    pub platform_y: i32,
    pub height: &'static str,
    pub mode: &'static str,
    pub edit_policy: &'static str,
    pub limitations: &'static str,
}

/// State IDs verified against the pinned official encoder, including default grass snowy=false.
pub fn block_state(position: play::BlockPosition) -> Option<i32> {
    if !(-WORLD_BORDER..=WORLD_BORDER).contains(&position.x)
        || !(-WORLD_BORDER..=WORLD_BORDER).contains(&position.z)
        || !(-64..=319).contains(&position.y)
    {
        return None;
    }
    Some(match position.y {
        62 => 88,
        63 => 1,
        64 => 9,
        _ => 0,
    })
}

pub struct World {
    pub login: Vec<u8>,
    pub initialization: Vec<Vec<u8>>,
    loading: Vec<u8>,
    spawn: Vec<u8>,
    abilities: Vec<u8>,
    position: Vec<u8>,
    center_template: Vec<u8>,
    render_distance_template: Vec<u8>,
    simulation_distance_template: Vec<u8>,
    batch_start: Vec<u8>,
    batch_end_template: Vec<u8>,
    pub chunk_template: Vec<u8>,
    pub unload_template: Vec<u8>,
}

impl World {
    pub fn initialization_for(
        &self,
        center_x: i32,
        center_z: i32,
        radius: i32,
    ) -> Result<Vec<Vec<u8>>, &'static str> {
        if !(0..=MAX_RENDER_DISTANCE).contains(&radius)
            || !(-MAX_CHUNK_COORD..=MAX_CHUNK_COORD).contains(&center_x)
            || !(-MAX_CHUNK_COORD..=MAX_CHUNK_COORD).contains(&center_z)
        {
            return Err("unsupported chunk view radius");
        }
        let mut packets = vec![
            self.loading.clone(),
            self.spawn.clone(),
            self.abilities.clone(),
            play::chunk_center_at(&self.center_template, center_x, center_z)?,
            play::chunk_distance(
                &self.render_distance_template,
                play::RENDER_DISTANCE,
                radius,
            )?,
            play::chunk_distance(
                &self.simulation_distance_template,
                play::SIMULATION_DISTANCE,
                radius,
            )?,
            self.batch_start.clone(),
        ];
        let mut count = 0;
        for z in center_z - radius..=center_z + radius {
            for x in center_x - radius..=center_x + radius {
                packets.push(play::chunk_at(&self.chunk_template, x, z)?);
                count += 1;
            }
        }
        packets.push(play::chunk_batch_finished(&self.batch_end_template, count)?);
        packets.push(self.position.clone());
        if packets
            .iter()
            .map(|packet| packet.len().saturating_add(5))
            .sum::<usize>()
            > 2 * 1024 * 1024
        {
            return Err("initial chunk window exceeds byte budget");
        }
        Ok(packets)
    }
}

pub fn world() -> Result<&'static World, &'static str> {
    static WORLD: OnceLock<Result<World, &'static str>> = OnceLock::new();
    WORLD
        .get_or_init(|| {
            let mut asset: Asset = serde_json::from_str(include_str!("../assets/world-26.3.json"))
                .map_err(|_| "invalid world asset")?;
            if asset.version != "26.3"
                || asset.protocol != 777
                || asset.server_sha1 != "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c"
                || asset.packets.len() != 9
            {
                return Err("wrong world asset provenance");
            }
            let mut take = |name: &str, id: u8| -> Result<Vec<u8>, &'static str> {
                let entry = asset.packets.remove(name).ok_or("missing world packet")?;
                if entry.hex.len() % 2 != 0 {
                    return Err("invalid world hex");
                }
                let bytes: Vec<u8> = entry
                    .hex
                    .as_bytes()
                    .as_chunks::<2>()
                    .0
                    .iter()
                    .map(|pair| {
                        let text = std::str::from_utf8(pair).map_err(|_| "invalid world hex")?;
                        u8::from_str_radix(text, 16).map_err(|_| "invalid world hex")
                    })
                    .collect::<Result<_, _>>()?;
                play::validate_official_packet(&bytes, id)?;
                Ok(bytes)
            };
            let login = take("login", play::LOGIN)?;
            let mut stream_asset: Asset =
                serde_json::from_str(include_str!("../assets/chunk-stream-26.3.json"))
                    .map_err(|_| "invalid chunk stream asset")?;
            if stream_asset.version != "26.3"
                || stream_asset.protocol != 777
                || stream_asset.server_sha1 != "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c"
                || stream_asset.packets.len() != 12
            {
                return Err("wrong chunk stream asset provenance");
            }
            let mut take_stream = |name: &str, id: u8| -> Result<Vec<u8>, &'static str> {
                let entry = stream_asset
                    .packets
                    .remove(name)
                    .ok_or("missing chunk stream packet")?;
                if entry.hex.len() % 2 != 0 {
                    return Err("invalid chunk stream hex");
                }
                let bytes: Vec<u8> = entry
                    .hex
                    .as_bytes()
                    .as_chunks::<2>()
                    .0
                    .iter()
                    .map(|pair| {
                        let text =
                            std::str::from_utf8(pair).map_err(|_| "invalid chunk stream hex")?;
                        u8::from_str_radix(text, 16).map_err(|_| "invalid chunk stream hex")
                    })
                    .collect::<Result<_, _>>()?;
                play::validate_official_packet(&bytes, id)?;
                Ok(bytes)
            };
            let stream_loading = take_stream("loading", play::LOADING)?;
            let stream_spawn = take_stream("spawn", play::SPAWN)?;
            let stream_abilities = take_stream("abilities", play::ABILITIES)?;
            let stream_position = take_stream("position", play::POSITION)?;
            let center_template = take_stream("center", play::CENTER)?;
            let render_distance_template = take_stream("render_distance", play::RENDER_DISTANCE)?;
            let simulation_distance_template =
                take_stream("simulation_distance", play::SIMULATION_DISTANCE)?;
            let unload_template = take_stream("unload_chunk", play::UNLOAD_CHUNK)?;
            let batch_start = take_stream("batch_start", play::BATCH_START)?;
            let batch_end_template = take_stream("batch_end", play::BATCH_END)?;
            let chunk_template = take_stream("chunk", play::CHUNK)?;
            let world = World {
                login,
                initialization: Vec::new(),
                loading: stream_loading,
                spawn: stream_spawn,
                abilities: stream_abilities,
                position: stream_position,
                center_template,
                render_distance_template,
                simulation_distance_template,
                batch_start,
                batch_end_template,
                chunk_template,
                unload_template,
            };
            let initialization =
                world.initialization_for(0, 0, METADATA.chunk_radius.min(MAX_RENDER_DISTANCE))?;
            Ok(World {
                initialization,
                ..world
            })
        })
        .as_ref()
        .map_err(|error| *error)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn cached_official_world_has_twenty_five_distinct_chunks() {
        let world = world().unwrap();
        assert!(std::ptr::eq(world, super::world().unwrap()));
        let chunks: Vec<_> = world
            .initialization
            .iter()
            .filter(|p| p[0] == 0x2e)
            .collect();
        assert_eq!(chunks.len(), 25);
        let coordinates: std::collections::BTreeSet<_> = chunks.iter().map(|p| &p[1..9]).collect();
        assert_eq!(coordinates.len(), 25);
        for chunk in &chunks {
            assert_eq!(&chunk[9..], &chunks[0][9..]);
        }
        assert_eq!(&world.chunk_template[9..], &chunks[0][9..]);
        assert_eq!(world.initialization.last().unwrap()[0], 0x49);
        assert!(world.initialization.iter().all(|p| p.len() < 0x1f_ffff));
    }

    #[test]
    fn bounded_chunk_windows_support_negative_centers_and_radius_caps() {
        let world = world().unwrap();
        for radius in 0..=MAX_RENDER_DISTANCE {
            let packets = world.initialization_for(-2, 1, radius).unwrap();
            let chunks: Vec<_> = packets
                .iter()
                .filter(|packet| packet[0] == play::CHUNK)
                .collect();
            assert_eq!(chunks.len(), ((radius * 2 + 1).pow(2)) as usize);
            let first = &chunks[0];
            assert_eq!(&first[1..5], &(-2 - radius).to_be_bytes());
            assert_eq!(&first[5..9], &(1 - radius).to_be_bytes());
            assert_eq!(packets[3][0], play::CENTER);
            assert_eq!(packets[4], vec![play::RENDER_DISTANCE, radius as u8]);
            assert_eq!(packets[5], vec![play::SIMULATION_DISTANCE, radius as u8]);
            assert_eq!(packets[6], vec![play::BATCH_START]);
            assert_eq!(
                packets[packets.len() - 2],
                vec![play::BATCH_END, chunks.len() as u8]
            );
        }
        assert!(world
            .initialization_for(0, 0, MAX_RENDER_DISTANCE + 1)
            .is_err());
        assert!(world.initialization_for(i32::MAX, 0, 1).is_err());
    }

    #[test]
    fn flat_platform_state_is_defined_to_world_border() {
        assert_eq!(METADATA.world_id, WORLD_ID);
        assert_eq!(METADATA.generator_version, GENERATOR_VERSION);
        assert_eq!(METADATA.seed, None);
        assert!(METADATA.protocol_fingerprint.contains("protocol-777"));
        assert!(!METADATA.edit_policy.is_empty());
        assert_eq!(
            block_state(play::BlockPosition {
                x: -WORLD_BORDER,
                y: 64,
                z: WORLD_BORDER,
            }),
            Some(9)
        );
        assert_eq!(
            block_state(play::BlockPosition {
                x: WORLD_BORDER + 1,
                y: 64,
                z: 0,
            }),
            None
        );
        assert_eq!(
            block_state(play::BlockPosition { x: 0, y: -65, z: 0 }),
            None
        );
    }
}
