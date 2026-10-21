package svc

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/authn"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/config"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/genericrpc"
	"github.com/wilebeast/local-platform-lab/pkg/serviceregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type ServiceContext struct {
	Config              config.Config
	UserClient          userv1.UserServiceClient
	GenericRPC          genericrpc.RouteCaller
	genericInvokers     []*genericrpc.Invoker
	genericConnections  []*grpc.ClientConn
	genericResolvers    []*serviceregistry.Builder
	userConn            *grpc.ClientConn
	resolver            *serviceregistry.Builder
	Provider            string
	GatewaySignatureKey string
	AuthAllowedOrigin   string
	AuthValidator       *authn.Validator
	RBACOrganizationID  string
}

func NewServiceContext(c config.Config) *ServiceContext {
	provider := c.UserRpc.Provider
	if value := os.Getenv("DISCOVERY_PROVIDER"); value != "" {
		provider = value
	}
	allowedOrigin := os.Getenv("AUTH_ALLOWED_ORIGIN")
	if allowedOrigin == "" {
		allowedOrigin = "https://localhost:18443"
	}
	ctx := &ServiceContext{Config: c, Provider: provider, GatewaySignatureKey: os.Getenv("GATEWAY_SIGNATURE_KEY"), AuthAllowedOrigin: allowedOrigin, RBACOrganizationID: envOr("RBAC_ORGANIZATION_ID", "local-org")}
	if publicKey := os.Getenv("JWT_PUBLIC_KEY_B64"); publicKey != "" {
		validator, err := authn.NewValidator(
			publicKey, os.Getenv("JWT_KEY_ID"),
			"local-platform-lab", "api-bff", envOr("REDIS_ADDR", "redis.redis.svc.cluster.local:6379"),
		)
		if err != nil {
			panic(err)
		}
		ctx.AuthValidator = validator
		if err := validator.Ping(context.Background()); err != nil {
			panic(fmt.Errorf("connect to authentication Redis: %w", err))
		}
	}
	if c.UserRpc.Target != "" {
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
	}
	if len(c.GenericRPC.Schemas) > 0 {
		if err := ctx.configureGenericRPC(c.GenericRPC.ReloadInterval, c.GenericRPC.Schemas, c.GenericRPC.Routes); err != nil {
			panic(err)
		}
	}
	return ctx
}

func (s *ServiceContext) Close() error {
	if s.AuthValidator != nil {
		_ = s.AuthValidator.Close()
	}
	for _, invoker := range s.genericInvokers {
		invoker.Close()
	}
	for _, connection := range s.genericConnections {
		_ = connection.Close()
	}
	for _, builder := range s.genericResolvers {
		builder.Close()
	}
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

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (s *ServiceContext) configureGenericRPC(reloadText string, schemas []config.GenericSchemaConfig, routes []config.GenericRouteConfig) error {
	reloadInterval := 10 * time.Second
	if reloadText != "" {
		parsed, err := time.ParseDuration(reloadText)
		if err != nil {
			return fmt.Errorf("parse generic gRPC reload interval: %w", err)
		}
		reloadInterval = parsed
	}

	routesBySchema := make(map[string][]config.GenericRouteConfig)
	definitions := make([]genericrpc.RouteDefinition, 0, len(routes))
	for _, route := range routes {
		timeout := time.Duration(0)
		if route.Timeout != "" {
			parsed, err := time.ParseDuration(route.Timeout)
			if err != nil {
				return fmt.Errorf("parse timeout for generic gRPC route %q: %w", route.Name, err)
			}
			timeout = parsed
		}
		routesBySchema[route.SchemaRef] = append(routesBySchema[route.SchemaRef], route)
		definitions = append(definitions, genericrpc.RouteDefinition{Name: route.Name, SchemaRef: route.SchemaRef, Method: route.Method, Timeout: timeout})
	}

	callers := make(map[string]genericrpc.Caller, len(schemas))
	for _, schema := range schemas {
		if schema.Name == "" {
			return fmt.Errorf("generic gRPC schema name is required")
		}
		if schema.Service == "" || schema.Schema == "" || schema.Version == "" {
			return fmt.Errorf("generic gRPC schema %q service, schema, and version are required", schema.Name)
		}
		if _, exists := callers[schema.Name]; exists {
			return fmt.Errorf("generic gRPC schema %q is duplicated", schema.Name)
		}
		connection, builder, err := newGenericConnection(schema)
		if err != nil {
			return err
		}
		contracts, err := methodContracts(routesBySchema[schema.Name])
		if err != nil {
			return err
		}
		invoker, err := genericrpc.New(connection, genericrpc.Options{
			DescriptorPath:  schema.DescriptorPath,
			MetadataPath:    schema.MetadataPath,
			BootstrapSHA256: schema.DescriptorSHA256,
			ReloadInterval:  reloadInterval,
			ExpectedMetadata: genericrpc.MetadataIdentity{
				Service: schema.Service,
				Schema:  schema.Schema,
				Version: schema.Version,
			},
			MethodContracts: contracts,
			OnReload: func(digest string) {
				log.Printf("generic gRPC descriptor reloaded: schema=%s digest=%s", schema.Name, digest)
			},
			OnReloadError: func(err error) {
				log.Printf("generic gRPC descriptor reload failed: schema=%s error=%v", schema.Name, err)
			},
		})
		if err != nil {
			return fmt.Errorf("configure generic gRPC schema %q: %w", schema.Name, err)
		}
		callers[schema.Name] = invoker
		s.genericInvokers = append(s.genericInvokers, invoker)
		s.genericConnections = append(s.genericConnections, connection)
		if builder != nil {
			s.genericResolvers = append(s.genericResolvers, builder)
		}
	}
	registry, err := genericrpc.NewRegistry(callers, definitions)
	if err != nil {
		return err
	}
	s.GenericRPC = registry
	return nil
}

func newGenericConnection(schema config.GenericSchemaConfig) (*grpc.ClientConn, *serviceregistry.Builder, error) {
	provider := schema.Provider
	if value := os.Getenv("DISCOVERY_PROVIDER"); value != "" {
		provider = value
	}
	target := schema.Target
	var builder *serviceregistry.Builder
	if provider != serviceregistry.ProviderDNS {
		var err error
		builder, err = serviceregistry.NewBuilder(serviceregistry.Config{
			Provider: provider, ServiceName: schema.Service,
			NacosAddress: schema.NacosAddress, EtcdEndpoints: schema.EtcdEndpoints,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("configure discovery for generic gRPC schema %q: %w", schema.Name, err)
		}
		resolver.Register(builder)
		target = builder.Target()
	}
	if target == "" {
		return nil, builder, fmt.Errorf("generic gRPC schema %q target is required", schema.Name)
	}
	connection, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultServiceConfig(`{"loadBalancingConfig":[{"round_robin":{}}]}`))
	if err != nil {
		return nil, builder, fmt.Errorf("connect generic gRPC schema %q: %w", schema.Name, err)
	}
	return connection, builder, nil
}

func methodContracts(routes []config.GenericRouteConfig) ([]genericrpc.MethodContract, error) {
	contracts := make([]genericrpc.MethodContract, 0, len(routes))
	for _, route := range routes {
		input, err := fieldContracts(route.InputFields)
		if err != nil {
			return nil, fmt.Errorf("configure input fields for generic gRPC route %q: %w", route.Name, err)
		}
		output, err := fieldContracts(route.OutputFields)
		if err != nil {
			return nil, fmt.Errorf("configure output fields for generic gRPC route %q: %w", route.Name, err)
		}
		contracts = append(contracts, genericrpc.MethodContract{FullMethod: route.Method, InputFields: input, OutputFields: output})
	}
	return contracts, nil
}

func fieldContracts(fields []config.GenericFieldConfig) ([]genericrpc.FieldContract, error) {
	contracts := make([]genericrpc.FieldContract, 0, len(fields))
	for _, field := range fields {
		kind, err := protobufKind(field.Kind)
		if err != nil {
			return nil, err
		}
		contracts = append(contracts, genericrpc.FieldContract{ProtoName: field.ProtoName, JSONName: field.JSONName, Kind: kind, Repeated: field.Repeated})
	}
	return contracts, nil
}

func protobufKind(value string) (protoreflect.Kind, error) {
	kinds := map[string]protoreflect.Kind{
		"bool": protoreflect.BoolKind, "enum": protoreflect.EnumKind,
		"int32": protoreflect.Int32Kind, "sint32": protoreflect.Sint32Kind, "sfixed32": protoreflect.Sfixed32Kind,
		"int64": protoreflect.Int64Kind, "sint64": protoreflect.Sint64Kind, "sfixed64": protoreflect.Sfixed64Kind,
		"uint32": protoreflect.Uint32Kind, "fixed32": protoreflect.Fixed32Kind,
		"uint64": protoreflect.Uint64Kind, "fixed64": protoreflect.Fixed64Kind,
		"float": protoreflect.FloatKind, "double": protoreflect.DoubleKind,
		"string": protoreflect.StringKind, "bytes": protoreflect.BytesKind, "message": protoreflect.MessageKind,
	}
	kind, ok := kinds[strings.ToLower(strings.TrimSpace(value))]
	if !ok {
		return 0, fmt.Errorf("unsupported protobuf kind %q", value)
	}
	return kind, nil
}
