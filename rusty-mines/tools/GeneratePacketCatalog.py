"""Generate packet metadata modules from the pinned protocol-777 inventory.

Inventory names/IDs: Minecraft Wiki contributors, CC BY-SA 3.0 Unported.
Source: https://minecraft.wiki/w/Java_Edition_protocol/Packets?oldid=3810839
This does not generate codecs or enable unsupported packets.
"""

from pathlib import Path

SOURCE = "https://minecraft.wiki/w/Java_Edition_protocol/Packets?oldid=3810839"
# Ordered by numeric ID within each state and direction, not globally.
INVENTORY = {
    ("handshaking", "serverbound"): "intention",
    ("status", "clientbound"): "status_response pong_response",
    ("status", "serverbound"): "status_request ping_request",
    (
        "login",
        "clientbound",
    ): "login_disconnect hello login_finished login_compression custom_query cookie_request",
    (
        "login",
        "serverbound",
    ): "hello key custom_query_answer login_acknowledged cookie_response",
    (
        "configuration",
        "clientbound",
    ): "cookie_request custom_payload disconnect finish_configuration keep_alive ping reset_chat registry_data resource_pack_pop resource_pack_push post_effects store_cookie transfer update_enabled_features update_tags select_known_packs custom_report_details server_links clear_dialog show_dialog code_of_conduct",
    (
        "configuration",
        "serverbound",
    ): "client_information cookie_response custom_payload finish_configuration keep_alive pong resource_pack select_known_packs custom_click_action accept_code_of_conduct",
    (
        "play",
        "clientbound",
    ): "bundle_delimiter add_entity animate award_stats block_changed_ack block_destruction block_entity_data block_event block_update boss_event change_difficulty chunk_batch_finished chunk_batch_start chunks_biomes clear_titles command_suggestions commands container_close container_set_content container_set_data container_set_slot cookie_request cooldown custom_chat_completions custom_payload damage_event debug_block_value debug_chunk_value debug_entity_value debug_event debug_sample delete_chat disconnect disguised_chat entity_event entity_position_sync explode add_transient_block forget_level_chunk game_event game_rule_values game_test_highlight_pos mount_screen_open hurt_animation initialize_border keep_alive level_chunk_with_light level_event level_particles light_update login low_disk_space_warning map_item_data merchant_offers move_entity_pos move_entity_pos_rot move_minecart_along_track move_entity_rot move_vehicle open_book open_screen open_sign_editor ping pong_response place_ghost_recipe player_abilities player_chat player_combat_end player_combat_enter player_combat_kill player_info_remove player_info_update player_look_at player_position player_rotation recipe_book_add recipe_book_remove recipe_book_settings remove_entities remove_mob_effect reset_score resource_pack_pop resource_pack_push post_effects respawn rotate_head section_blocks_update select_advancements_tab server_data set_action_bar_text set_border_center set_border_lerp_size set_border_size set_border_warning_delay set_border_warning_distance set_camera set_chunk_cache_center set_chunk_cache_radius set_cursor_item set_default_spawn_position set_display_objective set_entity_data set_entity_link set_entity_motion set_equipment set_experience set_health set_held_slot set_objective set_passengers set_player_inventory set_player_team set_score set_simulation_distance set_subtitle_text set_time set_title_text set_titles_animation sound_entity sound start_configuration stop_sound store_cookie swing_animation system_chat tab_list tag_query take_item_entity teleport_entity test_instance_block_status ticking_state ticking_step transfer update_advancements update_attributes update_mob_effect update_recipes update_tags projectile_power custom_report_details server_links waypoint clear_dialog show_dialog",
    (
        "play",
        "serverbound",
    ): "accept_teleportation attack block_entity_tag_query bundle_item_selected change_difficulty change_game_mode chat_ack chat_command chat_command_signed chat chat_session_update chunk_batch_received client_command client_tick_end client_information command_suggestion configuration_acknowledged container_button_click container_click container_close container_slot_state_changed cookie_response custom_payload debug_subscription_request edit_book entity_tag_query interact jigsaw_generate keep_alive lock_difficulty move_player_pos move_player_pos_rot move_player_rot move_player_status_only move_vehicle paddle_boat pick_item_from_block pick_item_from_entity ping_request place_recipe player_abilities player_action player_command player_input player_loaded pong punch recipe_book_change_settings recipe_book_seen_recipe rename_item resource_pack seen_advancements select_trade set_beacon set_carried_item set_command_block set_command_minecart set_creative_mode_slot set_game_rule set_jigsaw_block set_structure_block set_test_block sign_update spectator_action teleport_to_entity test_instance_block_action use_item_on use_item custom_click_action",
}
EXPECTED = [1, 2, 2, 6, 5, 21, 10, 144, 69]


def generate():
    root = Path(__file__).resolve().parents[1] / "src" / "packets" / "catalog"
    assert [len(names.split()) for names in INVENTORY.values()] == EXPECTED
    modules = {}
    references = []
    for (state, direction), names in INVENTORY.items():
        folder = root / state / direction
        folder.mkdir(parents=True, exist_ok=True)
        declarations = []
        for packet_id, name in enumerate(names.split()):
            content = (
                f"//! Protocol 777 {state} {direction}: `{name}` (0x{packet_id:02X}).\n"
                "//! Metadata only; codec support and gameplay acceptance are separate.\n"
                f"//! Source: {SOURCE}\n"
                "//! Inventory attribution: Minecraft Wiki contributors, CC BY-SA 3.0 Unported.\n\n"
                "use super::super::super::{Direction, PacketDescriptor, State};\n\n"
                f"pub const ID: i32 = 0x{packet_id:02X};\n"
                f"pub const DESCRIPTOR: PacketDescriptor = PacketDescriptor {{\n"
                f"    state: State::{state.title()},\n"
                f"    direction: Direction::{direction.title()},\n"
                f'    id: ID,\n    official_name: "{name}",\n}};\n'
            )
            (folder / f"{name}.rs").write_text(content, encoding="utf-8")
            declarations.append(f"pub mod {name};")
            references.append(f"    {state}::{direction}::{name}::DESCRIPTOR,")
        (folder / "mod.rs").write_text("\n".join(declarations) + "\n", encoding="utf-8")
        modules.setdefault(state, []).append(f"pub mod {direction};")
    for state, declarations in modules.items():
        (root / state / "mod.rs").write_text(
            "\n".join(declarations) + "\n", encoding="utf-8"
        )
    legacy = root / "handshaking" / "serverbound" / "legacy_server_list_ping.rs"
    legacy.write_text(
        "//! Nonstandard legacy ping, not a modern length-prefixed protocol-777 packet.\n"
        f"//! Source: {SOURCE}#Legacy_Server_List_Ping\n"
        "//! Attribution: Minecraft Wiki contributors, CC BY-SA 3.0 Unported.\n"
        "//! Metadata only; this does not enable legacy ping handling.\n\n"
        "pub const ID: u8 = 0xFE;\n",
        encoding="utf-8",
    )
    direction_mod = legacy.parent / "mod.rs"
    direction_mod.write_text(
        direction_mod.read_text(encoding="utf-8")
        + "pub mod legacy_server_list_ping;\n",
        encoding="utf-8",
    )
    facade = """//! State/direction-scoped packet inventory for Java 26.3 / protocol 777.
//! Metadata is not a claim of codec implementation or runtime support.

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum State { Handshaking, Status, Login, Configuration, Play }
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Direction { Clientbound, Serverbound }
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct PacketDescriptor {
    pub state: State,
    pub direction: Direction,
    pub id: i32,
    pub official_name: &'static str,
}
pub const PROTOCOL_VERSION: i32 = 777;
"""
    facade += f'pub const SOURCE: &str = "{SOURCE}";\n'
    facade += "\n".join(f"pub mod {state};" for state in modules) + "\n"
    facade += (
        "pub const PACKETS: &[PacketDescriptor] = &[\n"
        + "\n".join(references)
        + "\n];\n"
    )
    facade += """
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn inventory_is_complete_and_state_direction_ids_are_unique() {
        assert_eq!(PACKETS.len(), 260);
        let mut keys = std::collections::HashSet::new();
        for packet in PACKETS {
            assert!(keys.insert((packet.state, packet.direction, packet.id)));
            assert!(!packet.official_name.is_empty());
        }
        for (state, direction, count) in [
            (State::Handshaking, Direction::Serverbound, 1),
            (State::Status, Direction::Clientbound, 2),
            (State::Status, Direction::Serverbound, 2),
            (State::Login, Direction::Clientbound, 6),
            (State::Login, Direction::Serverbound, 5),
            (State::Configuration, Direction::Clientbound, 21),
            (State::Configuration, Direction::Serverbound, 10),
            (State::Play, Direction::Clientbound, 144),
            (State::Play, Direction::Serverbound, 69),
        ] {
            let ids: Vec<_> = PACKETS.iter().filter(|p| p.state == state && p.direction == direction).map(|p| p.id).collect();
            assert_eq!(ids, (0..count).collect::<Vec<_>>());
        }
        assert_eq!(play::serverbound::set_carried_item::ID, 0x36);
        assert_eq!(handshaking::serverbound::legacy_server_list_ping::ID, 0xFE);
    }
}
"""
    (root / "mod.rs").write_text(facade, encoding="utf-8")
    (root / "README.md").write_text(
        "# Protocol 777 packet file catalog\n\n"
        "One Rust metadata file per modern `(state, direction, ID)`, using official packet names. "
        "Shared packets have separate state-specific IDs. There are 260 modern entries and one "
        "separate nonstandard legacy ping file.\n\n"
        "These files do not implement codecs, move existing codec implementations, enable packets, "
        "or change roadmap completion status. Existing Login/Configuration/Play codecs remain authoritative "
        "for runtime behavior. Future codec extraction can use these per-packet boundaries.\n\n"
        f"Inventory source: [Minecraft Wiki revision 3810839]({SOURCE}), Java 26.3 / protocol 777. "
        "Packet names and IDs attributed to Minecraft Wiki contributors and wiki.vg lineage; "
        "this catalog is licensed under [CC BY-SA 3.0 Unported](https://creativecommons.org/licenses/by-sa/3.0/).\n\n"
        "Regenerate with `python tools/GeneratePacketCatalog.py`. The generator uses a checked-in "
        "inventory and does not require network access. Do not put gameplay policy or backend dependencies here.\n",
        encoding="utf-8",
    )
    print("Generated 260 modern packet files and 1 legacy ping file.")


if __name__ == "__main__":
    generate()
