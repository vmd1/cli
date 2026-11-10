// Package node is `vmd1 node`: the edge-node image client (what used to be the standalone `nodectl`). The implementation lives in
// github.com/vmd1/edge-node-image/nodectl, shared with the nodectl binary shipped in the image; this module only maps `vmd1 node <verb>`
// onto it and moves nodectl's global flags (--addr, --yes, --no-kube, --port), which it only reads before the verb, to the front.
package node

import (
	"context"
	"strings"

	"github.com/vmd1/cli/internal/cli"
	"github.com/vmd1/edge-node-image/nodectl"
)

// Runner runs the nodectl client: prog is the name used in its messages. Tests replace it.
type Runner func(ctx context.Context, prog string, args []string) int

const prog = "vmd1 node"

// verbs are the nodectl commands, in the order of `nodectl` help.
var verbs = []struct{ name, usage, summary string }{
	{"list", "", "nodes with Ready state and image version"},
	{"status", "<node>", "image version, boot entry, boot assessment, pressure, units"},
	{"logs", "<node> <unit> [-n N] [-f]", "journal of a unit"},
	{"check", "<node>", "is a newer image available?"},
	{"stage", "<node> [version]", "download an update into the inactive slot (no reboot)"},
	{"update", "<node> [version]", "stage, cordon+drain, reboot, wait, uncordon (needs --yes)"},
	{"rollback", "<node> <version>", "boot an installed version once, reboot, wait (needs --yes)"},
	{"reboot", "<node>", "drain, reboot, wait, uncordon (needs --yes)"},
	{"registry", "<node> show|set [--mirror URL] [--fallback URL]", "container registry mirror"},
	{"channel", "<node> show|set <url>|reset", "update source (https release directory)"},
	{"rollout", "[version] [--canary N] [--settle 10m] [--from N] [--config]", "rolling update with health gates (needs --yes)"},
	{"reset", "<node> [--wipe k3s|all-var] [--keep-node-object] [--force] [--confirm N]", "wipe /var, back to first boot (needs --yes)"},
	{"hard-reset", "<node> [--timeout 10m]", "ask the platform adapter to power cycle it (needs --yes)"},
	{"config", "<node> show|validate -f F|diff -f F|set -f F [--ttl 4h]|clear", "declarative node config (EdgeNodeConfig)"},
	{"verify", "<file>...", "check GitHub build provenance attestations of release files (needs gh)"},
	{"debug", "<node>", "kubectl debug node/<node>"},
	{"get", "<node> <kind> [arg]", "image boot memory psi mounts sysctl modules processes netstat cgroups services tailscale kubelet time config firewall"},
	{"ps", "<node> [rss|cpu] [-n N]", "processes by memory or CPU"},
	{"netstat", "<node>", "listening sockets"},
	{"dmesg", "<node> [-f]", "kernel log"},
	{"events", "<node> [-f]", "unit state changes, boots, config changes"},
	{"service", "<node> status|start|stop|restart <unit>", "allowlisted units only"},
	{"ls", "<node> <path>", "list an allowlisted directory"},
	{"read", "<node> <path> [-n bytes]", "read an allowlisted file (never secrets)"},
	{"whoami", "<node>", "your role on the node (from the Tailscale grant)"},
	{"audit", "<node>", "who called what (the noded audit log)"},
	{"health", "<node>|--all", "the boot checks plus listeners and memory pressure"},
	{"support", "<node>... [-o dir]", "diagnostics bundle (tar.gz, encrypted with age if available)"},
	{"pcap", "<node> [--iface I] [--host IP] [--port N] [--proto tcp|udp|icmp] [--seconds 30]", "packet capture to stdout"},
	{"dashboard", "", "all nodes, refreshed"},
	{"completion", "bash|zsh", "shell completion"},
}

// Module builds the `node` module. run is injectable for tests; nil uses the real client.
func Module(run Runner) cli.Module {
	if run == nil {
		run = nodectl.Main
	}
	cmds := make([]cli.Command, 0, len(verbs))
	for _, v := range verbs {
		v := v
		cmds = append(cmds, cli.Command{Name: v.name, Summary: v.summary, Usage: v.usage,
			Run: func(ctx context.Context, _ *cli.Env, args []string) error {
				global, rest := Hoist(v.name, args)
				argv := append(append(global, v.name), rest...)
				if code := run(ctx, prog, argv); code != 0 {
					return cli.ExitError{Code: code}
				}
				return nil
			}})
	}
	return cli.Module{Name: "node", Summary: "edge nodes: status, update, rollout, config, inspect (nodectl)", Commands: cmds}
}

// Hoist splits args into nodectl's global flags (--addr V, --port V, --yes, --no-kube, one or two dashes, or --flag=value), found
// anywhere in args, and everything else in order. nodectl's own flag parsing stops at the first non-flag, so without this
// `update zeus-01 --yes` would silently run as a dry run. pcap has its own --port (a packet filter), which stays with the verb.
func Hoist(verb string, args []string) (global, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		name := strings.TrimLeft(a, "-")
		dashes := len(a) - len(name)
		base, _, hasEq := strings.Cut(name, "=")
		switch {
		case dashes == 0 || dashes > 2:
			rest = append(rest, a)
		case base == "yes" || base == "no-kube":
			global = append(global, a)
		case base == "addr" || (base == "port" && verb != "pcap"):
			global = append(global, a)
			if !hasEq && i+1 < len(args) {
				i++
				global = append(global, args[i])
			}
		default:
			rest = append(rest, a)
		}
	}
	return global, rest
}
