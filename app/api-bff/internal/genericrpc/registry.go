package genericrpc

import (
	"context"
	"fmt"
	"time"
)

// RouteCaller is the small interface used by HTTP handlers. It hides schema selection and gRPC method routing.
type RouteCaller interface {
	CallRoute(ctx context.Context, routeName string, request map[string]any) (map[string]any, error)
}

type RouteDefinition struct {
	Name      string
	SchemaRef string
	Method    string
	Timeout   time.Duration
}

type boundRoute struct {
	caller  Caller
	method  string
	timeout time.Duration
}

// Registry binds stable route names to schema-specific invokers.
type Registry struct {
	routes map[string]boundRoute
}

func NewRegistry(schemas map[string]Caller, definitions []RouteDefinition) (*Registry, error) {
	if len(schemas) == 0 {
		return nil, fmt.Errorf("at least one generic gRPC schema is required")
	}
	routes := make(map[string]boundRoute, len(definitions))
	for _, definition := range definitions {
		if definition.Name == "" {
			return nil, fmt.Errorf("generic gRPC route name is required")
		}
		if _, exists := routes[definition.Name]; exists {
			return nil, fmt.Errorf("generic gRPC route %q is duplicated", definition.Name)
		}
		caller, ok := schemas[definition.SchemaRef]
		if !ok {
			return nil, fmt.Errorf("generic gRPC route %q references unknown schema %q", definition.Name, definition.SchemaRef)
		}
		if definition.Method == "" {
			return nil, fmt.Errorf("generic gRPC route %q method is required", definition.Name)
		}
		routes[definition.Name] = boundRoute{caller: caller, method: definition.Method, timeout: definition.Timeout}
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("at least one generic gRPC route is required")
	}
	return &Registry{routes: routes}, nil
}

func (r *Registry) CallRoute(ctx context.Context, routeName string, request map[string]any) (map[string]any, error) {
	route, ok := r.routes[routeName]
	if !ok {
		return nil, fmt.Errorf("generic gRPC route %q is not configured", routeName)
	}
	if route.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, route.timeout)
		defer cancel()
	}
	return route.caller.Call(ctx, route.method, request)
}
