import { createContext, useContext, type ReactNode } from "react";
import { Handle, Position, type Node, type NodeProps } from "@xyflow/react";
import { bytes, duration, level, pct, rate } from "./format";
import { HISTORY_LIMIT, last, SERIES } from "./history";
import type { Container, LiveSnapshot } from "./types";

export const SnapshotContext = createContext<LiveSnapshot | null>(null);
const useSnap = () => useContext(SnapshotContext)!;

/** Recent samples by series key, see history.ts. */
export const HistoryContext = createContext<Record<string, number[]>>({});
const useSeries = () => useContext(HistoryContext);

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

const SPARK_W = 100;
const SPARK_H = 32;

/**
 * A line chart of recent samples, newest on the right. The y-axis runs from 0
 * to max when given, otherwise to the largest sample, but never below floor.
 * Small values stay visibly low instead of filling the box.
 */
export function Spark({ values, max, floor = 1, label }: { values: number[]; max?: number; floor?: number; label: string }) {
  const top = max ?? Math.max(floor, ...values);
  const step = SPARK_W / (HISTORY_LIMIT - 1);
  const points = values.map((v, i) => {
    const x = SPARK_W - (values.length - 1 - i) * step;
    const y = SPARK_H - 1 - (Math.min(v, top) / top) * (SPARK_H - 2);
    return [x, y] as const;
  });
  const line = points.map(([x, y]) => `${x.toFixed(2)},${y.toFixed(2)}`).join(" ");
  const area = points.length
    ? `M${points[0][0].toFixed(2)},${SPARK_H} L${line.replaceAll(" ", " L")} L${points[points.length - 1][0].toFixed(2)},${SPARK_H} Z`
    : "";
  return (
    <svg className="spark" viewBox={`0 0 ${SPARK_W} ${SPARK_H}`} preserveAspectRatio="none" role="img" aria-label={label}>
      <line x1="0" x2={SPARK_W} y1={SPARK_H - 0.5} y2={SPARK_H - 0.5} className="spark-base" />
      {points.length > 1 && <path d={area} className="spark-area" />}
      {points.length > 1 && <polyline points={line} className="spark-line" vectorEffect="non-scaling-stroke" />}
    </svg>
  );
}

/** A label, the newest value, and its history as a line chart. */
function CpuLine({ label, value, series, max, floor }: { label: ReactNode; value: number; series: number[]; max?: number; floor?: number }) {
  return (
    <div className="spark-row">
      <div className="meter-label">
        {label} <b>{pct(value, 1)}</b>
      </div>
      <Spark values={series} max={max} floor={floor} label={`${typeof label === "string" ? label : "CPU"} history`} />
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
  const series = useSeries();
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
      <CpuLine label="CPU" value={cpu.totalPercent} series={series[SERIES.cpu] ?? []} max={100} />
      <Meter value={memory.usedPercent} label={<>Memory <b>{pct(memory.usedPercent)}</b></>} />
    </Card>
  );
}

function CPUNode() {
  const { cpu } = useSnap();
  const series = useSeries();
  return (
    <Card icon="▣" title="CPU" badge={<span className={`badge ${level(cpu.totalPercent)}`}>{pct(cpu.totalPercent)}</span>}>
      <div className="muted small">{cpu.model}</div>
      <div className="muted small">
        {cpu.physicalCores} cores · {cpu.logicalCores} threads · {Math.round(cpu.mhz)} MHz
      </div>
      <CpuLine label="Total" value={cpu.totalPercent} series={series[SERIES.cpu] ?? []} max={100} />
      <div className="cores">
        {(cpu.cores ?? []).map((c) => {
          const tone = level(c.percent) === "crit" ? "var(--crit)" : level(c.percent) === "warn" ? "var(--warn)" : "var(--ok)";
          return (
            <div
              key={c.id}
              className="core-tile"
              style={{ background: `color-mix(in srgb, ${tone} ${Math.round(c.percent)}%, var(--panel-2))` }}
              title={`Thread ${c.id}: ${pct(c.percent, 1)} (user ${pct(c.userPercent)}, system ${pct(c.systemPercent)})`}
            >
              <b>{Math.round(c.percent)}</b>
              <small>{c.id}</small>
            </div>
          );
        })}
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
  const series = useSeries();
  const publicPorts = (network.publicPorts ?? []).slice(0, 14);
  const down = series[SERIES.netDown] ?? [];
  const up = series[SERIES.netUp] ?? [];
  return (
    <Card icon="⇄" title="Network" badge={<span className="badge">{network.inboundCount} inbound</span>}>
      <div className="spark-row">
        <div className="meter-label">
          Inbound ↓ <b>{rate(last(down))}</b>
        </div>
        <Spark values={down} floor={1024} label="Inbound rate history" />
      </div>
      <div className="spark-row">
        <div className="meter-label">
          Outbound ↑ <b>{rate(last(up))}</b>
        </div>
        <Spark values={up} floor={1024} label="Outbound rate history" />
      </div>
      {ifaces.slice(0, 3).map((i) => (
        <div key={i.name} className="iface">
          <span className="ellipsis">{i.name}</span>
          <span className="mono small">
            ↓{rate(i.recvRate)} ↑{rate(i.sendRate)}
          </span>
        </div>
      ))}
      <Stat k="Inbound connections" v={network.inboundCount} />
      <Stat k="Outbound connections" v={Math.max(0, network.connectionCount - network.inboundCount)} />
      <Stat k="Listening sockets" v={network.listeningCount} />
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
  const series = useSeries();
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
          <CpuLine label="CPU" value={c.cpuPercent} series={series[SERIES.container(c.id)] ?? []} floor={10} />
          <Stat k="Memory" v={bytes(c.memUsage)} />
          <Stat
            k="Network ↓ / ↑"
            v={
              <span className="mono small">
                {rate(last(series[SERIES.containerDown(c.id)]))} / {rate(last(series[SERIES.containerUp(c.id)]))}
              </span>
            }
          />
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
