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
