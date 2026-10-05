import { schema, table, t } from "spacetimedb/server";

/**
 * SpacetimeDB module for the TCP capture stream.
 *
 * `index-2.ts` remains the protocol-core implementation. This module owns the
 * durable, queryable observation model: packet events, endpoint aggregates,
 * and connection aggregates. The privileged pktmon process should parse a
 * packet and call `ingestPacket`; reducers never perform OS/network I/O.
 */

const endpoint = table(
    { name: "endpoint", public: true },
    {
        id: t.string().primaryKey(),
        address: t.string(),
        port: t.u16(),
        addressFamily: t.u8(),
        firstSeen: t.timestamp(),
        lastSeen: t.timestamp(),
        packetsSent: t.u64(),
        packetsReceived: t.u64(),
        bytesSent: t.u64(),
        bytesReceived: t.u64(),
        active: t.bool(),
    },
);

const connection = table(
    { name: "connection", public: true },
    {
        id: t.string().primaryKey(),
        source: t.string(),
        destination: t.string(),
        sourcePort: t.u16(),
        destinationPort: t.u16(),
        addressFamily: t.u8(),
        state: t.string(),
        packets: t.u64(),
        bytes: t.u64(),
        lastSequence: t.u32(),
        lastAcknowledgment: t.u32(),
        lastSeen: t.timestamp(),
        active: t.bool(),
    },
);

const packet = table(
    { name: "packet", public: true },
    {
        id: t.u64().primaryKey().autoInc(),
        captureId: t.string(),
        connectionId: t.string().index("btree"),
        direction: t.string(),
        source: t.string(),
        destination: t.string(),
        flags: t.string(),
        sequence: t.u32(),
        acknowledgment: t.u32(),
        window: t.u32(),
        payloadLength: t.u32(),
        capturedAt: t.timestamp(),
    },
);

const spacetimedb = schema({ endpoint, connection, packet });
export default spacetimedb;

type PacketInput = {
    captureId: string;
    direction: string;
    source: string;
    destination: string;
    sourcePort: number;
    destinationPort: number;
    addressFamily: number;
    flags: string;
    sequence: number;
    acknowledgment: number;
    window: number;
    payloadLength: number;
};

function endpointId(address: string, port: number): string {
    return `${address}:${port}`;
}

function connectionId(input: PacketInput): string {
    return `${input.addressFamily}:${input.source}:${input.sourcePort}->${input.destination}:${input.destinationPort}`;
}

function stateFromFlags(flags: string): string {
    const normalized = flags.toUpperCase();
    if (normalized.includes("RST")) return "CLOSED";
    if (normalized.includes("SYN") && normalized.includes("ACK")) return "SYN_RECEIVED";
    if (normalized.includes("SYN")) return "SYN_SENT";
    if (normalized.includes("FIN")) return "FIN_WAIT_1";
    return "ESTABLISHED";
}

function validatePacket(input: PacketInput): void {
    if (input.sourcePort < 0 || input.sourcePort > 65_535 || !Number.isInteger(input.sourcePort))
        throw new Error("sourcePort must be a TCP port");
    if (input.destinationPort < 0 || input.destinationPort > 65_535 || !Number.isInteger(input.destinationPort))
        throw new Error("destinationPort must be a TCP port");
    if (input.addressFamily !== 4 && input.addressFamily !== 6)
        throw new Error("addressFamily must be 4 or 6");
    if (input.sequence < 0 || input.sequence > 0xffff_ffff || !Number.isInteger(input.sequence))
        throw new Error("sequence must be a uint32");
    if (input.acknowledgment < 0 || input.acknowledgment > 0xffff_ffff || !Number.isInteger(input.acknowledgment))
        throw new Error("acknowledgment must be a uint32");
    if (input.window < 0 || input.window > 0xffff_ffff || !Number.isInteger(input.window))
        throw new Error("window must be a uint32");
    if (input.payloadLength < 0 || !Number.isInteger(input.payloadLength))
        throw new Error("payloadLength must be non-negative");
}

export const ingestPacket = spacetimedb.reducer(
    {
        captureId: t.string(),
        direction: t.string(),
        source: t.string(),
        destination: t.string(),
        sourcePort: t.u16(),
        destinationPort: t.u16(),
        addressFamily: t.u8(),
        flags: t.string(),
        sequence: t.u32(),
        acknowledgment: t.u32(),
        window: t.u32(),
        payloadLength: t.u32(),
    },
    (ctx, input) => {
        validatePacket(input);
        const now = ctx.timestamp;
        const sourceId = endpointId(input.source, input.sourcePort);
        const destinationId = endpointId(input.destination, input.destinationPort);
        const flowId = connectionId(input);
        const outbound = input.direction === "client-to-server" || input.direction === "outbound";

        const source = ctx.db.endpoint.id.find(sourceId);
        const destination = ctx.db.endpoint.id.find(destinationId);
        if (source) {
            ctx.db.endpoint.id.update({
                ...source,
                lastSeen: now,
                packetsSent: source.packetsSent + (outbound ? 1n : 0n),
                packetsReceived: source.packetsReceived + (outbound ? 0n : 1n),
                bytesSent: source.bytesSent + (outbound ? BigInt(input.payloadLength) : 0n),
                bytesReceived: source.bytesReceived + (outbound ? 0n : BigInt(input.payloadLength)),
                active: true,
            });
        } else {
            ctx.db.endpoint.insert({
                id: sourceId, address: input.source, port: input.sourcePort,
                addressFamily: input.addressFamily, firstSeen: now, lastSeen: now,
                packetsSent: outbound ? 1n : 0n, packetsReceived: outbound ? 0n : 1n,
                bytesSent: outbound ? BigInt(input.payloadLength) : 0n,
                bytesReceived: outbound ? 0n : BigInt(input.payloadLength), active: true,
            });
        }
        if (destination) {
            ctx.db.endpoint.id.update({
                ...destination,
                lastSeen: now,
                packetsSent: destination.packetsSent + (outbound ? 0n : 1n),
                packetsReceived: destination.packetsReceived + (outbound ? 1n : 0n),
                bytesSent: destination.bytesSent + (outbound ? 0n : BigInt(input.payloadLength)),
                bytesReceived: destination.bytesReceived + (outbound ? BigInt(input.payloadLength) : 0n),
                active: true,
            });
        } else {
            ctx.db.endpoint.insert({
                id: destinationId, address: input.destination, port: input.destinationPort,
                addressFamily: input.addressFamily, firstSeen: now, lastSeen: now,
                packetsSent: outbound ? 0n : 1n, packetsReceived: outbound ? 1n : 0n,
                bytesSent: outbound ? 0n : BigInt(input.payloadLength),
                bytesReceived: outbound ? BigInt(input.payloadLength) : 0n, active: true,
            });
        }

        const existingConnection = ctx.db.connection.id.find(flowId);
        if (existingConnection) {
            ctx.db.connection.id.update({
                ...existingConnection, state: stateFromFlags(input.flags), packets: existingConnection.packets + 1n,
                bytes: existingConnection.bytes + BigInt(input.payloadLength), lastSequence: input.sequence,
                lastAcknowledgment: input.acknowledgment, lastSeen: now, active: true,
            });
        } else {
            ctx.db.connection.insert({
                id: flowId, source: input.source, destination: input.destination,
                sourcePort: input.sourcePort, destinationPort: input.destinationPort,
                addressFamily: input.addressFamily, state: stateFromFlags(input.flags), packets: 1n,
                bytes: BigInt(input.payloadLength), lastSequence: input.sequence,
                lastAcknowledgment: input.acknowledgment, lastSeen: now, active: true,
            });
        }

        ctx.db.packet.insert({
            id: 0n, captureId: input.captureId, connectionId: flowId, direction: input.direction,
            source: input.source, destination: input.destination, flags: input.flags,
            sequence: input.sequence, acknowledgment: input.acknowledgment, window: input.window,
            payloadLength: input.payloadLength, capturedAt: now,
        });
    },
);

export const clearCapture = spacetimedb.reducer((ctx) => {
    for (const row of ctx.db.packet.iter()) ctx.db.packet.id.delete(row.id);
    for (const row of ctx.db.connection.iter()) ctx.db.connection.id.delete(row.id);
    for (const row of ctx.db.endpoint.iter()) ctx.db.endpoint.id.delete(row.id);
});
