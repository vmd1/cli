// Command vmd1 is the homelab's command-line tool. Each area is a module under internal/modules, registered here;
// `nodectl` will become another one.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/vmd1/cli/internal/cli"
	"github.com/vmd1/cli/internal/modules/agent"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	root := &cli.Root{Name: "vmd1", Version: version, Modules: []cli.Module{
		agent.Module(nil, nil),
		// node.Module(),  // nodectl, to be moved here
	}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(root.Run(ctx, cli.DefaultEnv(), os.Args[1:]))
}
