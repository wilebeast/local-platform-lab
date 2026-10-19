package genericrpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"gopkg.in/yaml.v2"
)

// Caller is the complete interface exposed to handlers that use descriptor-driven gRPC.
type Caller interface {
	Call(ctx context.Context, fullMethod string, request map[string]any) (map[string]any, error)
}

type Options struct {
	DescriptorPath   string
	MetadataPath     string
	BootstrapSHA256  string
	ReloadInterval   time.Duration
	ExpectedMetadata MetadataIdentity
	MethodContracts  []MethodContract
	OnReload         func(string)
	OnReloadError    func(error)
}

type MetadataIdentity struct {
	Service string
	Schema  string
	Version string
}

type FieldContract struct {
	ProtoName string
	JSONName  string
	Kind      protoreflect.Kind
	Repeated  bool
}

type MethodContract struct {
	FullMethod   string
	InputFields  []FieldContract
	OutputFields []FieldContract
}

type snapshot struct {
	digest  string
	methods map[string]protoreflect.MethodDescriptor
}

type descriptorMetadata struct {
	Service string `yaml:"service"`
	Schema  string `yaml:"schema"`
	Version string `yaml:"version"`
	Digest  string `yaml:"digest"`
	Source  string `yaml:"source"`
}

// Invoker owns the immutable descriptor snapshot and atomically replaces it after a valid reload.
type Invoker struct {
	conn             grpc.ClientConnInterface
	descriptorPath   string
	metadataPath     string
	expectedMetadata MetadataIdentity
	methodContracts  []MethodContract
	onReloadError    func(error)
	onReload         func(string)
	current          atomic.Pointer[snapshot]
	cancel           context.CancelFunc
	wait             sync.WaitGroup
}

func New(conn grpc.ClientConnInterface, options Options) (*Invoker, error) {
	if conn == nil {
		return nil, fmt.Errorf("gRPC connection is required")
	}
	if options.DescriptorPath == "" {
		return nil, fmt.Errorf("descriptor path is required")
	}

	expectedDigest := options.BootstrapSHA256
	if options.MetadataPath != "" {
		metadata, err := readMetadata(options.MetadataPath)
		if err != nil {
			return nil, err
		}
		if err := validateMetadata(metadata, options.ExpectedMetadata); err != nil {
			return nil, err
		}
		expectedDigest = metadata.Digest
	}
	loaded, err := loadSnapshot(options.DescriptorPath, expectedDigest, options.MethodContracts)
	if err != nil {
		return nil, err
	}

	invoker := &Invoker{
		conn:             conn,
		descriptorPath:   options.DescriptorPath,
		metadataPath:     options.MetadataPath,
		expectedMetadata: options.ExpectedMetadata,
		methodContracts:  append([]MethodContract(nil), options.MethodContracts...),
		onReloadError:    options.OnReloadError,
		onReload:         options.OnReload,
	}
	invoker.current.Store(loaded)
	if options.MetadataPath != "" && options.ReloadInterval > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		invoker.cancel = cancel
		invoker.wait.Add(1)
		go invoker.poll(ctx, options.ReloadInterval)
	}
	return invoker, nil
}

func (i *Invoker) Call(ctx context.Context, fullMethod string, request map[string]any) (map[string]any, error) {
	current := i.current.Load()
	method, ok := current.methods[fullMethod]
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

func (i *Invoker) Close() {
	if i.cancel != nil {
		i.cancel()
		i.wait.Wait()
	}
}

func (i *Invoker) poll(ctx context.Context, interval time.Duration) {
	defer i.wait.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := i.reloadIfChanged(); err != nil && i.onReloadError != nil {
				i.onReloadError(err)
			}
		}
	}
}

func (i *Invoker) reloadIfChanged() error {
	metadata, err := readMetadata(i.metadataPath)
	if err != nil {
		return err
	}
	if err := validateMetadata(metadata, i.expectedMetadata); err != nil {
		return err
	}
	digest := metadata.Digest
	if digest == i.current.Load().digest {
		return nil
	}
	loaded, err := loadSnapshot(i.descriptorPath, digest, i.methodContracts)
	if err != nil {
		return err
	}
	i.current.Store(loaded)
	if i.onReload != nil {
		i.onReload(loaded.digest)
	}
	return nil
}

func readMetadata(path string) (descriptorMetadata, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return descriptorMetadata{}, fmt.Errorf("read descriptor metadata %q: %w", path, err)
	}
	var metadata descriptorMetadata
	if err := yaml.UnmarshalStrict(contents, &metadata); err != nil {
		return descriptorMetadata{}, fmt.Errorf("decode descriptor metadata %q: %w", path, err)
	}
	metadata.Digest, err = normalizeDigest(metadata.Digest)
	if err != nil {
		return descriptorMetadata{}, err
	}
	return metadata, nil
}

func validateMetadata(actual descriptorMetadata, expected MetadataIdentity) error {
	checks := []struct {
		name     string
		actual   string
		expected string
	}{
		{name: "service", actual: actual.Service, expected: expected.Service},
		{name: "schema", actual: actual.Schema, expected: expected.Schema},
		{name: "version", actual: actual.Version, expected: expected.Version},
	}
	for _, check := range checks {
		if check.expected != "" && check.actual != check.expected {
			return fmt.Errorf("descriptor metadata %s mismatch: expected %q, got %q", check.name, check.expected, check.actual)
		}
	}
	return nil
}

func loadSnapshot(path, expectedDigest string, contracts []MethodContract) (*snapshot, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read descriptor set %q: %w", path, err)
	}
	digest, err := verifyDigest(contents, expectedDigest)
	if err != nil {
		return nil, err
	}

	set := new(descriptorpb.FileDescriptorSet)
	if err := proto.Unmarshal(contents, set); err != nil {
		return nil, fmt.Errorf("decode descriptor set %q: %w", path, err)
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
		return nil, fmt.Errorf("descriptor set %q contains no gRPC methods", path)
	}
	for _, contract := range contracts {
		method, ok := methods[contract.FullMethod]
		if !ok {
			return nil, fmt.Errorf("required gRPC method %q is not present in descriptor set %q", contract.FullMethod, path)
		}
		if method.IsStreamingClient() || method.IsStreamingServer() {
			return nil, fmt.Errorf("required gRPC method %q must be unary", contract.FullMethod)
		}
		if err := validateFields(contract.FullMethod+" request", method.Input().Fields(), contract.InputFields); err != nil {
			return nil, err
		}
		if err := validateFields(contract.FullMethod+" response", method.Output().Fields(), contract.OutputFields); err != nil {
			return nil, err
		}
	}
	return &snapshot{digest: digest, methods: methods}, nil
}

func validateFields(owner string, fields protoreflect.FieldDescriptors, contracts []FieldContract) error {
	for _, contract := range contracts {
		field := fields.ByName(protoreflect.Name(contract.ProtoName))
		if field == nil {
			return fmt.Errorf("%s is missing required field %q", owner, contract.ProtoName)
		}
		if field.JSONName() != contract.JSONName {
			return fmt.Errorf("%s field %q JSON name mismatch: expected %q, got %q", owner, contract.ProtoName, contract.JSONName, field.JSONName())
		}
		if field.Kind() != contract.Kind {
			return fmt.Errorf("%s field %q kind mismatch: expected %s, got %s", owner, contract.ProtoName, contract.Kind, field.Kind())
		}
		if (field.Cardinality() == protoreflect.Repeated) != contract.Repeated {
			return fmt.Errorf("%s field %q repeated cardinality mismatch", owner, contract.ProtoName)
		}
	}
	return nil
}

func verifyDigest(contents []byte, expected string) (string, error) {
	expected, err := normalizeDigest(expected)
	if err != nil {
		return "", err
	}
	actual := sha256.Sum256(contents)
	actualHex := hex.EncodeToString(actual[:])
	if actualHex != expected {
		return "", fmt.Errorf("descriptor SHA-256 mismatch: expected %s, got %s", expected, actualHex)
	}
	return actualHex, nil
}

func normalizeDigest(value string) (string, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(value), "sha256:"))
	if value == "" {
		return "", fmt.Errorf("descriptor SHA-256 is required")
	}
	if _, err := hex.DecodeString(value); err != nil || len(value) != sha256.Size*2 {
		return "", fmt.Errorf("descriptor SHA-256 %q is invalid", value)
	}
	return value, nil
}
