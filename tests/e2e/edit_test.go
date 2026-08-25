//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// editMarker is the content the agent is driven to write into the file — a
// distinctive token so the assertion proves the agent actually applied the edit.
const editMarker = "PODDLE_EDIT_OK"

// editFile is the file the agent is driven to create in the pod workspace.
const editFile = "poddle_edit.txt"

// startEditMock starts a scripted OpenAI chat-completions mock on 0.0.0.0 (so the
// broker container reaches it at host.containers.internal). It records every
// request's Authorization header (for the secretless assertion) and serves
// `editReply` on the FIRST POST and `followReply` on every subsequent POST — the
// shape most CLI agents need: one edit turn, then follow-up/commit-message turns
// that must not re-trigger an edit. Both are returned as plain-JSON chat.completion
// objects (agents here run with streaming off).
func startEditMock(t *testing.T, auths *[]string, mu *sync.Mutex, editReply, followReply string) *httptest.Server {
	t.Helper()
	var calls int
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		calls++
		first := calls == 1
		mu.Unlock()
		content := followReply
		if first {
			content = editReply
		}
		writeChatCompletion(w, content)
	}))
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind 0.0.0.0: %v", err)
	}
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// writeChatCompletion writes a minimal OpenAI chat.completion JSON with the given
// assistant content. json.Marshal handles escaping the SEARCH/REPLACE payload.
func writeChatCompletion(w http.ResponseWriter, content string) {
	resp := map[string]any{
		"id": "chatcmpl-mock", "object": "chat.completion", "created": 1, "model": "gpt-4o",
		"choices": []any{map[string]any{
			"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": content},
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// mockURLFor returns the host.containers.internal URL the broker container uses to
// reach a mock bound on 0.0.0.0 (its 127.0.0.1 is its own loopback).
func mockURLFor(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("mock addr: %v", err)
	}
	return "http://host.containers.internal:" + port
}

// writeOpenAIIdentity writes a sentinel openai identity under cfg (XDG_CONFIG_HOME)
// so the broker holds `sentinel` and the pod only ever gets a handle.
func writeOpenAIIdentity(t *testing.T, cfg, sentinel string) {
	t.Helper()
	idDir := filepath.Join(cfg, "poddle", "identities", "work")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(idDir, "meta.toml"), "name = \"work\"\nprovider = \"openai\"\n")
	writeFile(t, filepath.Join(idDir, "openai-token"), sentinel)
}

// assertFileInPod fails unless some file named editFile in the pod contains the
// edit marker. `find` sidesteps the agent's working-directory ambiguity.
func assertFileInPod(t *testing.T, pod string) {
	t.Helper()
	out, err := exec.Command("podman", "exec", pod, "sh", "-c",
		`cat "$(find / -name `+editFile+` 2>/dev/null | head -1)" 2>/dev/null`).CombinedOutput()
	if err != nil {
		t.Fatalf("read %s from pod: %v\n%s", editFile, err, out)
	}
	if !strings.Contains(string(out), editMarker) {
		t.Fatalf("%s in pod did not contain %q (agent did not apply the edit); got:\n%s", editFile, editMarker, out)
	}
}

// assertSecretless fails if the upstream ever saw the handle, or never saw the
// real (sentinel) secret — the broker must have swapped handle→secret on the wire.
func assertSecretless(t *testing.T, auths []string, sentinel string, mu *sync.Mutex) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	sawSecret := false
	for _, a := range auths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("the handle leaked to the upstream: %q", a)
		}
		if a == "Bearer "+sentinel {
			sawSecret = true
		}
	}
	if !sawSecret {
		t.Errorf("upstream never saw the sentinel secret; got %v", auths)
	}
}

// aiderEditReply is the turn-1 assistant content that drives aider to CREATE
// editFile with editMarker: filename alone on its own line, a bare ``` fence, and
// an EMPTY SEARCH section (aider's new-file signal). Captured verbatim from the
// aider edit spike (aider 0.86.2, gpt-4o → diff edit format).
const aiderEditReply = "I'll create " + editFile + " with the requested content.\n\n" +
	editFile + "\n```\n<<<<<<< SEARCH\n=======\n" + editMarker + "\n>>>>>>> REPLACE\n```\n"

// TestE2E_Edit_Aider proves aider does REAL work through the broker: a fresh
// secretless pod installs aider, which — driven by a scripted mock upstream —
// applies a SEARCH/REPLACE edit that creates a file, all while the pod holds only
// a handle and the upstream sees only the real (sentinel) key.
func TestE2E_Edit_Aider(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	const sentinel = "SENTINEL-EDIT-AIDER"
	var mu sync.Mutex
	var auths []string
	mock := startEditMock(t, &auths, &mu, aiderEditReply, "Done. Created "+editFile+".")
	mockURL := mockURLFor(t, mock)

	cfg := t.TempDir()
	writeOpenAIIdentity(t, cfg, sentinel)
	env := append(os.Environ(), "XDG_CONFIG_HOME="+cfg, "PODDLE_OPENAI_BASE_URL="+mockURL)

	pod := "poddle-edit-aider"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		down := exec.Command(bin, "down", pod)
		down.Env = env
		_ = down.Run()
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
		_ = exec.Command("podman", "network", "rm", "poddle-lock-"+pod).Run()
	})

	// Bring the pod up (installs aider via the harness Provisions) and keep it.
	up := exec.Command(bin, "up", pod, "--detach",
		"--identity", "work", "--harness", "aider",
		"--image", "docker.io/library/python:3.12")
	up.Env = env
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("up --detach failed: %v\n%s", err, out)
	}

	// Run aider headless in the workspace to create the file (through the broker).
	// `poddle run` wraps the command in `sh -c`.
	runCmd := exec.Command(bin, "run", pod,
		"cd /workspace && aider --message 'create "+editFile+" containing "+editMarker+"' "+
			"--yes-always --no-stream --no-pretty --no-check-update --no-analytics --model gpt-4o")
	runCmd.Env = env
	if out, err := runCmd.CombinedOutput(); err != nil {
		t.Fatalf("aider run failed: %v\n%s", err, out)
	}

	assertFileInPod(t, pod)
	assertSecretless(t, auths, sentinel, &mu)
}

// sseEvent formats one Responses-API SSE event (used by the codex edit mock).
func sseEvent(event string, data map[string]any) string {
	b, _ := json.Marshal(data)
	return "event: " + event + "\ndata: " + string(b) + "\n\n"
}

// codexEditSSE builds codex's two-turn Responses-API conversation (captured from
// the codex edit spike): turn 1 is a `custom_tool_call` named `exec` whose freeform
// JS input calls tools.exec_command with a POSIX shell command that creates the
// file; turn 2 completes with a plain assistant message so codex stops. Returns the
// turn-1 and turn-2 SSE payloads.
func codexEditSSE() (turn1, turn2 string) {
	// POSIX command (the pod is Linux); the spike's PowerShell Set-Content is
	// host-specific. echo adds a trailing newline — the assertion uses Contains.
	js := `const r = await tools.exec_command({cmd: "echo ` + editMarker + ` > ` + editFile + `"});` + "\n" + `text(JSON.stringify(r));`
	tool := map[string]any{
		"id": "ctc_1", "type": "custom_tool_call", "status": "completed",
		"call_id": "call_poddle_edit_1", "name": "exec", "input": js,
	}
	turn1 = sseEvent("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": tool}) +
		sseEvent("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{
			"id": "resp_1", "object": "response", "status": "completed", "output": []any{tool},
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
		}})
	msg := map[string]any{
		"id": "msg_2", "type": "message", "role": "assistant", "status": "completed",
		"content": []any{map[string]any{"type": "output_text", "text": "poddle-edit-done", "annotations": []any{}}},
	}
	turn2 = sseEvent("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": msg}) +
		sseEvent("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{
			"id": "resp_2", "object": "response", "status": "completed", "output": []any{msg},
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
		}})
	return turn1, turn2
}

// startCodexEditMock serves codex's Responses-API SSE: the exec tool-call on the
// first POST, the completion on later POSTs (a pure request counter suffices — the
// mock never parses codex's tool-result). Records auth headers; binds 0.0.0.0.
func startCodexEditMock(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	turn1, turn2 := codexEditSSE()
	var calls int
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		calls++
		body := turn2
		if calls == 1 {
			body = turn1
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind 0.0.0.0: %v", err)
	}
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// TestE2E_Edit_Codex proves codex does REAL work through the broker: a scripted
// Responses-API mock drives codex to run a shell command (via its `exec` custom
// tool) that creates a file — while the pod holds only a handle and the upstream
// sees only the real (sentinel) key. Exercises the codex harness's autonomy flags
// (--dangerously-bypass-approvals-and-sandbox) end to end.
func TestE2E_Edit_Codex(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	const sentinel = "SENTINEL-EDIT-CODEX"
	var mu sync.Mutex
	var auths []string
	mock := startCodexEditMock(t, &auths, &mu)
	mockURL := mockURLFor(t, mock)

	cfg := t.TempDir()
	writeOpenAIIdentity(t, cfg, sentinel)
	env := append(os.Environ(), "XDG_CONFIG_HOME="+cfg, "PODDLE_OPENAI_BASE_URL="+mockURL)

	pod := "poddle-edit-codex"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		down := exec.Command(bin, "down", pod)
		down.Env = env
		_ = down.Run()
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
		_ = exec.Command("podman", "network", "rm", "poddle-lock-"+pod).Run()
	})

	// Bring the pod up (installs codex; the provider rides -c flags, no config.toml).
	up := exec.Command(bin, "up", pod, "--detach",
		"--identity", "work", "--harness", "codex",
		"--image", "docker.io/library/node:22")
	up.Env = env
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("up --detach failed: %v\n%s", err, out)
	}

	// Run codex headless in the workspace to create the file (through the broker).
	// The bypass/skip flags mirror the codex harness TaskCommand so it runs
	// autonomously in the (isolated) pod.
	runCmd := exec.Command(bin, "run", pod,
		"cd /workspace && codex exec --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check "+
			`-c 'model_provider="poddle"' `+
			`-c 'model_providers.poddle.name="poddle"' `+
			`-c model_providers.poddle.base_url="\"$PODDLE_CODEX_BASE_URL\"" `+
			`-c 'model_providers.poddle.env_key="OPENAI_API_KEY"' `+
			`-c 'model_providers.poddle.wire_api="responses"' `+
			"'create a file "+editFile+" containing "+editMarker+"'")
	runCmd.Env = env
	if out, err := runCmd.CombinedOutput(); err != nil {
		t.Fatalf("codex run failed: %v\n%s", err, out)
	}

	assertFileInPod(t, pod)
	assertSecretless(t, auths, sentinel, &mu)
}

// piChunk formats one OpenAI chat.completion.chunk SSE data line.
func piChunk(delta map[string]any, finish any) string {
	obj := map[string]any{
		"id": "c", "object": "chat.completion.chunk", "created": 1, "model": "poddle-model",
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	}
	b, _ := json.Marshal(obj)
	return "data: " + string(b) + "\n\n"
}

// piEditSSE builds pi's two-turn chat-completions conversation (captured from the
// pi edit spike): turn 1 streams a `bash` tool_call whose arguments create the file;
// turn 2 is a plain text completion. pi requests stream:true, so both are SSE.
func piEditSSE() (turn1, turn2 string) {
	argsJSON := `{"command":"printf 'PODDLE_EDIT_OK' > ` + editFile + `"}`
	turn1 = piChunk(map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
		map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "bash", "arguments": ""}},
	}}, nil) +
		piChunk(map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "function": map[string]any{"arguments": argsJSON}},
		}}, nil) +
		piChunk(map[string]any{}, "tool_calls") +
		"data: [DONE]\n\n"
	turn2 = piChunk(map[string]any{"role": "assistant", "content": ""}, nil) +
		piChunk(map[string]any{"content": editMarker + " done"}, nil) +
		piChunk(map[string]any{}, "stop") +
		"data: [DONE]\n\n"
	return turn1, turn2
}

// startPiEditMock serves pi's chat-completions SSE: the bash tool_call on the first
// POST, the completion on later POSTs (a pure request counter suffices — the mock
// never parses pi's tool-result). Records auth headers; binds 0.0.0.0.
func startPiEditMock(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	turn1, turn2 := piEditSSE()
	var calls int
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		calls++
		body := turn2
		if calls == 1 {
			body = turn1
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind 0.0.0.0: %v", err)
	}
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// TestE2E_Edit_Pi proves the pi coding agent does REAL work through the broker: a
// scripted chat-completions SSE mock drives pi to run its `bash` tool to create a
// file, secretlessly (the pod holds only a handle; the upstream sees only the
// sentinel). pi reuses the `openai` provider and is configured via a models.json
// the harness writes.
func TestE2E_Edit_Pi(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	const sentinel = "SENTINEL-EDIT-PI"
	var mu sync.Mutex
	var auths []string
	mock := startPiEditMock(t, &auths, &mu)
	mockURL := mockURLFor(t, mock)

	cfg := t.TempDir()
	writeOpenAIIdentity(t, cfg, sentinel)
	env := append(os.Environ(), "XDG_CONFIG_HOME="+cfg, "PODDLE_OPENAI_BASE_URL="+mockURL)

	pod := "poddle-edit-pi"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		down := exec.Command(bin, "down", pod)
		down.Env = env
		_ = down.Run()
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
		_ = exec.Command("podman", "network", "rm", "poddle-lock-"+pod).Run()
	})

	// Bring the pod up (installs pi + writes its models.json via Provisions).
	up := exec.Command(bin, "up", pod, "--detach",
		"--identity", "work", "--harness", "pi",
		"--image", "docker.io/library/node:22")
	up.Env = env
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("up --detach failed: %v\n%s", err, out)
	}

	// Run pi headless in the workspace to create the file (through the broker).
	runCmd := exec.Command(bin, "run", pod,
		"cd /workspace && pi --provider poddle --model poddle-model -p "+
			"'create "+editFile+" containing "+editMarker+" using a shell command'")
	runCmd.Env = env
	if out, err := runCmd.CombinedOutput(); err != nil {
		t.Fatalf("pi run failed: %v\n%s", err, out)
	}

	assertFileInPod(t, pod)
	assertSecretless(t, auths, sentinel, &mu)
}

// writeAnthropicIdentity writes a sentinel anthropic identity (claude-code's
// provider) so the broker holds `sentinel` and the pod only gets a handle.
func writeAnthropicIdentity(t *testing.T, cfg, sentinel string) {
	t.Helper()
	idDir := filepath.Join(cfg, "poddle", "identities", "work")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(idDir, "meta.toml"), "name = \"work\"\nprovider = \"anthropic\"\n")
	writeFile(t, filepath.Join(idDir, "anthropic-token"), sentinel)
}

// jsonQuote returns s as a JSON string literal (quoted + escaped).
func jsonQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

// startClaudeEditMock serves the Anthropic Messages API (SSE) that the broker
// forwards from claude-code: a `Bash` tool_use on the first POST /v1/messages, an
// end_turn text reply on later ones; count_tokens is stubbed and never counted.
// (claude-code's unauthenticated HEAD / connectivity probe never reaches the mock
// — the broker gateway absorbs it, so only the forwarded, handle-bearing POSTs
// arrive here.) Records auth headers; binds 0.0.0.0.
func startClaudeEditMock(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	var turns int
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "count_tokens") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"input_tokens":1}`))
			return
		}
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/v1/messages") {
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		turns++
		turn := turns
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		fl, _ := w.(http.Flusher)
		ev := func(event, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			if fl != nil {
				fl.Flush()
			}
		}
		if turn == 1 {
			claudeBashToolUse(ev)
		} else {
			claudeEndTurnText(ev, "poddle-edit-done")
		}
	}))
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind 0.0.0.0: %v", err)
	}
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// claudeBashToolUse streams one assistant turn with a `Bash` tool_use that runs a
// POSIX command creating the file (shape + tool captured from the claude-code edit
// spike). The input JSON assembles from a single input_json_delta chunk.
func claudeBashToolUse(ev func(string, string)) {
	inputJSON := `{"command":` + jsonQuote("echo "+editMarker+" > "+editFile) + `}`
	ev("message_start", `{"type":"message_start","message":{"id":"m2","type":"message","role":"assistant","model":"claude-opus-4-8","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":20,"output_tokens":1}}}`)
	ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"On it."}}`)
	ev("content_block_stop", `{"type":"content_block_stop","index":0}`)
	ev("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_poddle_edit","name":"Bash","input":{}}}`)
	ev("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":`+jsonQuote(inputJSON)+`}}`)
	ev("content_block_stop", `{"type":"content_block_stop","index":1}`)
	ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":30}}`)
	ev("message_stop", `{"type":"message_stop"}`)
}

// claudeEndTurnText streams a plain-text end_turn assistant reply (baseline shape).
func claudeEndTurnText(ev func(string, string), text string) {
	ev("message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-opus-4-8","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`)
	ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+jsonQuote(text)+`}}`)
	ev("content_block_stop", `{"type":"content_block_stop","index":0}`)
	ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`)
	ev("message_stop", `{"type":"message_stop"}`)
}

// TestE2E_Edit_ClaudeCode proves claude-code does REAL work through the broker: a
// scripted Messages-API mock drives it to run its Bash tool to create a file,
// secretlessly (the pod holds only a handle; the upstream sees only the sentinel).
func TestE2E_Edit_ClaudeCode(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	const sentinel = "SENTINEL-EDIT-CLAUDE"
	var mu sync.Mutex
	var auths []string
	mock := startClaudeEditMock(t, &auths, &mu)
	mockURL := mockURLFor(t, mock)

	cfg := t.TempDir()
	writeAnthropicIdentity(t, cfg, sentinel)
	env := append(os.Environ(), "XDG_CONFIG_HOME="+cfg, "PODDLE_ANTHROPIC_BASE_URL="+mockURL)

	pod := "poddle-edit-claude"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		down := exec.Command(bin, "down", pod)
		down.Env = env
		_ = down.Run()
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
		_ = exec.Command("podman", "network", "rm", "poddle-lock-"+pod).Run()
	})

	up := exec.Command(bin, "up", pod, "--detach",
		"--identity", "work", "--harness", "claude-code",
		"--image", "docker.io/library/node:22")
	up.Env = env
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("up --detach failed: %v\n%s", err, out)
	}

	// Run claude-code headless in the workspace to create the file (through the
	// broker), using the harness's verified non-interactive recipe.
	runCmd := exec.Command(bin, "run", pod,
		"cd /workspace && export IS_SANDBOX=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1; "+
			`echo '{"hasCompletedOnboarding":true}' > $HOME/.claude.json; `+
			"claude -p 'create a file "+editFile+" containing "+editMarker+"' "+
			"--dangerously-skip-permissions --max-turns 5 --output-format json </dev/null")
	runCmd.Env = env
	if out, err := runCmd.CombinedOutput(); err != nil {
		t.Fatalf("claude run failed: %v\n%s", err, out)
	}

	assertFileInPod(t, pod)
	assertSecretless(t, auths, sentinel, &mu)
}

// opencodeEditSSE builds opencode's two-turn chat-completions conversation
// (captured from the opencode edit spike): turn 1 streams a `bash` tool_call whose
// arguments create the file; turn 2 is a plain text completion. Reuses piChunk
// (opencode also speaks streaming chat-completion-chunks).
func opencodeEditSSE() (toolCall, text string) {
	argsJSON := `{"command":"printf 'PODDLE_EDIT_OK' > ` + editFile + `","description":"create the marker file"}`
	toolCall = piChunk(map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
		map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "bash", "arguments": ""}},
	}}, nil) +
		piChunk(map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "function": map[string]any{"arguments": argsJSON}},
		}}, nil) +
		piChunk(map[string]any{}, "tool_calls") +
		"data: [DONE]\n\n"
	text = piChunk(map[string]any{"role": "assistant", "content": ""}, nil) +
		piChunk(map[string]any{"content": editMarker + " done"}, nil) +
		piChunk(map[string]any{}, "stop") +
		"data: [DONE]\n\n"
	return toolCall, text
}

// startOpencodeEditMock serves opencode's chat-completions SSE. opencode fires a
// title-generation call (no tools) before the main turn, and re-POSTs after running
// the tool, so a pure request counter is unreliable. Instead: return the `bash`
// tool_call for the first request that carries the bash tool DEFINITION and has no
// tool RESULT yet (the main turn); everything else (title-gen, the post-tool turn)
// gets a plain text reply. Records auth headers; binds 0.0.0.0.
func startOpencodeEditMock(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	toolCall, text := opencodeEditSSE()
	var fired bool
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		useTool := strings.Contains(s, `"name":"bash"`) && !strings.Contains(s, `"role":"tool"`) && !fired
		if useTool {
			fired = true
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if useTool {
			_, _ = w.Write([]byte(toolCall))
		} else {
			_, _ = w.Write([]byte(text))
		}
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind 0.0.0.0: %v", err)
	}
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// TestE2E_Edit_Opencode proves the opencode agent does REAL work through the
// broker: a scripted chat-completions SSE mock drives opencode to run its `bash`
// tool to create a file, secretlessly. opencode reuses the `openai` provider and is
// configured via an opencode.json the harness writes.
func TestE2E_Edit_Opencode(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	const sentinel = "SENTINEL-EDIT-OPENCODE"
	var mu sync.Mutex
	var auths []string
	mock := startOpencodeEditMock(t, &auths, &mu)
	mockURL := mockURLFor(t, mock)

	cfg := t.TempDir()
	writeOpenAIIdentity(t, cfg, sentinel)
	env := append(os.Environ(), "XDG_CONFIG_HOME="+cfg, "PODDLE_OPENAI_BASE_URL="+mockURL)

	pod := "poddle-edit-opencode"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		down := exec.Command(bin, "down", pod)
		down.Env = env
		_ = down.Run()
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
		_ = exec.Command("podman", "network", "rm", "poddle-lock-"+pod).Run()
	})

	// Bring the pod up (installs opencode + writes its opencode.json via Provisions).
	up := exec.Command(bin, "up", pod, "--detach",
		"--identity", "work", "--harness", "opencode",
		"--image", "docker.io/library/node:22")
	up.Env = env
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("up --detach failed: %v\n%s", err, out)
	}

	// Run opencode headless in the workspace to create the file (through the broker).
	runCmd := exec.Command(bin, "run", pod,
		"cd /workspace && opencode run 'create "+editFile+" containing "+editMarker+
			" using a shell command' -m poddle/poddle-model --format json --auto")
	runCmd.Env = env
	if out, err := runCmd.CombinedOutput(); err != nil {
		t.Fatalf("opencode run failed: %v\n%s", err, out)
	}

	assertFileInPod(t, pod)
	assertSecretless(t, auths, sentinel, &mu)
}
