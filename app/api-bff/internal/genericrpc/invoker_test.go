package genericrpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

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
	invoker, err := New(connection, descriptorPath, digest)
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
	_, err := New(new(fakeConnection), descriptorPath, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err == nil {
		t.Fatal("expected digest mismatch")
	}
}

func TestCallRejectsUnknownMethod(t *testing.T) {
	descriptorPath, digest := writeDescriptorSet(t)
	invoker, err := New(new(fakeConnection), descriptorPath, digest)
	if err != nil {
		t.Fatalf("new invoker: %v", err)
	}
	if _, err := invoker.Call(context.Background(), "/unknown.Service/Method", nil); err == nil {
		t.Fatal("expected unknown method error")
	}
}

func writeDescriptorSet(t *testing.T) (string, string) {
	t.Helper()
	file := protodesc.ToFileDescriptorProto(userv1.File_v1_user_proto)
	contents, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{file}})
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	path := filepath.Join(t.TempDir(), "user-service-v1.pb")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}
	digest := sha256.Sum256(contents)
	return path, hex.EncodeToString(digest[:])
}
