package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	userserver "github.com/wilebeast/local-platform-lab/app/user-service/internal/server"
	"github.com/wilebeast/local-platform-lab/pkg/serviceregistry"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

var configFile = flag.String("f", "etc/user-service.yaml", "configuration file")

func main() {
	flag.Parse()

	var c zrpc.RpcServerConf
	conf.MustLoad(*configFile, &c)

	server := zrpc.MustNewServer(c, func(grpcServer *grpc.Server) {
		userv1.RegisterUserServiceServer(grpcServer, userserver.NewUserServer())
	})
	defer server.Stop()

	podIP := os.Getenv("POD_IP")
	if podIP == "" {
		podIP = "127.0.0.1"
	}
	_, port, err := net.SplitHostPort(c.ListenOn)
	if err != nil {
		log.Fatalf("parse listen address: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	registration, err := serviceregistry.RegisterBoth(ctx, serviceregistry.Config{
		ServiceName:   "user-service",
		NacosAddress:  os.Getenv("NACOS_SERVER_ADDR"),
		EtcdEndpoints: os.Getenv("ETCD_ENDPOINTS"),
	}, net.JoinHostPort(podIP, port))
	if err != nil {
		log.Fatalf("register service: %v", err)
	}
	defer registration.Close()

	fmt.Printf("Starting %s at %s; registered %s:%s in Nacos and Etcd\n", c.Name, c.ListenOn, podIP, port)
	server.Start()
}
