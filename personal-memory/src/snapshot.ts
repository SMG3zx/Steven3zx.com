import type { MemorySnapshot } from "./types";

const allowedLabel = /^[A-Za-z_][A-Za-z0-9_]*$/;
const allowedRelationship = /^[A-Za-z_][A-Za-z0-9_]*$/;

export function validateSnapshot(snapshot: MemorySnapshot) {
  if (snapshot.format !== "personal-memory-snapshot" || snapshot.version !== 1) throw new Error("Unsupported snapshot format");
  const nodeKeys = new Set<string>();
  for (const node of snapshot.nodes) {
    if (!node.key || nodeKeys.has(node.key)) throw new Error(`snapshot contains duplicate or empty node key: ${node.key}`);
    if (!Array.isArray(node.labels) || !node.labels.length || node.labels.some((label) => !allowedLabel.test(label))) throw new Error(`snapshot contains an invalid label for node ${node.key}`);
    if (!node.properties || typeof node.properties !== "object" || Array.isArray(node.properties)) throw new Error(`snapshot contains invalid properties for node ${node.key}`);
    nodeKeys.add(node.key);
  }
  for (const relationship of snapshot.relationships) {
    if (!allowedRelationship.test(relationship.type)) throw new Error(`snapshot contains an invalid relationship type: ${relationship.type}`);
    if (!nodeKeys.has(relationship.sourceKey) || !nodeKeys.has(relationship.targetKey)) throw new Error(`snapshot relationship ${relationship.type} references a missing node`);
    if (!relationship.properties || typeof relationship.properties !== "object" || Array.isArray(relationship.properties)) throw new Error(`snapshot contains invalid relationship properties`);
  }
  return { nodes: snapshot.nodes.length, relationships: snapshot.relationships.length };
}
