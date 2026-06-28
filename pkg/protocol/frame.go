// Package protocol defines the VPN frame format shared between server and client.
package protocol

// PaddingBlockSize is the alignment unit for packet-length obfuscation.
const PaddingBlockSize = 128

// HeaderSize is the length-prefix size in bytes (uint32 big-endian).
const HeaderSize = 4

// MaxPacketLen is the maximum IP packet payload size.
const MaxPacketLen = 65535

// PaddedFrameSize computes the total frame size (header + payload + padding)
// aligned to PaddingBlockSize.
func PaddedFrameSize(payloadLen int) int {
	total := HeaderSize + payloadLen
	if rem := total % PaddingBlockSize; rem != 0 {
		total += PaddingBlockSize - rem
	}
	return total
}

// PaddingLen returns the number of padding bytes for a given payload length.
func PaddingLen(payloadLen int) int {
	return PaddedFrameSize(payloadLen) - HeaderSize - payloadLen
}
