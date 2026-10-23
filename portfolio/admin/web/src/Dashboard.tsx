import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Background,
  Controls,
  MiniMap,
  ReactFlow,
  useNodesState,
  type Edge,
} from "@xyflow/react";
import { api } from "./api";
import { Detail, type DetailTab } from "./Detail";
import { rate } from "./format";
import { JobTracker } from "./JobTracker";
import { LiveConnection, type Subscription } from "./live";
import { ActionsContext, nodeTypes, SnapshotContext, type Kind, type MapNode } from "./nodes";
import type { LiveSnapshot, LogLine } from "./types";

// Fixed positions for the singleton nodes; containers are laid out in a grid
// to the right of the Docker node. Users can drag anything; positions persist
// across refreshes because only node data changes on each frame.
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

/** Topics the map draws. Summaries only: no connection, process or service lists. */
const MAP_TOPICS = ["host", "cpu", "memory", "storage", "errors", "network", "applications", "services", "docker"];

/** Extra topics a detail panel needs. Only sent while that panel is open. */
const DETAIL_TOPICS: Partial<Record<Kind, string[]>> = {
  network: ["connections", "listening"],
  applications: ["processes"],
  services: ["serviceList"],
};

/** How many log lines the browser keeps for the open container. */
const LOG_LIMIT = 2000;

type Selection = { kind: Kind; containerId?: string; tab?: DetailTab } | null;

// Cards grow with their port chips and the Logs button, so the grid leaves room
// for the tallest card and keeps neighbours clear of each other.
const CONTAINER_COL_WIDTH = 340;
const CONTAINER_ROW_HEIGHT = 260;

function containerPosition(i: number) {
  const perCol = 6;
  return {
    x: 800 + Math.floor(i / perCol) * CONTAINER_COL_WIDTH,
    y: -470 + (i % perCol) * CONTAINER_ROW_HEIGHT,
  };
}

function layout(snap: LiveSnapshot, prev: MapNode[]): MapNode[] {
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

function edges(snap: LiveSnapshot): Edge[] {
  const net = snap.network.interfaces ?? [];
  const down = net.reduce((a, i) => a + i.recvRate, 0);
  const up = net.reduce((a, i) => a + i.sendRate, 0);
  const inbound = snap.network.inboundCount;
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
  const [snap, setSnap] = useState<LiveSnapshot | null>(null);
  const [error, setError] = useState("");
  const [connected, setConnected] = useState(false);
  const [visible, setVisible] = useState(() => document.visibilityState === "visible");
  const [nodes, setNodes, onNodesChange] = useNodesState<MapNode>([]);
  const [selected, setSelected] = useState<Selection>(null);
  const [paused, setPaused] = useState(false);
  const [view, setView] = useState<"map" | "jobs">("map");
  const [logs, setLogs] = useState<{ container: string; lines: LogLine[] }>({ container: "", lines: [] });
  const [logNotice, setLogNotice] = useState("");
  const conn = useRef<LiveConnection | null>(null);
  const signedOut = useRef(false);

  const handleSignedOut = useCallback(() => {
    if (signedOut.current) return;
    signedOut.current = true;
    onSignedOut();
  }, [onSignedOut]);

  // One socket for the life of the dashboard. Views change what it subscribes to.
  useEffect(() => {
    const c = new LiveConnection(
      (msg) => {
        if (msg.type === "frame") {
          setSnap((prev) => ({ ...prev, ...msg.data }) as LiveSnapshot);
          setError("");
        } else if (msg.type === "log") {
          setLogs((prev) => {
            const keep = prev.container === msg.container && !msg.reset;
            const base = keep ? prev.lines : [];
            return { container: msg.container, lines: [...base, ...(msg.lines ?? [])].slice(-LOG_LIMIT) };
          });
          if (msg.reset) setLogNotice("");
        } else if (msg.scope === "logs") {
          setLogNotice(msg.message);
        } else {
          setError(msg.message);
        }
      },
      handleSignedOut,
      setConnected,
    );
    conn.current = c;
    c.start();
    return () => c.close();
  }, [handleSignedOut]);

  useEffect(() => {
    const onVisibility = () => setVisible(document.visibilityState === "visible");
    document.addEventListener("visibilitychange", onVisibility);
    return () => document.removeEventListener("visibilitychange", onVisibility);
  }, []);

  // Subscribe to exactly what the open views draw; an empty subscription stops all frames.
  const logContainer = selected?.kind === "container" && selected.tab === "logs" ? (selected.containerId ?? "") : "";
  const detailTopics = selected ? (DETAIL_TOPICS[selected.kind] ?? []) : [];
  const detailKey = detailTopics.join(",");
  useEffect(() => {
    const live = view === "map" && !paused && visible;
    const sub: Subscription = live
      ? { topics: [...MAP_TOPICS, ...(detailKey ? detailKey.split(",") : [])], logs: logContainer }
      : { topics: [], logs: "" };
    conn.current?.subscribe(sub);
  }, [view, paused, visible, detailKey, logContainer]);

  useEffect(() => {
    if (snap) setNodes((prev) => layout(snap, prev));
  }, [snap, setNodes]);

  const edgeList = useMemo(() => (snap ? edges(snap) : []), [snap]);
  const actions = useMemo(
    () => ({
      openLogs: (containerId: string) => setSelected({ kind: "container", containerId, tab: "logs" }),
    }),
    [],
  );

  async function signOut() {
    signedOut.current = true;
    conn.current?.close();
    await api.logout().catch(() => {});
    onSignedOut();
  }

  const showLogs = selected?.kind === "container" && selected.tab === "logs";
  const shownLogs = logContainer && logs.container === logContainer ? logs.lines : [];

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
          {view === "map" && !connected && !paused && <span className="badge warn">reconnecting</span>}
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
              <ActionsContext.Provider value={actions}>
                <ReactFlow
                  nodes={nodes}
                  edges={edgeList}
                  nodeTypes={nodeTypes}
                  onNodesChange={onNodesChange}
                  onNodeClick={(_, n) => setSelected({ kind: n.data.kind, containerId: n.data.containerId, tab: "details" })}
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
              </ActionsContext.Provider>
              {selected && (
                <Detail
                  kind={selected.kind}
                  containerId={selected.containerId}
                  tab={selected.tab ?? "details"}
                  onTab={(tab) => setSelected((s) => (s ? { ...s, tab } : s))}
                  snap={snap}
                  logLines={showLogs ? shownLogs : []}
                  logNotice={showLogs ? logNotice : ""}
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
