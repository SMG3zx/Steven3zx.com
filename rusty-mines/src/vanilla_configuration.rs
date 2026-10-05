//! Checked-in official 26.3 export. Never derive network IDs from sorted names.
use crate::configuration::{Clientbound, KnownPack, RegistryEntry, RegistryTags, Tag};
use serde::Deserialize;
use std::sync::OnceLock;

#[derive(Deserialize)]
struct Asset {
    version: String,
    protocol: i32,
    server_sha1: String,
    core: Pack,
    registries: Vec<Registry>,
    tags: Vec<Tags>,
}
#[derive(Deserialize)]
struct Pack {
    namespace: String,
    name: String,
    version: String,
}
#[derive(Deserialize)]
struct Registry {
    registry: String,
    entries: Vec<String>,
}
#[derive(Deserialize)]
struct Tags {
    registry: String,
    tags: Vec<AssetTag>,
}
#[derive(Deserialize)]
struct AssetTag {
    id: String,
    entries: Vec<i32>,
}

pub struct VanillaConfiguration {
    pub core: KnownPack,
    pub registries: Vec<Clientbound>,
    pub tags: Clientbound,
}
/// Parsing/validation happens once, before any registry fields are sent.
pub fn vanilla() -> Result<&'static VanillaConfiguration, &'static str> {
    static CONFIGURATION: OnceLock<Result<VanillaConfiguration, &'static str>> = OnceLock::new();
    CONFIGURATION
        .get_or_init(|| {
            let asset: Asset =
                serde_json::from_str(include_str!("../assets/configuration-26.3.json"))
                    .map_err(|_| "invalid vanilla export")?;
            if asset.version != "26.3"
                || asset.protocol != 777
                || asset.server_sha1 != "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c"
                || asset.core.namespace != "minecraft"
                || asset.core.name != "core"
                || asset.core.version != "26.3"
                || asset.registries.len() != 32
                || asset.tags.len() != 15
            {
                return Err("wrong vanilla export version/counts");
            }
            let registries: Vec<_> = asset
                .registries
                .into_iter()
                .map(|r| Clientbound::RegistryData {
                    registry: r.registry,
                    entries: r
                        .entries
                        .into_iter()
                        .map(|id| RegistryEntry { id, data: None })
                        .collect(),
                })
                .collect();
            let tags = Clientbound::Tags(
                asset
                    .tags
                    .into_iter()
                    .map(|r| RegistryTags {
                        registry: r.registry,
                        tags: r
                            .tags
                            .into_iter()
                            .map(|t| Tag {
                                id: t.id,
                                entries: t.entries,
                            })
                            .collect(),
                    })
                    .collect(),
            );
            for packet in registries.iter().chain(std::iter::once(&tags)) {
                packet.encode()?;
            }
            Ok(VanillaConfiguration {
                core: KnownPack {
                    namespace: asset.core.namespace,
                    name: asset.core.name,
                    version: asset.core.version,
                },
                registries,
                tags,
            })
        })
        .as_ref()
        .map_err(|error| *error)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn official_export_is_complete_and_ordered() {
        let v = vanilla().unwrap();
        let mut keys = std::collections::HashSet::new();
        let mut total = 0;
        for packet in &v.registries {
            let Clientbound::RegistryData { registry, entries } = packet else {
                panic!()
            };
            assert!(keys.insert(registry));
            let mut names = std::collections::HashSet::new();
            for entry in entries {
                assert!(names.insert(&entry.id));
                assert!(entry.data.is_none());
            }
            total += entries.len();
            if registry == "minecraft:dimension_type" {
                assert_eq!(
                    entries.iter().map(|e| e.id.as_str()).collect::<Vec<_>>(),
                    [
                        "minecraft:overworld",
                        "minecraft:overworld_caves",
                        "minecraft:the_end",
                        "minecraft:the_nether"
                    ]
                );
            }
        }
        assert!(keys.contains(&"minecraft:world_clock".to_owned()));
        assert!(keys.contains(&"minecraft:sulfur_cube_archetype".to_owned()));
        assert_eq!(total, 432);
        let Clientbound::Tags(tags) = &v.tags else {
            panic!()
        };
        assert_eq!(tags.iter().map(|r| r.tags.len()).sum::<usize>(), 773);
        for r in tags {
            let mut names = std::collections::HashSet::new();
            for tag in &r.tags {
                assert!(names.insert(&tag.id));
            }
            if let Some(Clientbound::RegistryData { entries, .. }) = v.registries.iter().find(|p| matches!(p, Clientbound::RegistryData { registry, .. } if registry == &r.registry)) {
                for tag in &r.tags { for id in &tag.entries { assert!((*id as usize) < entries.len()); } }
            }
        }
    }
}
