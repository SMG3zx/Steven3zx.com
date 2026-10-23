import { useMemo, useState, type ReactNode } from "react";
import { bytes, duration, pct, rate } from "./format";
import { LogView } from "./LogView";
import type { Kind } from "./nodes";
import type { LiveSnapshot, LogLine } from "./types";

export type DetailTab = "details" | "logs";

type Column<T> = {
  label: string;
  value: (row: T) => string | number;
  render?: (row: T) => ReactNode;
  num?: boolean;
};

/** Filterable, sortable table used by every detail view. */
function Table<T>({
  rows,
  columns,
  rowKey,
  initialSort = 0,
  initialDesc = false,
}: {
  rows: T[];
  columns: Column<T>[];
  rowKey: (row: T) => string;
  initialSort?: number;
  initialDesc?: boolean;
}) {
  const [filter, setFilter] = useState("");
  const [sort, setSort] = useState({ col: initialSort, desc: initialDesc });

  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const filtered = q
      ? rows.filter((r) => columns.some((c) => String(c.value(r)).toLowerCase().includes(q)))
      : rows;
    const col = columns[sort.col];
    return [...filtered].sort((a, b) => {
      const x = col.value(a);
      const y = col.value(b);
      const cmp = typeof x === "number" && typeof y === "number" ? x - y : String(x).localeCompare(String(y));
      return sort.desc ? -cmp : cmp;
    });
  }, [rows, columns, filter, sort]);

  return (
    <div className="table-wrap">
      <input
        className="filter"
        placeholder={`Filter ${rows.length} rows…`}
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
      />
      <div className="table-scroll">
        <table className="data">
          <thead>
            <tr>
              {columns.map((c, i) => (
                <th
                  key={c.label}
                  className={c.num ? "num" : ""}
                  onClick={() => setSort((s) => ({ col: i, desc: s.col === i ? !s.desc : !!c.num }))}
                >
                  {c.label}
                  {sort.col === i ? (sort.desc ? " ▾" : " ▴") : ""}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {shown.map((r) => (
              <tr key={rowKey(r)}>
                {columns.map((c) => (
                  <td key={c.label} className={c.num ? "num" : ""}>
                    {c.render ? c.render(r) : c.value(r)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Tabs({ tabs }: { tabs: { label: string; body: ReactNode }[] }) {
  const [active, setActive] = useState(0);
  return (
    <>
      <div className="tabs" role="tablist">
        {tabs.map((t, i) => (
          <button key={t.label} role="tab" aria-selected={i === active} onClick={() => setActive(i)}>
            {t.label}
          </button>
        ))}
      </div>
      {tabs[active].body}
    </>
  );
}

function KV({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="kv">
      {rows.map(([k, v]) => (
        <div key={k}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}

export function Detail({
  kind,
  containerId,
  tab,
  onTab,
  snap,
  logLines,
  logNotice,
  onClose,
}: {
  kind: Kind;
  containerId?: string;
  tab: DetailTab;
  onTab: (tab: DetailTab) => void;
  snap: LiveSnapshot;
  logLines: LogLine[];
  logNotice: string;
  onClose: () => void;
}) {
  const titles: Record<Kind, string> = {
    host: "Host",
    cpu: "CPU",
    memory: "Memory",
    network: "Network",
    storage: "Storage",
    applications: "Applications",
    services: "Services",
    docker: "Docker",
    container: "Container",
  };
  return (
    <aside className="detail" aria-label={`${titles[kind]} details`}>
      <header>
        <h2>{titles[kind]}</h2>
        <button className="ghost" onClick={onClose} aria-label="Close details">
          ✕
        </button>
      </header>
      {kind === "container" ? (
        <div className={`detail-body ${tab === "logs" ? "logs-mode" : ""}`}>
          <div className="tabs" role="tablist">
            {(["details", "logs"] as const).map((t) => (
              <button key={t} role="tab" aria-selected={tab === t} onClick={() => onTab(t)}>
                {t === "details" ? "Details" : "Logs"}
              </button>
            ))}
          </div>
          {tab === "logs" ? (
            <LogView lines={logLines} notice={logNotice} />
          ) : (
            body(kind, snap, containerId)
          )}
        </div>
      ) : (
        <div className="detail-body">{body(kind, snap, containerId)}</div>
      )}
    </aside>
  );
}

function body(kind: Kind, s: LiveSnapshot, containerId?: string): ReactNode {
  switch (kind) {
    case "host":
      return (
        <>
          <KV
            rows={[
              ["Hostname", s.host.hostname],
              ["OS", `${s.host.platform} ${s.host.platformVersion}`],
              ["Kernel", s.host.kernelVersion],
              ["Uptime", duration(s.host.uptimeSeconds)],
              ["Booted", new Date(s.host.bootTime * 1000).toLocaleString()],
              ["Processes", s.host.processCount],
              ["Sampled", new Date(s.collectedAt).toLocaleTimeString()],
            ]}
          />
          {s.errors?.length ? (
            <>
              <h3>Probe errors</h3>
              <ul className="errors">
                {s.errors.map((e) => (
                  <li key={e}>{e}</li>
                ))}
              </ul>
            </>
          ) : null}
        </>
      );
    case "cpu":
      return (
        <>
          <KV
            rows={[
              ["Model", s.cpu.model],
              ["Cores / threads", `${s.cpu.physicalCores} / ${s.cpu.logicalCores}`],
              ["Clock", `${Math.round(s.cpu.mhz)} MHz`],
              ["Total", pct(s.cpu.totalPercent, 1)],
            ]}
          />
          <Table
            rows={s.cpu.cores ?? []}
            rowKey={(c) => String(c.id)}
            columns={[
              { label: "Thread", value: (c) => c.id, num: true },
              { label: "Busy", value: (c) => c.percent, render: (c) => pct(c.percent, 1), num: true },
              { label: "User", value: (c) => c.userPercent, render: (c) => pct(c.userPercent, 1), num: true },
              { label: "System", value: (c) => c.systemPercent, render: (c) => pct(c.systemPercent, 1), num: true },
              { label: "Idle", value: (c) => c.idlePercent, render: (c) => pct(c.idlePercent, 1), num: true },
            ]}
          />
        </>
      );
    case "memory":
      return (
        <KV
          rows={[
            ["Total", bytes(s.memory.total)],
            ["Used", `${bytes(s.memory.used)} (${pct(s.memory.usedPercent, 1)})`],
            ["Available", bytes(s.memory.available)],
            ["Page file", `${bytes(s.memory.swapUsed)} / ${bytes(s.memory.swapTotal)} (${pct(s.memory.swapPercent, 1)})`],
          ]}
        />
      );
    case "network": {
      const conns = s.connections ?? [];
      const inbound = conns.filter((c) => c.inbound);
      const outbound = conns.filter((c) => !c.inbound);
      // Outbound grouped by the process making the calls: how many connections,
      // how many distinct remote hosts, and which remote ports it uses.
      const groups = new Map<string, { process: string; pid: number; connections: number; hosts: Set<string>; ports: Set<number> }>();
      for (const c of outbound) {
        const key = `${c.pid}:${c.process}`;
        const g = groups.get(key) ?? { process: c.process || "unknown", pid: c.pid, connections: 0, hosts: new Set(), ports: new Set() };
        g.connections++;
        g.hosts.add(c.remoteAddr);
        g.ports.add(c.remotePort);
        groups.set(key, g);
      }
      const byProcess = [...groups.values()].map((g) => ({
        process: g.process,
        pid: g.pid,
        connections: g.connections,
        hosts: g.hosts.size,
        ports: [...g.ports].sort((a, b) => a - b).join(", "),
      }));
      return (
        <Tabs
          tabs={[
            {
              label: "Inbound",
              body: (
                <Table
                  rows={inbound}
                  rowKey={(c) => `${c.protocol}${c.localAddr}${c.localPort}${c.remoteAddr}${c.remotePort}`}
                  columns={[
                    { label: "From", value: (c) => c.remoteAddr, render: (c) => <span className="mono">{c.remoteAddr}:{c.remotePort}</span> },
                    { label: "To port", value: (c) => c.localPort, render: (c) => <span className="mono">{c.localAddr}:{c.localPort}</span>, num: true },
                    { label: "State", value: (c) => c.status },
                    { label: "Process", value: (c) => c.process || `pid ${c.pid}` },
                  ]}
                />
              ),
            },
            {
              label: "Outbound",
              body: (
                <Table
                  rows={outbound}
                  rowKey={(c) => `${c.protocol}${c.localAddr}${c.localPort}${c.remoteAddr}${c.remotePort}`}
                  columns={[
                    { label: "Process", value: (c) => c.process || `pid ${c.pid}` },
                    { label: "To", value: (c) => `${c.remoteAddr}:${c.remotePort}`, render: (c) => <span className="mono">{c.remoteAddr}:{c.remotePort}</span> },
                    { label: "State", value: (c) => c.status },
                  ]}
                />
              ),
            },
            {
              label: "By process",
              body: (
                <Table
                  rows={byProcess}
                  rowKey={(g) => `${g.pid}:${g.process}`}
                  initialSort={1}
                  initialDesc
                  columns={[
                    { label: "Process", value: (g) => g.process },
                    { label: "Connections", value: (g) => g.connections, num: true },
                    { label: "Remote hosts", value: (g) => g.hosts, num: true },
                    { label: "Remote ports", value: (g) => g.ports, render: (g) => <span className="mono small">{g.ports}</span> },
                  ]}
                />
              ),
            },
            {
              label: "Connections",
              body: (
                <Table
                  rows={s.connections ?? []}
                  rowKey={(c) => `${c.protocol}${c.localAddr}${c.localPort}${c.remoteAddr}${c.remotePort}`}
                  columns={[
                    { label: "Dir", value: (c) => (c.inbound ? "in" : "out") },
                    { label: "Local", value: (c) => `${c.localAddr}:${c.localPort}`, render: (c) => <span className="mono">{c.localAddr}:{c.localPort}</span> },
                    { label: "Remote", value: (c) => `${c.remoteAddr}:${c.remotePort}`, render: (c) => <span className="mono">{c.remoteAddr}:{c.remotePort}</span> },
                    { label: "State", value: (c) => c.status },
                    { label: "Process", value: (c) => c.process || `pid ${c.pid}` },
                  ]}
                />
              ),
            },
            {
              label: "Listening",
              body: (
                <Table
                  rows={s.listening ?? []}
                  rowKey={(l) => `${l.protocol}${l.address}${l.port}`}
                  columns={[
                    { label: "Port", value: (l) => l.port, num: true },
                    { label: "Proto", value: (l) => l.protocol },
                    { label: "Address", value: (l) => l.address, render: (l) => <span className="mono">{l.address}</span> },
                    { label: "Process", value: (l) => l.process || `pid ${l.pid}` },
                  ]}
                />
              ),
            },
            {
              label: "Interfaces",
              body: (
                <Table
                  rows={s.network.interfaces ?? []}
                  rowKey={(i) => i.name}
                  initialSort={1}
                  initialDesc
                  columns={[
                    { label: "Name", value: (i) => i.name },
                    { label: "Down", value: (i) => i.recvRate, render: (i) => rate(i.recvRate), num: true },
                    { label: "Up", value: (i) => i.sendRate, render: (i) => rate(i.sendRate), num: true },
                    { label: "Received", value: (i) => i.bytesRecv, render: (i) => bytes(i.bytesRecv), num: true },
                    { label: "Sent", value: (i) => i.bytesSent, render: (i) => bytes(i.bytesSent), num: true },
                    { label: "Addresses", value: (i) => (i.addresses ?? []).join(" "), render: (i) => <span className="mono small">{(i.addresses ?? []).join(", ")}</span> },
                  ]}
                />
              ),
            },
          ]}
        />
      );
    }
    case "storage":
      return (
        <Table
          rows={s.storage ?? []}
          rowKey={(d) => d.mount}
          columns={[
            { label: "Volume", value: (d) => d.mount },
            { label: "FS", value: (d) => d.fsType },
            { label: "Used", value: (d) => d.usedPercent, render: (d) => pct(d.usedPercent, 1), num: true },
            { label: "Free", value: (d) => d.free, render: (d) => bytes(d.free), num: true },
            { label: "Size", value: (d) => d.total, render: (d) => bytes(d.total), num: true },
            { label: "Read", value: (d) => d.readRate, render: (d) => rate(d.readRate), num: true },
            { label: "Write", value: (d) => d.writeRate, render: (d) => rate(d.writeRate), num: true },
          ]}
        />
      );
    case "applications":
      return (
        <Table
          rows={s.processes ?? []}
          rowKey={(p) => String(p.pid)}
          initialSort={2}
          initialDesc
          columns={[
            { label: "Name", value: (p) => p.name },
            { label: "PID", value: (p) => p.pid, num: true },
            { label: "CPU", value: (p) => p.cpuPercent, render: (p) => pct(p.cpuPercent, 1), num: true },
            { label: "Memory", value: (p) => p.memoryRss, render: (p) => bytes(p.memoryRss), num: true },
            { label: "Threads", value: (p) => p.threads, num: true },
          ]}
        />
      );
    case "services":
      return (
        <Table
          rows={s.serviceList ?? []}
          rowKey={(v) => v.name}
          columns={[
            { label: "Service", value: (v) => v.displayName },
            { label: "State", value: (v) => v.state, render: (v) => <span className={`state ${v.state}`}>{v.state}</span> },
            { label: "Start", value: (v) => v.startType },
            { label: "PID", value: (v) => v.pid, render: (v) => (v.pid ? v.pid : ""), num: true },
            { label: "Name", value: (v) => v.name, render: (v) => <span className="mono small">{v.name}</span> },
          ]}
        />
      );
    case "docker":
      return (
        <>
          <KV
            rows={[
              ["Engine", s.docker.serverVersion || "unavailable"],
              ["OS", s.docker.operatingSystem],
              ["VM resources", `${s.docker.cpus} CPU · ${bytes(s.docker.memoryTotal)}`],
              ["Images", s.docker.images],
            ]}
          />
          <Table
            rows={s.docker.containers ?? []}
            rowKey={(c) => c.id}
            columns={[
              { label: "Name", value: (c) => c.name },
              { label: "State", value: (c) => c.state, render: (c) => <span className={`state ${c.state}`}>{c.state}</span> },
              { label: "CPU", value: (c) => c.cpuPercent, render: (c) => pct(c.cpuPercent, 1), num: true },
              { label: "Memory", value: (c) => c.memUsage, render: (c) => bytes(c.memUsage), num: true },
              { label: "Project", value: (c) => c.project },
            ]}
          />
        </>
      );
    case "container": {
      const c = s.docker.containers?.find((x) => x.id === containerId);
      if (!c) return <p className="muted">This container no longer exists.</p>;
      return (
        <KV
          rows={[
            ["Name", c.name],
            ["ID", <span className="mono">{c.id}</span>],
            ["Image", <span className="mono">{c.image}</span>],
            ["Status", c.status],
            ["Compose project", c.project || "—"],
            ["CPU", pct(c.cpuPercent, 2)],
            ["Memory", `${bytes(c.memUsage)} / ${bytes(c.memLimit)} (${pct(c.memPercent, 1)})`],
            ["Network", `↓ ${bytes(c.netRx)} · ↑ ${bytes(c.netTx)}`],
            ["Block I/O", `R ${bytes(c.blockRead)} · W ${bytes(c.blockWrite)}`],
            ["PIDs", c.pids],
            ["Ports", <span className="mono">{(c.ports ?? []).join(", ") || "—"}</span>],
            ["Networks", (c.networks ?? []).join(", ") || "—"],
          ]}
        />
      );
    }
  }
}
