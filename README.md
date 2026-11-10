# vmd1

One command-line tool for the homelab, built from modules: `agent` (the tunnel agent) and `node` (the edge-node image client, formerly `nodectl`).

```
vmd1 agent path list              # every pathway, its state, rtt, and which one carries new connections
vmd1 agent path pick <name>       # make an active pathway carry new connections (persists across restarts)
vmd1 agent path pick default      # back to the configured one (PREFERRED_PATHWAY, tailscale-wss)
vmd1 agent exit-node set <node>   # tailscale pathways: send this host's internet traffic through a tailnet exit node
vmd1 agent exit-node show|clear
vmd1 agent routes set|add|remove <cidr>...   # advertise subnets to the tailnet (turns on IP forwarding; run as root)
vmd1 agent routes list|clear
vmd1 agent down                   # docker compose down for the tunnel agent (keeps its identity)
vmd1 agent up                     # docker compose up -d
vmd1 node list | status <node> | check <node> | update <node> --yes | rollout --yes   # edge nodes (the full list: vmd1 node)
vmd1 version | help [command]
```

`node` is the same client as the `nodectl` binary in the edge-node image (`github.com/vmd1/edge-node-image/nodectl`, pinned in `go.mod`); it needs
`kubectl` for the node lookup, drain and wait, and a Tailscale grant for `vmd1.dev/cap/noded`. Its global flags (`--addr`, `--port`, `--yes`,
`--no-kube`) work before or after the verb: `vmd1 node update zeus-01 --yes`. Bump the client with `go get github.com/vmd1/edge-node-image@<commit>`.

`path` talks to the agent's loopback status API (`http://127.0.0.1:9099`, override with `--url` or `VMD1_AGENT_URL`). `down` / `up` run
`docker compose` on `compose.yml` in `/opt/tunnel-agent` (`--dir` or `VMD1_AGENT_DIR`), so they need root or the docker group.
Only a pathway that is currently up can be picked; the others keep running as standbys, and if the picked one drops, the best remaining carries traffic.

## Layout

```
cmd/vmd1/main.go             registers the modules
internal/cli/                the small command framework (Module -> nested Commands, help, exit codes); standard library only
internal/modules/agent/      `vmd1 agent ...`
internal/modules/node/       `vmd1 node ...` (maps verbs onto the edge-node-image nodectl package)
```

## Adding a module

1. `internal/modules/<name>/<name>.go` with `func Module(...) cli.Module` (commands nest: `cli.Command{Name, Summary, Usage, Run | Commands}`).
2. Register it in `cmd/vmd1/main.go`.
3. Return `cli.UsageError{Msg}` from a command for a usage mistake (exit 2); any other error exits 1.

Inject what a module talks to (HTTP client, command runner) so tests need no network or docker: see `internal/modules/agent/agent_test.go`.

## Release

Tag `vX.Y.Z`: the Release workflow publishes `vmd1_linux_amd64`, `vmd1_linux_arm64` and `SHA256SUMS` on a GitHub release, and a scratch image
`registry.kmnet.uk/vmd1:<tag>` (and `:latest`) that the tunnel-agent provisioning script pulls and copies the binary out of. The workflow needs the
repository secrets `REGISTRY_USERNAME` / `REGISTRY_PASSWORD` (a `registry-admin` account).
