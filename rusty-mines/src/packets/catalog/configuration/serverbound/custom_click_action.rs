//! Protocol 777 configuration serverbound: `custom_click_action` (0x08).
//! Metadata only; codec support and gameplay acceptance are separate.
//! Source: https://minecraft.wiki/w/Java_Edition_protocol/Packets?oldid=3810839#Custom_Click_Action
//! Inventory attribution: Minecraft Wiki contributors, CC BY-SA 3.0 Unported.

use super::super::super::{Direction, PacketDescriptor, State};

pub const ID: i32 = 0x08;
pub const DESCRIPTOR: PacketDescriptor = PacketDescriptor {
    state: State::Configuration,
    direction: Direction::Serverbound,
    id: ID,
    official_name: "custom_click_action",
};
