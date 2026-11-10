package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tsAgent is a stateful stand-in for the agent's /tailscale endpoint.
type tsAgent struct {
	exit     string
	routes   []string
	approved []string
	noTUN    bool
	posts    []string
}

func (f *tsAgent) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tailscale" {
			http.NotFound(w, r)
			return
		}
		if r.Method == "POST" {
			b, _ := io.ReadAll(r.Body)
			f.posts = append(f.posts, string(b))
			var req map[string]json.RawMessage
			json.Unmarshal(b, &req)
			if v, ok := req["exit_node"]; ok {
				var n string
				json.Unmarshal(v, &n)
				if n != "" && f.noTUN {
					w.WriteHeader(409)
					io.WriteString(w, `{"ok": false, "error": "an exit node needs TUN mode"}`)
					return
				}
				f.exit = n
			}
			if v, ok := req["advertise_routes"]; ok {
				json.Unmarshal(v, &f.routes)
			}
		}
		out := map[string]any{"ok": true, "exit_node": f.exit, "advertise_routes": f.routes, "applied_to": []string{"tailscale-wss"},
			"warnings": []string{}, "pathway": "tailscale-wss",
			"live": map[string]any{"exit_node_in_use": f.exit != "", "exit_node_online": true, "routes_approved": f.approved}}
		json.NewEncoder(w).Encode(out)
	})
}

func tsModule(forwarded *[]string) func() (*tsAgent, *httptest.Server, map[string]string) {
	return func() (*tsAgent, *httptest.Server, map[string]string) {
		f := &tsAgent{}
		srv := httptest.NewServer(f.handler())
		return f, srv, map[string]string{"VMD1_AGENT_URL": srv.URL}
	}
}

func TestExitNodeSetShowClear(t *testing.T) {
	f, srv, env := tsModule(nil)()
	defer srv.Close()
	m := module(nil, nil, func([]string) error { return nil })
	if code, out, errs := run(t, m, env, "exit-node", "set", "exit-1"); code != 0 || !strings.Contains(out, "exit node is now exit-1") {
		t.Fatal(code, out, errs)
	}
	if f.exit != "exit-1" {
		t.Fatalf("agent state %q", f.exit)
	}
	if code, out, _ := run(t, m, env, "exit-node", "show"); code != 0 || !strings.Contains(out, "exit-1 (in use)") {
		t.Fatal(code, out)
	}
	if code, out, _ := run(t, m, env, "exit-node", "clear"); code != 0 || !strings.Contains(out, "no exit node") || f.exit != "" {
		t.Fatal(code, out, f.exit)
	}
	if code, out, _ := run(t, m, env, "exit-node", "show"); code != 0 || !strings.Contains(out, "none") {
		t.Fatal(code, out)
	}
	if code, _, errs := run(t, m, env, "exit-node", "set"); code != 2 || !strings.Contains(errs, "one node") {
		t.Fatal(code, errs)
	}
}

func TestExitNodeRefusalIsShown(t *testing.T) {
	f, srv, env := tsModule(nil)()
	defer srv.Close()
	f.noTUN = true
	code, _, errs := run(t, module(nil, nil, func([]string) error { return nil }), env, "exit-node", "set", "exit-1")
	if code != 1 || !strings.Contains(errs, "needs TUN mode") {
		t.Fatal(code, errs)
	}
}

func TestRoutesAddRemoveSetClearAndForwarding(t *testing.T) {
	f, srv, env := tsModule(nil)()
	defer srv.Close()
	var fwd [][]string
	m := module(nil, nil, func(r []string) error { fwd = append(fwd, r); return nil })
	if code, out, errs := run(t, m, env, "routes", "set", "192.168.1.7/24,10.0.0.0/8"); code != 0 || !strings.Contains(out, "advertising") {
		t.Fatal(code, out, errs)
	}
	if strings.Join(f.routes, " ") != "10.0.0.0/8 192.168.1.0/24" { // host bits cleared, sorted
		t.Fatal(f.routes)
	}
	run(t, m, env, "routes", "add", "172.16.0.0/12")
	run(t, m, env, "routes", "remove", "10.0.0.0/8")
	if strings.Join(f.routes, " ") != "172.16.0.0/12 192.168.1.0/24" {
		t.Fatal(f.routes)
	}
	if len(fwd) != 3 {
		t.Fatalf("forwarding was set %d times", len(fwd))
	}
	f.approved = []string{"192.168.1.0/24"}
	_, out, _ := run(t, m, env, "routes", "list")
	if !strings.Contains(out, "192.168.1.0/24\tapproved") || !strings.Contains(out, "172.16.0.0/12\tadvertised, waiting for approval") {
		t.Fatal(out)
	}
	if code, out, _ := run(t, m, env, "routes", "clear"); code != 0 || !strings.Contains(out, "no subnet routes") || len(f.routes) != 0 || len(fwd) != 3 {
		t.Fatal(code, out, f.routes, len(fwd))
	}
	if code, out, _ := run(t, m, env, "routes", "list"); code != 0 || !strings.Contains(out, "no subnet routes advertised") {
		t.Fatal(code, out)
	}
}

func TestRoutesRejectJunkAndNeedArguments(t *testing.T) {
	_, srv, env := tsModule(nil)()
	defer srv.Close()
	m := module(nil, nil, func([]string) error { return nil })
	if code, _, errs := run(t, m, env, "routes", "add", "nonsense"); code != 2 || !strings.Contains(errs, "not a network") {
		t.Fatal(code, errs)
	}
	if code, _, errs := run(t, m, env, "routes", "set"); code != 2 || !strings.Contains(errs, "one or more networks") {
		t.Fatal(code, errs)
	}
	if code, _, errs := run(t, m, env, "routes", "clear", "10.0.0.0/8"); code != 2 {
		t.Fatal(code, errs)
	}
}
