// Mirrors portfolio/admin/agent/snapshot.go and topics.go.

/**
 * What the map and detail views draw. The agent sends only the topics the open
 * views ask for, so the map gets summaries and the detail-only lists are
 * present only while a detail panel that needs them is open.
 */
export interface LiveSnapshot {
  collectedAt: string;
  host: HostInfo;
  cpu: CPUInfo;
  memory: MemoryInfo;
  storage: Disk[] | null;
  docker: DockerInfo;
  errors: string[] | null;
  network: NetworkSummary;
  applications: ApplicationSummary;
  services: ServiceSummary;
  // Detail topics.
  connections?: Connection[] | null;
  listening?: Listener[] | null;
  processes?: Process[] | null;
  serviceList?: Service[] | null;
}

export interface HostInfo {
  hostname: string;
  os: string;
  platform: string;
  platformVersion: string;
  kernelVersion: string;
  uptimeSeconds: number;
  bootTime: number;
  processCount: number;
}

export interface CPUInfo {
  model: string;
  physicalCores: number;
  logicalCores: number;
  mhz: number;
  totalPercent: number;
  cores: CPUCore[] | null;
}

export interface CPUCore {
  id: number;
  percent: number;
  userPercent: number;
  systemPercent: number;
  idlePercent: number;
}

export interface MemoryInfo {
  total: number;
  used: number;
  available: number;
  usedPercent: number;
  swapTotal: number;
  swapUsed: number;
  swapPercent: number;
}

export interface NetworkSummary {
  interfaces: Interface[] | null;
  listeningCount: number;
  connectionCount: number;
  inboundCount: number;
  publicPorts: number[] | null;
}

export interface ApplicationSummary {
  count: number;
  top: Process[] | null;
}

export interface ServiceSummary {
  count: number;
  running: number;
  autoStopped: Service[] | null;
}

export interface Interface {
  name: string;
  addresses: string[] | null;
  up: boolean;
  bytesSent: number;
  bytesRecv: number;
  sendRate: number;
  recvRate: number;
  packetsSent: number;
  packetsRecv: number;
  errors: number;
  drops: number;
}

export interface Listener {
  protocol: string;
  address: string;
  port: number;
  pid: number;
  process: string;
}

export interface Connection {
  protocol: string;
  localAddr: string;
  localPort: number;
  remoteAddr: string;
  remotePort: number;
  status: string;
  pid: number;
  process: string;
  inbound: boolean;
}

export interface Disk {
  mount: string;
  device: string;
  fsType: string;
  total: number;
  used: number;
  free: number;
  usedPercent: number;
  readRate: number;
  writeRate: number;
}

export interface Process {
  pid: number;
  name: string;
  cpuPercent: number;
  memoryRss: number;
  memPercent: number;
  threads: number;
  startedAt: number;
}

export interface Service {
  name: string;
  displayName: string;
  state: string;
  startType: string;
  pid: number;
}

export interface DockerInfo {
  available: boolean;
  error?: string;
  serverVersion: string;
  operatingSystem: string;
  cpus: number;
  memoryTotal: number;
  images: number;
  containersRunning: number;
  containersStopped: number;
  containers: Container[] | null;
}

export interface Container {
  id: string;
  name: string;
  image: string;
  state: string;
  status: string;
  project: string;
  ports: string[] | null;
  networks: string[] | null;
  cpuPercent: number;
  memUsage: number;
  memLimit: number;
  memPercent: number;
  netRx: number;
  netTx: number;
  blockRead: number;
  blockWrite: number;
  pids: number;
}

/** One line of container output, as the admin server forwards it from the agent. */
export interface LogLine {
  time?: string;
  stream: "stdout" | "stderr";
  text: string;
}

// Messages the admin server sends on /admin/api/live.
export type LiveMessage =
  | { type: "frame"; data: Partial<LiveSnapshot> & { collectedAt: string } }
  | { type: "log"; container: string; reset?: boolean; lines?: LogLine[] }
  | { type: "error"; scope: "frame" | "logs"; message: string; code?: string };

/** Code on a frame error when the host agent did not answer. Matches server/live.go. */
export const CODE_AGENT_UNREACHABLE = "agent_unreachable";

// Mirrors portfolio/admin/server/jobs.go.

export const JOB_STATUSES = ["saved", "applied", "interviewing", "offer", "rejected", "withdrawn"] as const;

export type JobStatus = (typeof JOB_STATUSES)[number];

export interface Job {
  id: string;
  company: string;
  title: string;
  location: string;
  url: string;
  status: JobStatus;
  salary: string;
  applied: string;
  notes: string;
  createdAt: number;
  updatedAt: number;
}

export type JobInput = Omit<Job, "id" | "createdAt" | "updatedAt">;

// Mirrors scrapedJob in portfolio/admin/server/scrape.go. Empty means the page did not say.
export interface ScrapedJob {
  company: string;
  title: string;
  location: string;
  salary: string;
}
