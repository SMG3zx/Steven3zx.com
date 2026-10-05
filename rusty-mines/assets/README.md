# Official Java 26.3 Configuration export

`configuration-26.3.json` is a compact export of the **official vanilla Java 26.3
server**, not mcmeta's alphabetical registry lists. It contains 32 synchronized
registries, 432 entries, 15 tag registries, and 773 tags. It is 129746 bytes, UTF-8,
with a final LF. Array order within each registry defines the network IDs; do not
sort those entries. Tag registry/name maps are sorted only for reproducibility;
that does not change the numeric IDs in their values.

## Pinned provenance

- Official manifest: <https://piston-meta.mojang.com/mc/game/version_manifest_v2.json>
- Version metadata: <https://piston-meta.mojang.com/v1/packages/4fe1aa1ef8da1cb95c5bad1fb98890ca56dd8ca3/26.3.json>
  - Metadata SHA-1: `4fe1aa1ef8da1cb95c5bad1fb98890ca56dd8ca3`
  - Java 25, Minecraft 26.3, protocol 777.
- Server download: <https://piston-data.mojang.com/v1/objects/33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c/server.jar>
  - SHA-1: `33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c`
  - Size: 62294556 bytes.
- Checked-in export SHA-256:
  `287890f1dd0e3b8e7623fa8f0400f85a08310f891a0a69d4a2a3dc4c9d78ae83`

The download's SHA-1 was verified. The launcher verifies that SHA-1 and each
unpacked version/library JAR's SHA-256 against manifests inside the pinned bundle.
The extractor asserts the running game's version/protocol and reads the vanilla
pack's own `knownPackInfo`; it requires exactly `minecraft / core / 26.3`.

The extractor loads `RegistryDataLoader.WORLD_REGISTRIES` from the official vanilla
resource pack and invokes `RegistrySynchronization.packRegistries` with the known
core pack. This is the actual server packing path, preserving numeric registry
order. It asserts that every packed entry has **no NBT** after negotiation, and
that the emitted registry count equals `SYNCHRONIZED_REGISTRIES.size()`. New 26.3
registries including `world_clock` and `sulfur_cube_archetype` are included.

Tags are loaded for the existing static registries, and the official world-registry
loader loads dynamic tags. `TagNetworkSerialization.serializeTagsToNetwork` on
the resulting static/world layers determines the transmitted registries and IDs.
The export is vanilla only: experimental packs are not selected.

The built-in `net.minecraft.data.Main --reports --server` generator was also run
successfully as a finite extraction task. Its reports are useful for inspection,
but are **not** substituted for the actual synchronization path above. The exporter
was run twice and produced byte-identical output with the SHA-256 above.

## Reproduce (optional development tooling)

Use a Java **25 JDK**, not just a JRE. No Java dependency is needed to run Rusty
Mines; Minecraft, JDK downloads, unpacked libraries, and generated reports remain
under ignored `target/`, and are not checked in.

1. Create `target/configuration-source`, download the pinned server to `server.jar`
   there, and verify its SHA-1/size before executing it.
2. From that directory, run these **finite** commands with Java 25:

   ```sh
   java -DbundlerMainClass=net.minecraft.data.Main -jar server.jar --help
   java -DbundlerMainClass=net.minecraft.data.Main -jar server.jar --reports --server --output generated
   java ../../tools/RunConfigurationExtractor.java configuration-26.3.json
   ```

   The first command unpacks the official server/libraries. The source launcher
   compiles `tools/ExtractConfiguration.java` using only those verified bundled
   JARs, then runs it with an isolated classloader. It does not open a server socket,
   accept an EULA on the user's behalf, publish a database, or load a saved world.
3. Verify the generated export's SHA-256 above and compare it byte-for-byte with
   `assets/configuration-26.3.json` before replacing any asset.

The extraction here used a portable Eclipse Temurin 25.0.4.1+1 Windows x64 JDK.
Its ZIP SHA-256 was verified as
`00c847d804f4a78e9f04f2683faf14fed898535b177b7fc704486cb0284e9283`.
The JDK is external development tooling, not a redistributed project dependency.

## Corrective teleport fixtures

`teleport-26.3.json` uses the pinned official Java 26.3 / protocol 777 codecs.
Ten packets vary positive IDs (1, 127, 128, 16384 and 2147483647) and empty/all
relative flags, with signed coordinates, nonzero velocity and rotations. The
official codec decodes every packet with full consumption and re-encodes it
byte-for-byte; Rust tests compare their own encoder output to these bytes.
These samples do not prove graphical-client handling of every possible pose.
The gateway currently sends absolute corrections (relative flags zero).

SHA-256: `d959a75340de1d0526da1e136934bb07f318c03c5875487a64e9c54a349b278e`.

From `target/configuration-source` with the verified Java 25 toolchain:

```sh
java ../../tools/RunWorldExtractor.java world-26.3-verify.json teleport-26.3-verify.json
```

Compare the resulting files with checked-in assets before replacing them.

## Rights and compatibility

Minecraft names and vanilla identifier/tag relationships are derived from Mojang's
Minecraft distribution. Minecraft is copyright Mojang/Microsoft; this project is
not affiliated with or endorsed by them. No Minecraft JAR, class file, texture,
sound, or copied Java implementation is checked in. The project's Apache-2.0
license covers its own Rust/exporter code; it does **not** relicense Minecraft or
its distribution. Obtaining/using Minecraft remains subject to its own terms,
including <https://www.minecraft.net/eula> and applicable usage guidelines.

Only an exact echoed core pack is supported. Clients that decline it receive an
explicit Configuration Disconnect rather than omitted NBT they cannot resolve.
Official graphical-client interoperability remains untested. Configuration now
ends with an acknowledged finish and the static-world initialization described below.

## Official static-world packet export

`world-26.3.json` contains nine immutable clientbound Play packets, generated with
`tools/ExtractWorldInitialization.java` and the same pinned server/Java 25 toolchain.
It is 80810 UTF-8 bytes (final LF), SHA-256:
`3399bcbd9b4de58314c94a6de64c8675015b206b5e107e1e9f6be8aaa0169275`.
Two runs produced identical assets. The prior partial-task asset was verified
byte-for-byte before changing its adventure-mode login to creative flight.

From `target/configuration-source`, reproduce using:

```sh
java ../../tools/RunWorldExtractor.java world-26.3.json
```

The launcher verifies the server SHA-1 and every bundled JAR SHA-256 before
compilation/execution. The exporter binds the official Play codec against the
same world registries as Configuration and encodes/decodes every emitted packet.
It additionally writes, reads, and compares all 4096 block states of each of the
24 sections using official section codecs. Palettes use official block-state
IDs and the official plains biome holder. Heightmaps are constructed with official
9-bit storage (first air 65 relative to minY -64 gives 129); the packet constructor
and codec choose their actual network format. All 26 light sections are covered:
opaque terrain/below is dark; the air above receives sky light 15, block light 0.
The chunk packet is 40066 bytes, well below the 2 MiB frame limit.

The runtime caches the identical flat chunk body and patches only chunk coordinates
and the login entity ID; palette/light/heightmap encoding stays inside the pinned
asset. The extractor compares every coordinate-patched chunk byte-for-byte with
the official encoder and decodes a 25-chunk sample, checking coordinates and full
buffer consumption. Runtime windows are generated only within the configured cap.
The exporter also official-codec roundtrips protocol-777 serverbound teleport
confirmation, Player Loaded, tick end, batch acknowledgement, all four movement
variants, abilities, and chat-session metadata and prints their wire samples.
No Minecraft code, JARs, class files, or saved worlds are checked in.

`chunk-stream-26.3.json` adds official-codec fixtures for unload chunk, center
chunk, render distance, simulation distance, and chunk-batch completion. It pins
the same server SHA-1 and protocol, is 80924 bytes (final LF), and has SHA-256
`5828c36ffb6e2a25befcd44dcbd9e2e725f13c940b9cb885b6bbf3557da908f0`.
Reproduce it from `target/configuration-source` with
`java ../../tools/RunWorldExtractor.java ../../assets/chunk-stream-26.3.json`.

### Next Play verification

The same extractor now prints official-codec roundtrips for empty Punch, held slot,
all seven Player Command enum IDs, all input flags, Use Item (yaw/pitch), Use Item On,
Player Action and a multi-byte teleport confirmation (16384). It also prints
clientbound System Chat (including supplementary Unicode), block ACK and block
update samples. Verification packets are removed before writing the nine-packet
world asset, so no asset format or initialization payload changes are required.
The official default block-state IDs used for read-only correction are air=0,
bedrock=88, stone=1, grass_block[snowy=false]=9. These are emitted from
`Block.getId(defaultBlockState())`, not inferred from block registry IDs.

Field definitions were cross-checked against
<https://minecraft.wiki/w/Java_Edition_protocol/Packets> (protocol 777, accessed
2026-10-04) and the pinned official encoder. In particular official held-slot wire
sample is `360008`; `0x35` is Set Beacon Effect. Modern Creative Slot components
are intentionally not implemented; the runtime discards a bounded opaque payload
and does not claim to decode or validate its contents.

### Inventory extraction

`inventory-26.3.json` pins the same official server SHA-1 and protocol 777.
It contains all 1,658 item IDs/defaults, 122 component IDs, 69 default component
wire samples, five Slot samples and an empty 46-slot player inventory packet.
The extractor uses the official resource reload to bind item components before
reading defaults. Every emitted wire sample is decoded with full consumption
and byte-identically re-encoded; Slot samples also compare stack semantics.
JSON defaults and codec class names do not describe complete network schemas.
Full Rust component decoding remains pending; this asset grants no write permission.

Generate from `target/configuration-source` with the hash-verified launcher:

```powershell
& './jdk-25.0.4.1+1/bin/java.exe' '../../tools/RunWorldExtractor.java' 'world-check.json' 'teleport-check.json' '../../assets/inventory-26.3.json'
```

The first two outputs above are scratch files; the existing world and teleport
assets remain regression oracles. No Java classes or server JARs are committed.
