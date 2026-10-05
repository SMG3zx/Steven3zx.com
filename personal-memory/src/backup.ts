import { mkdir } from "node:fs/promises";
import { join } from "node:path";
import { Neo4jStore } from "./neo4j-store";

const store = new Neo4jStore();
await store.verify();
const snapshot = await store.exportSnapshot();
const directory = process.env.BACKUP_DIR ?? "./backups";
await mkdir(directory, { recursive: true });
const filename = `personal-memory-${snapshot.exportedAt.replace(/[:.]/g, "-")}.json`;
const path = join(directory, filename);
await Bun.write(path, JSON.stringify(snapshot, null, 2));
await store.close();
console.log(JSON.stringify({ path, nodes: snapshot.nodes.length, relationships: snapshot.relationships.length }));
