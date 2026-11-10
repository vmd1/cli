// Package agent is `vmd1 agent`: control of the tunnel-agent running on this host. Pathway selection talks to the agent's
// loopback status API (127.0.0.1:9099); down/up drive its docker compose project.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/vmd1/cli/internal/cli"
)

const (
	defaultURL = "http://127.0.0.1:9099"
	defaultDir = "/opt/tunnel-agent"
)

// Runner runs a command with its output on the given streams; tests replace it.
type Runner func(ctx context.Context, dir string, out, errw io.Writer, name string, args ...string) error

func execRunner(ctx context.Context, dir string, out, errw io.Writer, name string, args ...string) error {
	c := exec.CommandContext(ctx, name, args...)
	c.Dir, c.Stdout, c.Stderr = dir, out, errw
	return c.Run()
}

// Module builds the `agent` module. HTTP and Run are injectable for tests.
func Module(httpc *http.Client, run Runner) cli.Module { return module(httpc, run, hostForward) }

func module(httpc *http.Client, run Runner, forward func(routes []string) error) cli.Module {
	if httpc == nil {
		httpc = &http.Client{Timeout: 10 * time.Second}
	}
	if run == nil {
		run = execRunner
	}
	a := &agent{http: httpc, run: run, forward: forward}
	return cli.Module{Name: "agent", Summary: "the tunnel agent on this host: pathways, exit node, routes, down, up", Commands: []cli.Command{
		{Name: "path", Summary: "which pathway carries traffic", Commands: []cli.Command{
			{Name: "list", Summary: "show every pathway, its state and which one is selected", Usage: "[--json]", Run: a.pathList},
			{Name: "pick", Summary: "choose the pathway that carries new connections (\"default\" undoes it)", Usage: "<name|default>", Run: a.pathPick},
		}},
		{Name: "exit-node", Summary: "carry this host's internet traffic through another tailnet node (tailscale pathways)", Commands: []cli.Command{
			{Name: "show", Summary: "the exit node and whether it is in use", Usage: "", Run: a.exitShow},
			{Name: "set", Summary: "use this node as the exit node", Usage: "<node|ip>", Run: a.exitSet},
			{Name: "clear", Summary: "stop using an exit node", Usage: "", Run: a.exitClear},
		}},
		{Name: "routes", Summary: "subnets this host advertises to the tailnet (tailscale pathways)", Commands: a.routesCommands()},
		{Name: "down", Summary: "stop the agent (docker compose down; keeps its identity)", Usage: "", Run: a.down},
		{Name: "up", Summary: "start the agent (docker compose up -d)", Usage: "", Run: a.up},
	}}
}

type agent struct {
	http    *http.Client
	run     Runner
	forward func(routes []string) error // turns on IP forwarding on the host for advertised routes
}

type common struct{ url, dir string }

func (a *agent) flags(name string, env *cli.Env, args []string, extra func(*flag.FlagSet)) (common, []string, error) {
	c := common{url: orDefault(env.Getenv("VMD1_AGENT_URL"), defaultURL), dir: orDefault(env.Getenv("VMD1_AGENT_DIR"), defaultDir)}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.url, "url", c.url, "agent status API")
	fs.StringVar(&c.dir, "dir", c.dir, "directory holding the agent's compose.yml")
	if extra != nil {
		extra(fs)
	}
	if err := fs.Parse(args); err != nil {
		return c, nil, cli.UsageError{Msg: err.Error()}
	}
	return c, fs.Args(), nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// pathway keeps the order the agent lists them in (its priority order), which a Go map would lose.
type pathway struct {
	Name     string
	State    string   `json:"state"`
	Error    *string  `json:"error"`
	RttMs    *float64 `json:"rtt_ms"`
	Tunnel   *string  `json:"tunnel"`
	RetryInS int      `json:"retry_in_s"`
}

type status struct {
	Active    *string   `json:"active"`
	ActiveAll []string  `json:"active_all"`
	Selected  *string   `json:"selected"`
	Preferred string    `json:"preferred"`
	Pathways  []pathway `json:"-"` // filled by parseStatus, in the agent's order
}

func parseStatus(raw []byte) (*status, error) {
	var s status
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(top["pathways"]))
	if _, err := dec.Token(); err != nil { // {
		return nil, errors.New("status has no pathways")
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var p pathway
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		p.Name = key.(string)
		s.Pathways = append(s.Pathways, p)
	}
	return &s, nil
}

func (a *agent) fetch(ctx context.Context, url string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(url, "/")+"/", nil)
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the agent at %s (is it running? try: vmd1 agent up): %w", url, err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (a *agent) pathList(ctx context.Context, env *cli.Env, args []string) error {
	var asJSON bool
	c, rest, err := a.flags("path list", env, args, func(fs *flag.FlagSet) { fs.BoolVar(&asJSON, "json", false, "print the agent's raw status") })
	if err != nil || len(rest) != 0 {
		return orUsage(err, "path list takes no arguments")
	}
	raw, err := a.fetch(ctx, c.url)
	if err != nil {
		return err
	}
	if asJSON {
		_, err = env.Out.Write(append(bytes.TrimRight(raw, "\n"), '\n'))
		return err
	}
	st, err := parseStatus(raw)
	if err != nil {
		return fmt.Errorf("unexpected status from the agent: %w", err)
	}
	up := map[string]bool{}
	for _, n := range st.ActiveAll {
		up[n] = true
	}
	sel := ""
	if st.Selected != nil {
		sel = *st.Selected
	}
	tw := tabwriter.NewWriter(env.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tROLE\tRTT\tDETAIL")
	for _, p := range st.Pathways {
		role := ""
		switch {
		case p.Name == sel:
			role = "selected"
		case up[p.Name]:
			role = "standby"
		}
		rtt := "-"
		if p.RttMs != nil && up[p.Name] {
			rtt = fmt.Sprintf("%.0fms", *p.RttMs)
		}
		detail := ""
		switch {
		case p.Error != nil && *p.Error != "":
			detail = *p.Error
			if p.RetryInS > 0 {
				detail += fmt.Sprintf(" (retry in %ds)", p.RetryInS)
			}
		case p.Tunnel != nil:
			detail = *p.Tunnel
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, p.State, role, rtt, detail)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if sel == "" {
		fmt.Fprintln(env.Out, "\nno pathway is up")
	} else if st.Preferred != "" && st.Preferred != sel {
		fmt.Fprintf(env.Out, "\npreferred %s is not up: %s carries new connections\n", st.Preferred, sel)
	}
	return nil
}

func (a *agent) pathPick(ctx context.Context, env *cli.Env, args []string) error {
	c, rest, err := a.flags("path pick", env, args, nil)
	if err != nil || len(rest) != 1 {
		return orUsage(err, "path pick needs one pathway name (see: vmd1 agent path list)")
	}
	body, _ := json.Marshal(map[string]string{"name": rest[0]})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.url, "/")+"/path", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the agent at %s (is it running? try: vmd1 agent up): %w", c.url, err)
	}
	defer resp.Body.Close()
	var out struct {
		OK       bool    `json:"ok"`
		Selected *string `json:"selected"`
		Error    string  `json:"error"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("unexpected answer from the agent (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || !out.OK {
		return errors.New(orDefault(out.Error, fmt.Sprintf("the agent refused (HTTP %d)", resp.StatusCode)))
	}
	if out.Selected == nil {
		fmt.Fprintln(env.Out, "ok, but no pathway is up right now")
		return nil
	}
	fmt.Fprintf(env.Out, "new connections now use %s\n", *out.Selected)
	return nil
}

func (a *agent) compose(ctx context.Context, env *cli.Env, args []string, name string, composeArgs ...string) error {
	c, rest, err := a.flags(name, env, args, nil)
	if err != nil || len(rest) != 0 {
		return orUsage(err, name+" takes no arguments")
	}
	file := filepath.Join(c.dir, "compose.yml")
	if _, err := os.Stat(file); err != nil {
		return fmt.Errorf("no agent installed here (%s not found; use --dir or VMD1_AGENT_DIR)", file)
	}
	if err := a.run(ctx, c.dir, env.Out, env.Err, "docker", append([]string{"compose", "-f", file}, composeArgs...)...); err != nil {
		return fmt.Errorf("docker compose %s failed (needs root or the docker group): %w", composeArgs[0], err)
	}
	return nil
}

func (a *agent) down(ctx context.Context, env *cli.Env, args []string) error {
	return a.compose(ctx, env, args, "down", "down")
}

func (a *agent) up(ctx context.Context, env *cli.Env, args []string) error {
	return a.compose(ctx, env, args, "up", "up", "-d")
}

func orUsage(err error, msg string) error {
	if err != nil {
		return err
	}
	return cli.UsageError{Msg: msg}
}
