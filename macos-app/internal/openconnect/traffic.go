package openconnect

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
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
		return TrafficStats{}
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

func counterDelta(previous, current uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}

func interfaceByteCounters(device string) (byteCounters, error) {
	if !strings.HasPrefix(device, "utun") {
		return byteCounters{}, errors.New("invalid tunnel device")
	}
	output, err := exec.Command("/usr/sbin/netstat", "-b", "-I", device).Output()
	if err != nil {
		return byteCounters{}, err
	}
	return parseNetstatByteCounters(string(output), device)
}

func parseNetstatByteCounters(output, device string) (byteCounters, error) {
	headerSeen := false
	var result byteCounters
	found := false
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if !headerSeen {
			headerSeen = fields[0] == "Name" && slicesContain(fields, "Ibytes") && slicesContain(fields, "Obytes")
			continue
		}
		if len(fields) < 7 || fields[0] != device {
			continue
		}
		// The Network/Address columns vary by row, while the seven counter
		// columns at the end are stable: Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll.
		received, receivedErr := strconv.ParseUint(fields[len(fields)-5], 10, 64)
		sent, sentErr := strconv.ParseUint(fields[len(fields)-2], 10, 64)
		if receivedErr != nil || sentErr != nil {
			continue
		}
		// netstat can print the same interface counters for multiple address
		// families. Taking the maximum avoids counting those rows twice.
		result.received = max(result.received, received)
		result.sent = max(result.sent, sent)
		found = true
	}
	if !found {
		return byteCounters{}, errors.New("tunnel counters not found")
	}
	return result, nil
}

func slicesContain(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
