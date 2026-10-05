# Protocol 777 packet file catalog

One Rust metadata file per modern `(state, direction, ID)`, using official packet names. Shared packets have separate state-specific IDs. There are 260 modern entries and one separate nonstandard legacy ping file.

These files do not implement codecs, move existing codec implementations, enable packets, or change roadmap completion status. Existing Login/Configuration/Play codecs remain authoritative for runtime behavior. Future codec extraction can use these per-packet boundaries.

Inventory source: [Minecraft Wiki revision 3810839](https://minecraft.wiki/w/Java_Edition_protocol/Packets?oldid=3810839), Java 26.3 / protocol 777. Packet names and IDs attributed to Minecraft Wiki contributors and wiki.vg lineage; this catalog is licensed under [CC BY-SA 3.0 Unported](https://creativecommons.org/licenses/by-sa/3.0/).

Regenerate with `python tools/GeneratePacketCatalog.py`. The generator uses a checked-in inventory and does not require network access. Do not put gameplay policy or backend dependencies here.
