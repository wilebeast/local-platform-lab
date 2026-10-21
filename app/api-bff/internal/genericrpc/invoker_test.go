package genericrpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

type fakeConnection struct {
	method  string
	request protoreflect.ProtoMessage
}

func (f *fakeConnection) Invoke(_ context.Context, method string, args, reply any, _ ...grpc.CallOption) error {
	f.method = method
	f.request = args.(protoreflect.ProtoMessage)
	response := reply.(*dynamicpb.Message)
	response.Set(response.Descriptor().Fields().ByName("user_id"), protoreflect.ValueOfString("user-123"))
	response.Set(response.Descriptor().Fields().ByName("verification_status"), protoreflect.ValueOfString("verified"))
	return nil
}

func (*fakeConnection) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("streaming is not expected")
}

func TestInvokerCallsMethodFromDescriptor(t *testing.T) {
	descriptorPath, digest := writeDescriptorSet(t)
	connection := new(fakeConnection)
	invoker, err := New(connection, Options{DescriptorPath: descriptorPath, BootstrapSHA256: digest})
	if err != nil {
		t.Fatalf("new invoker: %v", err)
	}

	response, err := invoker.Call(context.Background(), "/user.v1.UserService/GetProfile", map[string]any{"userId": "user-123"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if connection.method != "/user.v1.UserService/GetProfile" {
		t.Fatalf("unexpected method: %q", connection.method)
	}
	request := connection.request.ProtoReflect()
	if got := request.Get(request.Descriptor().Fields().ByName("user_id")).String(); got != "user-123" {
		t.Fatalf("unexpected user id: %q", got)
	}
	if response["userId"] != "user-123" || response["verificationStatus"] != "verified" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestNewRejectsDescriptorDigestMismatch(t *testing.T) {
	descriptorPath, _ := writeDescriptorSet(t)
	_, err := New(new(fakeConnection), Options{DescriptorPath: descriptorPath, BootstrapSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err == nil {
		t.Fatal("expected digest mismatch")
	}
}

func TestCallRejectsUnknownMethod(t *testing.T) {
	descriptorPath, digest := writeDescriptorSet(t)
	invoker, err := New(new(fakeConnection), Options{DescriptorPath: descriptorPath, BootstrapSHA256: digest})
	if err != nil {
		t.Fatalf("new invoker: %v", err)
	}
	if _, err := invoker.Call(context.Background(), "/unknown.Service/Method", nil); err == nil {
		t.Fatal("expected unknown method error")
	}
}

func TestInvokerPollsMetadataAndAtomicallyReloadsDescriptor(t *testing.T) {
	descriptorPath, digest := writeDescriptorSet(t)
	metadataPath := filepath.Join(filepath.Dir(descriptorPath), "metadata.yaml")
	writeMetadata(t, metadataPath, digest)
	connection := new(fakeConnection)
	reloaded := make(chan string, 1)
	invoker, err := New(connection, Options{
		DescriptorPath:  descriptorPath,
		MetadataPath:    metadataPath,
		ReloadInterval:  5 * time.Millisecond,
		MethodContracts: []MethodContract{profileMethodContract()},
		OnReload:        func(digest string) { reloaded <- digest },
	})
	if err != nil {
		t.Fatalf("new invoker: %v", err)
	}
	t.Cleanup(invoker.Close)

	request := map[string]any{"userId": "user-123", "tenantId": "tenant-456"}
	if _, err := invoker.Call(context.Background(), "/user.v1.UserService/GetProfile", request); err == nil {
		t.Fatal("expected the original descriptor to reject tenantId")
	}

	updated := protodesc.ToFileDescriptorProto(userv1.File_v1_user_proto)
	for _, message := range updated.MessageType {
		if message.GetName() != "GetProfileRequest" {
			continue
		}
		label := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
		fieldType := descriptorpb.FieldDescriptorProto_TYPE_STRING
		message.Field = append(message.Field, &descriptorpb.FieldDescriptorProto{
			Name:     proto.String("tenant_id"),
			JsonName: proto.String("tenantId"),
			Number:   proto.Int32(2),
			Label:    &label,
			Type:     &fieldType,
		})
	}
	updatedDigest := writeDescriptorFile(t, descriptorPath, updated)
	writeMetadata(t, metadataPath, updatedDigest)

	deadline := time.Now().Add(time.Second)
	for {
		if _, err := invoker.Call(context.Background(), "/user.v1.UserService/GetProfile", request); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("descriptor was not hot reloaded before the deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case actual := <-reloaded:
		if actual != updatedDigest {
			t.Fatalf("unexpected reloaded digest: %s", actual)
		}
	case <-time.After(time.Second):
		t.Fatal("successful reload was not reported")
	}
}

func TestFailedReloadKeepsLastValidSnapshot(t *testing.T) {
	descriptorPath, digest := writeDescriptorSet(t)
	metadataPath := filepath.Join(filepath.Dir(descriptorPath), "metadata.yaml")
	writeMetadata(t, metadataPath, digest)
	invoker, err := New(new(fakeConnection), Options{DescriptorPath: descriptorPath, MetadataPath: metadataPath})
	if err != nil {
		t.Fatalf("new invoker: %v", err)
	}

	invalid := []byte("not a descriptor")
	if err := os.WriteFile(descriptorPath, invalid, 0o600); err != nil {
		t.Fatalf("write invalid descriptor: %v", err)
	}
	invalidDigest := sha256.Sum256(invalid)
	writeMetadata(t, metadataPath, hex.EncodeToString(invalidDigest[:]))
	if err := invoker.reloadIfChanged(); err == nil {
		t.Fatal("expected invalid descriptor reload to fail")
	}
	if _, err := invoker.Call(context.Background(), "/user.v1.UserService/GetProfile", map[string]any{"userId": "user-123"}); err != nil {
		t.Fatalf("last valid snapshot was not retained: %v", err)
	}
}

func TestNewRejectsUnexpectedMetadataVersion(t *testing.T) {
	descriptorPath, digest := writeDescriptorSet(t)
	metadataPath := filepath.Join(filepath.Dir(descriptorPath), "metadata.yaml")
	writeMetadataVersion(t, metadataPath, digest, "2.0.0")
	_, err := New(new(fakeConnection), Options{
		DescriptorPath: descriptorPath,
		MetadataPath:   metadataPath,
		ExpectedMetadata: MetadataIdentity{
			Service: "user-service",
			Schema:  "user.v1.UserService",
			Version: "1.0.0",
		},
	})
	if err == nil {
		t.Fatal("expected metadata version mismatch")
	}
}

func TestReloadRejectsIncompatibleRequiredMethodAndKeepsSnapshot(t *testing.T) {
	descriptorPath, digest := writeDescriptorSet(t)
	metadataPath := filepath.Join(filepath.Dir(descriptorPath), "metadata.yaml")
	writeMetadata(t, metadataPath, digest)
	contract := profileMethodContract()
	invoker, err := New(new(fakeConnection), Options{
		DescriptorPath:  descriptorPath,
		MetadataPath:    metadataPath,
		MethodContracts: []MethodContract{contract},
	})
	if err != nil {
		t.Fatalf("new invoker: %v", err)
	}

	incompatible := protodesc.ToFileDescriptorProto(userv1.File_v1_user_proto)
	incompatible.Service[0].Method[0].ServerStreaming = proto.Bool(true)
	incompatibleDigest := writeDescriptorFile(t, descriptorPath, incompatible)
	writeMetadata(t, metadataPath, incompatibleDigest)
	if err := invoker.reloadIfChanged(); err == nil {
		t.Fatal("expected streaming method reload to fail")
	}
	if _, err := invoker.Call(context.Background(), contract.FullMethod, map[string]any{"userId": "user-123"}); err != nil {
		t.Fatalf("last valid snapshot was not retained: %v", err)
	}
}

func TestNewRejectsRequiredFieldTypeMismatch(t *testing.T) {
	descriptorPath, _ := writeDescriptorSet(t)
	incompatible := protodesc.ToFileDescriptorProto(userv1.File_v1_user_proto)
	fieldType := descriptorpb.FieldDescriptorProto_TYPE_INT64
	for _, message := range incompatible.MessageType {
		if message.GetName() == "GetProfileRequest" {
			message.Field[0].Type = &fieldType
		}
	}
	digest := writeDescriptorFile(t, descriptorPath, incompatible)
	_, err := New(new(fakeConnection), Options{
		DescriptorPath:  descriptorPath,
		BootstrapSHA256: digest,
		MethodContracts: []MethodContract{profileMethodContract()},
	})
	if err == nil {
		t.Fatal("expected required field type mismatch")
	}
}

func profileMethodContract() MethodContract {
	return MethodContract{
		FullMethod: "/user.v1.UserService/GetProfile",
		InputFields: []FieldContract{
			{ProtoName: "user_id", JSONName: "userId", Kind: protoreflect.StringKind},
		},
		OutputFields: []FieldContract{
			{ProtoName: "user_id", JSONName: "userId", Kind: protoreflect.StringKind},
			{ProtoName: "verification_status", JSONName: "verificationStatus", Kind: protoreflect.StringKind},
		},
	}
}

func writeDescriptorSet(t *testing.T) (string, string) {
	t.Helper()
	file := protodesc.ToFileDescriptorProto(userv1.File_v1_user_proto)
	path := filepath.Join(t.TempDir(), "user-service-v1.pb")
	return path, writeDescriptorFile(t, path, file)
}

func writeDescriptorFile(t *testing.T, path string, file *descriptorpb.FileDescriptorProto) string {
	t.Helper()
	contents, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{file}})
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func writeMetadata(t *testing.T, path, digest string) {
	writeMetadataVersion(t, path, digest, "1.0.0")
}

func writeMetadataVersion(t *testing.T, path, digest, version string) {
	t.Helper()
	contents := []byte("service: user-service\nschema: user.v1.UserService\nversion: " + version + "\ndigest: sha256:" + digest + "\nsource: test\n")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
}
