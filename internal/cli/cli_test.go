package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func testRoot(ran *[]string) *Root {
	leaf := func(name string) func(context.Context, *Env, []string) error {
		return func(_ context.Context, _ *Env, args []string) error {
			*ran = append(*ran, name+":"+strings.Join(args, ","))
			return nil
		}
	}
	return &Root{Name: "vmd1", Version: "1.2.3", Modules: []Module{
		{Name: "agent", Summary: "the tunnel agent", Commands: []Command{
			{Name: "path", Summary: "pathways", Commands: []Command{
				{Name: "list", Summary: "list", Run: leaf("list")},
				{Name: "pick", Summary: "pick", Usage: "<name>", Run: func(_ context.Context, _ *Env, a []string) error {
					if len(a) != 1 {
						return UsageError{"need a name"}
					}
					*ran = append(*ran, "pick:"+a[0])
					return nil
				}},
			}},
			{Name: "down", Summary: "stop", Run: func(context.Context, *Env, []string) error { return errors.New("boom") }},
		}},
		{Name: "node", Summary: "edge nodes (later)"},
	}}
}

func run(r *Root, args ...string) (int, string, string) {
	var o, e bytes.Buffer
	code := r.Run(context.Background(), &Env{Out: &o, Err: &e}, args)
	return code, o.String(), e.String()
}

func TestNestedDispatchAndArgs(t *testing.T) {
	var ran []string
	r := testRoot(&ran)
	if c, _, _ := run(r, "agent", "path", "list"); c != 0 {
		t.Fatal(c)
	}
	if c, _, _ := run(r, "agent", "path", "pick", "wireguard"); c != 0 {
		t.Fatal(c)
	}
	if got := strings.Join(ran, " "); got != "list: pick:wireguard" {
		t.Fatal(got)
	}
}

func TestUsageErrorExits2WithUsageAndRunErrorExits1(t *testing.T) {
	var ran []string
	r := testRoot(&ran)
	c, _, e := run(r, "agent", "path", "pick")
	if c != 2 || !strings.Contains(e, "need a name") || !strings.Contains(e, "usage: vmd1 agent path pick <name>") {
		t.Fatal(c, e)
	}
	c, _, e = run(r, "agent", "down")
	if c != 1 || !strings.Contains(e, "boom") {
		t.Fatal(c, e)
	}
}

func TestHelpVersionAndUnknown(t *testing.T) {
	var ran []string
	r := testRoot(&ran)
	if c, o, _ := run(r); c != 0 || !strings.Contains(o, "agent") || !strings.Contains(o, "node") {
		t.Fatal(o)
	}
	if c, o, _ := run(r, "version"); c != 0 || o != "vmd1 1.2.3\n" {
		t.Fatal(o)
	}
	if c, o, _ := run(r, "agent", "path"); c != 0 || !strings.Contains(o, "pick <name>") {
		t.Fatal(o)
	}
	if c, o, _ := run(r, "help", "agent", "path"); c != 0 || !strings.Contains(o, "list") {
		t.Fatal(o)
	}
	if c, _, e := run(r, "nope"); c != 2 || !strings.Contains(e, "unknown command") {
		t.Fatal(e)
	}
	if c, _, e := run(r, "agent", "nope"); c != 2 || !strings.Contains(e, "unknown command") {
		t.Fatal(e)
	}
}
