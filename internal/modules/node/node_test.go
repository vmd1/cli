package node

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/vmd1/cli/internal/cli"
)

func TestHoist(t *testing.T) {
	for _, c := range []struct {
		verb        string
		in, g, rest []string
	}{
		{"update", []string{"zeus-01", "--yes"}, []string{"--yes"}, []string{"zeus-01"}},
		{"update", []string{"--yes", "zeus-01", "0.3.0"}, []string{"--yes"}, []string{"zeus-01", "0.3.0"}},
		{"status", []string{"x", "--addr", "127.0.0.1:7777", "--no-kube"}, []string{"--addr", "127.0.0.1:7777", "--no-kube"}, []string{"x"}},
		{"status", []string{"x", "-addr=1.2.3.4:7777"}, []string{"-addr=1.2.3.4:7777"}, []string{"x"}},
		{"rollout", []string{"--canary", "1", "--yes"}, []string{"--yes"}, []string{"--canary", "1"}},
		{"pcap", []string{"x", "--port", "53", "--seconds", "5"}, nil, []string{"x", "--port", "53", "--seconds", "5"}},
		{"get", []string{"x", "firewall"}, nil, []string{"x", "firewall"}},
	} {
		g, r := Hoist(c.verb, c.in)
		if !reflect.DeepEqual(g, c.g) || !reflect.DeepEqual(r, c.rest) {
			t.Errorf("Hoist(%s, %v) = %v / %v, want %v / %v", c.verb, c.in, g, r, c.g, c.rest)
		}
	}
}

func run(t *testing.T, fake Runner, args ...string) (int, []string, string) {
	t.Helper()
	var got []string
	r := &cli.Root{Name: "vmd1", Modules: []cli.Module{Module(func(ctx context.Context, prog string, a []string) int {
		got = a
		return fake(ctx, prog, a)
	})}}
	var out, errb bytes.Buffer
	code := r.Run(context.Background(), &cli.Env{Out: &out, Err: &errb}, args)
	return code, got, errb.String()
}

func TestVerbsDelegateWithGlobalFlagsFirst(t *testing.T) {
	code, got, _ := run(t, func(context.Context, string, []string) int { return 0 }, "node", "update", "zeus-01", "--yes")
	if code != 0 || !reflect.DeepEqual(got, []string{"--yes", "update", "zeus-01"}) {
		t.Fatalf("code %d args %v", code, got)
	}
}

func TestExitStatusPassesThroughSilently(t *testing.T) {
	code, _, errb := run(t, func(context.Context, string, []string) int { return 1 }, "node", "health", "x")
	if code != 1 || errb != "" {
		t.Fatalf("code %d stderr %q (the embedded tool already printed its error)", code, errb)
	}
	if code, _, _ := run(t, func(context.Context, string, []string) int { return 2 }, "node", "status"); code != 2 {
		t.Fatalf("exit status 2 must pass through, got %d", code)
	}
}

func TestEveryNodectlVerbIsRegistered(t *testing.T) {
	m := Module(nil)
	have := map[string]bool{}
	for _, c := range m.Commands {
		have[c.Name] = true
	}
	for _, want := range []string{"list", "status", "update", "rollout", "reset", "hard-reset", "config", "get", "health", "support", "pcap", "whoami", "audit", "completion"} {
		if !have[want] {
			t.Errorf("verb %q missing", want)
		}
	}
}
