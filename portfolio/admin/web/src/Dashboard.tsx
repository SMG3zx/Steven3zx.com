import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Background,
  Controls,
  MiniMap,
  ReactFlow,
  useNodesState,
  type Edge,
} from "@xyflow/react";
import { api, Unauthorized } from "./api";
import { Detail } from "./Detail";
import { rate } from "./format";
import { JobTracker } from "./JobTracker";
import { nodeTypes, SnapshotContext, type Kind, type MapNode } from "./nodes";
import type { Snapshot } from "./types";

const POLL_MS = 3000;

// Fixed positions for the singleton nodes; containers are laid out in a grid
// to the right of the Docker node. Users can drag anything; positions persist
// across refreshes because only node data changes on each poll.
const fixed: Record<Exclude<Kind, "container">, { x: number; y: number }> = {
  network: { x: 0, y: -420 },
  cpu: { x: -480, y: -330 },
  memory: { x: -480, y: 30 },
  host: { x: 0, y: -90 },
  storage: { x: 0, y: 200 },
  applications: { x: -480, y: 300 },
  services: { x: 420, y: 330 },
  docker: { x: 420, y: -120 },
};

function containerPosition(i: number) {
  const perCol = 6;
  return { x: 800 + Math.floor(i / perCol) * 310, y: -470 + (i % perCol) * 165 };
}

function layout(snap: Snapshot, prev: MapNode[]): MapNode[] {
  const byId = new Map(prev.map((n) => [n.id, n]));
  const keep = (id: string, data: MapNode["data"], type: Kind, pos: { x: number; y: number }): MapNode => {
    const old = byId.get(id);
    return old ? { ...old, data } : { id, type, data, position: pos };
  };
  const nodes: MapNode[] = (Object.keys(fixed) as (keyof typeof fixed)[]).map((k) =>
    keep(k, { kind: k }, k, fixed[k]),
  );
  (snap.docker.containers ?? []).forEach((c, i) => {
    nodes.push(keep(`ctr-${c.id}`, { kind: "container", containerId: c.id }, "container", containerPosition(i)));
  });
  return nodes;
}

function edges(snap: Snapshot): Edge[] {
  const net = snap.network.interfaces ?? [];
  const down = net.reduce((a, i) => a + i.recvRate, 0);
  const up = net.reduce((a, i) => a + i.sendRate, 0);
  const inbound = (snap.network.connections ?? []).filter((c) => c.inbound).length;
  const link = (target: string, sourceHandle: string, targetHandle: string, extra: Partial<Edge> = {}): Edge => ({
    id: `host-${target}`,
    source: "host",
    target,
    sourceHandle: `${sourceHandle}-s`,
    targetHandle: `${targetHandle}-t`,
    type: "smoothstep",
    ...extra,
  });
  const out: Edge[] = [
    link("network", "t", "b", {
      animated: inbound > 0 || down > 1024,
      label: `↓ ${rate(down)} · ↑ ${rate(up)} · ${inbound} in`,
      className: "edge-net",
    }),
    link("cpu", "l", "r"),
    link("memory", "l", "r"),
    link("applications", "l", "r"),
    link("storage", "b", "t"),
    link("services", "b", "l"),
    link("docker", "r", "l", { animated: snap.docker.containersRunning > 0, className: "edge-docker" }),
  ];
  for (const c of snap.docker.containers ?? []) {
    out.push({
      id: `docker-${c.id}`,
      source: "docker",
      target: `ctr-${c.id}`,
      sourceHandle: "r-s",
      targetHandle: "l-t",
      type: "smoothstep",
      animated: c.state === "running",
      className: c.state === "running" ? "edge-running" : "edge-stopped",
    });
  }
  return out;
}

export function Dashboard({ username, onSignedOut }: { username: string; onSignedOut: () => void }) {
  const [snap, setSnap] = useState<Snapshot | null>(null);
  const [error, setError] = useState("");
  const [nodes, setNodes, onNodesChange] = useNodesState<MapNode>([]);
  const [selected, setSelected] = useState<{ kind: Kind; containerId?: string } | null>(null);
  const [paused, setPaused] = useState(false);
  const [view, setView] = useState<"map" | "jobs">("map");
  const signedOut = useRef(false);

  const poll = useCallback(async () => {
    try {
      const s = await api.snapshot();
      setSnap(s);
      setNodes((prev) => layout(s, prev));
      setError("");
    } catch (e) {
      if (e instanceof Unauthorized) {
        if (!signedOut.current) {
          signedOut.current = true;
          onSignedOut();
        }
        return;
      }
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [onSignedOut, setNodes]);

  // Only the map polls the host agent; the job tracker has no live data.
  useEffect(() => {
    if (paused || view !== "map") return;
    poll();
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") poll();
    }, POLL_MS);
    return () => window.clearInterval(id);
  }, [poll, paused, view]);

  const edgeList = useMemo(() => (snap ? edges(snap) : []), [snap]);

  async function signOut() {
    signedOut.current = true;
    await api.logout().catch(() => {});
    onSignedOut();
  }

  return (
    <div className="dashboard">
      <header className="topbar">
        <div className="brand">
          <span aria-hidden>◆</span> Origin map
          {snap && <span className="muted small"> · {snap.host.hostname} · updated {new Date(snap.collectedAt).toLocaleTimeString()}</span>}
        </div>
        <div className="topbar-actions">
          <nav className="tabs view-tabs" aria-label="Admin views">
            {(["map", "jobs"] as const).map((v) => (
              <button key={v} aria-selected={view === v} onClick={() => setView(v)}>
                {v === "map" ? "Map" : "Jobs"}
              </button>
            ))}
          </nav>
          {error && view === "map" && <span className="error small">{error}</span>}
          {view === "map" && (
            <button className="ghost" onClick={() => setPaused((p) => !p)}>
              {paused ? "Resume" : "Pause"}
            </button>
          )}
          <span className="muted small">{username}</span>
          <button className="ghost" onClick={signOut}>
            Sign out
          </button>
        </div>
      </header>
      {view === "jobs" ? (
        <div className="canvas scroll">
          <JobTracker onSignedOut={onSignedOut} />
        </div>
      ) : (
        <div className="canvas">
          {!snap ? (
            <div className="splash">{error || "Loading snapshot…"}</div>
          ) : (
            <SnapshotContext.Provider value={snap}>
              <ReactFlow
                nodes={nodes}
                edges={edgeList}
                nodeTypes={nodeTypes}
                onNodesChange={onNodesChange}
                onNodeClick={(_, n) => setSelected({ kind: n.data.kind, containerId: n.data.containerId })}
                onPaneClick={() => setSelected(null)}
                nodesConnectable={false}
                fitView
                fitViewOptions={{ padding: 0.12 }}
                minZoom={0.2}
                colorMode="system"
              >
                <Background gap={24} />
                <Controls showInteractive={false} />
                <MiniMap pannable zoomable className="minimap" />
              </ReactFlow>
              {selected && (
                <Detail
                  kind={selected.kind}
                  containerId={selected.containerId}
                  snap={snap}
                  onClose={() => setSelected(null)}
                />
              )}
            </SnapshotContext.Provider>
          )}
        </div>
      )}
    </div>
  );
}
