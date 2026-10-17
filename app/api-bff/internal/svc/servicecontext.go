package svc

import (
	"os"

	"github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/config"
	"github.com/wilebeast/local-platform-lab/pkg/serviceregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"
)

type ServiceContext struct {
	Config     config.Config
	UserClient userv1.UserServiceClient
	userConn   *grpc.ClientConn
	resolver   *serviceregistry.Builder
	Provider   string
}

func NewServiceContext(c config.Config) *ServiceContext {
	provider := c.UserRpc.Provider
	if value := os.Getenv("DISCOVERY_PROVIDER"); value != "" {
		provider = value
	}
	ctx := &ServiceContext{Config: c, Provider: provider}
	if c.UserRpc.Target == "" {
		return ctx
	}

	target := c.UserRpc.Target
	if provider != serviceregistry.ProviderDNS {
		builder, err := serviceregistry.NewBuilder(serviceregistry.Config{
			Provider: provider, ServiceName: c.UserRpc.ServiceName,
			NacosAddress: c.UserRpc.NacosAddress, EtcdEndpoints: c.UserRpc.EtcdEndpoints,
		})
		if err != nil {
			panic(err)
		}
		ctx.resolver = builder
		resolver.Register(builder)
		target = builder.Target()
	}

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultServiceConfig(`{"loadBalancingConfig":[{"round_robin":{}}]}`))
	if err != nil {
		panic(err)
	}
	ctx.userConn = conn
	ctx.UserClient = userv1.NewUserServiceClient(conn)
	return ctx
}

func (s *ServiceContext) Close() error {
	if s.userConn == nil {
		if s.resolver != nil {
			s.resolver.Close()
		}
		return nil
	}
	err := s.userConn.Close()
	if s.resolver != nil {
		s.resolver.Close()
	}
	return err
}
