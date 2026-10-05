import { readFile } from "node:fs/promises";
import type { Driver } from "neo4j-driver";
import { Neo4jStore } from "./neo4j-store";
import { validateSnapshot } from "./snapshot";
import type { MemorySnapshot } from "./types";

const file = process.argv[2];
if (!file) throw new Error("Usage: bun run restore <snapshot.json> with RESTORE_CONFIRM=RESTORE_PERSONAL_MEMORY");

const snapshot = JSON.parse(await readFile(file, "utf8")) as MemorySnapshot;
validateSnapshot(snapshot);
const dryRun = process.env.RESTORE_DRY_RUN === "1";
if (dryRun) {
  console.log(JSON.stringify({ dryRun: true, file, nodes: snapshot.nodes.length, relationships: snapshot.relationships.length }));
  process.exit(0);
}
if (process.env.RESTORE_CONFIRM !== "RESTORE_PERSONAL_MEMORY") throw new Error("Restore is disabled without RESTORE_CONFIRM=RESTORE_PERSONAL_MEMORY");

const store = new Neo4jStore();
await store.verify();
const driver = (store as unknown as { driver: Driver }).driver;
const database = process.env.NEO4J_DATABASE ?? "neo4j";
const session = driver.session({ database });
const allowedLabel = /^[A-Za-z_][A-Za-z0-9_]*$/;
const allowedRelationship = /^[A-Za-z_][A-Za-z0-9_]*$/;
try {
  for (const node of snapshot.nodes) {
    const labels = node.labels.filter((label) => allowedLabel.test(label));
    if (!labels.length) continue;
    const labelClause = labels.map((label) => `:${label}`).join("");
    const hasId = typeof node.properties.id === "string";
    await session.run(
      `MERGE (n${labelClause} {${hasId ? "id" : "__snapshotKey"}: $identity}) SET n += $properties`,
      { identity: hasId ? node.properties.id : node.key, properties: { ...node.properties, __snapshotKey: node.key } },
    );
  }
  for (const relationship of snapshot.relationships) {
    if (!allowedRelationship.test(relationship.type)) continue;
    await session.run(
      `MATCH (source {__snapshotKey: $sourceKey}), (target {__snapshotKey: $targetKey})
       MERGE (source)-[r:${relationship.type}]->(target) SET r += $properties`,
      { sourceKey: relationship.sourceKey, targetKey: relationship.targetKey, properties: relationship.properties },
    );
  }
} finally {
  await session.close();
  await store.close();
}
console.log(JSON.stringify({ restoredNodes: snapshot.nodes.length, restoredRelationships: snapshot.relationships.length, file }));
