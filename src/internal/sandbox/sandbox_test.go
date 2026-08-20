package sandbox

import "testing"

func TestSpec_NetworkZeroValue(t *testing.T) {
	var s Spec
	if s.Network != nil {
		t.Error("Network should default nil (open egress unless locked)")
	}
}
