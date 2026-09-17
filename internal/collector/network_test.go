package collector

import (
	"context"
	"testing"
	"time"
)

func TestNetworkCollectorEmitsOnlyNetworkInterfaceChanges(t *testing.T) {
	call := 0
	snapshot := func() ([]NetworkInterface, error) {
		call++
		if call == 1 {
			return []NetworkInterface{{Index: 1, Name: "Ethernet", Addresses: []string{"10.0.0.1"}}}, nil
		}
		return []NetworkInterface{{Index: 1, Name: "Ethernet", Addresses: []string{"10.0.0.2"}}}, nil
	}
	collector, err := newNetworkCollector(time.Millisecond, snapshot, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sink := &observationSink{onEmit: func(count int) { cancel() }}
	if err := collector.Run(ctx, sink); err != context.Canceled {
		t.Fatalf("Run() error = %v", err)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].Action != "network_interface_changed" ||
		observations[0].Source != "windows_network_snapshot" {
		t.Fatalf("network observations = %#v", observations)
	}
}
