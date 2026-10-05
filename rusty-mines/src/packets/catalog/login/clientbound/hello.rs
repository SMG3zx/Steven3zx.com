//! Protocol 777 login clientbound: `hello` (0x01).
//! Metadata only; codec support and gameplay acceptance are separate.
//! Source: https://minecraft.wiki/w/Java_Edition_protocol/Packets?oldid=3810839
//! Inventory attribution: Minecraft Wiki contributors, CC BY-SA 3.0 Unported.

use super::super::super::{Direction, PacketDescriptor, State};

pub const ID: i32 = 0x01;
pub const DESCRIPTOR: PacketDescriptor = PacketDescriptor {
    state: State::Login,
    direction: Direction::Clientbound,
    id: ID,
    official_name: "hello",
};
