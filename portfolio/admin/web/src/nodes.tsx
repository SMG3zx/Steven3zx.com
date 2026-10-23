import { createContext, useContext, type ReactNode } from "react";
import { Handle, Position, type Node, type NodeProps } from "@xyflow/react";
import { bytes, duration, level, pct, rate } from "./format";
import type { Container, LiveSnapshot } from "./types";

export const SnapshotContext = createContext<LiveSnapshot | null>(null);
const useSnap = () => useContext(SnapshotContext)!;

/** Lets a card ask the dashboard to open a view. */
export const ActionsContext = createContext<{ openLogs: (containerId: string) => void }>({ openLogs: () => {} });

export type Kind =
  | "host"
  | "cpu"
  | "memory"
  | "network"
  | "storage"
  | "applications"
  | "services"
  | "docker"
  | "container";

export type MapNodeData = { kind: Kind; containerId?: string };
export type MapNode = Node<MapNodeData>;

const sides = [
  ["t", Position.Top],
  ["r", Position.Right],
  ["b", Position.Bottom],
  ["l", Position.Left],
] as const;

/** Invisible source+target handles on every side so edges can attach anywhere. */
function Handles() {
  return (
    <>
      {sides.map(([id, pos]) => (
        <span key={id}>
          <Handle id={`${id}-s`} type="source" position={pos} className="handle" isConnectable={false} />
          <Handle id={`${id}-t`} type="target" position={pos} className="handle" isConnectable={false} />
        </span>
      ))}
    </>
  );
}

function Card({
  title,
  icon,
  badge,
  className = "",
  children,
}: {
  title: ReactNode;
  icon: string;
  badge?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={`card ${className}`}>
      <Handles />
      <header>
        <span className="icon" aria-hidden>
          {icon}
        </span>
        <span className="title">{title}</span>
        {badge}
      </header>
      <div className="body">{children}</div>
    </div>
  );
}

export function Meter({ value, label }: { value: number; label?: ReactNode }) {
  return (
    <div className="meter-row">
      {label && <div className="meter-label">{label}</div>}
      <div className={`meter ${level(value)}`}>
        <span style={{ width: `${Math.min(100, Math.max(0, value))}%` }} />
      </div>
    </div>
  );
}

function Stat({ k, v }: { k: string; v: ReactNode }) {
  return (
    <div className="stat">
      <span>{k}</span>
      <b>{v}</b>
    </div>
  );
}

function HostNode() {
  const { host, cpu, memory, errors } = useSnap();
  return (
    <Card
      icon="◆"
      title={host.hostname || "origin"}
      className="host"
      badge={errors?.length ? <span className="badge warn">{errors.length} probe errors</span> : <span className="badge ok">live</span>}
    >
      <div className="muted small">
        {host.platform} {host.platformVersion}
      </div>
      <Stat k="Uptime" v={duration(host.uptimeSeconds)} />
      <Stat k="Processes" v={host.processCount} />
      <Meter value={cpu.totalPercent} label={<>CPU <b>{pct(cpu.totalPercent)}</b></>} />
      <Meter value={memory.usedPercent} label={<>Memory <b>{pct(memory.usedPercent)}</b></>} />
    </Card>
  );
}

function CPUNode() {
  const { cpu } = useSnap();
  return (
    <Card icon="▣" title="CPU" badge={<span className={`badge ${level(cpu.totalPercent)}`}>{pct(cpu.totalPercent)}</span>}>
      <div className="muted small">{cpu.model}</div>
      <div className="muted small">
        {cpu.physicalCores} cores · {cpu.logicalCores} threads · {Math.round(cpu.mhz)} MHz
      </div>
      <div className="cores">
        {(cpu.cores ?? []).map((c) => (
          <div key={c.id} className="core" title={`CPU ${c.id}: ${pct(c.percent, 1)} (user ${pct(c.userPercent)}, system ${pct(c.systemPercent)})`}>
            <div className={`core-bar ${level(c.percent)}`}>
              <span style={{ height: `${c.percent}%` }} />
            </div>
            <small>{c.id}</small>
          </div>
        ))}
      </div>
    </Card>
  );
}

function MemoryNode() {
  const { memory: m } = useSnap();
  return (
    <Card icon="▤" title="Memory" badge={<span className={`badge ${level(m.usedPercent)}`}>{pct(m.usedPercent)}</span>}>
      <Meter value={m.usedPercent} label={<>RAM <b>{bytes(m.used)}</b> / {bytes(m.total)}</>} />
      <Stat k="Available" v={bytes(m.available)} />
      <Meter value={m.swapPercent} label={<>Page file <b>{bytes(m.swapUsed)}</b> / {bytes(m.swapTotal)}</>} />
    </Card>
  );
}

function NetworkNode() {
  const { network } = useSnap();
  const ifaces = network.interfaces ?? [];
  const publicPorts = (network.publicPorts ?? []).slice(0, 14);
  return (
    <Card icon="⇄" title="Network" badge={<span className="badge">{network.inboundCount} inbound</span>}>
      {ifaces.slice(0, 3).map((i) => (
        <div key={i.name} className="iface">
          <span className="ellipsis">{i.name}</span>
          <span className="mono small">
            ↓{rate(i.recvRate)} ↑{rate(i.sendRate)}
          </span>
        </div>
      ))}
      <Stat k="Listening sockets" v={network.listeningCount} />
      <Stat k="Open connections" v={network.connectionCount} />
      <div className="chips">
        {publicPorts.map((p) => (
          <span key={p} className="chip mono">
            :{p}
          </span>
        ))}
      </div>
    </Card>
  );
}

function StorageNode() {
  const disks = useSnap().storage ?? [];
  return (
    <Card icon="◫" title="Storage" badge={<span className="badge">{disks.length} volumes</span>}>
      {disks.map((d) => (
        <div key={d.mount} className="disk">
          <Meter
            value={d.usedPercent}
            label={
              <>
                <b>{d.mount}</b> {bytes(d.used)} / {bytes(d.total)}
              </>
            }
          />
          <div className="mono small muted">
            R {rate(d.readRate)} · W {rate(d.writeRate)}
          </div>
        </div>
      ))}
    </Card>
  );
}

function ApplicationsNode() {
  const apps = useSnap().applications;
  return (
    <Card icon="▦" title="Applications" badge={<span className="badge">{apps.count} processes</span>}>
      <table className="mini">
        <tbody>
          {(apps.top ?? []).map((p) => (
            <tr key={p.pid}>
              <td className="ellipsis">{p.name}</td>
              <td className="num">{pct(p.cpuPercent, 1)}</td>
              <td className="num">{bytes(p.memoryRss, 0)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  );
}

function ServicesNode() {
  const { count, running, autoStopped: stoppedAuto } = useSnap().services;
  const autoStopped = stoppedAuto ?? [];
  return (
    <Card icon="⚙" title="Services" badge={<span className="badge">{count}</span>}>
      <Stat k="Running" v={running} />
      <Stat k="Stopped" v={count - running} />
      <Stat k="Automatic but stopped" v={<span className={autoStopped.length ? "warn-text" : ""}>{autoStopped.length}</span>} />
      {autoStopped.slice(0, 3).map((s) => (
        <div key={s.name} className="small muted ellipsis">
          ⚠ {s.displayName}
        </div>
      ))}
    </Card>
  );
}

function DockerNode() {
  const d = useSnap().docker;
  if (!d.available) {
    return (
      <Card icon="🐳" title="Docker" badge={<span className="badge crit">down</span>}>
        <div className="small error">{d.error ?? "engine unreachable"}</div>
      </Card>
    );
  }
  return (
    <Card icon="🐳" title="Docker" badge={<span className="badge ok">{d.serverVersion}</span>}>
      <Stat k="Running" v={d.containersRunning} />
      <Stat k="Stopped" v={d.containersStopped} />
      <Stat k="Images" v={d.images} />
      <Stat k="VM" v={`${d.cpus} CPU · ${bytes(d.memoryTotal)}`} />
    </Card>
  );
}

function ContainerNode({ data }: NodeProps<MapNode>) {
  const c: Container | undefined = useSnap().docker.containers?.find((x) => x.id === data.containerId);
  if (!c) {
    return (
      <Card icon="▢" title="gone" className="container gone">
        <div className="muted small">Container no longer exists</div>
      </Card>
    );
  }
  const running = c.state === "running";
  return (
    <Card
      icon="▢"
      title={c.name}
      className={`container ${running ? "" : "stopped"}`}
      badge={<span className={`badge ${running ? "ok" : "muted-badge"}`}>{c.state}</span>}
    >
      <div className="mono small muted ellipsis">{c.image}</div>
      {running ? (
        <>
          <Meter value={c.cpuPercent} label={<>CPU <b>{pct(c.cpuPercent, 1)}</b></>} />
          <Stat k="Memory" v={bytes(c.memUsage)} />
        </>
      ) : (
        <div className="small muted">{c.status}</div>
      )}
      {c.ports && c.ports.length > 0 && (
        <div className="chips">
          {c.ports.slice(0, 3).map((p) => (
            <span key={p} className="chip mono">
              {p}
            </span>
          ))}
        </div>
      )}
      <LogsButton containerId={c.id} />
    </Card>
  );
}

function LogsButton({ containerId }: { containerId: string }) {
  const { openLogs } = useContext(ActionsContext);
  return (
    <button
      className="ghost logs-btn nodrag"
      onClick={(e) => {
        // The card itself opens the details panel; this button goes straight to logs.
        e.stopPropagation();
        openLogs(containerId);
      }}
    >
      Logs
    </button>
  );
}

export const nodeTypes = {
  host: HostNode,
  cpu: CPUNode,
  memory: MemoryNode,
  network: NetworkNode,
  storage: StorageNode,
  applications: ApplicationsNode,
  services: ServicesNode,
  docker: DockerNode,
  container: ContainerNode,
};
