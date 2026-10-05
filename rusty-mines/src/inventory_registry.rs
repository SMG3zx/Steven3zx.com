//! Pinned item/component metadata. JSON defaults are data, not network codecs.
use serde::Deserialize;
use serde_json::{Map, Value};
use std::collections::{BTreeMap, HashSet};
use std::sync::OnceLock;

#[derive(Debug, Deserialize)]
pub struct Item {
    pub id: i32,
    pub name: String,
    pub defaults: Map<String, Value>,
}

#[derive(Debug, Deserialize)]
pub struct Component {
    pub id: i32,
    pub name: String,
    pub persistent: bool,
}

#[derive(Deserialize)]
struct Manifest {
    version: String,
    protocol: i32,
    server_sha1: String,
    items: Vec<Item>,
    components: Vec<Component>,
}

pub struct InventoryRegistry {
    items: BTreeMap<i32, Item>,
    components: BTreeMap<i32, Component>,
}

impl InventoryRegistry {
    pub fn item(&self, id: i32) -> Option<&Item> {
        self.items.get(&id)
    }

    pub fn component(&self, id: i32) -> Option<&Component> {
        self.components.get(&id)
    }

    fn parse(json: &str) -> Result<Self, &'static str> {
        let manifest: Manifest =
            serde_json::from_str(json).map_err(|_| "invalid inventory registry JSON")?;
        if manifest.version != "26.3"
            || manifest.protocol != 777
            || manifest.server_sha1 != "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c"
        {
            return Err("inventory registry provenance mismatch");
        }
        let mut components = BTreeMap::new();
        let mut component_names = HashSet::new();
        for component in manifest.components {
            if component.id < 0
                || !component.name.starts_with("minecraft:")
                || !component_names.insert(component.name.clone())
                || components.insert(component.id, component).is_some()
            {
                return Err("invalid or duplicate component registry entry");
            }
        }
        let mut items = BTreeMap::new();
        let mut item_names = HashSet::new();
        for item in manifest.items {
            if item.id < 0
                || !item.name.starts_with("minecraft:")
                || !item_names.insert(item.name.clone())
                || item
                    .defaults
                    .keys()
                    .any(|name| !component_names.contains(name))
                || items.insert(item.id, item).is_some()
            {
                return Err("invalid or duplicate item registry entry");
            }
        }
        if items.len() != 1658 || components.len() != 122 {
            return Err("incomplete inventory registry");
        }
        Ok(Self { items, components })
    }
}

pub fn vanilla() -> Result<&'static InventoryRegistry, &'static str> {
    static REGISTRY: OnceLock<Result<InventoryRegistry, &'static str>> = OnceLock::new();
    REGISTRY
        .get_or_init(|| InventoryRegistry::parse(include_str!("../assets/inventory-26.3.json")))
        .as_ref()
        .map_err(|error| *error)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn official_ids_and_defaults_are_loaded_without_name_ordering() {
        let registry = vanilla().unwrap();
        assert_eq!(registry.item(1).unwrap().name, "minecraft:stone");
        assert_eq!(registry.item(1050).unwrap().name, "minecraft:diamond_sword");
        assert_eq!(registry.component(3).unwrap().name, "minecraft:damage");
        assert_eq!(
            registry.item(1).unwrap().defaults["minecraft:max_stack_size"],
            64
        );
        assert!(registry.item(-1).is_none());
        assert!(registry.item(i32::MAX).is_none());
        assert!(registry.component(122).is_none());
    }

    #[test]
    fn rejects_provenance_duplicates_and_unknown_default_components() {
        let original: Value =
            serde_json::from_str(include_str!("../assets/inventory-26.3.json")).unwrap();
        for field in ["version", "protocol", "server_sha1"] {
            let mut changed = original.clone();
            changed[field] = Value::Null;
            assert!(InventoryRegistry::parse(&changed.to_string()).is_err());
        }
        for registry in ["items", "components"] {
            for field in ["id", "name"] {
                let mut changed = original.clone();
                changed[registry][1][field] = changed[registry][0][field].clone();
                assert!(InventoryRegistry::parse(&changed.to_string()).is_err());
            }
        }
        let mut changed = original;
        changed["items"][1]["defaults"]["minecraft:invented"] = Value::Bool(true);
        assert!(InventoryRegistry::parse(&changed.to_string()).is_err());
    }
}
