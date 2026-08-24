//go:build e2e

package e2e

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitTCP(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("nothing listening on %s", addr)
}

// TestE2E_L4_RedisThroughBroker proves the L4 broker: a pod runs redis-cli
// against the broker with only a handle as its password; the broker swaps in
// the real password and the real Redis answers. The pod never holds the password.
func TestE2E_L4_RedisThroughBroker(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	const realPass = "REALPASS-e2e"
	const upstreamPort = "16379"
	_ = exec.Command("podman", "rm", "-f", "poddle-redis-upstream").Run()
	// Host networking so redis binds 0.0.0.0, reachable both from the test
	// process at 127.0.0.1 and from the broker container at host.containers.internal.
	up := exec.Command("podman", "run", "-d", "--name", "poddle-redis-upstream", "--network=host",
		"docker.io/library/redis:7", "redis-server", "--requirepass", realPass, "--port", upstreamPort)
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("start upstream redis: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", "poddle-redis-upstream").Run() })
	waitTCP(t, "127.0.0.1:"+upstreamPort)

	xdg := t.TempDir()
	connDir := filepath.Join(xdg, "poddle", "connections", "cache")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"redis\"\nbase_url = \"redis://host.containers.internal:"+upstreamPort+"\"\nowner = \"local\"\n")
	writeFile(t, filepath.Join(connDir, "redis-token"), realPass)

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/redis:7\"\nconnectors = [\"cache\"]\n")

	// Isolate only the CLI config; DO NOT repoint XDG_RUNTIME_DIR — rootless
	// podman needs the real one (its own socket + the broker container's pasta
	// networking), and the shared broker container is the intended model.
	env := append(os.Environ(), "XDG_CONFIG_HOME="+xdg)

	pod := "poddle-l4-redis"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
	})

	upCmd := exec.Command(bin, "up", pod, "--detach")
	upCmd.Dir, upCmd.Env = proj, env
	if out, err := upCmd.CombinedOutput(); err != nil {
		t.Fatalf("up --detach failed: %v\n%s", err, out)
	}

	// The pod's redis-cli talks to the broker with the handle; the broker swaps
	// it for the real password. A PONG proves the whole chain worked.
	ping := exec.Command(bin, "run", pod, `redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" -a "$REDIS_PASSWORD" PING`)
	ping.Env = env
	out, err := ping.CombinedOutput()
	if err != nil {
		t.Fatalf("run redis-cli failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PONG") {
		t.Fatalf("expected PONG through the broker, got:\n%s", out)
	}

	// The pod's password is a handle, never the real one.
	pw, err := exec.Command(bin, "run", pod, `printf '%s' "$REDIS_PASSWORD"`).CombinedOutput()
	if err == nil {
		if strings.Contains(string(pw), realPass) {
			t.Errorf("the real password leaked into the pod")
		}
		if !strings.Contains(string(pw), "poddle_") {
			t.Errorf("pod password should be a handle, got %q", pw)
		}
	}

	down := exec.Command(bin, "down", pod)
	down.Env = env
	if out, err := down.CombinedOutput(); err != nil {
		t.Fatalf("down failed: %v\n%s", err, out)
	}
}

// TestE2E_L4_RedisLoopbackRewrite proves a pod can reach a datastore configured
// at loopback (redis://127.0.0.1:PORT) even though the broker is containerized.
// The containerized broker rewrites the loopback upstream to the host route
// (host.containers.internal) at dial time, so the user writes the natural
// "127.0.0.1" address instead of the container-aware "host.containers.internal".
func TestE2E_L4_RedisLoopbackRewrite(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	const realPass = "REALPASS-loopback-e2e"
	const upstreamPort = "16380"
	_ = exec.Command("podman", "rm", "-f", "poddle-redis-loopback-upstream").Run()
	// Host networking so redis binds 0.0.0.0, reachable at 127.0.0.1 on the host.
	up := exec.Command("podman", "run", "-d", "--name", "poddle-redis-loopback-upstream", "--network=host",
		"docker.io/library/redis:7", "redis-server", "--requirepass", realPass, "--port", upstreamPort)
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("start upstream redis: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", "poddle-redis-loopback-upstream").Run() })
	waitTCP(t, "127.0.0.1:"+upstreamPort)

	xdg := t.TempDir()
	connDir := filepath.Join(xdg, "poddle", "connections", "cache")
	// The KEY difference: a loopback base_url, not host.containers.internal.
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"redis\"\nbase_url = \"redis://127.0.0.1:"+upstreamPort+"\"\nowner = \"local\"\n")
	writeFile(t, filepath.Join(connDir, "redis-token"), realPass)

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/redis:7\"\nconnectors = [\"cache\"]\n")

	env := append(os.Environ(), "XDG_CONFIG_HOME="+xdg)

	pod := "poddle-l4-redis-loopback"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
	})

	upCmd := exec.Command(bin, "up", pod, "--detach")
	upCmd.Dir, upCmd.Env = proj, env
	if out, err := upCmd.CombinedOutput(); err != nil {
		t.Fatalf("up --detach failed: %v\n%s", err, out)
	}

	// A PONG proves the broker rewrote 127.0.0.1 to the host route and reached the
	// real Redis with the real password.
	ping := exec.Command(bin, "run", pod, `redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" -a "$REDIS_PASSWORD" PING`)
	ping.Env = env
	out, err := ping.CombinedOutput()
	if err != nil {
		t.Fatalf("run redis-cli failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PONG") {
		t.Fatalf("expected PONG through the broker (loopback rewrite), got:\n%s", out)
	}

	down := exec.Command(bin, "down", pod)
	down.Env = env
	if out, err := down.CombinedOutput(); err != nil {
		t.Fatalf("down failed: %v\n%s", err, out)
	}
}

// TestE2E_L4_PostgresThroughBroker proves the Postgres L4 broker end-to-end: a
// pod runs psql against the broker with only a handle; the broker performs the
// real SCRAM-SHA-256 handshake (postgres:16's default) with the real password.
func TestE2E_L4_PostgresThroughBroker(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	const realPass = "REALPGPASS-e2e"
	const pgPort = "15432"
	_ = exec.Command("podman", "rm", "-f", "poddle-pg-upstream").Run()
	// Host networking so postgres binds 0.0.0.0, reachable both from the test
	// process at 127.0.0.1 and from the broker container at host.containers.internal.
	up := exec.Command("podman", "run", "-d", "--name", "poddle-pg-upstream", "--network=host",
		"-e", "POSTGRES_PASSWORD="+realPass,
		"docker.io/library/postgres:16", "-c", "port="+pgPort)
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("start upstream postgres: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", "poddle-pg-upstream").Run() })
	waitTCP(t, "127.0.0.1:"+pgPort)

	xdg := t.TempDir()
	connDir := filepath.Join(xdg, "poddle", "connections", "db")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"postgres\"\nbase_url = \"postgres://host.containers.internal:"+pgPort+"/postgres\"\nuser = \"postgres\"\nowner = \"local\"\n")
	writeFile(t, filepath.Join(connDir, "postgres-token"), realPass)

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/postgres:16\"\nconnectors = [\"db\"]\n")

	// Isolate only the CLI config; DO NOT repoint XDG_RUNTIME_DIR — rootless
	// podman needs the real one (its own socket + the broker container's pasta
	// networking), and the shared broker container is the intended model.
	env := append(os.Environ(), "XDG_CONFIG_HOME="+xdg)

	pod := "poddle-l4-pg"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
	})

	upCmd := exec.Command(bin, "up", pod, "--detach")
	upCmd.Dir, upCmd.Env = proj, env
	if out, err := upCmd.CombinedOutput(); err != nil {
		t.Fatalf("up --detach failed: %v\n%s", err, out)
	}

	// psql uses PG* env (handle as PGPASSWORD). Retry while postgres warms up.
	var out []byte
	var qerr error
	for i := 0; i < 12; i++ {
		q := exec.Command(bin, "run", pod, `psql -tAc "SELECT 42"`)
		q.Env = env
		out, qerr = q.CombinedOutput()
		if qerr == nil && strings.Contains(string(out), "42") {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if qerr != nil || !strings.Contains(string(out), "42") {
		t.Fatalf("psql SELECT through the broker failed: %v\n%s", qerr, out)
	}

	// The pod's password is a handle, never the real one.
	pw, err := exec.Command(bin, "run", pod, `printf '%s' "$PGPASSWORD"`).CombinedOutput()
	if err == nil {
		if strings.Contains(string(pw), realPass) {
			t.Errorf("the real password leaked into the pod")
		}
		if !strings.Contains(string(pw), "poddle_") {
			t.Errorf("pod password should be a handle, got %q", pw)
		}
	}

	down := exec.Command(bin, "down", pod)
	down.Env = env
	if out, err := down.CombinedOutput(); err != nil {
		t.Fatalf("down failed: %v\n%s", err, out)
	}
}
