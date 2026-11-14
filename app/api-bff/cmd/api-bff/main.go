package main

import (
	"flag"
	"log"
	"log/slog"
	"os"

	kratos "github.com/go-kratos/kratos/v3"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/config"
	bffserver "github.com/wilebeast/local-platform-lab/app/api-bff/internal/server"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
)

var configFile = flag.String("f", "etc/api-bff.yaml", "configuration file")

func main() {
	flag.Parse()

	c, err := config.Load(*configFile)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx := svc.NewServiceContext(c)
	defer ctx.Close()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	server := bffserver.NewHTTPServer(c, ctx, logger)
	app := kratos.New(
		kratos.Name(c.Name),
		kratos.Version("v3"),
		kratos.Logger(logger),
		kratos.Server(server),
	)
	if err := app.Run(); err != nil {
		log.Fatalf("run %s: %v", c.Name, err)
	}
}
