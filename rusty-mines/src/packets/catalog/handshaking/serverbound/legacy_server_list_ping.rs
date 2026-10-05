//! Nonstandard legacy ping, not a modern length-prefixed protocol-777 packet.
//! Source: https://minecraft.wiki/w/Java_Edition_protocol/Packets?oldid=3810839#Legacy_Server_List_Ping
//! Attribution: Minecraft Wiki contributors, CC BY-SA 3.0 Unported.
//! Metadata only; this does not enable legacy ping handling.

pub const ID: u8 = 0xFE;
