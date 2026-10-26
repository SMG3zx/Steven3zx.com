//go:build !linux

package wasmrun

// openPacketSource reports raw capture as unavailable. Windows (Npcap) and macOS
// (BPF) providers are not written yet.
func openPacketSource(string) (PacketSource, error) { return nil, ErrRawNetUnavailable }
