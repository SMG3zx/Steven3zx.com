import assert from "node:assert/strict";

/* RFC 9293 TCP core. A socket adapter can feed/consume TCPSegment values. */

export enum TCPState {
    CLOSED, LISTEN, SYN_SENT, SYN_RECEIVED, ESTABLISHED,
    FIN_WAIT_1, FIN_WAIT_2, CLOSE_WAIT, CLOSING, LAST_ACK, TIME_WAIT,
}

class ByteQueue {
    private bytes = new Uint8Array(64);
    private start = 0;
    private end = 0;

    get length(): number { return this.end - this.start; }

    push(...values: number[]): number {
        if (values.length === 0) return this.length;
        this.ensure(values.length);
        for (const value of values) this.bytes[this.end++] = value & 255;
        return this.length;
    }

    append(values: Uint8Array): number {
        if (values.length === 0) return this.length;
        this.ensure(values.length); this.bytes.set(values, this.end); this.end += values.length;
        return this.length;
    }

    splice(start: number, deleteCount = this.length - start): number[] {
        assert.equal(start, 0);
        const count = Math.max(0, Math.min(deleteCount, this.length));
        const removed = Array.from(this.take(count));
        return removed;
    }

    take(count = this.length): Uint8Array {
        const length = Math.max(0, Math.min(count, this.length));
        const removed = this.bytes.slice(this.start, this.start + length);
        this.start += length;
        if (this.start === this.end) this.start = this.end = 0;
        return removed;
    }

    toUint8Array(): Uint8Array { return this.bytes.slice(this.start, this.end); }

    at(index: number): number | undefined {
        const offset = index < 0 ? this.length + index : index;
        return offset >= 0 && offset < this.length ? this.bytes[this.start + offset] : undefined;
    }

    *[Symbol.iterator](): IterableIterator<number> {
        yield* this.bytes.subarray(this.start, this.end);
    }

    private ensure(additional: number): void {
        if (this.end + additional <= this.bytes.length) return;
        if (this.length + additional <= this.bytes.length) {
            this.bytes.copyWithin(0, this.start, this.end);
            this.end = this.length; this.start = 0; return;
        }
        let capacity = this.bytes.length;
        while (capacity < this.length + additional) capacity *= 2;
        const next = new Uint8Array(capacity);
        next.set(this.bytes.subarray(this.start, this.end));
        this.bytes = next; this.end = this.length; this.start = 0;
    }
}

export class TCP_Control_Bits {
    CWR = false; ECE = false; URG = false; ACK = false;
    PSH = false; RST = false; SYN = false; FIN = false;

    toByte(): number {
        return (this.CWR ? 0x80 : 0) | (this.ECE ? 0x40 : 0) |
            (this.URG ? 0x20 : 0) | (this.ACK ? 0x10 : 0) |
            (this.PSH ? 0x08 : 0) | (this.RST ? 0x04 : 0) |
            (this.SYN ? 0x02 : 0) | (this.FIN ? 0x01 : 0);
    }

    static fromByte(value: number): TCP_Control_Bits {
        const bits = new TCP_Control_Bits();
        bits.CWR = !!(value & 0x80); bits.ECE = !!(value & 0x40);
        bits.URG = !!(value & 0x20); bits.ACK = !!(value & 0x10);
        bits.PSH = !!(value & 0x08); bits.RST = !!(value & 0x04);
        bits.SYN = !!(value & 0x02); bits.FIN = !!(value & 0x01);
        return bits;
    }
}

export type SackBlock = { left: number; right: number };
export type TCPConnectionStatus = {
    state: TCPState; addressFamily: 4 | 6; localPort: number; remotePort: number;
    sndUna: number; sndNxt: number; sndWnd: number; rcvNxt: number; rcvWnd: number;
    queuedSendBytes: number; bufferedReceiveBytes: number; outstandingSegments: number;
    retransmissionTimeout: number; smoothedRtt?: number;
};
export type TCPEvent = { type: "urgent" | "reset" | "remote-close" | "closed" | "timeout" | "retransmission-warning";
    urgentBytes?: number; retries?: number } | { type: "network-error"; error: string };
export type TCPPacketTrace = { direction: "inbound" | "outbound"; segment: TCPSegment; at: number };
export type TCPPacketObserver = (trace: TCPPacketTrace) => void;

export class TCP_Options {
    EOL = false; NOP = false;
    MSS?: number;
    Window_Scale?: number;
    SACK_Permitted = false;
    SACK: SackBlock[] = [];
    Timestamp_Value?: number;
    Timestamp_Echo_Reply?: number;

    encode(): Uint8Array {
        const validU32 = (value: number): boolean => Number.isInteger(value) && value >= 0 && value <= 0xffff_ffff;
        if (this.MSS !== undefined) assert(Number.isInteger(this.MSS) && this.MSS >= 1 && this.MSS <= 0xffff);
        if (this.Window_Scale !== undefined) assert(Number.isInteger(this.Window_Scale) && this.Window_Scale >= 0 && this.Window_Scale <= 14);
        assert(this.SACK.length <= 4);
        assert((this.Timestamp_Value === undefined) === (this.Timestamp_Echo_Reply === undefined));
        assert(this.Timestamp_Value === undefined || validU32(this.Timestamp_Value));
        assert(this.Timestamp_Echo_Reply === undefined || validU32(this.Timestamp_Echo_Reply));
        for (const block of this.SACK) {
            assert(validU32(block.left) && validU32(block.right) && block.left !== block.right);
        }
        const out = new Uint8Array(40); let used = 0;
        const put = (value: number): void => { out[used++] = value & 255; };
        if (this.MSS !== undefined) { put(2); put(4); put(this.MSS >>> 8); put(this.MSS); }
        if (this.Window_Scale !== undefined) { put(3); put(3); put(this.Window_Scale); }
        if (this.SACK_Permitted) { put(4); put(2); }
        if (this.SACK.length) {
            const length = 2 + this.SACK.length * 8;
            put(5); put(length);
            for (const block of this.SACK) {
                put(block.left >>> 24); put(block.left >>> 16); put(block.left >>> 8); put(block.left);
                put(block.right >>> 24); put(block.right >>> 16); put(block.right >>> 8); put(block.right);
            }
        }
        if (this.Timestamp_Value !== undefined && this.Timestamp_Echo_Reply !== undefined) {
            put(8); put(10); put(this.Timestamp_Value >>> 24); put(this.Timestamp_Value >>> 16);
            put(this.Timestamp_Value >>> 8); put(this.Timestamp_Value);
            put(this.Timestamp_Echo_Reply >>> 24); put(this.Timestamp_Echo_Reply >>> 16);
            put(this.Timestamp_Echo_Reply >>> 8); put(this.Timestamp_Echo_Reply);
        }
        if (this.NOP) put(1);
        if (this.EOL) put(0);
        while (used % 4) put(this.EOL ? 0 : 1);
        assert(used <= 40);
        return out.slice(0, used);
    }

    static decode(bytes: Uint8Array): TCP_Options {
        const options = new TCP_Options();
        for (let i = 0; i < bytes.length;) {
            const kind = bytes[i]!;
            if (kind === 0) {
                options.EOL = true;
                for (let padding = i + 1; padding < bytes.length; padding++)
                    assert.equal(bytes[padding], 0);
                break;
            }
            if (kind === 1) { options.NOP = true; i++; continue; }
            assert(i + 1 < bytes.length);
            const length = bytes[i + 1]!;
            assert(length >= 2 && i + length <= bytes.length);
            const view = new DataView(bytes.buffer, bytes.byteOffset + i, length);
            if (kind === 2 && length === 4) options.MSS = view.getUint16(2);
            else if (kind === 3 && length === 3) {
                const scale = view.getUint8(2); assert(scale <= 14); options.Window_Scale = scale;
            }
            else if (kind === 4 && length === 2) options.SACK_Permitted = true;
            else if (kind === 5 && length >= 10 && (length - 2) % 8 === 0) {
                for (let p = 2; p < length; p += 8) {
                    const left = view.getUint32(p), right = view.getUint32(p + 4);
                    assert(left !== right); options.SACK.push({ left, right });
                }
            } else if (kind === 8 && length === 10) {
                options.Timestamp_Value = view.getUint32(2);
                options.Timestamp_Echo_Reply = view.getUint32(6);
            }
            i += length;
        }
        return options;
    }
}

function ipv4(address: string): Uint8Array {
    const parts = address.split(".").map(Number);
    assert(parts.length === 4 && parts.every((x) => Number.isInteger(x) && x >= 0 && x <= 255));
    return Uint8Array.from(parts);
}

function ipv6(address: string): Uint8Array {
    const embeddedIpv4 = address.match(/^(.*:)(\d+\.\d+\.\d+\.\d+)$/);
    if (embeddedIpv4) {
        const tail = ipv4(embeddedIpv4[2]!);
        const high = ((tail[0]! << 8) | tail[1]!).toString(16);
        const low = ((tail[2]! << 8) | tail[3]!).toString(16);
        address = `${embeddedIpv4[1]}${high}:${low}`;
    }
    const halves = address.split("::");
    assert(halves.length <= 2);
    const left = halves[0] ? halves[0].split(":").filter(Boolean) : [];
    const right = halves.length === 2 && halves[1] ? halves[1].split(":").filter(Boolean) : [];
    const zeroes = halves.length === 2 ? 8 - left.length - right.length : 0;
    assert(zeroes >= 0 && (halves.length === 2 || left.length === 8));
    const words = [...left, ...Array.from({ length: zeroes }, () => "0"), ...right];
    assert(words.length === 8 && words.every((part) => /^[0-9a-f]{1,4}$/i.test(part)));
    const bytes = new Uint8Array(16);
    words.forEach((part, index) => new DataView(bytes.buffer).setUint16(index * 2, parseInt(part, 16)));
    return bytes;
}

const ipAddressCache = new Map<string, Uint8Array>();

function ipAddress(address: string): Uint8Array {
    const cached = ipAddressCache.get(address);
    if (cached) return cached;
    const parsed = address.includes(":") ? ipv6(address) : ipv4(address);
    if (ipAddressCache.size >= 16) {
        const oldest = ipAddressCache.keys().next().value as string | undefined;
        if (oldest !== undefined) ipAddressCache.delete(oldest);
    }
    ipAddressCache.set(address, parsed);
    return parsed;
}

function addressKey(address: string): string {
    return Array.from(ipAddress(address), (value) => value.toString(16).padStart(2, "0")).join("");
}

function isBroadcastOrMulticast(address: string): boolean {
    const bytes = ipAddress(address);
    if (bytes.length === 4)
        return bytes.every((value) => value === 255) || (bytes[0]! >= 224 && bytes[0]! <= 239);
    return bytes[0] === 0xff;
}

function isUnspecifiedAddress(address: string): boolean {
    return ipAddress(address).every((value) => value === 0);
}

function tcpChecksum(segment: Uint8Array, source: string, destination: string): number {
    const sourceBytes = ipAddress(source), destinationBytes = ipAddress(destination);
    assert.equal(sourceBytes.length, destinationBytes.length);
    let sum = 0;
    for (let i = 0; i < sourceBytes.length; i += 2)
        sum += (sourceBytes[i]! << 8) | (sourceBytes[i + 1] ?? 0);
    for (let i = 0; i < destinationBytes.length; i += 2)
        sum += (destinationBytes[i]! << 8) | (destinationBytes[i + 1] ?? 0);
    if (sourceBytes.length === 4) {
        sum += 6 + segment.length;
    } else {
        sum += Math.floor(segment.length / 0x1_0000) + (segment.length & 0xffff) + 6;
    }
    for (let i = 0; i < segment.length; i += 2) {
        const high = i === 16 || i === 17 ? 0 : segment[i]!;
        const lowIndex = i + 1;
        const low = i === 16 ? 0 : (segment[lowIndex] ?? 0);
        sum += (high << 8) | low;
    }
    while (sum >>> 16) sum = (sum & 0xffff) + (sum >>> 16);
    return (~sum) & 0xffff;
}

function referenceTcpChecksum(segment: Uint8Array, source: string, destination: string): number {
    const sourceBytes = ipAddress(source), destinationBytes = ipAddress(destination);
    assert.equal(sourceBytes.length, destinationBytes.length);
    const pseudoLength = sourceBytes.length === 4 ? 12 : 40;
    const pseudo = new Uint8Array(pseudoLength + segment.length);
    pseudo.set(sourceBytes); pseudo.set(destinationBytes, sourceBytes.length);
    const view = new DataView(pseudo.buffer);
    if (sourceBytes.length === 4) {
        pseudo[9] = 6; view.setUint16(10, segment.length); pseudo.set(segment, 12);
    } else {
        view.setUint32(32, segment.length); pseudo[39] = 6; pseudo.set(segment, 40);
    }
    pseudo[ pseudoLength + 16 ] = 0; pseudo[ pseudoLength + 17 ] = 0;
    let sum = 0;
    for (let i = 0; i < pseudo.length; i += 2) {
        sum += (pseudo[i]! << 8) | (pseudo[i + 1] ?? 0);
        sum = (sum & 0xffff) + (sum >>> 16);
    }
    while (sum >>> 16) sum = (sum & 0xffff) + (sum >>> 16);
    return (~sum) & 0xffff;
}

export class TCPSegment {
    constructor(
        public sourcePort = 0, public destinationPort = 0,
        public sequenceNumber = 0, public acknowledgmentNumber = 0,
        public controlBits = new TCP_Control_Bits(), public window = 0,
        public urgentPointer = 0, public options = new TCP_Options(),
        public payload: Uint8Array<ArrayBufferLike> = new Uint8Array(), public checksum = 0,
    ) { }

    validate(): void {
        assert(Number.isInteger(this.sourcePort) && this.sourcePort >= 0 && this.sourcePort <= 0xffff);
        assert(Number.isInteger(this.destinationPort) && this.destinationPort >= 0 && this.destinationPort <= 0xffff);
        assert(Number.isInteger(this.sequenceNumber) && this.sequenceNumber >= 0 && this.sequenceNumber <= 0xffff_ffff);
        assert(Number.isInteger(this.acknowledgmentNumber) && this.acknowledgmentNumber >= 0 && this.acknowledgmentNumber <= 0xffff_ffff);
        assert(Number.isInteger(this.window) && this.window >= 0 && this.window <= 0xffff);
        assert(Number.isInteger(this.urgentPointer) && this.urgentPointer >= 0 && this.urgentPointer <= 0xffff);
        assert(!(this.controlBits.SYN && this.controlBits.FIN));
    }

    encode(sourceAddress: string, destinationAddress: string): Uint8Array {
        this.validate();
        if (!this.controlBits.SYN) {
            assert(this.options.MSS === undefined);
            assert(this.options.Window_Scale === undefined);
            assert(!this.options.SACK_Permitted);
        } else {
            assert.equal(this.options.SACK.length, 0);
        }
        const optionBytes = this.options.encode();
        const headerLength = 20 + optionBytes.length;
        assert(headerLength <= 60 && headerLength % 4 === 0);
        const bytes = new Uint8Array(headerLength + this.payload.length);
        const view = new DataView(bytes.buffer);
        view.setUint16(0, this.sourcePort); view.setUint16(2, this.destinationPort);
        view.setUint32(4, this.sequenceNumber); view.setUint32(8, this.acknowledgmentNumber);
        view.setUint8(12, headerLength / 4 << 4); view.setUint8(13, this.controlBits.toByte());
        view.setUint16(14, this.window); view.setUint16(16, 0); view.setUint16(18, this.urgentPointer);
        bytes.set(optionBytes, 20); bytes.set(this.payload, headerLength);
        const sum = tcpChecksum(bytes, sourceAddress, destinationAddress);
        view.setUint16(16, sum); this.checksum = sum; return bytes;
    }

    static decode(bytes: Uint8Array, sourceAddress: string, destinationAddress: string, verify = true): TCPSegment {
        assert(bytes.length >= 20);
        const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
        const headerLength = (view.getUint8(12) >>> 4) * 4;
        assert(headerLength >= 20 && headerLength <= bytes.length);
        // RFC 9293: reserved bits are zero when generated and ignored when received.
        const checksum = view.getUint16(16);
        if (verify) assert.equal(tcpChecksum(bytes, sourceAddress, destinationAddress), checksum);
        const options = TCP_Options.decode(bytes.slice(20, headerLength));
        const controlBits = TCP_Control_Bits.fromByte(view.getUint8(13));
        if (!controlBits.SYN) {
            options.MSS = undefined; options.Window_Scale = undefined; options.SACK_Permitted = false;
        }
        const segment = new TCPSegment(view.getUint16(0), view.getUint16(2), view.getUint32(4), view.getUint32(8),
            controlBits, view.getUint16(14), view.getUint16(18), options,
            bytes.slice(headerLength), checksum);
        segment.validate(); return segment;
    }
}

export function sequenceInWindow(sequence: number, length: number, next: number, window: number): boolean {
    if (length === 0) return window === 0 ? sequence === next : ((sequence - next) >>> 0) < window;
    if (window === 0) return false;
    // Two half-open serial-number intervals overlap when either the segment
    // starts inside the receive window or the receive-window start lies
    // inside the segment. This also handles a segment spanning the entire
    // window and ranges crossing 2^32.
    return ((sequence - next) >>> 0) < window || ((next - sequence) >>> 0) < length;
}

export function sequenceSpaceLength(segment: TCPSegment): number {
    return segment.payload.length + (segment.controlBits.SYN ? 1 : 0) +
        (segment.controlBits.FIN ? 1 : 0);
}

export function segmentAcceptable(segment: TCPSegment, next: number, window: number): boolean {
    return sequenceInWindow(segment.sequenceNumber, sequenceSpaceLength(segment), next, window);
}

type Outstanding = { sequence: number; length: number; sentAt: number; segment: TCPSegment; retransmitted: boolean; retries: number };

function sequenceLess(left: number, right: number): boolean {
    return left !== right && ((right - left) >>> 0) < 0x8000_0000;
}

function sequenceLessOrEqual(left: number, right: number): boolean {
    return left === right || sequenceLess(left, right);
}

export class TCPConnection {
    state = TCPState.CLOSED; iss = 0; sndUna = 0; sndNxt = 0;
    private passiveOpen = false;
    irs = 0; rcvNxt = 0; rcvWnd = 65_535; sndWnd = 65_535; maximumSendWindow = 65_535;
    sndWndSeq = 0; sndWndAck = 0; sndWndAckValid = false;
    private sendWindowObserved = false;
    advertisedRcvWnd = 65_535; receiveBufferCapacity = 65_535; windowUpdatePending = false;
    sendMss = 1460; localMss = 1460; windowScale = 7; peerWindowScale = 0;
    pathMtu = 65_535;
    congestionWindow = 14_600; slowStartThreshold = 65_535;
    retransmissionTimeout = 1_000; retransmissionBackoff = 1;
    maxRetransmissions = 12;
    duplicateAcknowledgments = 0;
    userTimeoutMs = 300_000;
    smoothedRtt?: number; rttVariance = 0;
    timeWaitUntil = 0;
    ecnNegotiated = false; cwrPending = false; urgentSequenceEnd?: number;
    timestampsEnabled = false; timestampRecent = 0;
    sackPermitted = false;
    private selectiveAcknowledgments: SackBlock[] = [];
    delayedAcknowledgments = false; delayedAckDelayMs = 40; ackPending = false; ackDeadline = 0;
    private delayedAckSegments = 0; private delayedAckBytes = 0;
    nagleEnabled = true; swsOverrideDelayMs = 1_000; swsOverrideAt = 0;
    keepAliveEnabled = false; keepAliveIntervalMs = 7_200_000; lastActivity = performance.now();
    readonly receiveBuffer = new ByteQueue();
    readonly urgentBuffer = new ByteQueue();
    readonly outOfOrder: TCPSegment[] = [];
    readonly sendQueue = new ByteQueue();
    private pendingPush = false;
    private closePending = false;
    readonly retransmissionQueue: Outstanding[] = [];
    readonly events: TCPEvent[] = [];
    private readonly packetObservers = new Set<TCPPacketObserver>();
    private receiveDepth = 0;
    private pendingInitialPayload = new Uint8Array();
    private pendingInitialFin = false;
    private pendingFinSequence?: number;
    private pendingFinAcknowledgment = false;
    private zeroWindowProbeAt = 0;

    constructor(public readonly localPort: number, public remotePort: number, public readonly addressFamily: 4 | 6 = 4) {
        assert(Number.isInteger(localPort) && localPort >= 0 && localPort <= 0xffff);
        assert(Number.isInteger(remotePort) && remotePort >= 0 && remotePort <= 0xffff);
        if (addressFamily === 6) { this.localMss = 1220; this.sendMss = 1220; }
    }
    listen(): void { assert.equal(this.state, TCPState.CLOSED); this.passiveOpen = true; this.state = TCPState.LISTEN; }

    private initialSequence(): number {
        const clock = Math.floor(performance.now() * 250) >>> 0;
        const entropy = new Uint32Array(1);
        globalThis.crypto?.getRandomValues(entropy);
        return (clock ^ entropy[0]!) >>> 0;
    }

    private advertisedMss(): number {
        const ipHeader = this.addressFamily === 4 ? 20 : 40;
        return Math.max(1, Math.min(this.localMss, this.pathMtu - ipHeader - 20));
    }

    private synOptions(timestampEchoReply = 0): TCP_Options {
        const options = new TCP_Options();
        options.MSS = this.advertisedMss(); options.Window_Scale = this.windowScale; options.SACK_Permitted = true;
        options.Timestamp_Value = this.timestamp(); options.Timestamp_Echo_Reply = timestampEchoReply >>> 0;
        return options;
    }

    private timestamp(): number { return Math.floor(performance.now()) >>> 0; }

    private dataOptions(): TCP_Options {
        const options = new TCP_Options();
        if (this.timestampsEnabled) { options.Timestamp_Value = this.timestamp(); options.Timestamp_Echo_Reply = this.timestampRecent; }
        return options;
    }

    private applyPeerMss(mss: number): void {
        this.sendMss = mss;
        this.congestionWindow = Math.min(this.congestionWindow, 10 * mss);
    }

    private applySendWindow(window: number): void {
        this.sndWnd = Math.min(0xffff_ffff, window);
        if (this.sndWnd > 0) this.zeroWindowProbeAt = 0;
        if (!this.sendWindowObserved) {
            this.maximumSendWindow = this.sndWnd; this.sendWindowObserved = true;
        } else this.maximumSendWindow = Math.max(this.maximumSendWindow, this.sndWnd);
    }

    private effectiveSendMss(): number {
        const ipHeader = this.addressFamily === 4 ? 20 : 40;
        const optionLength = this.dataOptions().encode().length;
        return Math.max(1, Math.min(this.sendMss - optionLength,
            this.pathMtu - ipHeader - 20 - optionLength));
    }

    private advertisedWindow(): number {
        const scaled = this.state === TCPState.ESTABLISHED || this.state === TCPState.FIN_WAIT_1 ||
            this.state === TCPState.FIN_WAIT_2 || this.state === TCPState.CLOSE_WAIT;
        return Math.min(0xffff, scaled ? Math.floor(this.advertisedRcvWnd / 2 ** this.windowScale) : this.advertisedRcvWnd);
    }

    activeOpen(initialSequence = this.initialSequence(), payload = new Uint8Array()): TCPSegment {
        assert.equal(this.state, TCPState.CLOSED);
        this.passiveOpen = false;
        assert(payload.length <= this.sendMss);
        this.iss = initialSequence >>> 0; this.sndUna = this.iss;
        this.sndNxt = (this.iss + 1 + payload.length) >>> 0;
        this.state = TCPState.SYN_SENT;
        const segment = new TCPSegment(this.localPort, this.remotePort, this.iss, 0,
            new TCP_Control_Bits(), this.advertisedWindow(), 0, this.synOptions(), payload);
        segment.controlBits.SYN = true; segment.controlBits.CWR = true; segment.controlBits.ECE = true;
        this.track(segment, this.iss, 1 + payload.length); this.emitGenerated(segment); return segment;
    }

    observePackets(observer: TCPPacketObserver): () => void {
        this.packetObservers.add(observer);
        return () => this.packetObservers.delete(observer);
    }

    private emitPacket(direction: TCPPacketTrace["direction"], segment: TCPSegment): void {
        if (!this.packetObservers.size) return;
        const trace = { direction, segment, at: performance.now() };
        for (const observer of this.packetObservers) observer(trace);
    }

    private emitGenerated(segment: TCPSegment): void {
        if (this.receiveDepth === 0) this.emitPacket("outbound", segment);
    }

    receive(segment: TCPSegment): TCPSegment | undefined {
        try { segment.validate(); }
        catch { return; }
        this.receiveDepth++;
        let response: TCPSegment | undefined;
        try { response = this.receiveSegment(segment); }
        finally { this.receiveDepth--; }
        this.emitPacket("inbound", segment);
        if (response) this.emitPacket("outbound", response);
        return response;
    }

    private receiveSegment(segment: TCPSegment): TCPSegment | undefined {
        this.lastActivity = performance.now();
        assert(this.state === TCPState.LISTEN && this.remotePort === 0 || segment.sourcePort === this.remotePort);
        assert.equal(segment.destinationPort, this.localPort);
        if (this.state === TCPState.CLOSED && !segment.controlBits.RST) return this.resetFor(segment);
        if (this.state === TCPState.CLOSED && segment.controlBits.RST) return;
        if (this.state === TCPState.LISTEN && segment.controlBits.RST) return;
        if (this.state === TCPState.SYN_SENT && segment.controlBits.RST) {
            if (segment.controlBits.ACK && sequenceLess(this.iss, segment.acknowledgmentNumber) &&
                sequenceLessOrEqual(segment.acknowledgmentNumber, this.sndNxt)) {
                this.state = TCPState.CLOSED; this.events.push({ type: "reset" }, { type: "closed" });
            }
            return;
        }
        if (this.state === TCPState.SYN_SENT && segment.controlBits.ACK && !segment.controlBits.SYN)
            return this.resetFor(segment);
        const rstSequenceChecked = this.state === TCPState.SYN_RECEIVED || this.state === TCPState.ESTABLISHED ||
            this.state === TCPState.FIN_WAIT_1 || this.state === TCPState.FIN_WAIT_2 ||
            this.state === TCPState.CLOSE_WAIT || this.state === TCPState.CLOSING ||
            this.state === TCPState.LAST_ACK || this.state === TCPState.TIME_WAIT;
        if (segment.controlBits.RST) {
            if (rstSequenceChecked && !sequenceInWindow(segment.sequenceNumber, 0, this.rcvNxt, this.rcvWnd)) return;
            if (rstSequenceChecked && segment.sequenceNumber !== this.rcvNxt) return this.acknowledgment();
            if (this.state === TCPState.SYN_RECEIVED && this.passiveOpen) this.state = TCPState.LISTEN;
            else { this.state = TCPState.CLOSED; this.events.push({ type: "reset" }, { type: "closed" }); }
            return;
        }
        if (this.state === TCPState.LISTEN && segment.controlBits.ACK) return this.resetFor(segment);
        if (this.state === TCPState.LISTEN && segment.controlBits.SYN) {
            if (this.remotePort === 0) this.remotePort = segment.sourcePort;
            this.applyPeerMss(segment.options.MSS ?? (this.addressFamily === 6 ? 1220 : 536));
            if (segment.options.Window_Scale !== undefined) this.peerWindowScale = segment.options.Window_Scale;
            this.sackPermitted = segment.options.SACK_Permitted;
            this.ecnNegotiated = segment.controlBits.ECE && segment.controlBits.CWR;
            this.applySendWindow(segment.window);
            this.irs = segment.sequenceNumber; this.rcvNxt = (this.irs + 1) >>> 0;
            if (segment.options.Timestamp_Value !== undefined) {
                this.timestampRecent = segment.options.Timestamp_Value; this.timestampsEnabled = true;
            }
            if (segment.payload.length) this.pendingInitialPayload = segment.payload.slice();
            this.pendingInitialFin = segment.controlBits.FIN;
            this.iss = this.initialSequence(); this.sndUna = this.iss; this.sndNxt = (this.iss + 1) >>> 0;
            this.state = TCPState.SYN_RECEIVED;
            const reply = new TCPSegment(this.localPort, this.remotePort, this.iss, this.rcvNxt,
                new TCP_Control_Bits(), this.advertisedWindow(), 0, this.synOptions(segment.options.Timestamp_Value ?? 0));
            reply.controlBits.SYN = true; reply.controlBits.ACK = true; reply.controlBits.ECE = this.ecnNegotiated;
            this.track(reply, this.iss, 1); return reply;
        }
        if (this.state === TCPState.SYN_SENT && segment.controlBits.SYN && segment.controlBits.ACK) {
            if (!sequenceLess(this.iss, segment.acknowledgmentNumber) ||
                !sequenceLessOrEqual(segment.acknowledgmentNumber, this.sndNxt)) return this.resetFor(segment);
            this.applyPeerMss(segment.options.MSS ?? (this.addressFamily === 6 ? 1220 : 536));
            if (segment.options.Window_Scale !== undefined) this.peerWindowScale = segment.options.Window_Scale;
            this.sackPermitted = segment.options.SACK_Permitted;
            this.ecnNegotiated = segment.controlBits.ECE;
            this.applySendWindow(segment.window);
            this.irs = segment.sequenceNumber; this.rcvNxt = (this.irs + 1) >>> 0;
            if (segment.options.Timestamp_Value !== undefined) {
                this.timestampRecent = segment.options.Timestamp_Value; this.timestampsEnabled = true;
            }
            this.sndUna = segment.acknowledgmentNumber; this.acknowledgeRetransmissions(this.sndUna); this.state = TCPState.ESTABLISHED;
            if (segment.payload.length) this.acceptPayload(segment.payload);
            return this.acknowledgment();
        }
        if (this.state === TCPState.SYN_SENT && segment.controlBits.SYN && !segment.controlBits.ACK) {
            this.applyPeerMss(segment.options.MSS ?? (this.addressFamily === 6 ? 1220 : 536));
            if (segment.options.Window_Scale !== undefined) this.peerWindowScale = segment.options.Window_Scale;
            this.sackPermitted = segment.options.SACK_Permitted;
            this.ecnNegotiated = segment.controlBits.ECE && segment.controlBits.CWR;
            this.applySendWindow(segment.window);
            this.irs = segment.sequenceNumber; this.rcvNxt = (this.irs + 1) >>> 0;
            if (segment.options.Timestamp_Value !== undefined) {
                this.timestampRecent = segment.options.Timestamp_Value; this.timestampsEnabled = true;
            }
            this.state = TCPState.SYN_RECEIVED;
            const reply = new TCPSegment(this.localPort, this.remotePort, this.iss, this.rcvNxt,
                new TCP_Control_Bits(), this.rcvWnd, 0, this.synOptions(segment.options.Timestamp_Value ?? 0));
            reply.controlBits.SYN = true; reply.controlBits.ACK = true; reply.controlBits.ECE = this.ecnNegotiated;
            this.track(reply, this.iss, 1); return reply;
        }
        if (this.state === TCPState.SYN_RECEIVED && segment.controlBits.SYN && !segment.controlBits.ACK) {
            if (segment.sequenceNumber === this.irs) {
                return this.retransmissionQueue.find((entry) => entry.segment.controlBits.SYN)?.segment;
            }
            return this.acknowledgment();
        }
        if (this.state === TCPState.SYN_RECEIVED && segment.controlBits.ACK) {
            if (segment.acknowledgmentNumber !== this.sndNxt) return this.resetFor(segment);
            this.applySendWindow(segment.window * 2 ** this.peerWindowScale);
            this.sndWndSeq = segment.sequenceNumber; this.sndWndAck = segment.acknowledgmentNumber; this.sndWndAckValid = true;
            this.sndUna = segment.acknowledgmentNumber; this.acknowledgeRetransmissions(this.sndUna); this.state = TCPState.ESTABLISHED;
            const hadInitialData = this.pendingInitialPayload.length > 0;
            if (this.pendingInitialPayload.length) {
                this.acceptPayload(this.pendingInitialPayload);
                this.pendingInitialPayload = new Uint8Array();
            }
            if (this.pendingInitialFin) {
                this.pendingInitialFin = false; this.rcvNxt = (this.rcvNxt + 1) >>> 0;
                this.state = TCPState.CLOSE_WAIT; this.events.push({ type: "remote-close" });
            }
            // The third handshake ACK may carry data (and, in a half-close,
            // FIN). Accept it after transitioning to ESTABLISHED.
            if (segment.payload.length || segment.controlBits.FIN) {
                if (!segmentAcceptable(segment, this.rcvNxt, this.rcvWnd) || segment.sequenceNumber !== this.rcvNxt)
                    return this.acknowledgment();
                if (segment.payload.length) this.acceptPayload(segment.payload);
                if (segment.controlBits.FIN) this.acceptFin(segment);
                return this.acknowledgment();
            }
            return hadInitialData || this.state === TCPState.CLOSE_WAIT ? this.acknowledgment() : undefined;
        }
        const sequenceCheckedState = this.state === TCPState.ESTABLISHED || this.state === TCPState.FIN_WAIT_1 ||
            this.state === TCPState.FIN_WAIT_2 || this.state === TCPState.CLOSE_WAIT ||
            this.state === TCPState.CLOSING || this.state === TCPState.LAST_ACK || this.state === TCPState.TIME_WAIT;
        if (sequenceCheckedState && this.rcvWnd === 0 && segment.controlBits.URG &&
            segment.sequenceNumber === this.rcvNxt)
            this.recordUrgent(segment);
        if (sequenceCheckedState && !segmentAcceptable(segment, this.rcvNxt, this.rcvWnd))
            return segment.controlBits.RST ? undefined : this.acknowledgment();
        if (sequenceCheckedState && segment.controlBits.SYN) return this.acknowledgment();
        if (sequenceCheckedState && this.timestampsEnabled && segment.options.Timestamp_Value !== undefined &&
            sequenceLess(segment.options.Timestamp_Value, this.timestampRecent)) return this.acknowledgment();
        if (sequenceCheckedState && segment.options.Timestamp_Value !== undefined) {
            this.timestampRecent = segment.options.Timestamp_Value; this.timestampsEnabled = true;
        }
        if (sequenceCheckedState && segment.controlBits.ACK &&
            (!this.sndWndAckValid || sequenceLess(this.sndWndSeq, segment.sequenceNumber) ||
                (this.sndWndSeq === segment.sequenceNumber && sequenceLessOrEqual(this.sndWndAck, segment.acknowledgmentNumber))) &&
            sequenceLessOrEqual(this.sndUna, segment.acknowledgmentNumber) &&
            !sequenceLess(this.sndNxt, segment.acknowledgmentNumber)) {
            this.applySendWindow(segment.window * 2 ** this.peerWindowScale);
            this.sndWndSeq = segment.sequenceNumber; this.sndWndAck = segment.acknowledgmentNumber; this.sndWndAckValid = true;
        }
        if (this.state === TCPState.TIME_WAIT) {
            this.enterTimeWait(); return this.acknowledgment();
        }
        if (this.ecnNegotiated && segment.controlBits.ECE && this.state === TCPState.ESTABLISHED) {
            this.slowStartThreshold = Math.max(2 * this.sendMss, Math.floor(this.congestionWindow / 2));
            this.congestionWindow = this.slowStartThreshold; this.cwrPending = true;
        }
        const canProcessAcknowledgedData = segment.controlBits.ACK &&
            this.state !== TCPState.CLOSED && this.state !== TCPState.LISTEN &&
            this.state !== TCPState.SYN_SENT && this.state !== TCPState.SYN_RECEIVED;
        if (canProcessAcknowledgedData) {
            if (segment.options.SACK.length) this.applySelectiveAcknowledgments(segment.options.SACK);
            const previousAcknowledgment = this.sndUna;
            if (sequenceLess(this.sndNxt, segment.acknowledgmentNumber)) return this.acknowledgment();
            if (sequenceLessOrEqual(this.sndUna, segment.acknowledgmentNumber) &&
                sequenceLessOrEqual(segment.acknowledgmentNumber, this.sndNxt)) {
                this.sndUna = segment.acknowledgmentNumber;
                const acknowledgedBytes = (this.sndUna - previousAcknowledgment) >>> 0;
                if (acknowledgedBytes) {
                    this.duplicateAcknowledgments = 0;
                    if (this.congestionWindow < this.slowStartThreshold)
                        this.congestionWindow += acknowledgedBytes;
                    else
                        this.congestionWindow += Math.max(1, Math.floor(this.sendMss * this.sendMss / this.congestionWindow));
                } else if (segment.payload.length === 0 && segment.acknowledgmentNumber === this.sndUna)
                    this.duplicateAcknowledgments++;
            }
            const sampleEntry = this.retireAcknowledged(this.sndUna);
            if (sampleEntry) {
                const sample = performance.now() - sampleEntry.sentAt;
                this.updateRto(sample);
            }
            if (this.duplicateAcknowledgments === 3) {
                const retransmission = this.fastRetransmit();
                if (retransmission) return retransmission;
            }
            if (segment.payload.length && sequenceInWindow(segment.sequenceNumber, segment.payload.length, this.rcvNxt, this.rcvWnd)) {
                let payload = segment.payload;
                let payloadSequence = segment.sequenceNumber;
                if (sequenceLess(payloadSequence, this.rcvNxt)) {
                    const overlap = (this.rcvNxt - payloadSequence) >>> 0;
                    if (overlap >= payload.length) return this.acknowledgment();
                    payload = payload.slice(overlap); payloadSequence = this.rcvNxt;
                }
                const inOrder = payloadSequence === this.rcvNxt;
                if (inOrder) {
                    this.acceptPayload(payload);
                    this.recordUrgent(segment);
                    if (this.consumePendingFinIfReady()) return this.acknowledgment();
                }
                else this.queueOutOfOrder(segment, payloadSequence, payload);
                if (!inOrder && segment.controlBits.FIN) {
                    this.pendingFinSequence = (segment.sequenceNumber + segment.payload.length) >>> 0;
                    this.pendingFinAcknowledgment = segment.controlBits.ACK && segment.acknowledgmentNumber === this.sndNxt;
                }
                if (inOrder && segment.controlBits.FIN) {
                    this.acceptFin(segment);
                    return this.acknowledgment();
                }
                if (!inOrder || !this.delayedAcknowledgments) return this.acknowledgment();
                this.delayedAckSegments++; this.delayedAckBytes += payload.length;
                if (this.delayedAckSegments >= 2 || this.delayedAckBytes >= 2 * this.sendMss)
                    return this.acknowledgment();
                this.ackPending = true; this.ackDeadline = performance.now() + this.delayedAckDelayMs; return;
            }
            if (segment.payload.length && !sequenceInWindow(segment.sequenceNumber, segment.payload.length, this.rcvNxt, this.rcvWnd))
                return this.acknowledgment();
        }
        if (segment.controlBits.FIN &&
            ((segment.sequenceNumber + segment.payload.length) >>> 0) !== this.rcvNxt) {
            this.pendingFinSequence = (segment.sequenceNumber + segment.payload.length) >>> 0;
            this.pendingFinAcknowledgment = segment.controlBits.ACK && segment.acknowledgmentNumber === this.sndNxt;
            return this.acknowledgment();
        }
        if (segment.controlBits.FIN && (this.state === TCPState.ESTABLISHED || this.state === TCPState.FIN_WAIT_1 || this.state === TCPState.FIN_WAIT_2)) {
            this.acceptFin(segment);
            return this.acknowledgment();
        }
        if (segment.controlBits.ACK && sequenceLessOrEqual(this.sndNxt, segment.acknowledgmentNumber)) {
            if (this.state === TCPState.FIN_WAIT_1 && segment.acknowledgmentNumber === this.sndNxt) this.state = TCPState.FIN_WAIT_2;
            else if (this.state === TCPState.CLOSING && segment.acknowledgmentNumber === this.sndNxt) this.enterTimeWait();
            else if (this.state === TCPState.LAST_ACK && segment.acknowledgmentNumber === this.sndNxt) this.state = TCPState.CLOSED;
        }
        return;
    }

    receiveWire(bytes: Uint8Array, sourceAddress: string, destinationAddress: string, verifyChecksum = true): TCPSegment | undefined {
        try { return this.receive(TCPSegment.decode(bytes, sourceAddress, destinationAddress, verifyChecksum)); }
        catch { return; }
    }

    private resetFor(segment: TCPSegment): TCPSegment {
        // RFC 9293 reset generation reverses the incoming port pair. A
        // wildcard LISTEN endpoint has remotePort === 0, so using the
        // connection's stored peer port here would address the RST wrongly.
        const reset = new TCPSegment(this.localPort, segment.sourcePort, 0, 0,
            new TCP_Control_Bits(), this.advertisedWindow());
        reset.controlBits.RST = true;
        if (segment.controlBits.ACK) reset.sequenceNumber = segment.acknowledgmentNumber;
        else {
            reset.controlBits.ACK = true;
            reset.acknowledgmentNumber = (segment.sequenceNumber + segment.payload.length +
                (segment.controlBits.SYN ? 1 : 0) + (segment.controlBits.FIN ? 1 : 0)) >>> 0;
        }
        return reset;
    }

    private retireAcknowledged(acknowledgment: number): Outstanding | undefined {
        let write = 0;
        let sample: Outstanding | undefined;
        for (const entry of this.retransmissionQueue) {
            const end = (entry.sequence + entry.length) >>> 0;
            if (sequenceLessOrEqual(end, acknowledgment)) {
                if (!sample && !entry.retransmitted) sample = entry;
            } else {
                const partiallyAcknowledged = sequenceLess(entry.sequence, acknowledgment);
                this.retransmissionQueue[write++] = partiallyAcknowledged
                    ? this.trimOutstanding(entry, acknowledgment)
                    : entry;
            }
        }
        this.retransmissionQueue.length = write;
        return sample;
    }

    private trimOutstanding(entry: Outstanding, acknowledgment: number): Outstanding {
        let consumed = (acknowledgment - entry.sequence) >>> 0;
        const flags = TCP_Control_Bits.fromByte(entry.segment.controlBits.toByte());
        if (flags.SYN) { flags.SYN = false; consumed--; }
        const payloadOffset = Math.min(consumed, entry.segment.payload.length);
        consumed -= payloadOffset;
        if (consumed > 0) flags.FIN = false;
        const options = new TCP_Options();
        options.NOP = entry.segment.options.NOP; options.EOL = entry.segment.options.EOL;
        options.MSS = flags.SYN ? entry.segment.options.MSS : undefined;
        options.Window_Scale = flags.SYN ? entry.segment.options.Window_Scale : undefined;
        options.SACK_Permitted = flags.SYN && entry.segment.options.SACK_Permitted;
        options.SACK = entry.segment.options.SACK.slice();
        options.Timestamp_Value = entry.segment.options.Timestamp_Value;
        options.Timestamp_Echo_Reply = entry.segment.options.Timestamp_Echo_Reply;
        const segment = new TCPSegment(entry.segment.sourcePort, entry.segment.destinationPort,
            acknowledgment, entry.segment.acknowledgmentNumber, flags, entry.segment.window,
            Math.max(0, entry.segment.urgentPointer - payloadOffset), options,
            entry.segment.payload.slice(payloadOffset));
        const length = sequenceSpaceLength(segment);
        assert(length > 0);
        return { ...entry, sequence: acknowledgment, length, segment };
    }

    private acknowledgeRetransmissions(acknowledgment: number): void {
        this.retireAcknowledged(acknowledgment);
    }

    private applySelectiveAcknowledgments(blocks: readonly SackBlock[]): void {
        if (!this.sackPermitted) return;
        this.selectiveAcknowledgments = blocks.slice(0, 4).map((block) => ({ left: block.left, right: block.right }));
        const next: Outstanding[] = [];
        for (const entry of this.retransmissionQueue) next.push(...this.sackRemainders(entry));
        this.retransmissionQueue.splice(0, this.retransmissionQueue.length, ...next);
    }

    private sackRemainders(entry: Outstanding): Outstanding[] {
        const payloadLength = entry.segment.payload.length;
        if (payloadLength === 0 || entry.segment.controlBits.SYN || entry.segment.controlBits.FIN)
            return this.selectivelyAcknowledged(entry) ? [] : [entry];
        let gaps: Array<[number, number]> = [[0, payloadLength]];
        for (const block of this.selectiveAcknowledgments) {
            const blockLength = (block.right - block.left) >>> 0;
            if (blockLength === 0 || blockLength >= 0x8000_0000) continue;
            const leftOffset = (block.left - entry.sequence) >>> 0;
            const rightOffset = (block.right - entry.sequence) >>> 0;
            const entryStartInBlock = ((entry.sequence - block.left) >>> 0) < blockLength;
            const overlaps = leftOffset < payloadLength || entryStartInBlock;
            if (!overlaps) continue;
            const start = leftOffset < payloadLength ? leftOffset : 0;
            const end = rightOffset > 0 && rightOffset <= payloadLength ? rightOffset : payloadLength;
            if (end <= start) continue;
            const remaining: Array<[number, number]> = [];
            for (const [gapStart, gapEnd] of gaps) {
                if (end <= gapStart || start >= gapEnd) remaining.push([gapStart, gapEnd]);
                else {
                    if (gapStart < start) remaining.push([gapStart, start]);
                    if (end < gapEnd) remaining.push([end, gapEnd]);
                }
            }
            gaps = remaining;
        }
        return gaps.map(([start, end]) => {
            const flags = TCP_Control_Bits.fromByte(entry.segment.controlBits.toByte());
            flags.SYN = false; flags.FIN = false; flags.PSH = flags.PSH && end === payloadLength;
            const options = new TCP_Options();
            options.Timestamp_Value = entry.segment.options.Timestamp_Value;
            options.Timestamp_Echo_Reply = entry.segment.options.Timestamp_Echo_Reply;
            const segment = new TCPSegment(entry.segment.sourcePort, entry.segment.destinationPort,
                (entry.sequence + start) >>> 0, entry.segment.acknowledgmentNumber, flags,
                entry.segment.window, Math.max(0, entry.segment.urgentPointer - start), options,
                entry.segment.payload.slice(start, end));
            return { ...entry, sequence: segment.sequenceNumber, length: end - start, segment };
        });
    }

    private selectivelyAcknowledged(entry: Outstanding): boolean {
        return this.selectiveAcknowledgments.some((block) => {
            const blockLength = (block.right - block.left) >>> 0;
            const entryStart = (entry.sequence - block.left) >>> 0;
            const entryEnd = (entry.sequence + entry.length - block.left) >>> 0;
            // SACK blocks are serial-number intervals.  Modular distances
            // preserve correctness when a reported range crosses 2^32.
            return blockLength > 0 && blockLength < 0x8000_0000 &&
                entryStart < blockLength && entryEnd <= blockLength;
        });
    }

    private fastRetransmit(now = performance.now()): TCPSegment | undefined {
        const entry = this.retransmissionQueue.find((candidate) => !this.selectivelyAcknowledged(candidate));
        if (!entry) return;
        entry.retransmitted = true; entry.retries++; entry.sentAt = now;
        this.slowStartThreshold = Math.max(2 * this.sendMss, Math.floor(this.congestionWindow / 2));
        this.congestionWindow = this.slowStartThreshold + 3 * this.sendMss;
        return entry.segment;
    }

    private enterTimeWait(now = performance.now(), msl = 120_000): void {
        this.state = TCPState.TIME_WAIT; this.timeWaitUntil = now + 2 * msl;
    }

    private acceptPayload(payload: Uint8Array<ArrayBufferLike>): void {
        this.receiveBuffer.append(payload);
        this.rcvWnd -= payload.length; this.advertisedRcvWnd -= payload.length; this.rcvNxt = (this.rcvNxt + payload.length) >>> 0;
        let advanced = true;
        while (advanced) {
            advanced = false;
            const index = this.outOfOrder.findIndex((item) => {
                // Also select segments fully covered by rcvNxt so their
                // reserved receive-window space can be released.
                return sequenceLessOrEqual(item.sequenceNumber, this.rcvNxt);
            });
            if (index >= 0) {
                const [next] = this.outOfOrder.splice(index, 1);
                if (next) {
                    const overlap = (this.rcvNxt - next.sequenceNumber) >>> 0;
                    const payload = next.payload.slice(Math.min(overlap, next.payload.length));
                    const duplicateBytes = next.payload.length - payload.length;
                    if (duplicateBytes) {
                        // Out-of-order bytes were charged to the receive
                        // window when queued. Restore bytes that overlap
                        // data already delivered by an earlier segment.
                        this.rcvWnd = Math.min(0xffff_ffff, this.rcvWnd + duplicateBytes);
                        this.advertisedRcvWnd = Math.min(0xffff_ffff, this.advertisedRcvWnd + duplicateBytes);
                    }
                    if (payload.length) {
                        this.receiveBuffer.append(payload);
                        this.rcvNxt = (this.rcvNxt + payload.length) >>> 0;
                    }
                    this.recordUrgent(next);
                    advanced = true;
                }
            }
        }
    }

    private queueOutOfOrder(segment: TCPSegment, sequence: number, payload: Uint8Array<ArrayBufferLike>): void {
        // Keep queued ranges disjoint.  Without subtracting existing ranges,
        // overlapping retransmissions would consume the receive window more
        // than once and could make a live connection appear out of space.
        let gaps: Array<[number, number]> = [[0, payload.length]];
        for (const queued of this.outOfOrder) {
            const forward = (queued.sequenceNumber - sequence) >>> 0;
            const queuedStart = forward < 0x8000_0000 ? forward : 0;
            const queuedEnd = forward < 0x8000_0000
                ? Math.min(payload.length, forward + queued.payload.length)
                : Math.min(payload.length, queued.payload.length - ((sequence - queued.sequenceNumber) >>> 0));
            if (queuedEnd <= queuedStart) continue;
            const remaining: Array<[number, number]> = [];
            for (const [start, end] of gaps) {
                if (queuedEnd <= start || queuedStart >= end) remaining.push([start, end]);
                else {
                    if (start < queuedStart) remaining.push([start, queuedStart]);
                    if (queuedEnd < end) remaining.push([queuedEnd, end]);
                }
            }
            gaps = remaining;
            if (gaps.length === 0) return;
        }
        for (const [start, end] of gaps) {
            const queued = new TCPSegment(segment.sourcePort, segment.destinationPort, (sequence + start) >>> 0,
                segment.acknowledgmentNumber, TCP_Control_Bits.fromByte(segment.controlBits.toByte()), segment.window,
                Math.max(0, segment.urgentPointer - start), segment.options, payload.slice(start, end));
            this.outOfOrder.push(queued);
            const added = end - start;
            this.rcvWnd = Math.max(0, this.rcvWnd - added);
            this.advertisedRcvWnd = Math.max(0, this.advertisedRcvWnd - added);
        }
    }

    private consumePendingFinIfReady(): boolean {
        if (this.pendingFinSequence === undefined || this.pendingFinSequence !== this.rcvNxt) return false;
        const acknowledged = this.pendingFinAcknowledgment;
        this.pendingFinSequence = undefined; this.pendingFinAcknowledgment = false;
        const fin = new TCPSegment(this.remotePort, this.localPort, this.rcvNxt, this.sndNxt,
            new TCP_Control_Bits(), this.advertisedWindow());
        fin.controlBits.FIN = true; fin.controlBits.ACK = acknowledged;
        if (acknowledged) fin.acknowledgmentNumber = this.sndNxt;
        this.acceptFin(fin);
        return true;
    }

    private acceptFin(segment: TCPSegment): void {
        assert(segment.controlBits.FIN);
        assert.equal((segment.sequenceNumber + segment.payload.length) >>> 0, this.rcvNxt);
        this.rcvNxt = (this.rcvNxt + 1) >>> 0;
        if (this.state === TCPState.FIN_WAIT_1 && segment.controlBits.ACK && segment.acknowledgmentNumber === this.sndNxt)
            this.enterTimeWait();
        else if (this.state === TCPState.FIN_WAIT_1) this.state = TCPState.CLOSING;
        else if (this.state === TCPState.ESTABLISHED) {
            this.state = TCPState.CLOSE_WAIT; this.events.push({ type: "remote-close" });
        } else if (this.state === TCPState.FIN_WAIT_2) this.enterTimeWait();
    }

    private recordUrgent(segment: TCPSegment): void {
        if (!segment.controlBits.URG || segment.payload.length === 0) return;
        const end = (segment.sequenceNumber + segment.urgentPointer) >>> 0;
        if (this.urgentSequenceEnd !== undefined && !sequenceLess(this.urgentSequenceEnd, end)) return;
        const start = this.urgentSequenceEnd === undefined ? segment.sequenceNumber : this.urgentSequenceEnd;
        const startOffset = Math.max(0, (start - segment.sequenceNumber) >>> 0);
        const endOffset = Math.min(segment.urgentPointer, segment.payload.length);
        if (startOffset < endOffset)
            this.urgentBuffer.append(segment.payload.slice(startOffset, endOffset));
        this.urgentSequenceEnd = end;
        this.events.push({ type: "urgent", urgentBytes: this.urgentBuffer.length });
    }

    expireTimeWait(now = performance.now()): void {
        if (this.state === TCPState.TIME_WAIT && now >= this.timeWaitUntil) this.state = TCPState.CLOSED;
    }

    enableDelayedAcks(delayMs = 40): void {
        assert(delayMs >= 0 && delayMs < 500); this.delayedAcknowledgments = true; this.delayedAckDelayMs = delayMs;
    }

    poll(now = performance.now()): TCPSegment | undefined {
        if (this.ackPending && now >= this.ackDeadline) { this.ackPending = false; return this.acknowledgment(); }
        if (this.windowUpdatePending) return this.acknowledgment();
    }

    enableKeepAlive(intervalMs = 7_200_000): void {
        assert(intervalMs >= 7_200_000); this.keepAliveEnabled = true; this.keepAliveIntervalMs = intervalMs;
    }

    setUserTimeout(timeoutMs: number): void {
        assert(timeoutMs > 0); this.userTimeoutMs = timeoutMs;
    }

    setMaxRetransmissions(retries: number): void {
        assert(Number.isInteger(retries) && retries >= 1); this.maxRetransmissions = retries;
    }

    setNagle(enabled: boolean): void {
        this.nagleEnabled = enabled;
    }

    setPathMtu(mtu: number): void {
        assert(Number.isInteger(mtu) && mtu >= (this.addressFamily === 4 ? 576 : 1280));
        this.pathMtu = mtu;
    }

    tick(now = performance.now()): TCPSegment[] {
        const output: TCPSegment[] = [];
        const delayedAck = this.poll(now); if (delayedAck) output.push(delayedAck);
        this.expireTimeWait(now);
        if ((this.state as TCPState) === TCPState.CLOSED) return output;
        const oldest = this.retransmissionQueue[0];
        if (oldest && now - oldest.sentAt >= this.userTimeoutMs) {
            this.state = TCPState.CLOSED; this.retransmissionQueue.length = 0;
            this.events.push({ type: "timeout", retries: oldest.retries }, { type: "closed" }); return output;
        }
        output.push(...this.onRetransmissionTimeout(now));
        if ((this.state as TCPState) === TCPState.CLOSED) return output;
        if (this.sndWnd === 0) {
            if (this.zeroWindowProbeAt === 0) this.zeroWindowProbeAt = now + this.retransmissionTimeout;
            if (now >= this.zeroWindowProbeAt) {
                const probe = this.sendQueue.length
                    ? this.zeroWindowProbe(this.sendQueue.toUint8Array())
                    : this.outstandingWindowProbe();
                if (probe) {
                    if (this.sendQueue.length) this.sendQueue.take(1);
                    output.push(probe);
                    this.zeroWindowProbeAt = now + this.retransmissionTimeout * this.retransmissionBackoff;
                }
            }
        } else output.push(...this.flushSendQueue(false, now));
        if (this.keepAliveEnabled && this.state === TCPState.ESTABLISHED &&
            this.retransmissionQueue.length === 0 && now - this.lastActivity >= this.keepAliveIntervalMs) {
            const keepAlive = new TCPSegment(this.localPort, this.remotePort, (this.sndNxt - 1) >>> 0,
                this.rcvNxt, new TCP_Control_Bits(), this.advertisedWindow(), 0, this.dataOptions());
            keepAlive.controlBits.ACK = true; output.push(keepAlive); this.lastActivity = now;
        }
        return output;
    }

    send(payload: Uint8Array<ArrayBufferLike>, push = false): TCPSegment {
        assert(this.state === TCPState.ESTABLISHED || this.state === TCPState.CLOSE_WAIT);
        const inFlight = (this.sndNxt - this.sndUna) >>> 0;
        const sendWindow = Math.min(this.congestionWindow, this.sndWnd);
        const options = this.dataOptions();
        const ipHeader = this.addressFamily === 4 ? 20 : 40;
        const optionLength = options.encode().length;
        const effectiveMss = Math.max(1, Math.min(this.sendMss - optionLength,
            this.pathMtu - ipHeader - 20 - optionLength));
        assert(payload.length <= effectiveMss && payload.length <= sendWindow - inFlight);
        const segment = new TCPSegment(this.localPort, this.remotePort, this.sndNxt, this.rcvNxt,
            new TCP_Control_Bits(), this.advertisedWindow(), 0, options, payload);
        segment.controlBits.ACK = true; segment.controlBits.PSH = push; this.sndNxt = (this.sndNxt + payload.length) >>> 0;
        if (this.cwrPending) { segment.controlBits.CWR = true; this.cwrPending = false; }
        this.lastActivity = performance.now();
        if (payload.length) this.track(segment, segment.sequenceNumber, payload.length);
        this.emitGenerated(segment); return segment;
    }

    sendAll(payload: Uint8Array<ArrayBufferLike>, push = false): TCPSegment[] {
        const segments: TCPSegment[] = [];
        const effectiveMss = this.effectiveSendMss();
        assert(this.state === TCPState.ESTABLISHED || this.state === TCPState.CLOSE_WAIT);
        let offset = 0;
        while (offset < payload.length) {
            const inFlight = (this.sndNxt - this.sndUna) >>> 0;
            const usable = Math.max(0, Math.min(this.congestionWindow, this.sndWnd) - inFlight);
            if (usable === 0) {
                this.queueSend(payload.slice(offset), push, performance.now());
                break;
            }
            const length = Math.min(effectiveMss, usable, payload.length - offset);
            offset += length;
            segments.push(this.send(payload.slice(offset - length, offset), push && offset === payload.length));
        }
        return segments;
    }

    queueSend(payload: Uint8Array<ArrayBufferLike>, push = false, now = performance.now()): TCPSegment[] {
        assert(this.state === TCPState.ESTABLISHED || this.state === TCPState.CLOSE_WAIT);
        assert(!this.closePending);
        this.sendQueue.append(payload);
        this.pendingPush = this.pendingPush || push;
        if (this.swsOverrideAt === 0) this.swsOverrideAt = now + this.swsOverrideDelayMs;
        return this.flushSendQueue(this.pendingPush, now);
    }

    flushSendQueue(push = false, now = performance.now()): TCPSegment[] {
        const output: TCPSegment[] = [];
        const requestedPush = push || this.pendingPush;
        while (this.sendQueue.length) {
            const inFlight = (this.sndNxt - this.sndUna) >>> 0;
            const usable = Math.max(0, Math.min(this.congestionWindow, this.sndWnd) - inFlight);
            if (usable === 0) break;
            const effectiveMss = this.effectiveSendMss();
            const fullSegment = this.sendQueue.length >= effectiveMss && usable >= effectiveMss;
            const allPushed = requestedPush && inFlight === 0 && this.sendQueue.length <= usable;
            const halfWindow = inFlight === 0 &&
                Math.min(this.sendQueue.length, usable) >= Math.floor(this.maximumSendWindow / 2);
            const override = now >= this.swsOverrideAt;
            const noNagle = !this.nagleEnabled && inFlight === 0;
            if (!(fullSegment || allPushed || halfWindow || override || noNagle)) break;
            const length = Math.min(effectiveMss, usable, this.sendQueue.length);
            const payload = this.sendQueue.take(length);
            output.push(this.send(payload, requestedPush && this.sendQueue.length === 0));
            if (this.sendQueue.length === 0) {
                this.pendingPush = false;
                if (this.closePending) {
                    this.closePending = false;
                    const fin = this.close();
                    if (fin) output.push(fin);
                }
            }
            this.swsOverrideAt = this.sendQueue.length ? now + this.swsOverrideDelayMs : 0;
            if (this.nagleEnabled && inFlight === 0) break;
        }
        return output;
    }

    read(maxLength = this.receiveBuffer.length): Uint8Array {
        const length = Math.max(0, Math.min(maxLength, this.receiveBuffer.length));
        const data = this.receiveBuffer.take(length);
        this.rcvWnd = Math.min(0xffff_ffff, this.rcvWnd + length);
        const queuedBytes = this.outOfOrder.reduce((total, segment) => total + segment.payload.length, 0);
        const available = Math.max(0, this.receiveBufferCapacity - this.receiveBuffer.length - queuedBytes);
        const unrevealed = available - this.advertisedRcvWnd;
        if (unrevealed >= Math.min(this.localMss, Math.floor(this.receiveBufferCapacity / 2))) {
            this.advertisedRcvWnd = Math.min(0xffff_ffff, available);
            this.windowUpdatePending = true;
        }
        return data;
    }

    readUrgent(maxLength = this.urgentBuffer.length): Uint8Array {
        return this.urgentBuffer.take(Math.max(0, Math.min(maxLength, this.urgentBuffer.length)));
    }

    pollEvents(): TCPEvent[] {
        return this.events.splice(0, this.events.length);
    }

    urgentBytesPending(): number {
        return this.urgentBuffer.length;
    }

    status(): TCPConnectionStatus {
        return {
            state: this.state, addressFamily: this.addressFamily,
            localPort: this.localPort, remotePort: this.remotePort,
            sndUna: this.sndUna, sndNxt: this.sndNxt, sndWnd: this.sndWnd,
            rcvNxt: this.rcvNxt, rcvWnd: this.rcvWnd,
            queuedSendBytes: this.sendQueue.length,
            bufferedReceiveBytes: this.receiveBuffer.length,
            outstandingSegments: this.retransmissionQueue.length,
            retransmissionTimeout: this.retransmissionTimeout,
            smoothedRtt: this.smoothedRtt,
        };
    }

    sendUrgent(payload: Uint8Array<ArrayBufferLike>): TCPSegment {
        assert(payload.length > 0);
        const segment = this.send(payload, true);
        segment.controlBits.URG = true; segment.urgentPointer = payload.length;
        return segment;
    }

    zeroWindowProbe(payload: Uint8Array<ArrayBufferLike>): TCPSegment | undefined {
        if (this.sndWnd !== 0 || payload.length === 0) return;
        const segment = new TCPSegment(this.localPort, this.remotePort, this.sndNxt, this.rcvNxt,
            new TCP_Control_Bits(), this.advertisedWindow(), 0, new TCP_Options(), payload.slice(0, 1));
        segment.controlBits.ACK = true; this.sndNxt = (this.sndNxt + 1) >>> 0;
        this.track(segment, segment.sequenceNumber, 1); this.emitGenerated(segment); return segment;
    }

    private outstandingWindowProbe(): TCPSegment | undefined {
        const entry = this.retransmissionQueue[0];
        if (!entry || !entry.segment.payload.length || entry.segment.controlBits.SYN) return;
        const bits = TCP_Control_Bits.fromByte(entry.segment.controlBits.toByte());
        bits.SYN = false; bits.FIN = false; bits.PSH = false; bits.ACK = true;
        const probe = new TCPSegment(this.localPort, this.remotePort, entry.sequence, this.rcvNxt,
            bits, this.advertisedWindow(), 0, entry.segment.options, entry.segment.payload.slice(0, 1));
        this.emitGenerated(probe); return probe;
    }

    close(): TCPSegment | undefined {
        // FIN consumes sequence space after all queued application data.
        // Defer FIN until those bytes have left the send queue.
        if (this.sendQueue.length) { this.closePending = true; return; }
        if (this.state === TCPState.ESTABLISHED) this.state = TCPState.FIN_WAIT_1;
        else if (this.state === TCPState.CLOSE_WAIT) this.state = TCPState.LAST_ACK;
        else return;
        const segment = new TCPSegment(this.localPort, this.remotePort, this.sndNxt, this.rcvNxt,
            new TCP_Control_Bits(), this.advertisedWindow(), 0, this.dataOptions());
        segment.controlBits.FIN = true; segment.controlBits.ACK = true;
        this.sndNxt = (this.sndNxt + 1) >>> 0; this.track(segment, segment.sequenceNumber, 1); this.emitGenerated(segment); return segment;
    }

    abort(): TCPSegment {
        const segment = new TCPSegment(this.localPort, this.remotePort, this.sndNxt, this.rcvNxt,
            new TCP_Control_Bits(), this.advertisedWindow(), 0, this.dataOptions());
        segment.controlBits.RST = true; this.state = TCPState.CLOSED; this.retransmissionQueue.length = 0;
        this.events.push({ type: "reset" }, { type: "closed" }); this.emitGenerated(segment); return segment;
    }

    /**
     * Deliver an ICMP/ICMPv6 error from the IP layer.  TCP does not encode
     * ICMP in a segment, but it must expose the lower-layer result to the
     * connection state machine.
     */
    handleIcmpError(addressFamily: 4 | 6, type: number, code: number,
        quotedSegment?: TCPSegment, reportedMtu?: number): boolean {
        assert(Number.isInteger(type) && type >= 0 && type <= 255);
        assert(Number.isInteger(code) && code >= 0 && code <= 255);
        if (quotedSegment && (quotedSegment.sourcePort !== this.localPort ||
            quotedSegment.destinationPort !== this.remotePort)) return false;
        let error: string | undefined;
        let fatal = false;
        if (addressFamily === 4 && type === 3) {
            error = `ipv4-destination-unreachable:${code}`;
            // Port/protocol/address failures are fatal; fragmentation-needed
            // is handled as a path-MTU signal when an MTU is supplied.
            fatal = code === 2 || code === 3;
            if (code === 4 && reportedMtu !== undefined && reportedMtu !== 0) {
                assert(Number.isInteger(reportedMtu) && reportedMtu >= 576);
                this.setPathMtu(reportedMtu);
            }
        } else if (addressFamily === 4 && type === 4) {
            // Source Quench is obsolete and must be silently discarded.
            return false;
        } else if (addressFamily === 4 && (type === 11 || type === 12)) {
            error = type === 11 ? `ipv4-time-exceeded:${code}` : `ipv4-parameter-problem:${code}`;
        } else if (addressFamily === 6 && type === 1) {
            error = `ipv6-destination-unreachable:${code}`; fatal = true;
        } else if (addressFamily === 6 && type === 2) {
            error = `ipv6-packet-too-big:${code}`;
            if (reportedMtu !== undefined) {
                assert(Number.isInteger(reportedMtu) && reportedMtu >= 1_280);
                this.setPathMtu(reportedMtu);
            }
        } else if (addressFamily === 6 && (type === 3 || type === 4)) {
            error = type === 3 ? `ipv6-time-exceeded:${code}` : `ipv6-parameter-problem:${code}`;
        } else return false;
        this.events.push({ type: "network-error", error });
        if (!fatal || this.state === TCPState.CLOSED) return false;
        this.state = TCPState.CLOSED; this.retransmissionQueue.length = 0;
        this.events.push({ type: "closed" });
        return true;
    }

    acknowledgment(): TCPSegment {
        this.windowUpdatePending = false;
        this.ackPending = false; this.delayedAckSegments = 0; this.delayedAckBytes = 0;
        const options = this.dataOptions();
        const segment = new TCPSegment(this.localPort, this.remotePort, this.sndNxt, this.rcvNxt,
            new TCP_Control_Bits(), this.advertisedWindow(), 0, options);
        segment.controlBits.ACK = true;
        if (this.sackPermitted && this.outOfOrder.length)
            options.SACK = this.outOfOrder.slice(0, this.timestampsEnabled ? 3 : 4).map((item) => ({
                left: item.sequenceNumber,
                right: (item.sequenceNumber + item.payload.length) >>> 0,
            }));
        if (this.cwrPending) { segment.controlBits.CWR = true; this.cwrPending = false; }
        this.emitGenerated(segment); return segment;
    }

    track(segment: TCPSegment, sequence: number, length: number, now = performance.now()): void {
        this.retransmissionQueue.push({ sequence, length, sentAt: now, segment, retransmitted: false, retries: 0 });
    }

    updateRto(sample: number): void {
        this.retransmissionBackoff = 1;
        if (this.smoothedRtt === undefined) {
            this.smoothedRtt = sample; this.rttVariance = sample / 2;
        } else {
            this.rttVariance = 0.75 * this.rttVariance + 0.25 * Math.abs(this.smoothedRtt - sample);
            this.smoothedRtt = 0.875 * this.smoothedRtt + 0.125 * sample;
        }
        this.retransmissionTimeout = Math.max(1_000, Math.min(60_000, this.smoothedRtt + 4 * this.rttVariance));
    }

    due(now: number, retransmissionTimeout = this.retransmissionTimeout): TCPSegment[] {
        return this.retransmissionQueue.filter((entry) => !this.selectivelyAcknowledged(entry) &&
            now - entry.sentAt >= retransmissionTimeout * this.retransmissionBackoff).map((entry) => entry.segment);
    }

    onRetransmissionTimeout(now: number): TCPSegment[] {
        const segments = this.due(now);
        if (segments.length) {
            for (const entry of this.retransmissionQueue) {
                if (now - entry.sentAt >= this.retransmissionTimeout * this.retransmissionBackoff) {
                    entry.retransmitted = true; entry.retries++; entry.sentAt = now;
                }
            }
            if (this.retransmissionQueue.some((entry) => entry.retries > this.maxRetransmissions)) {
                this.state = TCPState.CLOSED; this.retransmissionQueue.length = 0;
                this.events.push({ type: "timeout" }, { type: "closed" }); return [];
            }
            for (const entry of this.retransmissionQueue)
                if (entry.retries === 3) this.events.push({ type: "retransmission-warning", retries: entry.retries });
            this.slowStartThreshold = Math.max(2 * this.sendMss, Math.floor(this.congestionWindow / 2));
            this.congestionWindow = this.sendMss;
            this.retransmissionBackoff = Math.min(64, this.retransmissionBackoff * 2);
        }
        if (this.receiveDepth === 0)
            for (const segment of segments) this.emitPacket("outbound", segment);
        return segments;
    }
}

export class TCPHost {
    // A listening port is scoped by address family.  Keeping the family in
    // the key allows IPv4 and IPv6 listeners to coexist on the same port.
    readonly listeners = new Map<string, TCPConnection>();
    readonly connections = new Map<string, TCPConnection>();
    private readonly packetObservers = new Set<TCPPacketObserver>();

    private key(localPort: number, remotePort: number, addressFamily: 4 | 6 = 4,
        localAddress?: string, remoteAddress?: string): string {
        const portKey = addressFamily === 4 ? `${localPort}:${remotePort}` : `6:${localPort}:${remotePort}`;
        if (localAddress === undefined || remoteAddress === undefined) return portKey;
        return `${addressFamily}:${addressKey(localAddress)}->${addressKey(remoteAddress)}:${portKey}`;
    }

    private listenerKey(localPort: number, addressFamily: 4 | 6): string {
        return `${addressFamily}:${localPort}`;
    }

    observePackets(observer: TCPPacketObserver): () => void {
        this.packetObservers.add(observer);
        return () => this.packetObservers.delete(observer);
    }

    status(): TCPConnectionStatus[] {
        return Array.from(this.connections.values(), (connection) => connection.status());
    }

    private emitPacket(direction: TCPPacketTrace["direction"], segment: TCPSegment): void {
        if (!this.packetObservers.size) return;
        const trace = { direction, segment, at: performance.now() };
        for (const observer of this.packetObservers) observer(trace);
    }

    listen(localPort: number, addressFamily: 4 | 6 = 4): TCPConnection {
        const listenerKey = this.listenerKey(localPort, addressFamily);
        assert(!this.listeners.has(listenerKey));
        const connection = new TCPConnection(localPort, 0, addressFamily); connection.listen();
        this.listeners.set(listenerKey, connection); return connection;
    }

    connect(localPort: number, remotePort: number, initialSequence?: number, addressFamily: 4 | 6 = 4,
        localAddress?: string, remoteAddress?: string): { connection: TCPConnection; syn: TCPSegment } {
        const connection = new TCPConnection(localPort, remotePort, addressFamily);
        this.connections.set(this.key(localPort, remotePort, addressFamily, localAddress, remoteAddress), connection);
        const syn = connection.activeOpen(initialSequence);
        this.emitPacket("outbound", syn);
        return { connection, syn };
    }

    receive(segment: TCPSegment, addressFamily: 4 | 6 = 4,
        localAddress?: string, remoteAddress?: string): TCPSegment | undefined {
        this.emitPacket("inbound", segment);
        const exactKey = this.key(segment.destinationPort, segment.sourcePort, addressFamily, localAddress, remoteAddress);
        const portKey = this.key(segment.destinationPort, segment.sourcePort, addressFamily);
        let connectionKey = exactKey;
        let connection = this.connections.get(exactKey);
        // Preserve the low-level port-only API for callers that created a
        // connection without address metadata, while preferring the RFC
        // four-tuple whenever wire addresses are available.
        if (!connection && exactKey !== portKey) {
            connectionKey = portKey;
            connection = this.connections.get(portKey);
        }
        if (!connection && segment.controlBits.SYN) {
            const listener = this.listeners.get(this.listenerKey(segment.destinationPort, addressFamily));
            if (listener) {
                connection = new TCPConnection(segment.destinationPort, segment.sourcePort, listener.addressFamily);
                connection.listen(); connectionKey = exactKey; this.connections.set(connectionKey, connection);
            }
        }
        if (!connection) {
            const resetConnection = new TCPConnection(segment.destinationPort, segment.sourcePort, addressFamily);
            const response = resetConnection.receive(segment);
            if (response) this.emitPacket("outbound", response);
            return response;
        }
        const response = connection.receive(segment);
        if (response) this.emitPacket("outbound", response);
        if (connection.state === TCPState.CLOSED) this.connections.delete(connectionKey);
        return response;
    }

    receiveWire(bytes: Uint8Array, sourceAddress: string, destinationAddress: string, verifyChecksum = true): TCPSegment | undefined {
        const addressFamily = sourceAddress.includes(":") ? 6 : 4;
        let segment: TCPSegment;
        try {
            segment = TCPSegment.decode(bytes, sourceAddress, destinationAddress, verifyChecksum);
        } catch {
            // A bad checksum or malformed option is a bad network input, not
            // a reason to tear down the host's packet-processing loop.
            return;
        }
        // RFC 9293 requires SYNs addressed to broadcast/multicast destinations
        // to be silently discarded rather than creating a connection.
        if (segment.controlBits.SYN && (isBroadcastOrMulticast(destinationAddress) ||
            isUnspecifiedAddress(destinationAddress) || isBroadcastOrMulticast(sourceAddress) ||
            isUnspecifiedAddress(sourceAddress))) return;
        return this.receive(segment, addressFamily, destinationAddress, sourceAddress);
    }

    handleIcmpError(localPort: number, remotePort: number, addressFamily: 4 | 6,
        type: number, code: number, quotedSegment?: TCPSegment, reportedMtu?: number,
        localAddress?: string, remoteAddress?: string): boolean {
        const exactKey = this.key(localPort, remotePort, addressFamily, localAddress, remoteAddress);
        const portKey = this.key(localPort, remotePort, addressFamily);
        const connectionKey = this.connections.has(exactKey) ? exactKey : portKey;
        const connection = this.connections.get(connectionKey);
        if (!connection) return false;
        const closed = connection.handleIcmpError(addressFamily, type, code, quotedSegment, reportedMtu);
        if (connection.state === TCPState.CLOSED) this.connections.delete(connectionKey);
        return closed;
    }

    tick(now = performance.now()): Array<{ key: string; segment: TCPSegment }> {
        const output: Array<{ key: string; segment: TCPSegment }> = [];
        for (const [key, connection] of this.connections) {
            for (const segment of connection.tick(now)) {
                output.push({ key, segment }); this.emitPacket("outbound", segment);
            }
            if (connection.state === TCPState.CLOSED) this.connections.delete(key);
        }
        return output;
    }
}

const TRIALS = 2_000;
let traceDepth = 0;
let traceRunStartedAt = 0;
let traceRunStartHeap = 0;

function nowNanoseconds(): number {
    return typeof Bun !== "undefined" ? Number(Bun.nanoseconds()) : performance.now() * 1e6;
}

function durationText(nanoseconds: number): string {
    const microseconds = nanoseconds / 1e3;
    const milliseconds = nanoseconds / 1e6;
    return `${microseconds.toFixed(2)} µs / ${milliseconds.toFixed(3)} ms`;
}

function memoryText(bytes: number): string {
    return `${(bytes / (1024 * 1024)).toFixed(3)} MB`;
}

function traceResult(label: string, startedAt: number, startHeap: number): void {
    const elapsed = nowNanoseconds() - startedAt;
    const heap = process.memoryUsage().heapUsed;
    const indent = "  ".repeat(traceDepth);
    console.log(`${indent}├─ ${label}: ${durationText(elapsed)} | heap=${memoryText(heap)} | Δheap=${memoryText(heap - startHeap)}`);
}

function traceMark(label: string): void {
    const heap = process.memoryUsage().heapUsed;
    console.log(`${"  ".repeat(traceDepth)}│  ↳ ${label} @ ${durationText(nowNanoseconds() - traceRunStartedAt)} | heap=${memoryText(heap)} | Δheap=${memoryText(heap - traceRunStartHeap)}`);
}

function property(name: string, check: (trial: number) => void, trials = TRIALS): void {
    const startedAt = nowNanoseconds();
    const startHeap = process.memoryUsage().heapUsed;
    for (let trial = 0; trial < trials; trial++) check(trial);
    traceResult(`PASS ${name} (${trials} trials)`, startedAt, startHeap);
}

function runTests(): void {
    const source = "192.0.2.1", destination = "198.51.100.2";
    const started = nowNanoseconds();
    const startMemory = process.memoryUsage().heapUsed;
    traceRunStartedAt = started; traceRunStartHeap = startMemory;
    console.log("TCP test trace");
    console.log("└─ run");
    traceDepth = 1;
    property("segment encoding and decoding preserve fields", (trial) => {
        const segment = new TCPSegment(1234, 443, trial, trial + 1);
        segment.controlBits.SYN = trial % 2 === 0; segment.controlBits.ACK = trial % 3 === 0;
        if (segment.controlBits.SYN) { segment.options.MSS = 1460; segment.options.Window_Scale = 7; }
        segment.payload = Uint8Array.of(trial & 255, 0xaa, 0x55);
        const encoded = segment.encode(source, destination);
        assert.equal(encoded[12]! & 0x0f, 0);
        const decoded = TCPSegment.decode(encoded, source, destination);
        assert.equal(decoded.sourcePort, segment.sourcePort); assert.equal(decoded.sequenceNumber, segment.sequenceNumber);
        assert.deepEqual(decoded.payload, segment.payload); assert.equal(decoded.options.MSS, segment.controlBits.SYN ? 1460 : undefined);
    });
    property("reserved header bits are ignored on receive", (trial) => {
        const bytes = new TCPSegment(1234, 443, trial, trial + 1).encode(source, destination);
        bytes[12] = bytes[12]! | (1 << (trial % 4));
        const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
        view.setUint16(16, 0); view.setUint16(16, tcpChecksum(bytes, source, destination));
        assert.doesNotThrow(() => TCPSegment.decode(bytes, source, destination));
    });
    property("randomized wire segments round-trip with valid checksums", (trial) => {
        const word = (Math.imul(trial + 1, 0x9e37_79b1) >>> 0);
        const segment = new TCPSegment(1024 + (word % 50_000), 1024 + ((word >>> 8) % 50_000),
            word, (word ^ 0xa5a5_a5a5) >>> 0);
        segment.controlBits.SYN = true; segment.controlBits.ACK = (word & 1) !== 0;
        segment.options.MSS = 536 + (word % 1400); segment.options.Window_Scale = word % 15;
        segment.payload = Uint8Array.from({ length: word % 33 }, (_, index) => (word + index) & 255);
        const decoded = TCPSegment.decode(segment.encode(source, destination), source, destination);
        assert.equal(decoded.sequenceNumber, segment.sequenceNumber); assert.deepEqual(decoded.payload, segment.payload);
        assert.equal(decoded.options.Window_Scale, segment.options.Window_Scale);
    });
    property("randomized IPv6 wire segments round-trip with valid checksums", (trial) => {
        const word = Math.imul(trial + 17, 0x27d4_eb2d) >>> 0;
        const segment = new TCPSegment(2048 + (word % 40_000), 4096 + ((word >>> 7) % 40_000),
            word, (word ^ 0x6d2b_79f5) >>> 0);
        segment.controlBits.ACK = true; segment.controlBits.PSH = (word & 2) !== 0;
        segment.options.Timestamp_Value = word;
        segment.options.Timestamp_Echo_Reply = (word ^ 0xffff_ffff) >>> 0;
        segment.payload = Uint8Array.from({ length: word % 61 }, (_, index) => (word + index * 13) & 255);
        const source6 = "2001:db8::" + ((word & 0xffff) || 1).toString(16);
        const destination6 = "2001:db8:1::" + (((word >>> 16) & 0xffff) || 1).toString(16);
        const encoded = segment.encode(source6, destination6);
        const decoded = TCPSegment.decode(encoded, source6, destination6);
        assert.equal(decoded.sequenceNumber, segment.sequenceNumber);
        assert.equal(decoded.checksum, segment.checksum);
        assert.deepEqual(decoded.payload, segment.payload);
    }, 512);
    property("TCP option combinations preserve their wire representation", (trial) => {
        const word = Math.imul(trial + 31, 0x9e37_79b1) >>> 0;
        const options = new TCP_Options();
        options.MSS = 536 + (word % 1400);
        options.Window_Scale = word % 15;
        options.SACK_Permitted = true;
        options.Timestamp_Value = word;
        options.Timestamp_Echo_Reply = (word ^ 0xa5a5_a5a5) >>> 0;
        options.SACK.push({ left: word, right: (word + 64) >>> 0 });
        if (trial % 2 === 0) options.SACK.push({ left: (word + 128) >>> 0, right: (word + 192) >>> 0 });
        options.NOP = trial % 3 === 0; options.EOL = trial % 5 === 0;
        const bytes = options.encode();
        assert(bytes.length <= 40 && bytes.length % 4 === 0);
        const decoded = TCP_Options.decode(bytes);
        assert.equal(decoded.MSS, options.MSS);
        assert.equal(decoded.Window_Scale, options.Window_Scale);
        assert.equal(decoded.SACK_Permitted, options.SACK_Permitted);
        assert.deepEqual(decoded.SACK, options.SACK);
        assert.equal(decoded.Timestamp_Value, options.Timestamp_Value);
        assert.equal(decoded.Timestamp_Echo_Reply, options.Timestamp_Echo_Reply);
    }, 512);
    property("randomized wire corruption is rejected by checksum verification", (trial) => {
        const word = Math.imul(trial + 11, 0x45d9_f3b) >>> 0;
        const segment = new TCPSegment(2000 + (word % 40_000), 3000 + ((word >>> 7) % 30_000), word,
            (word ^ 0x1357_9bdf) >>> 0);
        segment.controlBits.ACK = true;
        segment.options.Timestamp_Value = word; segment.options.Timestamp_Echo_Reply = (word ^ 0xffff_ffff) >>> 0;
        segment.payload = Uint8Array.from({ length: word % 47 }, (_, index) => (word + index * 17) & 255);
        const bytes = segment.encode(source, destination);
        const corrupted = bytes.slice();
        corrupted[20 + (word % (corrupted.length - 20))]! ^= 1;
        assert.throws(() => TCPSegment.decode(corrupted, source, destination));
    }, 512);
    property("sequence windows obey RFC boundaries", (trial) => {
        const next = trial >>> 0; assert(sequenceInWindow(next, 1, next, 1024));
        assert(!sequenceInWindow((next - 1024) >>> 0, 1, next, 1024));
    });
    property("sequence windows wrap at 2^32", (trial) => {
        const next = (0xffff_ff00 + trial) >>> 0;
        assert(sequenceInWindow(next, 16, next, 1024));
        assert(sequenceInWindow((next + 512) >>> 0, 1, next, 1024));
    });
    property("overlapping out-of-order ranges preserve unique receive-window bytes", (trial) => {
        const base = (0xffff_ff00 + trial * 97) >>> 0;
        const receiver = new TCPConnection(7000, 7001); receiver.state = TCPState.ESTABLISHED;
        receiver.rcvNxt = base; receiver.rcvWnd = 128; receiver.advertisedRcvWnd = 128;
        const flags = new TCP_Control_Bits(); flags.ACK = true;
        const first = new TCPSegment(7001, 7000, (base + 32) >>> 0, 0, flags, 65_535, 0, new TCP_Options(),
            Uint8Array.from({ length: 32 }, (_, index) => (trial + index) & 255));
        const overlap = new TCPSegment(7001, 7000, (base + 48) >>> 0, 0, flags, 65_535, 0, new TCP_Options(),
            Uint8Array.from({ length: 32 }, (_, index) => (trial + index + 32) & 255));
        const prefix = new TCPSegment(7001, 7000, base, 0, flags, 65_535, 0, new TCP_Options(),
            Uint8Array.from({ length: 32 }, (_, index) => (trial + index + 64) & 255));
        receiver.receive(first); receiver.receive(overlap); receiver.receive(prefix);
        assert.equal(receiver.rcvNxt, (base + 80) >>> 0);
        assert.equal(receiver.rcvWnd, 48); assert.equal(receiver.receiveBuffer.length, 80);
        assert.equal(receiver.outOfOrder.length, 0);
    }, 512);
    property("segment acceptability covers payload, FIN, and zero windows", (trial) => {
        const next = (0xffff_ff00 + trial) >>> 0;
        const data = new TCPSegment(1, 2, next, 0, new TCP_Control_Bits(), 0, 0,
            new TCP_Options(), Uint8Array.of(1, 2, 3));
        assert.equal(sequenceSpaceLength(data), 3); assert(segmentAcceptable(data, next, 3));
        const spanning = new TCPSegment(1, 2, (next - 2) >>> 0, 0, new TCP_Control_Bits(), 0, 0,
            new TCP_Options(), new Uint8Array(8));
        assert(segmentAcceptable(spanning, next, 4));
        const fin = new TCPSegment(1, 2, (next + 3) >>> 0, 0, new TCP_Control_Bits());
        fin.controlBits.FIN = true; assert.equal(sequenceSpaceLength(fin), 1);
        assert(segmentAcceptable(fin, next, 4)); assert(!segmentAcceptable(fin, next, 3));
        assert(!segmentAcceptable(data, next, 0));
    });
    property("unknown options are ignored when well-formed", (trial) => {
        const unknown = TCP_Options.decode(Uint8Array.of(99, 4, trial & 255, 0));
        assert.equal(unknown.MSS, undefined);
    });
    property("flow-controlled sendAll preserves bytes without exceeding the window", (trial) => {
        const connection = new TCPConnection(1500, 2500); connection.state = TCPState.ESTABLISHED;
        const window = 1 + (trial % 256); const payloadLength = trial % 2_048;
        connection.sndWnd = window; connection.congestionWindow = window;
        const sent = connection.sendAll(new Uint8Array(payloadLength), trial % 2 === 0);
        const sentBytes = sent.reduce((total, segment) => total + segment.payload.length, 0);
        assert(((connection.sndNxt - connection.sndUna) >>> 0) <= window);
        assert.equal(sentBytes + connection.sendQueue.length, payloadLength);
    });
    const eolPadding = TCP_Options.decode(Uint8Array.of(2, 4, 5, 180, 0, 0, 0, 0));
    assert.equal(eolPadding.MSS, 1460); assert.equal(eolPadding.EOL, true);
    assert.throws(() => TCP_Options.decode(Uint8Array.of(2, 4, 5, 180, 0, 0, 1, 0)));
    const integrationStartedAt = nowNanoseconds();
    const integrationStartHeap = process.memoryUsage().heapUsed;
    assert.throws(() => new TCPConnection(-1, 1));
    assert.throws(() => new TCPConnection(1, 65_536));
    assert.throws(() => TCP_Options.decode(Uint8Array.of(99, 0)));
    assert.throws(() => TCP_Options.decode(Uint8Array.of(3, 3, 15)));
    const invalidOptionNumbers = new TCP_Options(); invalidOptionNumbers.Timestamp_Value = -1;
    assert.throws(() => invalidOptionNumbers.encode());
    const malformedDirect = new TCPConnection(6100, 6101);
    const invalidControl = new TCP_Control_Bits(); invalidControl.SYN = true; invalidControl.FIN = true;
    assert.doesNotThrow(() => malformedDirect.receive(new TCPSegment(6101, 6100, 1, 0, invalidControl)));
    assert.equal(malformedDirect.state, TCPState.CLOSED);
    const partialTimestamp = new TCP_Options(); partialTimestamp.Timestamp_Value = 1;
    assert.throws(() => partialTimestamp.encode());
    const invalidSack = new TCP_Options(); invalidSack.SACK.push({ left: 0, right: 0x1_0000_0000 });
    assert.throws(() => invalidSack.encode());
    const synSack = new TCPSegment(1, 2, 3, 0); synSack.controlBits.SYN = true;
    synSack.options.SACK.push({ left: 4, right: 5 }); assert.throws(() => synSack.encode(source, destination));
    const ipv6Segment = new TCPSegment(1234, 443, 0xfeed_beef, 7);
    ipv6Segment.options.Timestamp_Value = 1; ipv6Segment.options.Timestamp_Echo_Reply = 2;
    const ipv6Bytes = ipv6Segment.encode("2001:db8::1", "2001:db8::2");
    assert.equal(tcpChecksum(ipv6Bytes, "2001:db8::1", "2001:db8::2"),
        referenceTcpChecksum(ipv6Bytes, "2001:db8::1", "2001:db8::2"));
    assert.equal(TCPSegment.decode(ipv6Bytes, "2001:db8::1", "2001:db8::2").options.Timestamp_Value, 1);
    const mappedIpv6 = new TCPSegment(1234, 443, 2, 3).encode("::ffff:192.0.2.1", "::ffff:198.51.100.2");
    assert.equal(TCPSegment.decode(mappedIpv6, "::ffff:192.0.2.1", "::ffff:198.51.100.2").destinationPort, 443);
    const ipv4Bytes = new TCPSegment(1234, 443, 8, 9).encode(source, destination);
    assert.equal(tcpChecksum(ipv4Bytes, source, destination), referenceTcpChecksum(ipv4Bytes, source, destination));
    const illegalNonSynOptions = new TCPSegment(1234, 443, 1, 0, new TCP_Control_Bits(), 65_535,
        0, new TCP_Options());
    illegalNonSynOptions.controlBits.SYN = true; illegalNonSynOptions.options.MSS = 1460;
    const illegalBytes = illegalNonSynOptions.encode(source, destination);
    illegalBytes[13] = illegalBytes[13]! & ~0x02;
    new DataView(illegalBytes.buffer, illegalBytes.byteOffset, illegalBytes.byteLength).setUint16(16, 0);
    const illegalChecksum = tcpChecksum(illegalBytes, source, destination);
    new DataView(illegalBytes.buffer, illegalBytes.byteOffset, illegalBytes.byteLength).setUint16(16, illegalChecksum);
    assert.equal(TCPSegment.decode(illegalBytes, source, destination).options.MSS, undefined);
    const listeningReset = new TCPConnection(6001, 6002); listeningReset.listen();
    const resetToListener = new TCPSegment(6002, 6001, 0, 0); resetToListener.controlBits.RST = true;
    listeningReset.receive(resetToListener); assert.equal(listeningReset.state, TCPState.LISTEN);
    const invalidListenerAck = new TCPSegment(6002, 6001, 0, 9); invalidListenerAck.controlBits.ACK = true;
    const listenerReset = listeningReset.receive(invalidListenerAck);
    assert.equal(listenerReset?.sourcePort, 6001); assert.equal(listenerReset?.destinationPort, 6002);
    assert(listenerReset?.controlBits.RST);
    const resetTarget = new TCPConnection(6003, 6004); resetTarget.state = TCPState.ESTABLISHED;
    resetTarget.rcvNxt = 100; resetTarget.rcvWnd = 1_000;
    const outsideReset = new TCPSegment(6004, 6003, 99, 0); outsideReset.controlBits.RST = true;
    assert.equal(resetTarget.receive(outsideReset), undefined); assert.equal(resetTarget.state, TCPState.ESTABLISHED);
    const challengeReset = new TCPSegment(6004, 6003, 101, 0); challengeReset.controlBits.RST = true;
    assert(resetTarget.receive(challengeReset)?.controlBits.ACK); assert.equal(resetTarget.state, TCPState.ESTABLISHED);
    const validReset = new TCPSegment(6004, 6003, 100, 0); validReset.controlBits.RST = true;
    resetTarget.receive(validReset); assert.equal(resetTarget.state, TCPState.CLOSED);
    const synchronizedSyn = new TCPConnection(6005, 6006); synchronizedSyn.state = TCPState.ESTABLISHED;
    synchronizedSyn.rcvNxt = 200; synchronizedSyn.rcvWnd = 1_000;
    const unexpectedSyn = new TCPSegment(6006, 6005, 200, 0); unexpectedSyn.controlBits.SYN = true;
    assert(synchronizedSyn.receive(unexpectedSyn)?.controlBits.ACK); assert.equal(synchronizedSyn.state, TCPState.ESTABLISHED);
    const outOfOrderFin = new TCPConnection(6009, 6010); outOfOrderFin.state = TCPState.ESTABLISHED;
    outOfOrderFin.rcvNxt = 400; outOfOrderFin.rcvWnd = 1_000;
    const skippedFin = new TCPSegment(6010, 6009, 405, 0); skippedFin.controlBits.FIN = true;
    assert(outOfOrderFin.receive(skippedFin)?.controlBits.ACK);
    assert.equal(outOfOrderFin.state, TCPState.ESTABLISHED); assert.equal(outOfOrderFin.rcvNxt, 400);
    const overlapTarget = new TCPConnection(6011, 6012); overlapTarget.state = TCPState.ESTABLISHED;
    overlapTarget.sackPermitted = true; overlapTarget.rcvNxt = 100; overlapTarget.rcvWnd = 1_000;
    const dataFlags = new TCP_Control_Bits(); dataFlags.ACK = true;
    const later = new TCPSegment(6012, 6011, 103, 0, dataFlags, 0, 0, new TCP_Options(), Uint8Array.of(7, 8, 9));
    const overlap = new TCPSegment(6012, 6011, 101, 0, dataFlags, 0, 0, new TCP_Options(), Uint8Array.of(4, 5, 6, 7));
    overlapTarget.receive(later); overlapTarget.receive(overlap);
    const first = new TCPSegment(6012, 6011, 100, 0, dataFlags, 0, 0, new TCP_Options(), Uint8Array.of(3));
    overlapTarget.receive(first); assert.equal(overlapTarget.receiveBuffer.length, 6); assert.equal(overlapTarget.rcvNxt, 106);
    const noSackTarget = new TCPConnection(6013, 6014); noSackTarget.state = TCPState.ESTABLISHED;
    noSackTarget.rcvNxt = 10; noSackTarget.rcvWnd = 1_000;
    noSackTarget.receive(new TCPSegment(6014, 6013, 12, 0, dataFlags, 0, 0, new TCP_Options(), Uint8Array.of(3, 4)));
    assert.equal(noSackTarget.rcvWnd, 998);
    noSackTarget.receive(new TCPSegment(6014, 6013, 10, 0, dataFlags, 0, 0, new TCP_Options(), Uint8Array.of(1, 2)));
    assert.deepEqual(noSackTarget.read(), Uint8Array.of(1, 2, 3, 4));
    assert.equal(noSackTarget.rcvWnd, 1_000);
    const duplicateQueue = new TCPConnection(6014, 6015); duplicateQueue.state = TCPState.ESTABLISHED;
    duplicateQueue.rcvNxt = 100; duplicateQueue.rcvWnd = 100; duplicateQueue.advertisedRcvWnd = 100;
    duplicateQueue.receive(new TCPSegment(6015, 6014, 105, 0, dataFlags, 0, 0, new TCP_Options(), Uint8Array.of(1, 2, 3, 4, 5)));
    duplicateQueue.receive(new TCPSegment(6015, 6014, 107, 0, dataFlags, 0, 0, new TCP_Options(), Uint8Array.of(6, 7, 8)));
    duplicateQueue.receive(new TCPSegment(6015, 6014, 100, 0, dataFlags, 0, 0, new TCP_Options(), Uint8Array.of(9, 10, 11, 12, 13)));
    assert.equal(duplicateQueue.rcvNxt, 110); assert.equal(duplicateQueue.rcvWnd, 90);
    assert.deepEqual(duplicateQueue.read(), Uint8Array.of(9, 10, 11, 12, 13, 1, 2, 3, 4, 5));
    const orderedUrgent = new TCPConnection(6017, 6018); orderedUrgent.state = TCPState.ESTABLISHED;
    orderedUrgent.rcvNxt = 50; orderedUrgent.rcvWnd = 1_000;
    const urgentFlags = new TCP_Control_Bits(); urgentFlags.ACK = true; urgentFlags.URG = true;
    orderedUrgent.receive(new TCPSegment(6018, 6017, 52, 0, urgentFlags, 0, 2, new TCP_Options(), Uint8Array.of(7, 8)));
    assert.equal(orderedUrgent.urgentBytesPending(), 0);
    const orderedDataFlags = new TCP_Control_Bits(); orderedDataFlags.ACK = true;
    orderedUrgent.receive(new TCPSegment(6018, 6017, 50, 0, orderedDataFlags, 0, 0, new TCP_Options(), Uint8Array.of(1, 2)));
    assert.deepEqual(orderedUrgent.readUrgent(), Uint8Array.of(7, 8));
    const queuedFinTarget = new TCPConnection(6015, 6016); queuedFinTarget.state = TCPState.ESTABLISHED;
    queuedFinTarget.rcvNxt = 20; queuedFinTarget.rcvWnd = 1_000;
    const queuedFin = new TCPSegment(6016, 6015, 22, 0, dataFlags, 0, 0, new TCP_Options(), Uint8Array.of(3, 4));
    queuedFin.controlBits.FIN = true; queuedFinTarget.receive(queuedFin);
    queuedFinTarget.receive(new TCPSegment(6016, 6015, 20, 0, dataFlags, 0, 0, new TCP_Options(), Uint8Array.of(1, 2)));
    assert.equal(queuedFinTarget.state, TCPState.CLOSE_WAIT); assert.equal(queuedFinTarget.rcvNxt, 25);
    assert.deepEqual(queuedFinTarget.read(), Uint8Array.of(1, 2, 3, 4));
    const payloadFinTarget = new TCPConnection(6019, 6020); payloadFinTarget.state = TCPState.ESTABLISHED;
    payloadFinTarget.rcvNxt = 30;
    const payloadFinFlags = new TCP_Control_Bits(); payloadFinFlags.ACK = true;
    const payloadFin = new TCPSegment(6020, 6019, 30, 0, payloadFinFlags, 0, 0, new TCP_Options(), Uint8Array.of(5, 6));
    payloadFin.controlBits.FIN = true;
    assert(payloadFinTarget.receive(payloadFin)?.controlBits.ACK);
    assert.equal(payloadFinTarget.state, TCPState.CLOSE_WAIT); assert.equal(payloadFinTarget.rcvNxt, 33);
    assert.deepEqual(payloadFinTarget.read(), Uint8Array.of(5, 6));
    const urgentWindow = new TCPConnection(6007, 6008); urgentWindow.state = TCPState.ESTABLISHED;
    urgentWindow.rcvNxt = 300; urgentWindow.rcvWnd = 10;
    const outsideUrgent = new TCPSegment(6008, 6007, 299, 0, new TCP_Control_Bits(), 0, 1,
        new TCP_Options(), Uint8Array.of(9)); outsideUrgent.controlBits.URG = true;
    assert(urgentWindow.receive(outsideUrgent)?.controlBits.ACK); assert.equal(urgentWindow.urgentBytesPending(), 0);
    const zeroWindowUrgent = new TCPConnection(6021, 6022); zeroWindowUrgent.state = TCPState.ESTABLISHED;
    zeroWindowUrgent.rcvWnd = 0; zeroWindowUrgent.advertisedRcvWnd = 0; zeroWindowUrgent.rcvNxt = 70;
    const zeroWindowUrgentFlags = new TCP_Control_Bits(); zeroWindowUrgentFlags.ACK = true; zeroWindowUrgentFlags.URG = true;
    const zeroWindowUrgentSegment = new TCPSegment(6022, 6021, 70, 0, zeroWindowUrgentFlags, 0, 2,
        new TCP_Options(), Uint8Array.of(9, 8));
    assert(zeroWindowUrgent.receive(zeroWindowUrgentSegment)?.controlBits.ACK);
    assert.equal(zeroWindowUrgent.receiveBuffer.length, 0);
    assert.deepEqual(zeroWindowUrgent.readUrgent(), Uint8Array.of(9, 8));
    const malformedAckClient = new TCPConnection(6100, 6200);
    const malformedSyn = malformedAckClient.activeOpen(10);
    const bareAck = new TCPSegment(6200, 6100, 20, 10); bareAck.controlBits.ACK = true;
    assert(malformedAckClient.receive(bareAck)?.controlBits.RST); assert.equal(malformedAckClient.state, TCPState.SYN_SENT);
    const malformedSynAck = new TCPSegment(6200, 6100, 20, 10);
    malformedSynAck.controlBits.SYN = true; malformedSynAck.controlBits.ACK = true;
    assert.equal(malformedAckClient.receive(malformedSynAck)?.controlBits.RST, true);
    assert.equal(malformedAckClient.state, TCPState.SYN_SENT);
    const corrupted = new TCPSegment(1, 2, 3).encode(source, destination);
    corrupted[19] = (corrupted[19] ?? 0) ^ 1;
    assert.throws(() => TCPSegment.decode(corrupted, source, destination));
    const malformedWireHost = new TCPHost(); malformedWireHost.listen(6211);
    const malformedWireClient = new TCPConnection(6212, 6211);
    const malformedWireSyn = malformedWireClient.activeOpen(86).encode(source, destination);
    malformedWireSyn[19] = (malformedWireSyn[19] ?? 0) ^ 1;
    assert.doesNotThrow(() => malformedWireHost.receiveWire(malformedWireSyn, source, destination));
    assert.equal(malformedWireHost.connections.size, 0);
    const malformedWireConnection = new TCPConnection(6213, 6214);
    assert.doesNotThrow(() => malformedWireConnection.receiveWire(malformedWireSyn, source, destination));
    const legacyServer = new TCPConnection(9000, 9001); legacyServer.listen();
    const legacySyn = new TCPSegment(9001, 9000, 1, 0); legacySyn.controlBits.SYN = true;
    legacyServer.receive(legacySyn); assert.equal(legacyServer.sendMss, 536);
    assert.equal(legacyServer.congestionWindow, 5_360);
    const legacyIpv6Server = new TCPConnection(9002, 9003, 6); legacyIpv6Server.listen();
    const legacyIpv6Syn = new TCPSegment(9003, 9002, 1, 0); legacyIpv6Syn.controlBits.SYN = true;
    legacyIpv6Server.receive(legacyIpv6Syn); assert.equal(legacyIpv6Server.sendMss, 1220);
    const synDataServer = new TCPConnection(6110, 6111); synDataServer.listen();
    const synData = new TCPSegment(6111, 6110, 70, 0, new TCP_Control_Bits(), 65_535,
        0, new TCP_Options(), Uint8Array.of(1, 2, 3)); synData.controlBits.SYN = true;
    const synDataAck = synDataServer.receive(synData)!; assert(synDataAck.controlBits.SYN);
    const synFinalAck = new TCPSegment(6111, 6110, 80, synDataAck.sequenceNumber + 1,
        new TCP_Control_Bits(), 65_535); synFinalAck.controlBits.ACK = true;
    const synDataResponse = synDataServer.receive(synFinalAck);
    assert.equal(synDataServer.state, TCPState.ESTABLISHED); assert.deepEqual(synDataServer.read(), Uint8Array.of(1, 2, 3));
    assert(synDataResponse?.controlBits.ACK);
    const finalAckDataServer = new TCPConnection(6112, 6113); finalAckDataServer.listen();
    const finalAckDataClient = new TCPConnection(6113, 6112);
    const finalAckSyn = finalAckDataClient.activeOpen(90);
    const finalAckSynAck = finalAckDataServer.receive(finalAckSyn)!;
    const finalAckWithData = new TCPSegment(6113, 6112, finalAckSyn.sequenceNumber + 1,
        finalAckSynAck.sequenceNumber + 1, new TCP_Control_Bits(), 65_535, 0, new TCP_Options(), Uint8Array.of(21, 22));
    finalAckWithData.controlBits.ACK = true;
    assert(finalAckDataServer.receive(finalAckWithData)?.controlBits.ACK);
    assert.equal(finalAckDataServer.state, TCPState.ESTABLISHED);
    assert.deepEqual(finalAckDataServer.read(), Uint8Array.of(21, 22));
    const activeDataClient = new TCPConnection(6120, 6121), activeDataServer = new TCPConnection(6121, 6120);
    activeDataServer.listen(); const activeSyn = activeDataClient.activeOpen(30, Uint8Array.of(4, 5));
    const activeSynAck = activeDataServer.receive(activeSyn)!; const activeFinalAck = activeDataClient.receive(activeSynAck)!;
    const activeDataAck = activeDataServer.receive(activeFinalAck)!; activeDataClient.receive(activeDataAck);
    assert.equal(activeDataServer.state, TCPState.ESTABLISHED); assert.deepEqual(activeDataServer.read(), Uint8Array.of(4, 5));
    assert.equal(activeDataClient.sndUna, activeDataClient.sndNxt);
    const constrainedClient = new TCPConnection(6122, 6123), constrainedServer = new TCPConnection(6123, 6122);
    constrainedServer.rcvWnd = 32; constrainedServer.advertisedRcvWnd = 32; constrainedServer.listen();
    const constrainedSynAck = constrainedServer.receive(constrainedClient.activeOpen(40))!;
    constrainedClient.receive(constrainedSynAck); assert.equal(constrainedClient.sndWnd, 32);
    assert.equal(constrainedClient.queueSend(new Uint8Array(16), false, 0).length, 1);
    const flowControlledSender = new TCPConnection(6124, 6125); flowControlledSender.state = TCPState.ESTABLISHED;
    flowControlledSender.sndWnd = 4; flowControlledSender.congestionWindow = 4;
    const immediateFlowControlled = flowControlledSender.sendAll(new Uint8Array(10), true);
    assert.equal(immediateFlowControlled.length, 1); assert.equal(immediateFlowControlled[0]?.payload.length, 4);
    assert.equal(flowControlledSender.sendQueue.length, 6);
    flowControlledSender.sndWnd = 100; flowControlledSender.congestionWindow = 100;
    const drainedFlowControlled = flowControlledSender.flushSendQueue(false, flowControlledSender.swsOverrideAt + 1);
    assert.equal(drainedFlowControlled.length, 1); assert.equal(drainedFlowControlled[0]?.payload.length, 6);
    assert.equal(drainedFlowControlled[0]?.controlBits.PSH, true);
    const closed = new TCPConnection(9100, 9101);
    const reset = closed.receive(new TCPSegment(9101, 9100, 10, 0));
    assert(reset && reset.controlBits.RST && reset.controlBits.ACK && reset.acknowledgmentNumber === 10);
    const closedRst = new TCPSegment(9101, 9100, 10, 0); closedRst.controlBits.RST = true;
    assert.equal(closed.receive(closedRst), undefined); assert.equal(closed.pollEvents().length, 0);
    const networkErrorConnection = new TCPConnection(9102, 9103); networkErrorConnection.state = TCPState.ESTABLISHED;
    assert.equal(networkErrorConnection.handleIcmpError(4, 3, 0), false);
    assert.equal(networkErrorConnection.state, TCPState.ESTABLISHED);
    assert.equal(networkErrorConnection.pollEvents()[0]?.type, "network-error");
    assert.equal(networkErrorConnection.handleIcmpError(4, 3, 3), true);
    assert.equal(networkErrorConnection.state, TCPState.CLOSED);
    assert.equal(networkErrorConnection.pollEvents().at(-1)?.type, "closed");
    const ipv4MtuErrorConnection = new TCPConnection(9112, 9113); ipv4MtuErrorConnection.state = TCPState.ESTABLISHED;
    assert.equal(ipv4MtuErrorConnection.handleIcmpError(4, 3, 4, undefined, 576), false);
    assert.equal(ipv4MtuErrorConnection.pathMtu, 576);
    assert.equal(ipv4MtuErrorConnection.state, TCPState.ESTABLISHED);
    const unmatchedErrorConnection = new TCPConnection(9108, 9109); unmatchedErrorConnection.state = TCPState.ESTABLISHED;
    const unrelatedQuotedSegment = new TCPSegment(9999, 9998);
    assert.equal(unmatchedErrorConnection.handleIcmpError(4, 3, 3, unrelatedQuotedSegment), false);
    assert.equal(unmatchedErrorConnection.state, TCPState.ESTABLISHED);
    assert.equal(unmatchedErrorConnection.pollEvents().length, 0);
    const sourceQuenchConnection = new TCPConnection(9104, 9105); sourceQuenchConnection.state = TCPState.ESTABLISHED;
    assert.equal(sourceQuenchConnection.handleIcmpError(4, 4, 0), false);
    assert.equal(sourceQuenchConnection.pollEvents().length, 0);
    const ipv6ErrorConnection = new TCPConnection(9106, 9107, 6); ipv6ErrorConnection.state = TCPState.ESTABLISHED;
    assert.equal(ipv6ErrorConnection.handleIcmpError(6, 3, 0), false);
    assert.equal(ipv6ErrorConnection.state, TCPState.ESTABLISHED);
    assert.equal(ipv6ErrorConnection.handleIcmpError(6, 2, 0, undefined, 1_280), false);
    assert.equal(ipv6ErrorConnection.pathMtu, 1_280);
    const errorHost = new TCPHost(); errorHost.connect(9110, 9111, 87);
    const quotedErrorSegment = new TCPSegment(9110, 9111);
    assert.equal(errorHost.handleIcmpError(9110, 9111, 4, 3, 3, quotedErrorSegment), true);
    assert.equal(errorHost.connections.size, 0);
    const client = new TCPConnection(1000, 2000), server = new TCPConnection(2000, 1000);
    server.listen(); const syn = client.activeOpen(10); const synAck = server.receive(syn)!;
    assert.equal(syn.options.MSS, 1460); assert.equal(synAck.options.SACK_Permitted, true);
    assert.equal(synAck.options.Timestamp_Echo_Reply, syn.options.Timestamp_Value);
    assert.equal(server.state, TCPState.SYN_RECEIVED); client.receive(synAck); assert.equal(client.state, TCPState.ESTABLISHED);
    const clientStatus = client.status();
    assert.equal(clientStatus.state, TCPState.ESTABLISHED);
    assert.equal(clientStatus.localPort, 1000); assert.equal(clientStatus.remotePort, 2000);
    assert.equal(clientStatus.addressFamily, 4); assert(clientStatus.sndWnd > 0);
    const duplicateSyn = new TCPSegment(1000, 2000, syn.sequenceNumber, 0, new TCP_Control_Bits(), 65_535);
    duplicateSyn.controlBits.SYN = true;
    assert.equal(server.receive(duplicateSyn)?.sequenceNumber, synAck.sequenceNumber);
    const wireClient = new TCPConnection(2001, 3001), wireServer = new TCPConnection(3001, 2001);
    wireServer.listen();
    const wireSyn = wireClient.activeOpen(12);
    const wireSynAck = wireServer.receiveWire(wireSyn.encode(source, destination), source, destination)!;
    assert(wireSynAck.controlBits.SYN && wireSynAck.controlBits.ACK);
    const wireAck = wireClient.receiveWire(wireSynAck.encode(destination, source), destination, source)!;
    assert(wireAck.controlBits.ACK);
    wireServer.receiveWire(wireAck.encode(source, destination), source, destination);
    assert.equal(wireServer.state, TCPState.ESTABLISHED);
    const ipv6ClientHost = new TCPHost(), ipv6ServerHost = new TCPHost();
    const ipv6Listener = ipv6ServerHost.listen(6200, 6);
    const ipv6Open = ipv6ClientHost.connect(6201, 6200, 77, 6);
    const ipv6SynAck = ipv6ServerHost.receiveWire(ipv6Open.syn.encode("2001:db8::1", "2001:db8::2"), "2001:db8::1", "2001:db8::2")!;
    const ipv6FinalAck = ipv6ClientHost.receiveWire(ipv6SynAck.encode("2001:db8::2", "2001:db8::1"), "2001:db8::2", "2001:db8::1")!;
    ipv6ServerHost.receiveWire(ipv6FinalAck.encode("2001:db8::1", "2001:db8::2"), "2001:db8::1", "2001:db8::2");
    assert.equal(ipv6Listener.addressFamily, 6);
    assert.equal(Array.from(ipv6ServerHost.connections.values())[0]?.addressFamily, 6);
    assert.equal(ipv6Open.connection.state, TCPState.ESTABLISHED);
    const tupleHost = new TCPHost(); tupleHost.listen(6250, 4);
    const tupleClientA = new TCPConnection(6251, 6250), tupleClientB = new TCPConnection(6251, 6250);
    const tupleSynA = tupleClientA.activeOpen(101), tupleSynB = tupleClientB.activeOpen(202);
    const tupleDestination = "198.51.100.40";
    tupleHost.receiveWire(tupleSynA.encode("192.0.2.40", tupleDestination), "192.0.2.40", tupleDestination);
    tupleHost.receiveWire(tupleSynB.encode("192.0.2.41", tupleDestination), "192.0.2.41", tupleDestination);
    assert.equal(tupleHost.connections.size, 2);
    assert.equal(new Set(tupleHost.connections.keys()).size, 2);
    assert([...tupleHost.connections.keys()].every((key) => key.startsWith("4:") && key.includes("->")));
    const dualFamilyHost = new TCPHost();
    const dualIpv4Listener = dualFamilyHost.listen(6202, 4);
    const dualIpv6Listener = dualFamilyHost.listen(6202, 6);
    assert.equal(dualFamilyHost.listeners.size, 2);
    assert.equal(dualIpv4Listener.addressFamily, 4);
    assert.equal(dualIpv6Listener.addressFamily, 6);
    const dualIpv4Client = new TCPConnection(6203, 6202, 4);
    const dualIpv6Client = new TCPConnection(6204, 6202, 6);
    const dualIpv4Syn = dualIpv4Client.activeOpen(81);
    const dualIpv6Syn = dualIpv6Client.activeOpen(82);
    assert.equal(dualFamilyHost.receive(dualIpv4Syn)?.controlBits.SYN, true);
    assert.equal(dualFamilyHost.receive(dualIpv6Syn, 6)?.controlBits.SYN, true);
    const broadcastHost = new TCPHost(); broadcastHost.listen(6205, 4);
    const broadcastClient = new TCPConnection(6206, 6205, 4);
    const broadcastSyn = broadcastClient.activeOpen(83);
    assert.equal(broadcastHost.receiveWire(broadcastSyn.encode("192.0.2.3", "255.255.255.255"),
        "192.0.2.3", "255.255.255.255"), undefined);
    assert.equal(broadcastHost.connections.size, 0);
    const multicastHost = new TCPHost(); multicastHost.listen(6207, 6);
    const multicastClient = new TCPConnection(6208, 6207, 6);
    const multicastSyn = multicastClient.activeOpen(84);
    assert.equal(multicastHost.receiveWire(multicastSyn.encode("2001:db8::3", "ff02::1"),
        "2001:db8::3", "ff02::1"), undefined);
    assert.equal(multicastHost.connections.size, 0);
    const invalidSourceHost = new TCPHost(); invalidSourceHost.listen(6209, 4);
    const invalidSourceClient = new TCPConnection(6210, 6209, 4);
    const invalidSourceSyn = invalidSourceClient.activeOpen(85);
    assert.equal(invalidSourceHost.receiveWire(invalidSourceSyn.encode("0.0.0.0", "198.51.100.9"),
        "0.0.0.0", "198.51.100.9"), undefined);
    assert.equal(invalidSourceHost.connections.size, 0);
    assert.equal(client.timestampsEnabled, true); assert(client.acknowledgment().options.Timestamp_Value !== undefined);
    assert.equal(client.ecnNegotiated, true); assert.equal(client.acknowledgment().window, 511);
    server.receive(client.acknowledgment()); assert.equal(server.state, TCPState.ESTABLISHED);
    assert.equal(client.retransmissionQueue.length, 0); assert.equal(server.retransmissionQueue.length, 0);
    traceMark("handshake established and SYN retransmissions retired");
    const timestampBeforeInvalid = client.timestampRecent;
    const invalidTimestamp = new TCPSegment(2000, 1000, (client.rcvNxt - 1) >>> 0, client.sndNxt,
        new TCP_Control_Bits(), 65_535, 0, new TCP_Options());
    invalidTimestamp.controlBits.ACK = true; invalidTimestamp.options.Timestamp_Value = (timestampBeforeInvalid + 100) >>> 0;
    assert(client.receive(invalidTimestamp)?.controlBits.ACK); assert.equal(client.timestampRecent, timestampBeforeInvalid);
    const optionMssProbe = new TCPConnection(6130, 6131); optionMssProbe.state = TCPState.ESTABLISHED;
    optionMssProbe.timestampsEnabled = true; optionMssProbe.sendMss = 1_460;
    optionMssProbe.send(Uint8Array.from({ length: 1_448 }));
    assert.throws(() => optionMssProbe.send(Uint8Array.from({ length: 1_449 })));
    const pathMtuProbe = new TCPConnection(6132, 6133); pathMtuProbe.state = TCPState.ESTABLISHED;
    pathMtuProbe.sendMss = 2_000; pathMtuProbe.setPathMtu(1_500);
    pathMtuProbe.send(Uint8Array.from({ length: 1_460 }));
    assert.throws(() => pathMtuProbe.send(Uint8Array.from({ length: 1_461 })));
    const mtuSyn = new TCPConnection(6134, 6135); mtuSyn.setPathMtu(576);
    assert.equal(mtuSyn.activeOpen(88).options.MSS, 536);
    const mtuIpv6Syn = new TCPConnection(6136, 6137, 6); mtuIpv6Syn.setPathMtu(1_280);
    assert.equal(mtuIpv6Syn.activeOpen(89).options.MSS, 1_220);
    client.sndWnd = 1_234; client.sndWndAck = client.sndUna; client.sndWndAckValid = true;
    const excessiveAck = new TCPSegment(2000, 1000, server.sndNxt, (client.sndNxt + 1) >>> 0);
    excessiveAck.controlBits.ACK = true; excessiveAck.window = 65_535; assert(client.receive(excessiveAck)?.controlBits.ACK);
    assert.equal(client.sndWnd, 1_234); client.sndWnd = 65_535;
    const sendWindowBeforeStaleUpdate = client.sndWnd;
    const staleWindow = new TCPSegment(2000, 1000, client.sndWndSeq, (client.sndUna - 1) >>> 0,
        new TCP_Control_Bits(), 0); staleWindow.controlBits.ACK = true;
    client.receive(staleWindow); assert.equal(client.sndWnd, sendWindowBeforeStaleUpdate);
    const newerStaleWindow = new TCPSegment(2000, 1000, client.rcvNxt, (client.sndUna - 1) >>> 0,
        new TCP_Control_Bits(), 0); newerStaleWindow.controlBits.ACK = true;
    client.receive(newerStaleWindow); assert.equal(client.sndWnd, sendWindowBeforeStaleUpdate);
    const congestionBefore = client.congestionWindow;
    const ece = new TCPSegment(2000, 1000, server.sndNxt, client.sndNxt, new TCP_Control_Bits(), server.rcvWnd);
    ece.controlBits.ACK = true; ece.controlBits.ECE = true; client.receive(ece);
    assert(client.congestionWindow < congestionBefore); assert.equal(client.acknowledgment().controlBits.CWR, true);
    const data = client.send(Uint8Array.of(1, 2, 3));
    const dataAck = server.receive(data)!; assert.deepEqual(data.payload, Uint8Array.of(1, 2, 3));
    client.receive(dataAck); assert.equal(client.retransmissionQueue.length, 0);
    const overlapReceiver = new TCPConnection(6142, 6143); overlapReceiver.state = TCPState.ESTABLISHED;
    overlapReceiver.rcvNxt = 100; overlapReceiver.rcvWnd = 20; overlapReceiver.advertisedRcvWnd = 20;
    const overlapFlags = new TCP_Control_Bits(); overlapFlags.ACK = true;
    const firstOutOfOrder = new TCPSegment(6143, 6142, 105, 0, overlapFlags, 65_535, 0, new TCP_Options(), Uint8Array.of(1, 2, 3, 4, 5));
    const overlappingOutOfOrder = new TCPSegment(6143, 6142, 107, 0, overlapFlags, 65_535, 0, new TCP_Options(), Uint8Array.of(3, 4, 5, 6, 7));
    overlapReceiver.receive(firstOutOfOrder); overlapReceiver.receive(overlappingOutOfOrder);
    assert.equal(overlapReceiver.rcvWnd, 13);
    const missingPrefix = new TCPSegment(6143, 6142, 100, 0, overlapFlags, 65_535, 0, new TCP_Options(), Uint8Array.of(9, 8, 7, 6, 5));
    overlapReceiver.receive(missingPrefix);
    assert.equal(overlapReceiver.rcvNxt, 112); assert.equal(overlapReceiver.receiveBuffer.length, 12);
    assert.equal(overlapReceiver.rcvWnd, 8); assert.equal(overlapReceiver.outOfOrder.length, 0);
    const emptySendConnection = new TCPConnection(6138, 6139); emptySendConnection.state = TCPState.ESTABLISHED;
    emptySendConnection.send(new Uint8Array());
    assert.equal(emptySendConnection.retransmissionQueue.length, 0);
    assert.throws(() => new TCPConnection(6139, 6140).queueSend(Uint8Array.of(1)));
    const partialAckConnection = new TCPConnection(6140, 6141); partialAckConnection.state = TCPState.ESTABLISHED;
    const partialData = partialAckConnection.send(Uint8Array.from({ length: 10 }, (_, index) => index));
    const partialAckFlags = new TCP_Control_Bits(); partialAckFlags.ACK = true;
    const partialAck = new TCPSegment(6141, 6140, 0, partialData.sequenceNumber + 4,
        partialAckFlags, 65_535);
    partialAckConnection.receive(partialAck);
    assert.equal(partialAckConnection.retransmissionQueue[0]?.sequence, partialData.sequenceNumber + 4);
    assert.equal(partialAckConnection.retransmissionQueue[0]?.length, 6);
    const partialRetry = partialAckConnection.onRetransmissionTimeout(performance.now() + 1_001)[0];
    assert(partialRetry); assert.deepEqual(partialRetry.payload, Uint8Array.from({ length: 6 }, (_, index) => index + 4));
    const batch = client.sendAll(new Uint8Array(3_000)); assert.equal(batch.length, 3);
    const pushProbe = new TCPConnection(1001, 2001); pushProbe.state = TCPState.ESTABLISHED;
    const pushedBatch = pushProbe.sendAll(new Uint8Array(3_000), true);
    assert(pushedBatch.length === 3 && pushedBatch.slice(0, -1).every(segment => !segment.controlBits.PSH));
    assert.equal(pushedBatch.at(-1)?.controlBits.PSH, true);
    for (const segment of batch) client.receive(server.receive(segment)!);
    assert(client.smoothedRtt !== undefined && client.retransmissionTimeout >= 1_000);
    assert.equal(server.receiveBuffer.length, 3_003);
    assert.equal(server.read(3).length, 3); assert.equal(server.rcvWnd, 65_535 - 3_000);
    const advertisedBeforeWindowUpdate = server.advertisedRcvWnd;
    server.read(2_000); assert(server.advertisedRcvWnd > advertisedBeforeWindowUpdate); assert(server.windowUpdatePending);
    const windowUpdate = server.tick(performance.now());
    assert(windowUpdate.some((segment) => segment.controlBits.ACK)); assert.equal(server.windowUpdatePending, false);
    traceMark("data transfer, reassembly, and receive-window accounting");
    server.enableDelayedAcks(1);
    const urgent = client.sendUrgent(Uint8Array.of(9, 8, 7));
    server.receive(urgent); assert(server.ackPending); assert(server.poll(server.ackDeadline));
    assert.equal(server.pollEvents()[0]?.type, "urgent");
    assert.equal(server.urgentBytesPending(), 3); assert.deepEqual(server.readUrgent(), Uint8Array.of(9, 8, 7));
    const delayedPair = new TCPConnection(2010, 2011); delayedPair.state = TCPState.ESTABLISHED; delayedPair.enableDelayedAcks(100);
    const delayedFlags = new TCP_Control_Bits(); delayedFlags.ACK = true;
    const fullPayload = new Uint8Array(1_460);
    const fullOne = new TCPSegment(2011, 2010, 0, 0, delayedFlags, 0, 0, new TCP_Options(), fullPayload);
    const fullTwo = new TCPSegment(2011, 2010, 1_460, 0, delayedFlags, 0, 0, new TCP_Options(), fullPayload);
    assert.equal(delayedPair.receive(fullOne), undefined); assert(delayedPair.receive(fullTwo)?.controlBits.ACK);
    const outOfOrderFlags = new TCP_Control_Bits(); outOfOrderFlags.ACK = true;
    const outOfOrder = new TCPSegment(1000, 2000, server.rcvNxt + 3, server.sndNxt,
        outOfOrderFlags, client.rcvWnd, 0, new TCP_Options(), Uint8Array.of(7, 8, 9));
    const sackAck = server.receive(outOfOrder)!;
    assert.equal(sackAck.options.SACK.length, 1);
    const sackSender = new TCPConnection(2020, 2021); sackSender.state = TCPState.ESTABLISHED;
    sackSender.sackPermitted = true;
    const sackFirst = sackSender.send(Uint8Array.of(1, 2, 3));
    const sackSecond = sackSender.send(Uint8Array.of(4, 5, 6));
    const selectiveAckFlags = new TCP_Control_Bits(); selectiveAckFlags.ACK = true;
    const selectiveAckOptions = new TCP_Options();
    selectiveAckOptions.SACK.push({ left: sackSecond.sequenceNumber, right: sackSecond.sequenceNumber + sackSecond.payload.length });
    const selectiveAck = new TCPSegment(2021, 2020, 0, sackFirst.sequenceNumber + sackFirst.payload.length,
        selectiveAckFlags, 65_535, 0, selectiveAckOptions);
    sackSender.receive(selectiveAck);
    assert.equal(sackSender.retransmissionQueue.length, 0);
    assert.equal(sackSender.due(performance.now() + 2_000).length, 0);
    const finalSelectiveAck = new TCPSegment(2021, 2020, 0, sackSecond.sequenceNumber + sackSecond.payload.length,
        selectiveAckFlags, 65_535);
    sackSender.receive(finalSelectiveAck);
    assert.equal(sackSender.retransmissionQueue.length, 0);
    const partialSackSender = new TCPConnection(2024, 2025); partialSackSender.state = TCPState.ESTABLISHED;
    partialSackSender.sackPermitted = true;
    const partialSackData = partialSackSender.send(Uint8Array.from({ length: 10 }, (_, index) => index));
    const partialSackOptions = new TCP_Options(); partialSackOptions.SACK.push({ left: 4, right: 8 });
    partialSackSender.receive(new TCPSegment(2025, 2024, 0, partialSackData.sequenceNumber,
        selectiveAckFlags, 65_535, 0, partialSackOptions));
    assert.equal(partialSackSender.retransmissionQueue.length, 2);
    assert.equal(partialSackSender.retransmissionQueue[0]?.length, 4);
    assert.equal(partialSackSender.retransmissionQueue[1]?.sequence, 8);
    assert.equal(partialSackSender.retransmissionQueue[1]?.length, 2);
    const partialWrapSackSender = new TCPConnection(2026, 2027); partialWrapSackSender.state = TCPState.ESTABLISHED;
    partialWrapSackSender.sackPermitted = true;
    partialWrapSackSender.sndUna = 0xffff_fffc; partialWrapSackSender.sndNxt = 0xffff_fffc;
    const partialWrapData = partialWrapSackSender.send(new Uint8Array(10));
    const partialWrapOptions = new TCP_Options(); partialWrapOptions.SACK.push({ left: 0xffff_ffff, right: 3 });
    partialWrapSackSender.receive(new TCPSegment(2027, 2026, 0, partialWrapData.sequenceNumber,
        selectiveAckFlags, 65_535, 0, partialWrapOptions));
    assert.equal(partialWrapSackSender.retransmissionQueue.length, 2);
    assert.equal(partialWrapSackSender.retransmissionQueue[0]?.length, 3);
    assert.equal(partialWrapSackSender.retransmissionQueue[1]?.length, 3);
    const sackWrapSender = new TCPConnection(2022, 2023); sackWrapSender.state = TCPState.ESTABLISHED;
    sackWrapSender.sackPermitted = true; sackWrapSender.sndUna = 0xffff_fffe; sackWrapSender.sndNxt = 0xffff_fffe;
    const sackWrapData = sackWrapSender.send(Uint8Array.of(7, 8, 9, 10));
    const sackWrapFlags = new TCP_Control_Bits(); sackWrapFlags.ACK = true;
    const sackWrapOptions = new TCP_Options(); sackWrapOptions.SACK.push({ left: 0xffff_fffe, right: 2 });
    sackWrapSender.receive(new TCPSegment(2023, 2022, 0, sackWrapData.sequenceNumber,
        sackWrapFlags, 65_535, 0, sackWrapOptions));
    assert.equal(sackWrapSender.due(performance.now() + 2_000).length, 0);
    const inOrder = new TCPSegment(1000, 2000, server.rcvNxt, server.sndNxt,
        outOfOrderFlags, client.rcvWnd, 0, new TCP_Options(), Uint8Array.of(4, 5, 6));
    server.receive(inOrder); assert.equal(server.outOfOrder.length, 0);
    const duplicate = new TCPSegment(1000, 2000, (server.rcvNxt - 3) >>> 0, server.sndNxt,
        outOfOrderFlags, client.rcvWnd, 0, new TCP_Options(), Uint8Array.of(4, 5, 6));
    assert(server.receive(duplicate)?.controlBits.ACK);
    const retryConnection = new TCPConnection(3000, 4000);
    retryConnection.activeOpen(99);
    const previousBackoff = retryConnection.retransmissionBackoff;
    assert.equal(retryConnection.onRetransmissionTimeout(performance.now() + retryConnection.retransmissionTimeout).length, 1);
    assert(retryConnection.retransmissionBackoff > previousBackoff);
    let retryClock = performance.now();
    for (let retry = 0; retry <= retryConnection.maxRetransmissions; retry++) {
        retryClock += retryConnection.retransmissionTimeout * retryConnection.retransmissionBackoff * 2;
        retryConnection.onRetransmissionTimeout(retryClock);
    }
    assert.equal(retryConnection.state, TCPState.CLOSED);
    const fastConnection = new TCPConnection(3002, 4002); fastConnection.state = TCPState.ESTABLISHED;
    const lost = fastConnection.send(Uint8Array.of(1, 2, 3));
    const duplicateFlags = new TCP_Control_Bits(); duplicateFlags.ACK = true;
    let fastResponse: TCPSegment | undefined;
    for (let duplicate = 0; duplicate < 3; duplicate++) {
        const acknowledgement = new TCPSegment(4002, 3002, 0, fastConnection.sndUna,
            duplicateFlags, 65_535);
        fastResponse = fastConnection.receive(acknowledgement);
    }
    assert.equal(fastResponse, lost); assert.equal(fastConnection.duplicateAcknowledgments, 3);
    assert.equal(fastConnection.retransmissionQueue[0]?.retransmitted, true);
    const userTimeoutConnection = new TCPConnection(3001, 4001); userTimeoutConnection.activeOpen(4); userTimeoutConnection.setUserTimeout(100);
    assert.equal(userTimeoutConnection.tick(performance.now() + 101).length, 0);
    assert.equal(userTimeoutConnection.state, TCPState.CLOSED);
    assert.equal(userTimeoutConnection.pollEvents()[0]?.type, "timeout");
    const probeConnection = new TCPConnection(7000, 8000); probeConnection.state = TCPState.ESTABLISHED; probeConnection.sndWnd = 0;
    assert(probeConnection.zeroWindowProbe(Uint8Array.of(1)));
    const persistedProbe = new TCPConnection(7001, 8001); persistedProbe.state = TCPState.ESTABLISHED;
    persistedProbe.retransmissionTimeout = 10_000;
    const persistedData = persistedProbe.send(Uint8Array.of(31, 32, 33)); persistedProbe.sndWnd = 0;
    const probeStart = performance.now(); persistedProbe.tick(probeStart);
    const persistedOutput = persistedProbe.tick(probeStart + 10_001);
    assert(persistedOutput.some((segment) => segment.sequenceNumber === persistedData.sequenceNumber && segment.payload.length === 1));
    const aborted = probeConnection.abort(); assert(aborted.controlBits.RST); assert.equal(probeConnection.state, TCPState.CLOSED);
    traceMark("retransmission timeout, fast retransmit, and zero-window recovery");
    const queuedConnection = new TCPConnection(8100, 8200); queuedConnection.state = TCPState.ESTABLISHED;
    assert.equal(queuedConnection.queueSend(Uint8Array.of(1, 2, 3), false, 0).length, 0);
    assert.equal(queuedConnection.close(), undefined);
    const queuedOutput = queuedConnection.flushSendQueue(false, 1_000);
    assert.equal(queuedOutput.length, 2); assert(queuedOutput.some((segment) => segment.controlBits.FIN));
    assert.equal(queuedConnection.state, TCPState.FIN_WAIT_1); assert.equal(queuedConnection.sendQueue.length, 0);
    const shrinkingWindow = new TCPConnection(8102, 8202); shrinkingWindow.state = TCPState.ESTABLISHED;
    shrinkingWindow.maximumSendWindow = 1_000; shrinkingWindow.sndWnd = 100;
    assert.equal(shrinkingWindow.queueSend(new Uint8Array(500), false, 0).length, 0);
    assert.equal(shrinkingWindow.flushSendQueue(false, 1_000).length, 1);
    const noNagleConnection = new TCPConnection(8101, 8201); noNagleConnection.state = TCPState.ESTABLISHED;
    noNagleConnection.setNagle(false); assert.equal(noNagleConnection.queueSend(Uint8Array.of(1, 2, 3), false, 0).length, 1);
    const timerHost = new TCPHost(); timerHost.connect(8_100, 8_200, 91);
    const timerOutput = timerHost.tick(performance.now() + 1_001);
    assert.equal(timerOutput.length, 1); assert.equal(timerOutput[0]?.key, "8100:8200");
    const wildcardListener = new TCPConnection(8_201, 0); wildcardListener.listen();
    const wildcardSyn = new TCPSegment(8_202, 8_201, 11, 0); wildcardSyn.controlBits.SYN = true;
    const wildcardSynAck = wildcardListener.receive(wildcardSyn);
    assert(wildcardSynAck?.controlBits.SYN && wildcardSynAck.controlBits.ACK);
    assert.equal(wildcardListener.remotePort, 8_202);
    const packetTraces: TCPPacketTrace[] = [];
    const stopTracing = wildcardListener.observePackets((trace) => packetTraces.push(trace));
    wildcardListener.receive(wildcardSyn);
    stopTracing(); wildcardListener.receive(wildcardSyn);
    assert.equal(packetTraces.length, 2);
    assert.equal(packetTraces[0]?.direction, "inbound"); assert.equal(packetTraces[1]?.direction, "outbound");
    const directTraceConnection = new TCPConnection(8_203, 8_204);
    const directTraces: TCPPacketTrace[] = [];
    directTraceConnection.observePackets((trace) => directTraces.push(trace));
    directTraceConnection.activeOpen(12);
    assert.equal(directTraces[0]?.direction, "outbound");
    const swsTarget = new TCPConnection(8_300, 8_400); swsTarget.receiveBufferCapacity = 1_000;
    swsTarget.localMss = 100; swsTarget.sendMss = 1_460; swsTarget.advertisedRcvWnd = 800;
    swsTarget.receiveBuffer.push(...new Array(100).fill(1)); swsTarget.read(0);
    assert.equal(swsTarget.windowUpdatePending, true);
    const scaledWindow = new TCPConnection(8_301, 8_401);
    scaledWindow.state = TCPState.ESTABLISHED; scaledWindow.windowScale = 4;
    scaledWindow.receiveBufferCapacity = 131_072; scaledWindow.rcvWnd = 65_536;
    scaledWindow.advertisedRcvWnd = 65_536; scaledWindow.receiveBuffer.append(new Uint8Array(65_536).fill(1));
    scaledWindow.read(65_536);
    assert.equal(scaledWindow.advertisedRcvWnd, 131_072);
    assert.equal(scaledWindow.acknowledgment().window, 8_192);
    const serverHost = new TCPHost(); serverHost.listen(12_000);
    const clientHost = new TCPHost(); const clientTraces: TCPPacketTrace[] = [];
    const stopClientTracing = clientHost.observePackets((trace) => clientTraces.push(trace));
    const opened = clientHost.connect(11_000, 12_000, 77);
    assert.equal(clientTraces[0]?.direction, "outbound"); stopClientTracing();
    const hostTraces: TCPPacketTrace[] = [];
    const stopHostTracing = serverHost.observePackets((trace) => hostTraces.push(trace));
    const hostSynAck = serverHost.receiveWire(opened.syn.encode(source, destination), source, destination)!;
    opened.connection.receiveWire(hostSynAck.encode(destination, source), destination, source);
    const hostFinalAck = opened.connection.acknowledgment();
    serverHost.receiveWire(hostFinalAck.encode(source, destination), source, destination);
    stopHostTracing();
    assert(hostTraces.some((trace) => trace.direction === "inbound"));
    assert(hostTraces.some((trace) => trace.direction === "outbound"));
    assert.equal(Array.from(serverHost.connections.values())[0]?.state, TCPState.ESTABLISHED);
    assert.equal(serverHost.status().length, 1);
    assert.equal(serverHost.status()[0]?.state, TCPState.ESTABLISHED);
    const timerConnection = new TCPConnection(8300, 8400); timerConnection.state = TCPState.ESTABLISHED;
    timerConnection.enableKeepAlive(); timerConnection.lastActivity = 0;
    assert.equal(timerConnection.tick(7_200_000).length, 1);
    const closeClient = new TCPConnection(10000, 11000), closeServer = new TCPConnection(11000, 10000);
    closeServer.listen(); const closeSyn = closeClient.activeOpen(500); const closeSynAck = closeServer.receive(closeSyn)!;
    closeClient.receive(closeSynAck); closeServer.receive(closeClient.acknowledgment());
    const fin = closeClient.close(); assert(fin); const finAck = closeServer.receive(fin!); assert(finAck);
    assert.equal(closeServer.state, TCPState.CLOSE_WAIT); closeClient.receive(finAck!); assert.equal(closeClient.state, TCPState.FIN_WAIT_2);
    const halfCloseData = closeServer.send(Uint8Array.of(6, 6, 6));
    closeClient.receive(halfCloseData); assert.equal(closeClient.receiveBuffer.at(-1), 6);
    const serverFin = closeServer.close()!; const clientFinAck = closeClient.receive(serverFin)!;
    assert.equal(closeClient.state, TCPState.TIME_WAIT); closeServer.receive(clientFinAck); assert.equal(closeServer.state, TCPState.CLOSED);
    closeClient.expireTimeWait(closeClient.timeWaitUntil); assert.equal(closeClient.state, TCPState.CLOSED);
    const simultaneousA = new TCPConnection(5000, 6000), simultaneousB = new TCPConnection(6000, 5000);
    const simultaneousSynA = simultaneousA.activeOpen(1), simultaneousSynB = simultaneousB.activeOpen(2);
    const simultaneousAckA = simultaneousA.receive(simultaneousSynB)!;
    const simultaneousAckB = simultaneousB.receive(simultaneousSynA)!;
    assert(simultaneousA.sackPermitted && simultaneousA.peerWindowScale === 7);
    assert(simultaneousA.retransmissionQueue.some((entry) => entry.segment.controlBits.SYN && entry.segment.controlBits.ACK));
    simultaneousA.receive(simultaneousAckB); simultaneousB.receive(simultaneousAckA);
    assert.equal(simultaneousA.state, TCPState.ESTABLISHED); assert.equal(simultaneousB.state, TCPState.ESTABLISHED);
    const closing = new TCPConnection(9200, 9300); closing.state = TCPState.FIN_WAIT_1; closing.sndNxt = 2; closing.rcvNxt = 5;
    const crossingFin = new TCPSegment(9300, 9200, 5, 1, new TCP_Control_Bits(), 65_535);
    crossingFin.controlBits.FIN = true; crossingFin.controlBits.ACK = true;
    closing.receive(crossingFin); assert.equal(closing.state, TCPState.CLOSING);
    const finalAck = new TCPSegment(9300, 9200, 6, 2, new TCP_Control_Bits(), 65_535); finalAck.controlBits.ACK = true;
    closing.receive(finalAck); assert.equal(closing.state, TCPState.TIME_WAIT);
    traceMark("host demultiplexing, keepalive, and connection close states");
    traceResult("PASS deterministic integration/state tests", integrationStartedAt, integrationStartHeap);
    const packetCorpus: Uint8Array[] = [];
    for (let trial = 0; trial < TRIALS; trial++) {
        const word = Math.imul(trial + 1, 0x9e37_79b1) >>> 0;
        const packet = new TCPSegment(1024 + word % 50_000, 1024 + (word >>> 8) % 50_000,
            word, (word ^ 0xa5a5_a5a5) >>> 0, new TCP_Control_Bits(), word & 0xffff, 0,
            new TCP_Options(), Uint8Array.of(word & 255, (word >>> 8) & 255, (word >>> 16) & 255));
        packet.controlBits.ACK = true; packetCorpus.push(packet.encode(source, destination));
    }
    traceMark(`packet corpus encoded (${packetCorpus.length} packets)`);
    const packetStart = nowNanoseconds();
    const packetStartHeap = process.memoryUsage().heapUsed;
    for (const packet of packetCorpus) TCPSegment.decode(packet, source, destination);
    const packetElapsedNs = nowNanoseconds() - packetStart;
    const packetElapsedMs = packetElapsedNs / 1e6;
    traceMark(`packet corpus decoded and checksummed (${packetCorpus.length} packets)`);
    traceResult(`PASS packet decode/checksum benchmark (${TRIALS} packets)`, packetStart, packetStartHeap);
    const elapsedNs = nowNanoseconds() - started;
    const elapsed = elapsedNs / 1e6;
    const packetRate = TRIALS / Math.max(packetElapsedMs, 0.001) * 1000;
    const endMemory = process.memoryUsage().heapUsed;
    console.log("  └─ summary");
    console.log(`     ├─ cycle_time_ms=${elapsed.toFixed(3)} (${durationText(elapsedNs)})`);
    console.log(`     ├─ segments_per_second=${(TRIALS / (elapsed / 1000)).toFixed(0)}`);
    console.log(`     ├─ tcp_packets_per_second=${packetRate.toFixed(0)}`);
    console.log(`     ├─ heap_used=${memoryText(endMemory)}`);
    console.log(`     ├─ heap_delta=${memoryText(endMemory - startMemory)}`);
    console.log("     └─ ipv4_tcp_option_space_limit_bytes=40");
}

if (import.meta.main) runTests();
