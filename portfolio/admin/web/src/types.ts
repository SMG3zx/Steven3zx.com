// Mirrors portfolio/admin/agent/snapshot.go.

export interface Snapshot {
  collectedAt: string;
  host: HostInfo;
  cpu: CPUInfo;
  memory: MemoryInfo;
  network: NetworkInfo;
  storage: Disk[] | null;
  applications: Process[] | null;
  services: Service[] | null;
  docker: DockerInfo;
  errors?: string[];
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

export interface NetworkInfo {
  interfaces: Interface[] | null;
  listening: Listener[] | null;
  connections: Connection[] | null;
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
