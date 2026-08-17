package up

import "testing"

func TestPodBrokerHost(t *testing.T) {
	t.Setenv("PODDLE_BROKER_ADDR", "")
	if got := podBrokerHost(); got != "host.containers.internal" {
		t.Errorf("default = %q, want host.containers.internal", got)
	}
	t.Setenv("PODDLE_BROKER_ADDR", "192.168.1.50")
	if got := podBrokerHost(); got != "192.168.1.50" {
		t.Errorf("override = %q, want 192.168.1.50", got)
	}
}
