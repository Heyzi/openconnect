package openconnect

import (
	"testing"
	"time"
)

func TestParseNetstatByteCounters(t *testing.T) {
	output := `Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll
	utun9 1380 <Link#31> 123 0 1000 80 0 700 0
`
	got, err := parseNetstatByteCounters(output, "utun9")
	if err != nil {
		t.Fatal(err)
	}
	if got.received != 1000 || got.sent != 700 {
		t.Fatalf("counters = %#v", got)
	}
}

func TestTrafficTrackerReportsSessionTotalsAndRates(t *testing.T) {
	start := time.Unix(100, 0)
	tracker := trafficTracker{}
	if got := tracker.sample(byteCounters{received: 1000, sent: 500}, start); got != (TrafficStats{}) {
		t.Fatalf("baseline = %#v", got)
	}
	got := tracker.sample(byteCounters{received: 5000, sent: 1500}, start.Add(2*time.Second))
	want := TrafficStats{DownloadBytes: 4000, UploadBytes: 1000, DownloadBytesPerSec: 2000, UploadBytesPerSec: 500}
	if got != want {
		t.Fatalf("stats = %#v, want %#v", got, want)
	}
}
