//go:build goexperiment.jsonv2

package main

import (
	"cmp"
	_ "embed"
	"os"
	"time"

	"log/slog"

	"github.com/niklasfasching/x/ops"
	"github.com/niklasfasching/x/tools/murks/server"
)

func main() {
	o := ops.Auto(cmp.Or(os.Getenv("Domain"), server.DevDomain), 30*time.Second)
	if err := server.Start(server.DefaultConfig); err != nil {
		slog.Error("server", "err", err.Error())
		o.Shutdown(time.Second)
		os.Exit(1)
	}
}
