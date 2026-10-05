// Run only against the SHA-1-pinned official 26.3 server and its bundled libraries.
import com.google.gson.*;
import java.nio.file.*;
import java.util.*;
import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.server.RegistryLayer;
import net.minecraft.core.*;
import net.minecraft.nbt.NbtOps;
import net.minecraft.resources.RegistryDataLoader;
import net.minecraft.server.packs.PackType;
import net.minecraft.server.packs.repository.*;
import net.minecraft.server.packs.resources.MultiPackResourceManager;
import net.minecraft.tags.*;

public class ExtractConfiguration {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        var version = SharedConstants.getCurrentVersion();
        if (!version.id().equals("26.3") || version.protocolVersion() != 777) throw new IllegalStateException("Wrong game version");
        Bootstrap.bootStrap();
        var repository = ServerPacksSource.createVanillaTrustedRepository();
        repository.reload();
        repository.setSelected(List.of("vanilla"));
        var selected = repository.getSelectedPacks();
        if (selected.size() != 1) throw new IllegalStateException("Not exclusively vanilla");
        var core = selected.iterator().next().location().knownPackInfo().orElseThrow();
        if (!core.equals(new KnownPack("minecraft", "core", "26.3"))) throw new IllegalStateException("Wrong core: " + core);
        JsonObject root = new JsonObject();
        root.addProperty("version", "26.3");
        root.addProperty("protocol", 777);
        root.addProperty("server_sha1", "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c");
        JsonObject pack = new JsonObject();
        pack.addProperty("namespace", core.namespace()); pack.addProperty("name", core.id()); pack.addProperty("version", core.version());
        root.add("core", pack);
        try (var resources = new MultiPackResourceManager(PackType.SERVER_DATA, repository.openAllSelected())) {
            var layers = RegistryLayer.createRegistryAccess();
            var base = layers.getAccessForLoading(RegistryLayer.WORLD);
            TagLoader.loadTagsForExistingRegistries(resources, base).forEach(Registry.PendingTags::apply);
            var world = RegistryDataLoader.load(resources, base.listRegistries().toList(), RegistryDataLoader.WORLD_REGISTRIES, Runnable::run).join();
            layers = layers.replaceFrom(RegistryLayer.WORLD, world);
            JsonArray registries = new JsonArray();
            // This is the server's actual synchronization path: ID order and pack omission
            // are determined by Mojang's code, not sorted filenames or summary lists.
            RegistrySynchronization.packRegistries(NbtOps.INSTANCE, world, Set.of(core), (key, entries) -> {
                JsonObject registry = new JsonObject(); registry.addProperty("registry", key.identifier().toString());
                JsonArray ids = new JsonArray();
                for (var entry : entries) {
                    if (entry.data().isPresent()) throw new IllegalStateException("Entry not supplied by known core: " + entry.id());
                    ids.add(entry.id().toString());
                }
                registry.add("entries", ids); registries.add(registry);
            });
            if (registries.size() != RegistryDataLoader.SYNCHRONIZED_REGISTRIES.size()) throw new IllegalStateException("Missing synchronized registry");
            root.add("registries", registries);
            JsonArray tags = new JsonArray();
            var networkTags = TagNetworkSerialization.serializeTagsToNetwork(layers);
            networkTags.entrySet().stream().sorted(Comparator.comparing(e -> e.getKey().identifier().toString())).forEach(e -> {
                JsonObject registry = new JsonObject(); registry.addProperty("registry", e.getKey().identifier().toString());
                JsonArray list = new JsonArray();
                e.getValue().tags().entrySet().stream().sorted(Comparator.comparing(t -> t.getKey().toString())).forEach(t -> {
                    JsonObject tag = new JsonObject(); tag.addProperty("id", t.getKey().toString());
                    JsonArray ids = new JsonArray(); for (int id : t.getValue()) ids.add(id);
                    tag.add("entries", ids); list.add(tag);
                });
                registry.add("tags", list); tags.add(registry);
            });
            root.add("tags", tags);
            Files.writeString(Path.of(args[0]), new GsonBuilder().disableHtmlEscaping().create().toJson(root) + "\n");
            int entryCount = registries.asList().stream().mapToInt(r -> r.getAsJsonObject().getAsJsonArray("entries").size()).sum();
            int tagCount = tags.asList().stream().mapToInt(r -> r.getAsJsonObject().getAsJsonArray("tags").size()).sum();
            System.out.println("Exported " + registries.size() + " synchronized registries, " + entryCount + " entries, " + tags.size() + " tag registries, " + tagCount + " tags");
        }
    }
}
