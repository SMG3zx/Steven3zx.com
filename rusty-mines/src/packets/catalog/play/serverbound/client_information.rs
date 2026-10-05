//! Protocol 777 play serverbound: `client_information` (0x0E).
//! Metadata only; codec support and gameplay acceptance are separate.
//! Source: https://minecraft.wiki/w/Java_Edition_protocol/Packets?oldid=3810839
//! Inventory attribution: Minecraft Wiki contributors, CC BY-SA 3.0 Unported.

use super::super::super::{Direction, PacketDescriptor, State};

pub const ID: i32 = 0x0E;
pub const DESCRIPTOR: PacketDescriptor = PacketDescriptor {
    state: State::Play,
    direction: Direction::Serverbound,
    id: ID,
    official_name: "client_information",
};
