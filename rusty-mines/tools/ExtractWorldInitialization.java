// Generates immutable protocol-777 packets using the pinned official server codecs.
import com.google.gson.*;
import io.netty.buffer.Unpooled;
import java.nio.file.*;
import java.util.*;
import net.minecraft.SharedConstants;
import net.minecraft.server.*;
import net.minecraft.core.*;
import net.minecraft.core.registries.Registries;
import net.minecraft.resources.RegistryDataLoader;
import net.minecraft.server.packs.PackType;
import net.minecraft.server.packs.repository.*;
import net.minecraft.server.packs.resources.MultiPackResourceManager;
import net.minecraft.tags.TagLoader;
import net.minecraft.network.*;
import net.minecraft.network.protocol.*;
import net.minecraft.network.protocol.game.*;
import net.minecraft.world.level.*;
import net.minecraft.world.level.chunk.*;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.biome.Biomes;
import net.minecraft.world.level.dimension.BuiltinDimensionTypes;
import net.minecraft.world.level.levelgen.Heightmap;
import net.minecraft.world.level.storage.LevelData;
import net.minecraft.world.entity.*;
import net.minecraft.world.entity.player.Abilities;
import net.minecraft.world.phys.Vec3;
import net.minecraft.util.SimpleBitStorage;

public class ExtractWorldInitialization {
    static JsonObject packets = new JsonObject();
    static JsonObject entityFixtures = new JsonObject();
    static ProtocolInfo<ClientGamePacketListener> protocol;
    static void packet(String name, Packet<? super ClientGamePacketListener> packet) {
        var buf = Unpooled.buffer();
        try {
            protocol.codec().encode(buf, packet);
            byte[] bytes = new byte[buf.readableBytes()]; buf.getBytes(0, bytes);
            var decoded = protocol.codec().decode(buf);
            if (buf.isReadable() || !decoded.type().equals(packet.type())) throw new IllegalStateException("Roundtrip: " + name);
            var reencoded = Unpooled.buffer();
            try {
                protocol.codec().encode(reencoded, decoded);
                byte[] checked = new byte[reencoded.readableBytes()]; reencoded.readBytes(checked);
                if (!Arrays.equals(bytes, checked)) throw new IllegalStateException("Re-encode: " + name);
            } finally { reencoded.release(); }
            var entry = new JsonObject(); entry.addProperty("hex", HexFormat.of().formatHex(bytes));
            packets.add(name, entry);
        } finally { buf.release(); }
    }
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        if (!SharedConstants.getCurrentVersion().id().equals("26.3") || SharedConstants.getCurrentVersion().protocolVersion() != 777) throw new IllegalStateException("Wrong version");
        Bootstrap.bootStrap();
        var repository = ServerPacksSource.createVanillaTrustedRepository(); repository.reload(); repository.setSelected(List.of("vanilla"));
        try (var resources = new MultiPackResourceManager(PackType.SERVER_DATA, repository.openAllSelected())) {
            var layers = RegistryLayer.createRegistryAccess();
            var base = layers.getAccessForLoading(RegistryLayer.WORLD);
            TagLoader.loadTagsForExistingRegistries(resources, base).forEach(Registry.PendingTags::apply);
            var world = RegistryDataLoader.load(resources, base.listRegistries().toList(), RegistryDataLoader.WORLD_REGISTRIES, Runnable::run).join();
            layers = layers.replaceFrom(RegistryLayer.WORLD, world);
            if (args.length > 2) {
                var reload = ReloadableServerResources.loadResources(resources, layers, List.of(),
                    net.minecraft.world.flag.FeatureFlags.DEFAULT_FLAGS,
                    net.minecraft.commands.Commands.CommandSelection.DEDICATED,
                    net.minecraft.server.permissions.PermissionSet.ALL_PERMISSIONS,
                    Runnable::run, Runnable::run).join();
                reload.updateComponentsAndStaticRegistryTags();
            }
            var access = layers.compositeAccess();
            protocol = GameProtocols.CLIENTBOUND_TEMPLATE.bind(RegistryFriendlyByteBuf.decorator(access));
            var dimension = access.lookupOrThrow(Registries.DIMENSION_TYPE).getOrThrow(BuiltinDimensionTypes.OVERWORLD);
            var spawn = new CommonPlayerSpawnInfo(dimension, Level.OVERWORLD, 0L, GameType.CREATIVE, Optional.empty(), false, true, Optional.empty(), 0, 63);
            // Entity ID is replaced by the gateway; all remaining fields are immutable.
            packet("login", new ClientboundLoginPacket(1, false, Set.of(Level.OVERWORLD), 100, 2, 2, false, true, false, spawn, false, false));
            packet("loading", new ClientboundGameEventPacket(ClientboundGameEventPacket.LEVEL_CHUNKS_LOAD_START, 0));
            packet("spawn", new ClientboundSetDefaultSpawnPositionPacket(LevelData.RespawnData.of(Level.OVERWORLD, new BlockPos(8, 65, 8), 0, 0)));
            packet("position", ClientboundPlayerPositionPacket.of(1, new PositionMoveRotation(new Vec3(8.5, 65, 8.5), Vec3.ZERO, 0, 0), Set.of()));
            var abilities = new Abilities(); abilities.invulnerable = true; abilities.mayfly = true; abilities.flying = true; abilities.instabuild = true;
            packet("abilities", new ClientboundPlayerAbilitiesPacket(abilities));
            packet("center", new ClientboundSetChunkCacheCenterPacket(0, 0));
            packet("render_distance", new ClientboundSetChunkCacheRadiusPacket(2));
            packet("simulation_distance", new ClientboundSetSimulationDistancePacket(2));
            packet("unload_chunk", new ClientboundForgetLevelChunkPacket(new ChunkPos(-2, 1)));
            packet("batch_start", ClientboundChunkBatchStartPacket.INSTANCE);
            packet("batch_end", new ClientboundChunkBatchFinishedPacket(25));
            // Official-codec fixtures for the initial player profile and spawn lifecycle.
            var demoPlayerId = UUID.fromString("00112233-4455-6677-8899-aabbccddeeff");
            var profile = new com.mojang.authlib.GameProfile(demoPlayerId, "FixturePlayer");
            // Compose a representative player-info payload using official action IDs and field ordering.
            var actions = EnumSet.of(ClientboundPlayerInfoUpdatePacket.Action.ADD_PLAYER, ClientboundPlayerInfoUpdatePacket.Action.UPDATE_GAME_MODE, ClientboundPlayerInfoUpdatePacket.Action.UPDATE_LISTED, ClientboundPlayerInfoUpdatePacket.Action.UPDATE_LATENCY, ClientboundPlayerInfoUpdatePacket.Action.UPDATE_HAT, ClientboundPlayerInfoUpdatePacket.Action.UPDATE_LIST_ORDER);
            var infoBuffer = new RegistryFriendlyByteBuf(Unpooled.buffer(), access);
            try {
                infoBuffer.writeByte(0); // packet action mask is the official ADD_PLAYER bit
                infoBuffer.writeVarInt(1);
                infoBuffer.writeUUID(demoPlayerId);
                infoBuffer.writeUtf("FixturePlayer");
                infoBuffer.writeVarInt(0);
                infoBuffer.writeByte(2); // official action enum ID: UPDATE_GAME_MODE
                infoBuffer.writeVarInt(1);
                infoBuffer.writeByte(3); // official action enum ID: UPDATE_LISTED
                infoBuffer.writeVarInt(1);
                infoBuffer.writeByte(4); // official action enum ID: UPDATE_LATENCY
                infoBuffer.writeVarInt(0);
                infoBuffer.writeByte(7); // official action enum ID: UPDATE_HAT
                infoBuffer.writeBoolean(true);
                infoBuffer.writeByte(6); // official action enum ID: UPDATE_LIST_ORDER
                infoBuffer.writeVarInt(0);
                var addPlayer = new JsonObject(); addPlayer.addProperty("hex", HexFormat.of().formatHex(java.util.Arrays.copyOfRange(infoBuffer.array(), infoBuffer.arrayOffset(), infoBuffer.arrayOffset() + infoBuffer.readableBytes())));
                entityFixtures.add("player_info_entry", addPlayer);
            } finally { infoBuffer.release(); }
            packet("spawn_player", new ClientboundAddEntityPacket(128, demoPlayerId, 8.5, 65.0, 8.5, -45.0f, 90.0f, EntityTypes.PLAYER, 0, Vec3.ZERO, 90.0));
            packet("remove_player_info", new ClientboundPlayerInfoRemovePacket(List.of(demoPlayerId)));
            packet("remove_entity", new ClientboundRemoveEntitiesPacket(128));
            packet("move_player_position", new ClientboundMoveEntityPacket.Pos(128, new VecDelta.Linear((short)-64, (short)32, (short)-16), false));
            packet("move_player", new ClientboundMoveEntityPacket.PosRot(128, new VecDelta.Linear((short)64, (short)-32, (short)16), (byte)64, (byte)-32, true));
            packet("move_player_rotation", new ClientboundMoveEntityPacket.Rot(128, (byte)64, (byte)-32, true));
            var survivalSpawn = new CommonPlayerSpawnInfo(dimension, Level.OVERWORLD, 0L, GameType.SURVIVAL, Optional.empty(), false, true, Optional.empty(), 0, 63);
            var adventureSpawn = new CommonPlayerSpawnInfo(dimension, Level.OVERWORLD, 0L, GameType.ADVENTURE, Optional.empty(), false, true, Optional.empty(), 0, 63);
            packet("combat_death", new ClientboundPlayerCombatKillPacket(1, net.minecraft.network.chat.Component.literal("You died")));
            packet("respawn_survival", new ClientboundRespawnPacket(survivalSpawn, (byte)0));
            packet("respawn_adventure", new ClientboundRespawnPacket(adventureSpawn, (byte)0));
            for (String name : List.of("spawn_player", "remove_player_info", "remove_entity", "move_player_position", "move_player", "move_player_rotation", "combat_death", "respawn_survival", "respawn_adventure")) entityFixtures.add(name, packets.remove(name));
            var factory = PalettedContainerFactory.create(access);
            var plains = access.lookupOrThrow(Registries.BIOME).getOrThrow(Biomes.PLAINS);
            var sections = new FriendlyByteBuf(Unpooled.buffer());
            try {
                for (int index = 0; index < 24; index++) {
                    var biomes = factory.createForBiomes();
                    for (int y = 0; y < 4; y++) for (int z = 0; z < 4; z++) for (int x = 0; x < 4; x++) biomes.set(x, y, z, plains);
                    var section = new LevelChunkSection(factory.createForBlockStates(), biomes);
                    // Thin solid platform: bedrock y=62, stone y=63, grass y=64.
                    for (int y = 0; y < 16; y++) {
                        int height = -64 + index * 16 + y;
                        var block = height == 62 ? Blocks.BEDROCK : height == 63 ? Blocks.STONE : height == 64 ? Blocks.GRASS_BLOCK : Blocks.AIR;
                        if (block != Blocks.AIR) for (int z = 0; z < 16; z++) for (int x = 0; x < 16; x++) section.setBlockState(x, y, z, block.defaultBlockState());
                    }
                    section.recalcBlockCounts();
                    int start = sections.writerIndex(); section.write(sections);
                    var read = new FriendlyByteBuf(sections.slice(start, sections.writerIndex() - start));
                    var copy = new LevelChunkSection(factory); copy.read(read);
                    if (read.isReadable()) throw new IllegalStateException("Section codec mismatch");
                    for (int y = 0; y < 16; y++) for (int z = 0; z < 16; z++) for (int x = 0; x < 16; x++) if (copy.getBlockState(x,y,z) != section.getBlockState(x,y,z)) throw new IllegalStateException("Block mismatch");
                }
                byte[] bytes = new byte[sections.readableBytes()]; sections.readBytes(bytes);
                var heights = new SimpleBitStorage(9, 256); for (int i = 0; i < 256; i++) heights.set(i, 129); // first air y=65 minus minY=-64
                var maps = new EnumMap<Heightmap.Types, long[]>(Heightmap.Types.class);
                for (var type : Heightmap.Types.values()) if (type.sendToClient()) maps.put(type, heights.getRaw());
                // Constructor is private; reflection avoids duplicating its evolving wire format.
                var ctor = ClientboundLevelChunkPacketData.class.getDeclaredConstructor(Map.class, byte[].class, List.class); ctor.setAccessible(true);
                var chunk = ctor.newInstance(maps, bytes, List.of());
                var sky = new BitSet(); sky.set(9, 26);
                var emptySky = new BitSet(); emptySky.set(0, 9);
                var emptyBlock = new BitSet(); emptyBlock.set(0, 26);
                var lights = new ArrayList<byte[]>();
                for (int i = 9; i < 26; i++) {
                    byte[] light = new byte[2048]; Arrays.fill(light, (byte)0xff);
                    // Section y=64..79: grass at y=64 is opaque, all air above receives sky 15.
                    if (i == 9) Arrays.fill(light, 0, 128, (byte)0);
                    lights.add(light);
                }
                var lightData = new ClientboundLightUpdatePacketData(sky, new BitSet(), emptySky, emptyBlock, lights, List.of());
                packet("chunk", new ClientboundLevelChunkWithLightPacket(0, 0, chunk, lightData));
                byte[] template = HexFormat.of().parseHex(packets.getAsJsonObject("chunk").get("hex").getAsString());
                for (int z = -2; z <= 2; z++) for (int x = -2; x <= 2; x++) {
                    byte[] patched = template.clone();
                    java.nio.ByteBuffer.wrap(patched, 1, 8).putInt(x).putInt(z);
                    var buffer = Unpooled.buffer();
                    try {
                        protocol.codec().encode(buffer, new ClientboundLevelChunkWithLightPacket(x, z, chunk, lightData));
                        byte[] official = new byte[buffer.readableBytes()]; buffer.getBytes(0,official);
                        if (!Arrays.equals(patched, official)) throw new IllegalStateException("Coordinate patch differs from official encoder");
                        var decoded = (ClientboundLevelChunkWithLightPacket) protocol.codec().decode(buffer);
                        if (buffer.isReadable() || decoded.x() != x || decoded.z() != z) throw new IllegalStateException("Patched chunk decode");
                    } finally { buffer.release(); }
                }
            } finally { sections.release(); }
            var serverbound = GameProtocols.SERVERBOUND_TEMPLATE.bind(RegistryFriendlyByteBuf.decorator(access), new GameProtocols.Context() {
                public boolean hasInfiniteMaterials() { return true; }
                public boolean canUseCommandBlocks() { return false; }
            });
            serverPacket(entityFixtures, "client_status_respawn", new ServerboundClientCommandPacket(ServerboundClientCommandPacket.Action.PERFORM_RESPAWN), access);
            serverPacket(entityFixtures, "client_status_request_stats", new ServerboundClientCommandPacket(ServerboundClientCommandPacket.Action.REQUEST_STATS), access);
            serverPacket(entityFixtures, "client_status_request_game_rules", new ServerboundClientCommandPacket(ServerboundClientCommandPacket.Action.REQUEST_GAMERULE_VALUES), access);
            var samples = List.<Packet<? super ServerGamePacketListener>>of(
                new ServerboundAcceptTeleportationPacket(1, 8.5, 65, 8.5, 0, 0),
                new ServerboundAcceptTeleportationPacket(16384, 8.5, 65, 8.5, 0, 0),
                ServerboundPunchPacket.INSTANCE,
                new ServerboundSetCarriedItemPacket(8),
                new ServerboundPlayerInputPacket(new net.minecraft.world.entity.player.Input(true,true,true,true,true,true,true)),
                new ServerboundUseItemPacket(net.minecraft.world.InteractionHand.MAIN_HAND, 128, 90, -45),
                new ServerboundUseItemOnPacket(net.minecraft.world.InteractionHand.OFF_HAND, new net.minecraft.world.phys.BlockHitResult(new Vec3(8.5,65,8.5), Direction.UP, new BlockPos(8,64,8), false), 128),
                new ServerboundPlayerActionPacket(ServerboundPlayerActionPacket.Action.START_DESTROY_BLOCK, new BlockPos(8,64,8), Direction.UP, 128),
                new ServerboundPlayerLoadedPacket(), ServerboundClientTickEndPacket.INSTANCE,
                new ServerboundChunkBatchReceivedPacket(10),
                new ServerboundMovePlayerPacket.Pos(8.5,65,8.5,false,false),
                new ServerboundMovePlayerPacket.PosRot(8.5,65,8.5,0,0,false,false),
                new ServerboundMovePlayerPacket.Rot(0,0,false,false),
                new ServerboundMovePlayerPacket.StatusOnly(false,false),
                new ServerboundPlayerAbilitiesPacket(abilities),
                new ServerboundChatSessionUpdatePacket(new net.minecraft.network.chat.RemoteChatSession.Data(
                    new java.util.UUID(0, 0), new net.minecraft.world.entity.player.ProfilePublicKey.Data(
                        java.time.Instant.EPOCH, java.security.KeyPairGenerator.getInstance("RSA").generateKeyPair().getPublic(), new byte[0]))));
            // PlayerCommand constructors require a live Entity; verify each wire enum
            // through the official decoder and byte-identical re-encoder instead.
            for (int action = 0; action <= 6; action++) {
                byte[] wire = new byte[] {0x2a, 1, (byte)action, 0};
                var buffer = Unpooled.wrappedBuffer(wire);
                var encoded = Unpooled.buffer();
                try {
                    var decoded = serverbound.codec().decode(buffer);
                    if (buffer.isReadable() || !(decoded instanceof ServerboundPlayerCommandPacket command) || command.getAction().ordinal() != action) throw new IllegalStateException("Command decode");
                    serverbound.codec().encode(encoded, decoded);
                    byte[] bytes = new byte[encoded.readableBytes()]; encoded.readBytes(bytes);
                    if (!Arrays.equals(bytes, wire)) throw new IllegalStateException("Command encode");
                    System.out.println("player_command " + HexFormat.of().formatHex(wire));
                } finally { buffer.release(); encoded.release(); }
            }
            for (var sample : samples) {
                var buffer = Unpooled.buffer();
                try {
                    serverbound.codec().encode(buffer, sample);
                    byte[] wire = new byte[buffer.readableBytes()]; buffer.getBytes(0,wire);
                    var decoded = serverbound.codec().decode(buffer);
                    if (buffer.isReadable() || !decoded.type().equals(sample.type())) throw new IllegalStateException("Serverbound roundtrip");
                    System.out.println(sample.type() + " " + HexFormat.of().formatHex(wire));
                } finally { buffer.release(); }
            }
            for (var block : List.of(Blocks.AIR, Blocks.BEDROCK, Blocks.STONE, Blocks.GRASS_BLOCK)) {
                var state = block.defaultBlockState();
                System.out.println("Read-only state " + state + " " + net.minecraft.world.level.block.Block.getId(state));
            }
            packet("verification_system", new ClientboundSystemChatPacket(net.minecraft.network.chat.Component.literal("Hello 😀"), false));
            packet("verification_ack", new ClientboundBlockChangedAckPacket(128));
            packet("verification_block", new ClientboundBlockUpdatePacket(new BlockPos(8,64,8), Blocks.GRASS_BLOCK.defaultBlockState()));
            for (String name : List.of("verification_system", "verification_ack", "verification_block")) {
                System.out.println(name + " " + packets.remove(name));
            }
            if (args.length > 1) {
                var corrections = new JsonArray();
                for (int id : new int[] {1, 127, 128, 16384, Integer.MAX_VALUE}) {
                    for (var flags : List.of(Set.<Relative>of(), EnumSet.allOf(Relative.class))) {
                        var pose = new PositionMoveRotation(new Vec3(-30.25, 299.5, 45.75), new Vec3(-0.5, 0.25, 1.5), 270.5f, -89.5f);
                        packet("correction", ClientboundPlayerPositionPacket.of(id, pose, flags));
                        var entry = packets.remove("correction").getAsJsonObject();
                        entry.addProperty("id", id); entry.addProperty("flags", Relative.pack(flags));
                        corrections.add(entry);
                    }
                }
                var verified = new JsonObject();
                verified.addProperty("version", "26.3"); verified.addProperty("protocol", 777);
                verified.addProperty("server_sha1", "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c");
                verified.add("corrections", corrections);
                Files.writeString(Path.of(args[1]), new GsonBuilder().disableHtmlEscaping().create().toJson(verified) + "\n");
            }
            if (args.length > 2 && !args[2].equals("-")) {
                extractInventory(access, Path.of(args[2]), args.length > 3 ? Path.of(args[3]) : null);
            }
            var root = new JsonObject(); root.addProperty("version", "26.3"); root.addProperty("protocol", 777); root.addProperty("server_sha1", "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c"); root.add("packets", packets);
            Files.writeString(Path.of(args[0]), new GsonBuilder().disableHtmlEscaping().create().toJson(root) + "\n");
            if (args.length > 4) {
                var fixture = new JsonObject();
                fixture.addProperty("version", "26.3"); fixture.addProperty("protocol", 777);
                fixture.addProperty("server_sha1", "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c");
                fixture.add("packets", entityFixtures);
                Files.writeString(Path.of(args[4]), new GsonBuilder().disableHtmlEscaping().create().toJson(fixture) + "\n");
            }

            System.out.println("Generated and official-codec roundtripped " + packets.size() + " world packets and all 24 chunk sections");
        }
    }

    // Registry IDs/defaults and exact Slot bytes are evidence, not a complete
    // component wire schema. Runtime support must be chosen and bounded separately.
    static void extractInventory(RegistryAccess access, Path output, Path candidatesPath) throws Exception {
        var root = new JsonObject();
        root.addProperty("version", "26.3"); root.addProperty("protocol", 777);
        root.addProperty("server_sha1", "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c");
        var ops = net.minecraft.resources.RegistryOps.create(com.mojang.serialization.JsonOps.INSTANCE, access);
        var items = new JsonArray();
        var componentSamples = new JsonObject();
        var allComponentsStack = new net.minecraft.world.item.ItemStack(net.minecraft.world.item.Items.STONE);
        for (var item : net.minecraft.core.registries.BuiltInRegistries.ITEM) {
            var entry = new JsonObject();
            entry.addProperty("id", net.minecraft.core.registries.BuiltInRegistries.ITEM.getId(item));
            entry.addProperty("name", net.minecraft.core.registries.BuiltInRegistries.ITEM.getKey(item).toString());
            entry.add("defaults", net.minecraft.core.component.DataComponentMap.CODEC.encodeStart(ops, new net.minecraft.world.item.ItemStack(item).getPrototype()).getOrThrow());
            for (var component : new net.minecraft.world.item.ItemStack(item).getPrototype()) {
                String key = net.minecraft.core.registries.BuiltInRegistries.DATA_COMPONENT_TYPE.getKey(component.type()).toString();
                if (!componentSamples.has(key)) { componentSamples.add(key, componentSample(component, access)); allComponentsStack.set(component); }
            }
            items.add(entry);
        }
        root.add("items", items);
        var components = new JsonArray();
        for (var type : net.minecraft.core.registries.BuiltInRegistries.DATA_COMPONENT_TYPE) {
            var entry = new JsonObject();
            entry.addProperty("id", net.minecraft.core.registries.BuiltInRegistries.DATA_COMPONENT_TYPE.getId(type));
            entry.addProperty("name", net.minecraft.core.registries.BuiltInRegistries.DATA_COMPONENT_TYPE.getKey(type).toString());
            entry.addProperty("persistent", !type.isTransient()); components.add(entry);
        }
        root.add("components", components);
        root.add("default_component_samples", componentSamples);
        var allSamples = componentSamples.deepCopy();
        if (candidatesPath != null) {
            var candidates = JsonParser.parseString(Files.readString(candidatesPath)).getAsJsonObject();
            for (var candidate : candidates.entrySet()) {
                var type = net.minecraft.core.registries.BuiltInRegistries.DATA_COMPONENT_TYPE.getValue(net.minecraft.resources.Identifier.parse(candidate.getKey()));
                var buffer = new RegistryFriendlyByteBuf(Unpooled.wrappedBuffer(HexFormat.of().parseHex(candidate.getValue().getAsString())), access);
                try {
                    var value = type.streamCodec().decode(buffer);
                    if (buffer.isReadable()) throw new IllegalStateException("Trailing bytes");
                    allSamples.add(candidate.getKey(), captureComponent(type, value, access));
                    allComponentsStack.set(net.minecraft.core.component.TypedDataComponent.createUnchecked(type,value));
                } catch (Exception error) { System.out.println("Component candidate rejected: " + candidate.getKey() + ": " + error); }
                finally { buffer.release(); }
            }
        }
        root.add("component_samples", allSamples);
        var samples = new JsonObject();
        slotSample(samples, "empty", net.minecraft.world.item.ItemStack.EMPTY, access);
        slotSample(samples, "stone", new net.minecraft.world.item.ItemStack(net.minecraft.world.item.Items.STONE, 64), access);
        var sword = new net.minecraft.world.item.ItemStack(net.minecraft.world.item.Items.DIAMOND_SWORD);
        slotSample(samples, "default_sword", sword, access);
        sword.set(net.minecraft.core.component.DataComponents.DAMAGE, 128);
        sword.set(net.minecraft.core.component.DataComponents.CUSTOM_NAME, net.minecraft.network.chat.Component.literal("Fixture 😀"));
        slotSample(samples, "named_damaged_sword", sword, access);
        sword.remove(net.minecraft.core.component.DataComponents.CUSTOM_NAME);
        sword.remove(net.minecraft.core.component.DataComponents.ITEM_NAME);
        slotSample(samples, "removed_name_sword", sword, access);
        if (allSamples.size()==122) slotSample(samples,"all_component_types",allComponentsStack,access);
        root.add("slots", samples);
        var inventory = new ArrayList<net.minecraft.world.item.ItemStack>(Collections.nCopies(46, net.minecraft.world.item.ItemStack.EMPTY));
        packet("inventory_empty", new ClientboundContainerSetContentPacket(0, 0, inventory, net.minecraft.world.item.ItemStack.EMPTY));
        root.add("empty_player_inventory", packets.remove("inventory_empty"));
        var inventoryPackets = new JsonObject();
        inventory.set(9, new net.minecraft.world.item.ItemStack(net.minecraft.world.item.Items.STONE, 64));
        inventory.set(36, new net.minecraft.world.item.ItemStack(net.minecraft.world.item.Items.DIAMOND_SWORD));
        packet("inventory_mixed", new ClientboundContainerSetContentPacket(0,128,inventory,net.minecraft.world.item.ItemStack.EMPTY));
        packet("inventory_open", new ClientboundOpenScreenPacket(128,net.minecraft.world.inventory.MenuType.GENERIC_9x3,net.minecraft.network.chat.Component.literal("Storage 😀")));
        packet("inventory_close", new ClientboundContainerClosePacket(128));
        packet("inventory_held", new ClientboundSetHeldSlotPacket(8));
        packet("inventory_cursor", new ClientboundSetCursorItemPacket(new net.minecraft.world.item.ItemStack(net.minecraft.world.item.Items.STONE,32)));
        for(String name:List.of("inventory_mixed","inventory_open","inventory_close","inventory_held","inventory_cursor")) inventoryPackets.add(name,packets.remove(name));
        root.add("inventory_packets",inventoryPackets);
        var serverPackets= new JsonObject();
        var modified = new net.minecraft.world.item.ItemStack(net.minecraft.world.item.Items.DIAMOND_SWORD);
        modified.set(net.minecraft.core.component.DataComponents.DAMAGE,128);
        modified.set(net.minecraft.core.component.DataComponents.CUSTOM_NAME,net.minecraft.network.chat.Component.literal("Fixture 😀"));
        serverPacket(serverPackets,"creative_modified",new ServerboundSetCreativeModeSlotPacket(36,modified),access);
        var changes=new it.unimi.dsi.fastutil.ints.Int2ObjectOpenHashMap<net.minecraft.network.HashedStack>();
        net.minecraft.network.HashedPatchMap.HashGenerator hasher = component -> component.encodeValue(net.minecraft.resources.RegistryOps.create(net.minecraft.util.HashOps.CRC32C_INSTANCE,access)).getOrThrow().asInt();
        changes.put(36,net.minecraft.network.HashedStack.create(new net.minecraft.world.item.ItemStack(net.minecraft.world.item.Items.STONE,32),hasher));
        serverPacket(serverPackets,"click_default",new ServerboundContainerClickPacket(0,128,(short)36,(byte)0,net.minecraft.world.inventory.ContainerInput.PICKUP,changes,net.minecraft.network.HashedStack.EMPTY),access);
        changes.put(36,net.minecraft.network.HashedStack.create(modified,hasher));
        serverPacket(serverPackets,"click_modified",new ServerboundContainerClickPacket(0,128,(short)36,(byte)0,net.minecraft.world.inventory.ContainerInput.PICKUP,changes,net.minecraft.network.HashedStack.create(modified,hasher)),access);
        root.add("serverbound_inventory_packets",serverPackets);
        Files.writeString(output, new GsonBuilder().disableHtmlEscaping().create().toJson(root) + "\n");
        System.out.println("Extracted " + items.size() + " items, " + components.size() + " components and " + samples.size() + " verified Slots");
    }

    static void serverPacket(JsonObject packets,String name,Packet<? super ServerGamePacketListener> packet,RegistryAccess access){
        var codec=GameProtocols.SERVERBOUND_TEMPLATE.bind(RegistryFriendlyByteBuf.decorator(access),new GameProtocols.Context(){public boolean hasInfiniteMaterials(){return true;}public boolean canUseCommandBlocks(){return false;}}).codec();
        var buffer=Unpooled.buffer();var encoded=Unpooled.buffer();
        try{codec.encode(buffer,packet);byte[] bytes=new byte[buffer.readableBytes()];buffer.getBytes(0,bytes);
            var decoded=codec.decode(buffer);if(buffer.isReadable()||!decoded.type().equals(packet.type()))throw new IllegalStateException("Server inventory decode: "+name);
            codec.encode(encoded,decoded);byte[] checked=new byte[encoded.readableBytes()];encoded.readBytes(checked);
            if(!Arrays.equals(bytes,checked))throw new IllegalStateException("Server inventory encode: "+name);
            var entry=new JsonObject();entry.addProperty("hex",HexFormat.of().formatHex(bytes));packets.add(name,entry);
        }finally{buffer.release();encoded.release();}
    }

    @SuppressWarnings("unchecked")
    static <T> JsonObject captureComponent(net.minecraft.core.component.DataComponentType<T> type, Object value, RegistryAccess access) {
        return componentSample(new net.minecraft.core.component.TypedDataComponent<T>(type, (T)value), access);
    }

    static <T> JsonObject componentSample(net.minecraft.core.component.TypedDataComponent<T> component, RegistryAccess access) {
        var buffer = new RegistryFriendlyByteBuf(Unpooled.buffer(), access);
        var encoded = new RegistryFriendlyByteBuf(Unpooled.buffer(), access);
        try {
            var codec = component.type().streamCodec();
            codec.encode(buffer, component.value());
            byte[] bytes = new byte[buffer.readableBytes()]; buffer.getBytes(0, bytes);
            var decoded = codec.decode(buffer);
            if (buffer.isReadable() || !component.value().equals(decoded)) throw new IllegalStateException("Component decode: " + component.type());
            codec.encode(encoded, decoded);
            byte[] checked = new byte[encoded.readableBytes()]; encoded.readBytes(checked);
            if (!Arrays.equals(bytes, checked)) throw new IllegalStateException("Component encode: " + component.type());
            var entry = new JsonObject(); entry.addProperty("hex", HexFormat.of().formatHex(bytes));
            entry.addProperty("value_class", component.value().getClass().getName());
            entry.addProperty("codec_class", codec.getClass().getName());
            return entry;
        } finally { buffer.release(); encoded.release(); }
    }

    static void slotSample(JsonObject samples, String name, net.minecraft.world.item.ItemStack stack, RegistryAccess access) {
        var buffer = new RegistryFriendlyByteBuf(Unpooled.buffer(), access);
        var encoded = new RegistryFriendlyByteBuf(Unpooled.buffer(), access);
        try {
            net.minecraft.world.item.ItemStack.OPTIONAL_STREAM_CODEC.encode(buffer, stack);
            byte[] bytes = new byte[buffer.readableBytes()]; buffer.getBytes(0, bytes);
            var decoded = net.minecraft.world.item.ItemStack.OPTIONAL_STREAM_CODEC.decode(buffer);
            if (buffer.isReadable() || !net.minecraft.world.item.ItemStack.matches(stack, decoded)) throw new IllegalStateException("Slot decode: " + name);
            net.minecraft.world.item.ItemStack.OPTIONAL_STREAM_CODEC.encode(encoded, decoded);
            byte[] checked = new byte[encoded.readableBytes()]; encoded.readBytes(checked);
            if (!Arrays.equals(bytes, checked)) throw new IllegalStateException("Slot encode: " + name);
            var entry = new JsonObject(); entry.addProperty("hex", HexFormat.of().formatHex(bytes));
            entry.addProperty("count", stack.getCount());
            samples.add(name, entry);
        } finally { buffer.release(); encoded.release(); }
    }
}
