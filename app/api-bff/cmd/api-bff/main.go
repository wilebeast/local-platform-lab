package main

import (
	"flag"
	"fmt"

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/config"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/handler"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/api-bff.yaml", "configuration file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	ctx := svc.NewServiceContext(c)
	defer ctx.Close()
	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting %s at %s:%d\n", c.Name, c.Host, c.Port)
	server.Start()
}
