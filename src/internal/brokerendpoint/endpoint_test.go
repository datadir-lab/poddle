package brokerendpoint

import "testing"

func TestColocated_Addr(t *testing.T) {
	ep := NewColocated("host.containers.internal", map[Channel]string{
		Redis: "127.0.0.1:16379", Gateway: "127.0.0.1:12345",
	})
	if got, err := ep.Addr(Redis); err != nil || got != "host.containers.internal:16379" {
		t.Fatalf("Redis Addr = %q, %v", got, err)
	}
	if _, err := ep.Addr(Postgres); err == nil {
		t.Error("absent channel should error")
	}
}

func TestColocated_AllowList(t *testing.T) {
	ep := NewColocated("host.containers.internal", map[Channel]string{Redis: "127.0.0.1:16379"})
	al := ep.AllowList()
	if len(al) != 1 || al[0] != (HostPort{"host.containers.internal", "16379"}) {
		t.Errorf("AllowList = %+v", al)
	}
}

func TestPeer_Addr(t *testing.T) {
	ep := NewPeer("10.89.1.2", map[Channel]string{Redis: "16379", Gateway: "12345"})
	if got, err := ep.Addr(Redis); err != nil || got != "10.89.1.2:16379" {
		t.Fatalf("Redis Addr = %q, %v", got, err)
	}
	if _, err := ep.Addr(Postgres); err == nil {
		t.Error("absent channel should error")
	}
}

func TestPeer_AllowList(t *testing.T) {
	ep := NewPeer("10.89.1.2", map[Channel]string{Redis: "16379", Gateway: "12345"})
	al := ep.AllowList()
	if len(al) != 2 {
		t.Fatalf("AllowList len = %d, want 2: %+v", len(al), al)
	}
	// every entry pins to the broker IP
	for _, hp := range al {
		if hp.Host != "10.89.1.2" {
			t.Errorf("allow entry host = %q, want broker IP", hp.Host)
		}
	}
}
