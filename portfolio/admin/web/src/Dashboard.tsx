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
import { recordFrame, newHistory } from "./history";
import { ActionsContext, HistoryContext, nodeTypes, SnapshotContext, type Kind, type MapNode } from "./nodes";
import { CODE_AGENT_UNREACHABLE, type LiveSnapshot, type LogLine } from "./types";

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

type Positions = Record<string, { x: number; y: number }>;

function layout(snap: LiveSnapshot, prev: MapNode[], saved: Positions): MapNode[] {
  const byId = new Map(prev.map((n) => [n.id, n]));
  // A node keeps where it is now, else where the admin last left it, else its default.
  const keep = (id: string, data: MapNode["data"], type: Kind, pos: { x: number; y: number }): MapNode => {
    const old = byId.get(id);
    return old ? { ...old, data } : { id, type, data, position: saved[id] ?? pos };
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
  const [agentDown, setAgentDown] = useState(false);
  const [restart, setRestart] = useState<{ busy: boolean; notice: string }>({ busy: false, notice: "" });
  const [connected, setConnected] = useState(false);
  const [visible, setVisible] = useState(() => document.visibilityState === "visible");
  const [nodes, setNodes, onNodesChange] = useNodesState<MapNode>([]);
  const [selected, setSelected] = useState<Selection>(null);
  const [paused, setPaused] = useState(false);
  const [view, setView] = useState<"map" | "jobs">("map");
  const [logs, setLogs] = useState<{ container: string; lines: LogLine[] }>({ container: "", lines: [] });
  const [logNotice, setLogNotice] = useState("");
  const conn = useRef<LiveConnection | null>(null);
  const history = useRef(newHistory());
  const savedLayout = useRef<Positions>({});
  const nodesRef = useRef<MapNode[]>([]);
  const [layoutError, setLayoutError] = useState("");
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
          recordFrame(history.current, msg.data);
          setSnap((prev) => ({ ...prev, ...msg.data }) as LiveSnapshot);
          setError("");
          setAgentDown(false);
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
          setAgentDown(msg.code === CODE_AGENT_UNREACHABLE);
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

  // Load the admin's saved node positions once. Nodes already on screen move to them;
  // nodes created later pick them up in layout().
  useEffect(() => {
    api.layout
      .get()
      .then(({ positions }) => {
        savedLayout.current = positions;
        setNodes((prev) => prev.map((n) => (positions[n.id] ? { ...n, position: positions[n.id] } : n)));
      })
      .catch(() => setLayoutError("Could not load the saved layout"));
  }, [setNodes]);

  useEffect(() => {
    if (snap) setNodes((prev) => layout(snap, prev, savedLayout.current));
  }, [snap, setNodes]);

  useEffect(() => {
    nodesRef.current = nodes;
  }, [nodes]);

  // Called when a drag ends. Saves every node position, not just the moved one,
  // so the stored layout is always complete. The drag callback's node list holds
  // only the dragged nodes, so this reads the latest full list instead.
  function saveLayout() {
    const positions: Positions = Object.fromEntries(
      nodesRef.current.map((n) => [n.id, { x: n.position.x, y: n.position.y }]),
    );
    savedLayout.current = positions;
    api.layout
      .save(positions)
      .then(() => setLayoutError(""))
      .catch(() => setLayoutError("Could not save the layout"));
  }

  const edgeList = useMemo(() => (snap ? edges(snap) : []), [snap]);
  const actions = useMemo(
    () => ({
      openLogs: (containerId: string) => setSelected({ kind: "container", containerId, tab: "logs" }),
    }),
    [],
  );

  // The supervisor restarts the agent; the live socket picks frames back up on its own.
  async function restartAgent() {
    if (!window.confirm("Restart the host agent? The map stays empty until it is back.")) return;
    setRestart({ busy: true, notice: "" });
    try {
      await api.agent.restart();
      setRestart({ busy: false, notice: "Restart requested. Waiting for the agent to answer…" });
    } catch (err) {
      setRestart({ busy: false, notice: err instanceof Error ? err.message : "Restart failed" });
    }
  }

  const restartButton = (className?: string) => (
    <button className={className} onClick={restartAgent} disabled={restart.busy}>
      {restart.busy ? "Restarting…" : "Restart host agent"}
    </button>
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
          {layoutError && view === "map" && <span className="error small">{layoutError}</span>}
          {view === "map" && agentDown && snap && restartButton("ghost")}
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
            <div className="splash">
              <div>{error || "Loading snapshot…"}</div>
              {agentDown && restartButton()}
              {restart.notice && <div className="muted small">{restart.notice}</div>}
            </div>
          ) : (
            <SnapshotContext.Provider value={snap}>
              <HistoryContext.Provider value={history.current.series}>
                <ActionsContext.Provider value={actions}>
                  <ReactFlow
                    nodes={nodes}
                    edges={edgeList}
                    nodeTypes={nodeTypes}
                    onNodesChange={onNodesChange}
                    onNodeDragStop={saveLayout}
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
              </HistoryContext.Provider>
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
