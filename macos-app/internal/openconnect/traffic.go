package openconnect

import (
	"encoding/binary"
	"errors"
	"net"
	"syscall"
	"time"

	"openconnect.local/desktop/internal/privileged"
)

type TrafficStats struct {
	DownloadBytes       uint64 `json:"downloadBytes"`
	UploadBytes         uint64 `json:"uploadBytes"`
	DownloadBytesPerSec uint64 `json:"downloadBytesPerSec"`
	UploadBytesPerSec   uint64 `json:"uploadBytesPerSec"`
}

type byteCounters struct {
	received uint64
	sent     uint64
}

type trafficTracker struct {
	initialized          bool
	previous             byteCounters
	previousAt           time.Time
	downloaded, uploaded uint64
}

func (t *trafficTracker) sample(current byteCounters, now time.Time) TrafficStats {
	if !t.initialized {
		t.initialized = true
		t.previous = current
		t.previousAt = now
		return TrafficStats{DownloadBytes: t.downloaded, UploadBytes: t.uploaded}
	}
	receivedDelta, sentDelta := counterDelta(t.previous.received, current.received), counterDelta(t.previous.sent, current.sent)
	t.downloaded += receivedDelta
	t.uploaded += sentDelta
	elapsed := now.Sub(t.previousAt).Seconds()
	stats := TrafficStats{DownloadBytes: t.downloaded, UploadBytes: t.uploaded}
	if elapsed > 0 {
		stats.DownloadBytesPerSec = uint64(float64(receivedDelta) / elapsed)
		stats.UploadBytesPerSec = uint64(float64(sentDelta) / elapsed)
	}
	t.previous, t.previousAt = current, now
	return stats
}

// counterDelta works modulo 2^32: the kernel reports only the low 32 bits here
// even though the field is 64-bit. Polled every second, a wrap is unambiguous.
func counterDelta(previous, current uint64) uint64 {
	return uint64(uint32(current) - uint32(previous))
}

// interfaceByteCounters reads counters straight from the kernel; this
// runs every second, so forking netstat is too expensive.
func interfaceByteCounters(device string) (byteCounters, error) {
	if !privileged.ValidTunnelDevice(device) {
		return byteCounters{}, errors.New("invalid tunnel device")
	}
	return kernelByteCounters(device)
}

func kernelByteCounters(device string) (byteCounters, error) {
	iface, err := net.InterfaceByName(device)
	if err != nil {
		return byteCounters{}, err
	}
	rib, err := syscall.RouteRIB(syscall.NET_RT_IFLIST2, iface.Index)
	if err != nil {
		return byteCounters{}, err
	}
	return parseIfList2(rib, iface.Index)
}

// Offsets into struct if_msghdr2 (<net/if.h>); if_data64 starts at 32.
const (
	rtmIfInfo2    = 0x12
	ifm2Type      = 3
	ifm2Index     = 12
	ifm2InBytes   = 32 + 64
	ifm2OutBytes  = 32 + 72
	ifm2MinLength = ifm2OutBytes + 8
)

func parseIfList2(rib []byte, index int) (byteCounters, error) {
	for len(rib) >= 2 {
		length := int(binary.NativeEndian.Uint16(rib))
		if length < 2 || length > len(rib) {
			break
		}
		message := rib[:length]
		rib = rib[length:]
		if length >= ifm2MinLength && message[ifm2Type] == rtmIfInfo2 && int(binary.NativeEndian.Uint16(message[ifm2Index:])) == index {
			return byteCounters{received: binary.NativeEndian.Uint64(message[ifm2InBytes:]), sent: binary.NativeEndian.Uint64(message[ifm2OutBytes:])}, nil
		}
	}
	return byteCounters{}, errors.New("tunnel counters not found")
}
