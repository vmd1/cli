package agent

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmd1/cli/internal/cli"
)

const statusJSON = `{
  "active": "wireguard", "active_all": ["wireguard", "tailscale-wss"], "selected": "tailscale-wss", "preferred": "tailscale-wss",
  "pathways": {
    "tailscale": {"state": "failed", "error": "precheck failed (network still blocks it)", "retry_in_s": 120, "tunnel": null},
    "wireguard": {"state": "active", "error": null, "rtt_ms": 18.2, "tunnel": null},
    "tailscale-wss": {"state": "active", "error": null, "rtt_ms": 42.4, "tunnel": "wss://coordination.vmd1.dev"}
  }
}`

type fakeAgent struct{ picked string }

func (f *fakeAgent) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET":
			io.WriteString(w, statusJSON)
		case r.Method == "POST" && r.URL.Path == "/path":
			b, _ := io.ReadAll(r.Body)
			if strings.Contains(string(b), "nope") {
				w.WriteHeader(409)
				io.WriteString(w, `{"ok": false, "error": "no pathway named 'nope'"}`)
				return
			}
			f.picked = string(b)
			io.WriteString(w, `{"ok": true, "selected": "wireguard"}`)
		}
	})
}

func run(t *testing.T, m cli.Module, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	root := &cli.Root{Name: "vmd1", Modules: []cli.Module{m}}
	var o, e bytes.Buffer
	code := root.Run(context.Background(), &cli.Env{Out: &o, Err: &e, Getenv: func(k string) string { return env[k] }}, append([]string{"agent"}, args...))
	return code, o.String(), e.String()
}

func TestPathListKeepsAgentOrderAndMarksSelected(t *testing.T) {
	srv := httptest.NewServer((&fakeAgent{}).handler())
	defer srv.Close()
	code, out, errs := run(t, Module(nil, nil), map[string]string{"VMD1_AGENT_URL": srv.URL}, "path", "list")
	if code != 0 {
		t.Fatal(code, errs)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.HasPrefix(lines[1], "tailscale ") || !strings.HasPrefix(lines[2], "wireguard ") || !strings.HasPrefix(lines[3], "tailscale-wss ") {
		t.Fatalf("order lost:\n%s", out)
	}
	if !strings.Contains(lines[3], "selected") || !strings.Contains(lines[2], "standby") || !strings.Contains(lines[3], "42ms") ||
		!strings.Contains(lines[1], "retry in 120s") || !strings.Contains(lines[3], "wss://coordination.vmd1.dev") {
		t.Fatalf("columns wrong:\n%s", out)
	}
}

func TestPathListJSONAndUnreachableAgent(t *testing.T) {
	srv := httptest.NewServer((&fakeAgent{}).handler())
	code, out, _ := run(t, Module(nil, nil), map[string]string{"VMD1_AGENT_URL": srv.URL}, "path", "list", "--json")
	if code != 0 || !strings.Contains(out, `"selected": "tailscale-wss"`) {
		t.Fatal(code, out)
	}
	srv.Close()
	code, _, errs := run(t, Module(nil, nil), map[string]string{"VMD1_AGENT_URL": srv.URL}, "path", "list")
	if code != 1 || !strings.Contains(errs, "vmd1 agent up") {
		t.Fatal(code, errs)
	}
}

func TestPathPick(t *testing.T) {
	f := &fakeAgent{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	env := map[string]string{"VMD1_AGENT_URL": srv.URL}
	code, out, _ := run(t, Module(nil, nil), env, "path", "pick", "wireguard")
	if code != 0 || out != "new connections now use wireguard\n" || !strings.Contains(f.picked, `"name":"wireguard"`) {
		t.Fatal(code, out, f.picked)
	}
	code, _, errs := run(t, Module(nil, nil), env, "path", "pick", "nope")
	if code != 1 || !strings.Contains(errs, "no pathway named 'nope'") {
		t.Fatal(code, errs)
	}
	if code, _, errs := run(t, Module(nil, nil), env, "path", "pick"); code != 2 || !strings.Contains(errs, "usage: vmd1 agent path pick") {
		t.Fatal(code, errs)
	}
}

func TestDownAndUpDriveCompose(t *testing.T) {
	dir := t.TempDir()
	var calls []string
	fake := func(_ context.Context, d string, _, _ io.Writer, name string, args ...string) error {
		calls = append(calls, d+"|"+name+" "+strings.Join(args, " "))
		return nil
	}
	env := map[string]string{"VMD1_AGENT_DIR": dir}
	if code, _, errs := run(t, Module(nil, fake), env, "down"); code != 1 || !strings.Contains(errs, "no agent installed here") {
		t.Fatal(code, errs)
	}
	os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services: {}\n"), 0o644)
	for _, c := range []string{"down", "up"} {
		if code, _, errs := run(t, Module(nil, fake), env, c); code != 0 {
			t.Fatal(c, code, errs)
		}
	}
	file := filepath.Join(dir, "compose.yml")
	want := []string{dir + "|docker compose -f " + file + " down", dir + "|docker compose -f " + file + " up -d"}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatal(calls)
	}
}
