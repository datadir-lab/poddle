// Package brokerendpoint resolves, for a broker placement, the pod-facing
// address of each egress channel and the exact allow-list the network lockdown
// pins to. Data plane only — no control-plane verbs.
package brokerendpoint

import (
	"fmt"
	"net"
)

type Channel int

const (
	Gateway Channel = iota
	Forward
	Redis
	Postgres
)

type HostPort struct{ Host, Port string }

type Endpoint interface {
	Addr(Channel) (string, error)
	AllowList() []HostPort
}

type colocated struct {
	podHost string
	local   map[Channel]string
}

// NewColocated maps each local broker channel address ("host:port") to the pod's
// host route (podHost). A channel absent from local yields ("", error) from Addr
// and is omitted from AllowList.
func NewColocated(podHost string, local map[Channel]string) Endpoint {
	return &colocated{podHost: podHost, local: local}
}

func (c *colocated) Addr(ch Channel) (string, error) {
	addr, ok := c.local[ch]
	if !ok {
		return "", fmt.Errorf("brokerendpoint: no local address for channel %d", ch)
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("brokerendpoint: bad local address %q: %w", addr, err)
	}
	return net.JoinHostPort(c.podHost, port), nil
}

func (c *colocated) AllowList() []HostPort {
	var out []HostPort
	for ch := Gateway; ch <= Postgres; ch++ {
		if addr, ok := c.local[ch]; ok {
			if _, port, err := net.SplitHostPort(addr); err == nil {
				out = append(out, HostPort{c.podHost, port})
			}
		}
	}
	return out
}
