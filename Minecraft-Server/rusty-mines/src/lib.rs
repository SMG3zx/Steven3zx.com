//! Minecraft wire APIs, independent of the server console and backend.
#[path = "packets/configuration.rs"]
pub mod configuration;
#[path = "packets/login.rs"]
pub mod login;
#[path = "packets/catalog/mod.rs"]
pub mod packet_catalog;
pub mod vanilla_configuration;
pub mod vanilla_world;

pub mod inventory_registry;
#[path = "packets/play.rs"]
pub mod play;
