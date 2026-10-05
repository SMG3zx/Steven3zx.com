import type { ServerWebSocket } from "bun";
import { DbConnection } from "./frontend/module_bindings";

type CapturePacket = {
    id: string;
    direction: "client-to-server" | "server-to-client";
    source: string;
    destination: string;
    flags: string[];
    sequence: number;
    acknowledgment: number;
    window: number;
    payloadLength: number;
    timestamp: number;
};

const clients = new Set<ServerWebSocket<unknown>>();
const port = Number(Bun.env.CAPTURE_PORT ?? 8787);
let packetId = 0;
let captureStatus: "starting" | "capturing" | "error" = "starting";
let captureStatusMessage: string | undefined;
let spacetime: DbConnection | undefined;

function endpointParts(value: string): { address: string; port: number; addressFamily: number } {
    const separator = value.lastIndexOf(":");
    const address = separator >= 0 ? value.slice(0, separator) : value;
    const port = Number(separator >= 0 ? value.slice(separator + 1) : 0);
    return { address, port, addressFamily: address.includes(":") ? 6 : 4 };
}

async function connectSpacetime(): Promise<void> {
    const uri = Bun.env.SPACETIMEDB_URI ?? "http://127.0.0.1:3000";
    const database = Bun.env.SPACETIMEDB_DB ?? "tcp-capture";
    try {
        spacetime = DbConnection.builder()
            .withUri(uri)
            .withDatabaseName(database)
            .onConnect(() => console.log(`SpacetimeDB connected: ${database}`))
            .onConnectError((_context, error) => console.warn(`SpacetimeDB unavailable: ${error.message}`))
            .onDisconnect(() => { spacetime = undefined; console.warn("SpacetimeDB disconnected"); })
            .build();
    } catch (error) {
        console.warn(`SpacetimeDB unavailable: ${error instanceof Error ? error.message : String(error)}`);
    }
}

async function ingestPacket(packet: CapturePacket): Promise<void> {
    if (!spacetime) return;
    const source = endpointParts(packet.source);
    const destination = endpointParts(packet.destination);
    try {
        await spacetime.reducers.ingestPacket({
            captureId: packet.id,
            direction: packet.direction,
            source: source.address,
            destination: destination.address,
            sourcePort: source.port,
            destinationPort: destination.port,
            addressFamily: source.addressFamily,
            flags: packet.flags.join(" "),
            sequence: packet.sequence,
            acknowledgment: packet.acknowledgment,
            window: packet.window,
            payloadLength: packet.payloadLength,
        });
    } catch (error) {
        console.warn(`SpacetimeDB ingest failed: ${error instanceof Error ? error.message : String(error)}`);
    }
}

function broadcastStatus(status: "starting" | "capturing" | "error", message?: string): void {
    captureStatus = status;
    captureStatusMessage = message;
    const payload = JSON.stringify({ type: "status", status, message });
    for (const client of clients) client.send(payload);
}

function broadcast(packet: CapturePacket): void {
    const message = JSON.stringify(packet);
    for (const client of clients) client.send(message);
}

function parseEndpoint(value: string): string {
    const trimmed = value.replace(/[,:]$/, "");
    const match = trimmed.match(/^(.*)[.:](\d+)$/);
    return match ? `${match[1]}:${match[2]}` : trimmed;
}

function parseTcpSummary(line: string, direction: "client-to-server" | "server-to-client"): CapturePacket | undefined {
    if (!/\bFlags\s*\[/i.test(line)) return;
    const endpoints = line.match(/\s(\S+)\s+>\s+(\S+):\s+Flags\s*\[([^\]]+)\]/i);
    if (!endpoints) return;
    const sequence = line.match(/\bseq\s+(\d+)/i);
    const acknowledgment = line.match(/\back\s+(\d+)/i);
    const window = line.match(/\bwin\s+(\d+)/i);
    const length = line.match(/\blength\s+(\d+)/i);
    // tcpdump-style '.' is ACK; splitting on punctuation discarded it.
    const rawFlags = [...endpoints[3]!.trim().toUpperCase()];
    const flagNames: Record<string, string> = { S: "SYN", F: "FIN", R: "RST", P: "PSH", A: "ACK", U: "URG", E: "ECE", W: "CWR" };
    return {
        id: `pkt-${Date.now()}-${packetId++}`,
        direction,
        source: parseEndpoint(endpoints[1]!), destination: parseEndpoint(endpoints[2]!),
        flags: [...new Set(rawFlags.flatMap(flag => flag === "." ? ["ACK"] : flagNames[flag] ? [flagNames[flag]] : []))],
        sequence: Number(sequence?.[1] ?? 0) >>> 0,
        acknowledgment: Number(acknowledgment?.[1] ?? 0) >>> 0,
        window: Number(window?.[1] ?? 0),
        payloadLength: Number(length?.[1] ?? 0),
        timestamp: Date.now(),
    };
}

async function capture(): Promise<void> {
    broadcastStatus("starting");
    const process = Bun.spawn([
        "pktmon", "start", "--capture", "--comp", "nics", "--type", "flow",
        "--pkt-size", "128", "--flags", "0x010", "--log-mode", "real-time",
    ], { stdout: "pipe", stderr: "pipe" });
    broadcastStatus("capturing");
    const decoder = new TextDecoder();
    let pending = "";
    let pendingDirection: "client-to-server" | "server-to-client" | undefined;
    for await (const chunk of process.stdout) {
        pending += decoder.decode(chunk, { stream: true });
        const lines = pending.split(/\r?\n/); pending = lines.pop() ?? "";
        for (const line of lines) {
            const header = line.match(/Direction\s+(Tx|Rx)\b/i);
            if (header) {
                pendingDirection = header[1]!.toLowerCase() === "tx" ? "client-to-server" : "server-to-client";
                continue;
            }
            if (!pendingDirection) continue;
            const summary = parseTcpSummary(line, pendingDirection);
            if (summary) pendingDirection = undefined;
            if (summary) {
                broadcast(summary);
                void ingestPacket(summary);
            }
        }
    }
    const error = await new Response(process.stderr).text();
    const exitCode = await process.exited;
    const stderr = error.trim();
    if (stderr) console.error(stderr);
    if (exitCode !== 0) {
        const permissionHint = exitCode === 159
            ? " PktMon access was denied; run this capture bridge from an elevated Administrator PowerShell."
            : "";
        broadcastStatus("error", `${stderr || `pktmon exited with code ${exitCode}`}${permissionHint}`);
    }
}

const server = Bun.serve({
    port,
    fetch(request, server) {
        if (server.upgrade(request)) return;
        return new Response(JSON.stringify({ capture: "pktmon", clients: clients.size }), { headers: { "content-type": "application/json" } });
    },
    websocket: {
        open(socket) {
            clients.add(socket);
            socket.send(JSON.stringify({ type: "status", status: captureStatus, message: captureStatusMessage }));
        },
        close(socket) { clients.delete(socket); },
        message() { /* capture is read-only */ },
    },
});

console.log(`TCP capture bridge listening on ws://127.0.0.1:${server.port}`);
console.log("Run this process from an elevated terminal so pktmon can access its driver.");
await connectSpacetime();
await capture();
