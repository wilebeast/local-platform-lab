package genericrpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Caller is the complete interface exposed to handlers that use descriptor-driven gRPC.
type Caller interface {
	Call(ctx context.Context, fullMethod string, request map[string]any) (map[string]any, error)
}

// Invoker loads a versioned descriptor set once and reuses its method descriptors.
type Invoker struct {
	conn    grpc.ClientConnInterface
	methods map[string]protoreflect.MethodDescriptor
}

func New(conn grpc.ClientConnInterface, descriptorPath, expectedSHA256 string) (*Invoker, error) {
	if conn == nil {
		return nil, fmt.Errorf("gRPC connection is required")
	}
	contents, err := os.ReadFile(descriptorPath)
	if err != nil {
		return nil, fmt.Errorf("read descriptor set %q: %w", descriptorPath, err)
	}
	if err := verifyDigest(contents, expectedSHA256); err != nil {
		return nil, err
	}

	set := new(descriptorpb.FileDescriptorSet)
	if err := proto.Unmarshal(contents, set); err != nil {
		return nil, fmt.Errorf("decode descriptor set %q: %w", descriptorPath, err)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, fmt.Errorf("build descriptor registry: %w", err)
	}

	methods := make(map[string]protoreflect.MethodDescriptor)
	files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		services := file.Services()
		for serviceIndex := 0; serviceIndex < services.Len(); serviceIndex++ {
			service := services.Get(serviceIndex)
			serviceMethods := service.Methods()
			for methodIndex := 0; methodIndex < serviceMethods.Len(); methodIndex++ {
				method := serviceMethods.Get(methodIndex)
				fullMethod := "/" + string(service.FullName()) + "/" + string(method.Name())
				methods[fullMethod] = method
			}
		}
		return true
	})
	if len(methods) == 0 {
		return nil, fmt.Errorf("descriptor set %q contains no gRPC methods", descriptorPath)
	}
	return &Invoker{conn: conn, methods: methods}, nil
}

func (i *Invoker) Call(ctx context.Context, fullMethod string, request map[string]any) (map[string]any, error) {
	method, ok := i.methods[fullMethod]
	if !ok {
		return nil, fmt.Errorf("gRPC method %q is not present in the loaded descriptor set", fullMethod)
	}
	if method.IsStreamingClient() || method.IsStreamingServer() {
		return nil, fmt.Errorf("gRPC method %q is streaming and cannot use unary generic invocation", fullMethod)
	}

	requestJSON, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode request for %q: %w", fullMethod, err)
	}
	requestMessage := dynamicpb.NewMessage(method.Input())
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(requestJSON, requestMessage); err != nil {
		return nil, fmt.Errorf("map request for %q: %w", fullMethod, err)
	}

	responseMessage := dynamicpb.NewMessage(method.Output())
	if err := i.conn.Invoke(ctx, fullMethod, requestMessage, responseMessage); err != nil {
		return nil, fmt.Errorf("invoke %q: %w", fullMethod, err)
	}
	responseJSON, err := (protojson.MarshalOptions{UseProtoNames: false}).Marshal(responseMessage)
	if err != nil {
		return nil, fmt.Errorf("encode response for %q: %w", fullMethod, err)
	}
	var response map[string]any
	if err := json.Unmarshal(responseJSON, &response); err != nil {
		return nil, fmt.Errorf("decode response for %q: %w", fullMethod, err)
	}
	return response, nil
}

func verifyDigest(contents []byte, expected string) error {
	expected = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(expected), "sha256:"))
	if expected == "" {
		return fmt.Errorf("descriptor SHA-256 is required")
	}
	if _, err := hex.DecodeString(expected); err != nil || len(expected) != sha256.Size*2 {
		return fmt.Errorf("descriptor SHA-256 %q is invalid", expected)
	}
	actual := sha256.Sum256(contents)
	actualHex := hex.EncodeToString(actual[:])
	if actualHex != expected {
		return fmt.Errorf("descriptor SHA-256 mismatch: expected %s, got %s", expected, actualHex)
	}
	return nil
}
