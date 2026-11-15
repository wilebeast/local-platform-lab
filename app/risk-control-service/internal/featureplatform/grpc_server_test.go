package featureplatform

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	featurev1 "github.com/wilebeast/local-platform-lab/app/risk-control-service/api/feature/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestServingGRPCErrorSeparatesInvalidRequestsFromBackendFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "invalid request", err: InvalidRequestError{Message: "request_id is required"}, code: codes.InvalidArgument},
		{name: "deadline", err: context.DeadlineExceeded, code: codes.DeadlineExceeded},
		{name: "backend failure", err: errors.New("ClickHouse unavailable"), code: codes.Internal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := status.Code(servingGRPCError(test.err)); actual != test.code {
				t.Fatalf("expected %s, got %s", test.code, actual)
			}
		})
	}
}

func TestGRPCFeatureServerMapsTypedServingResponse(t *testing.T) {
	cutoff := time.Date(2026, 11, 15, 13, 0, 0, 0, time.UTC)
	ref := FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "inflow_minor"}
	service := NewServingService(
		staticFeatureCatalog{definitions: []ServingFeatureDefinition{{Ref: ref, ValueType: "int", MaxAge: 10 * time.Minute}}},
		&recordingSnapshotReader{snapshots: []FeatureSnapshot{{
			FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1,
			Subject: Subject{Type: "organization", Keys: map[string]string{"subject_id": "org-grpc", "currency": "USD"}},
			AsOf:    cutoff.Add(-time.Minute), ComputedAt: cutoff,
			SourceRunID: "grpc-run", ContentFingerprint: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			QualityStatus: "passed", Values: map[string]any{"inflow_minor": int64(123_456)},
		}}},
		NewAllowListFeatureAuthorizer(map[string][]string{"risk-control-service": {"subject_fund_activity"}}),
	)
	transport := NewGRPCFeatureServer(service, PrincipalResolverFunc(func(context.Context) (string, error) {
		return "risk-control-service", nil
	}))

	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	featurev1.RegisterFeatureServingServiceServer(grpcServer, transport)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)
	connection, err := grpc.DialContext(
		context.Background(),
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	response, err := featurev1.NewFeatureServingServiceClient(connection).BatchGetLatestBefore(
		context.Background(),
		&featurev1.BatchGetLatestBeforeRequest{
			RequestId: "grpc-request-001",
			Subjects: []*featurev1.Subject{{Type: "organization", Keys: map[string]string{
				"subject_id": "org-grpc",
				"currency":   "USD",
			}}},
			Features: []*featurev1.FeatureRef{{
				FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "inflow_minor",
			}},
			Cutoff: cutoffProto(t, cutoff),
			MaxAge: durationpb.New(5 * time.Minute),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Values) != 1 || response.Values[0].Status != featurev1.ValueStatus_VALUE_STATUS_SUCCESS {
		t.Fatalf("unexpected gRPC response: %+v", response)
	}
	if response.Values[0].GetIntValue() != 123_456 || response.Values[0].SourceRunId != "grpc-run" {
		t.Fatalf("unexpected typed value: %+v", response.Values[0])
	}
}

func cutoffProto(t *testing.T, value time.Time) *timestamppb.Timestamp {
	t.Helper()
	timestamp := timestamppb.New(value)
	if err := timestamp.CheckValid(); err != nil {
		t.Fatal(err)
	}
	return timestamp
}
