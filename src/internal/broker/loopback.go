package broker

import "net"

// RewriteLoopbackHost rewrites a loopback upstream host to loopbackHost,
// preserving the port. A containerized broker uses it so a pod's "127.0.0.1:5432"
// (or "localhost", "::1", any 127.0.0.0/8 address) upstream reaches the *host's*
// loopback — e.g. host.containers.internal — instead of the broker container's
// own loopback, which holds nothing.
//
// It is applied at DIAL time, after the policy check, so governance and audit
// still see the pod-configured host. loopbackHost == "" disables the rewrite
// (a bare-host broker and the in-process tests, where loopback already means the
// host). A non-loopback host is returned unchanged.
func RewriteLoopbackHost(hostport, loopbackHost string) string {
	if loopbackHost == "" {
		return hostport
	}
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		// No port (a bare "127.0.0.1", "localhost", or "::1"): treat the whole
		// string as the host.
		host, port = hostport, ""
	}
	if !isLoopbackHost(host) {
		return hostport
	}
	if port == "" {
		return loopbackHost
	}
	return net.JoinHostPort(loopbackHost, port)
}

// isLoopbackHost reports whether host names the loopback interface: the literal
// "localhost", or any IP in 127.0.0.0/8 or ::1.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
