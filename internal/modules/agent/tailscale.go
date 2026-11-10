package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/vmd1/cli/internal/cli"
)

// The exit node and the subnet routes are Tailscale settings of the agent's tailscale / tailscale-wss pathways. The agent keeps
// them (they survive a restart) and applies them to the running tailscaled; this file is only the host-side control.

type tsStatus struct {
	OK       bool     `json:"ok"`
	Error    string   `json:"error"`
	ExitNode string   `json:"exit_node"`
	Routes   []string `json:"advertise_routes"`
	Applied  []string `json:"applied_to"`
	Warnings []string `json:"warnings"`
	Pathway  *string  `json:"pathway"`
	Live     *struct {
		ExitInUse  bool     `json:"exit_node_in_use"`
		ExitOnline *bool    `json:"exit_node_online"`
		Approved   []string `json:"routes_approved"`
	} `json:"live"`
}

// tsCall asks the agent's /tailscale endpoint (GET when body is nil, POST otherwise).
func (a *agent) tsCall(ctx context.Context, url string, body map[string]any) (*tsStatus, error) {
	method, rd := http.MethodGet, io.Reader(nil)
	if body != nil {
		b, _ := json.Marshal(body)
		method, rd = http.MethodPost, bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, strings.TrimRight(url, "/")+"/tailscale", rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the agent at %s (is it running? try: vmd1 agent up): %w", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var st tsStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("unexpected answer from the agent (HTTP %d); is it an older agent? update the image", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || !st.OK {
		return nil, errors.New(orDefault(st.Error, fmt.Sprintf("the agent refused (HTTP %d)", resp.StatusCode)))
	}
	return &st, nil
}

func printWarnings(env *cli.Env, st *tsStatus) {
	for _, w := range st.Warnings {
		fmt.Fprintln(env.Err, "warning:", w)
	}
}

func (a *agent) tsSet(ctx context.Context, env *cli.Env, url string, body map[string]any) (*tsStatus, error) {
	st, err := a.tsCall(ctx, url, body)
	if err != nil {
		return nil, err
	}
	printWarnings(env, st)
	return st, nil
}

func (a *agent) applied(st *tsStatus) string {
	if len(st.Applied) == 0 {
		return " (no tailscale pathway is up right now: it is applied when one starts)"
	}
	return ""
}

func (a *agent) exitShow(ctx context.Context, env *cli.Env, args []string) error {
	c, rest, err := a.flags("exit-node show", env, args, nil)
	if err != nil || len(rest) != 0 {
		return orUsage(err, "exit-node show takes no arguments")
	}
	st, err := a.tsCall(ctx, c.url, nil)
	if err != nil {
		return err
	}
	switch {
	case st.ExitNode == "":
		fmt.Fprintln(env.Out, "exit node: none (this host's traffic leaves directly)")
	case st.Live == nil:
		fmt.Fprintf(env.Out, "exit node: %s (no tailscale pathway is up, so it is not in use yet)\n", st.ExitNode)
	case st.Live.ExitInUse && st.Live.ExitOnline != nil && !*st.Live.ExitOnline:
		fmt.Fprintf(env.Out, "exit node: %s (OFFLINE: this host's internet traffic has nowhere to go)\n", st.ExitNode)
	case st.Live.ExitInUse:
		fmt.Fprintf(env.Out, "exit node: %s (in use)\n", st.ExitNode)
	default:
		fmt.Fprintf(env.Out, "exit node: %s (set, but tailscaled is not using it: see warnings)\n", st.ExitNode)
	}
	printWarnings(env, st)
	return nil
}

func (a *agent) exitSet(ctx context.Context, env *cli.Env, args []string) error {
	c, rest, err := a.flags("exit-node set", env, args, nil)
	if err != nil || len(rest) != 1 {
		return orUsage(err, "exit-node set needs one node (its tailnet name or IP)")
	}
	st, err := a.tsSet(ctx, env, c.url, map[string]any{"exit_node": rest[0]})
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "exit node is now %s%s\n", st.ExitNode, a.applied(st))
	return nil
}

func (a *agent) exitClear(ctx context.Context, env *cli.Env, args []string) error {
	c, rest, err := a.flags("exit-node clear", env, args, nil)
	if err != nil || len(rest) != 0 {
		return orUsage(err, "exit-node clear takes no arguments")
	}
	st, err := a.tsSet(ctx, env, c.url, map[string]any{"exit_node": ""})
	if err != nil {
		return err
	}
	fmt.Fprintln(env.Out, "no exit node: this host's traffic leaves directly"+a.applied(st))
	return nil
}

// normRoute canonicalises a CIDR the way the agent does (host bits cleared) so add/remove compare like with like.
func normRoute(s string) (string, error) {
	_, n, err := net.ParseCIDR(strings.TrimSpace(s))
	if err != nil {
		return "", cli.UsageError{Msg: fmt.Sprintf("%q is not a network (use CIDR, e.g. 192.168.1.0/24)", s)}
	}
	return n.String(), nil
}

func normRoutes(args []string) ([]string, error) {
	var out []string
	for _, a := range args {
		for _, p := range strings.FieldsFunc(a, func(r rune) bool { return r == ',' || r == ' ' }) {
			n, err := normRoute(p)
			if err != nil {
				return nil, err
			}
			out = append(out, n)
		}
	}
	return out, nil
}

func (a *agent) routesList(ctx context.Context, env *cli.Env, args []string) error {
	c, rest, err := a.flags("routes list", env, args, nil)
	if err != nil || len(rest) != 0 {
		return orUsage(err, "routes list takes no arguments")
	}
	st, err := a.tsCall(ctx, c.url, nil)
	if err != nil {
		return err
	}
	if len(st.Routes) == 0 {
		fmt.Fprintln(env.Out, "no subnet routes advertised")
		printWarnings(env, st)
		return nil
	}
	approved := map[string]bool{}
	live := st.Live != nil
	if live {
		for _, r := range st.Live.Approved {
			approved[r] = true
		}
	}
	for _, r := range st.Routes {
		state := "advertised"
		switch {
		case approved[r]:
			state = "approved"
		case live:
			state = "advertised, waiting for approval (admin console or ACL autoApprovers)"
		}
		fmt.Fprintf(env.Out, "%s\t%s\n", r, state)
	}
	printWarnings(env, st)
	return nil
}

// routesChange computes the new list from the current one and sets it.
func (a *agent) routesChange(name string, edit func(cur map[string]bool, given []string), needArgs bool) func(context.Context, *cli.Env, []string) error {
	return func(ctx context.Context, env *cli.Env, args []string) error {
		c, rest, err := a.flags("routes "+name, env, args, nil)
		if err != nil {
			return err
		}
		if needArgs && len(rest) == 0 || !needArgs && len(rest) != 0 {
			if needArgs {
				return cli.UsageError{Msg: "routes " + name + " needs one or more networks (CIDR, e.g. 192.168.1.0/24)"}
			}
			return cli.UsageError{Msg: "routes " + name + " takes no arguments"}
		}
		given, err := normRoutes(rest)
		if err != nil {
			return err
		}
		cur := map[string]bool{}
		if name == "add" || name == "remove" {
			st, err := a.tsCall(ctx, c.url, nil)
			if err != nil {
				return err
			}
			for _, r := range st.Routes {
				cur[r] = true
			}
		}
		edit(cur, given)
		want := make([]string, 0, len(cur))
		for r := range cur {
			want = append(want, r)
		}
		sort.Strings(want)
		if len(want) > 0 { // before the agent applies them, so it does not warn about forwarding that is about to be on
			if err := a.forward(want); err != nil {
				fmt.Fprintf(env.Err, "warning: %v\n", err)
			}
		}
		st, err := a.tsSet(ctx, env, c.url, map[string]any{"advertise_routes": want})
		if err != nil {
			return err
		}
		if len(st.Routes) == 0 {
			fmt.Fprintln(env.Out, "no subnet routes advertised"+a.applied(st))
			return nil
		}
		fmt.Fprintf(env.Out, "advertising %s%s\n", strings.Join(st.Routes, ", "), a.applied(st))
		fmt.Fprintln(env.Out, "approve them in the Tailscale admin console (or via ACL autoApprovers) before the tailnet uses them")
		return nil
	}
}

func (a *agent) routesCommands() []cli.Command {
	set := func(cur map[string]bool, given []string) {
		for _, g := range given {
			cur[g] = true
		}
	}
	return []cli.Command{
		{Name: "list", Summary: "show the advertised subnet routes and whether the tailnet approved them", Usage: "", Run: a.routesList},
		{Name: "set", Summary: "advertise exactly these subnets (replaces the list)", Usage: "<cidr>...", Run: a.routesChange("set", set, true)},
		{Name: "add", Summary: "also advertise these subnets", Usage: "<cidr>...", Run: a.routesChange("add", set, true)},
		{Name: "remove", Summary: "stop advertising these subnets", Usage: "<cidr>...", Run: a.routesChange("remove", func(cur map[string]bool, given []string) {
			for _, g := range given {
				delete(cur, g)
			}
		}, true)},
		{Name: "clear", Summary: "advertise no subnets", Usage: "", Run: a.routesChange("clear", func(map[string]bool, []string) {}, false)},
	}
}

// hostForward switches on IP forwarding on this host (needed for the agent to route the advertised subnets) and keeps it on
// across reboots. It needs root; the container cannot do it, because with host networking /proc/sys is read-only to it.
func hostForward(routes []string) error {
	var v4, v6 bool
	for _, r := range routes {
		if strings.Contains(r, ":") {
			v6 = true
		} else {
			v4 = true
		}
	}
	var conf []string
	set := func(file, key string) error {
		if err := os.WriteFile(file, []byte("1\n"), 0o644); err != nil {
			return err
		}
		conf = append(conf, key+" = 1")
		return nil
	}
	var err error
	if v4 {
		err = set("/proc/sys/net/ipv4/ip_forward", "net.ipv4.ip_forward")
	}
	if err == nil && v6 {
		err = set("/proc/sys/net/ipv6/conf/all/forwarding", "net.ipv6.conf.all.forwarding")
	}
	if err != nil {
		return fmt.Errorf("could not turn on IP forwarding (run with sudo): %w; the routes will not carry traffic until the host forwards", err)
	}
	if err := os.WriteFile("/etc/sysctl.d/99-vmd1-agent.conf", []byte(strings.Join(conf, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("IP forwarding is on but not saved for the next boot: %w", err)
	}
	return nil
}
