package vpnserver

import (
	"net"
	"testing"
)

func TestRuntimeTracksClientTraffic(t *testing.T) {
	rt := NewRuntime(RuntimeConfig{
		ListenAddr: ":1562",
		Subnet:     "10.0.8.0/24",
		NATIface:   "eth0",
		NATEnabled: true,
		Padding:    true,
	})
	id := rt.RegisterClient(&net.UDPAddr{IP: net.ParseIP("203.0.113.10"), Port: 44321})

	rt.AddClientTraffic(id, 128, 256)
	rt.AddClientTraffic(id, 64, 32)

	snapshot := rt.Snapshot()
	if snapshot.ListenAddr != ":1562" || snapshot.Subnet != "10.0.8.0/24" {
		t.Fatalf("snapshot config = %#v", snapshot)
	}
	if snapshot.TotalBytesIn != 192 || snapshot.TotalBytesOut != 288 {
		t.Fatalf("totals = in %d out %d", snapshot.TotalBytesIn, snapshot.TotalBytesOut)
	}
	if len(snapshot.Clients) != 1 {
		t.Fatalf("clients = %d", len(snapshot.Clients))
	}
	client := snapshot.Clients[0]
	if client.ID != id || client.RemoteAddr != "203.0.113.10:44321" {
		t.Fatalf("client = %#v", client)
	}
	if client.BytesIn != 192 || client.BytesOut != 288 {
		t.Fatalf("client bytes = in %d out %d", client.BytesIn, client.BytesOut)
	}
}

func TestRuntimeUnregistersClient(t *testing.T) {
	rt := NewRuntime(RuntimeConfig{})
	id := rt.RegisterClient(&net.UDPAddr{IP: net.ParseIP("203.0.113.10"), Port: 44321})

	rt.UnregisterClient(id)

	snapshot := rt.Snapshot()
	if snapshot.ActiveClients != 0 {
		t.Fatalf("active clients = %d", snapshot.ActiveClients)
	}
	if len(snapshot.Clients) != 0 {
		t.Fatalf("clients = %#v", snapshot.Clients)
	}
}
