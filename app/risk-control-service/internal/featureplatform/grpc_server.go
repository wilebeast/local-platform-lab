package featureplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	featurev1 "github.com/wilebeast/local-platform-lab/app/risk-control-service/api/feature/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type FeatureServing interface {
	BatchGetLatestBefore(context.Context, BatchGetLatestBeforeRequest) (BatchGetResponse, error)
	BatchGetAt(context.Context, BatchGetAtRequest) (BatchGetResponse, error)
}

type PrincipalResolver interface {
	ResolvePrincipal(context.Context) (string, error)
}

type PrincipalResolverFunc func(context.Context) (string, error)

func (f PrincipalResolverFunc) ResolvePrincipal(ctx context.Context) (string, error) {
	return f(ctx)
}

type GRPCFeatureServer struct {
	featurev1.UnimplementedFeatureServingServiceServer
	service           FeatureServing
	principalResolver PrincipalResolver
}

func NewGRPCFeatureServer(service FeatureServing, resolver PrincipalResolver) *GRPCFeatureServer {
	return &GRPCFeatureServer{service: service, principalResolver: resolver}
}

func (s *GRPCFeatureServer) BatchGetLatestBefore(
	ctx context.Context,
	request *featurev1.BatchGetLatestBeforeRequest,
) (*featurev1.BatchGetResponse, error) {
	principal, err := s.resolvePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Cutoff == nil {
		return nil, status.Error(codes.InvalidArgument, "request and cutoff are required")
	}
	if err := request.Cutoff.CheckValid(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "valid cutoff is required")
	}
	maxAge := time.Duration(0)
	if request.MaxAge != nil {
		if err := request.MaxAge.CheckValid(); err != nil || request.MaxAge.AsDuration() < 0 {
			return nil, status.Error(codes.InvalidArgument, "valid non-negative max_age is required")
		}
		maxAge = request.MaxAge.AsDuration()
	}
	response, err := s.service.BatchGetLatestBefore(ctx, BatchGetLatestBeforeRequest{
		Principal: principal,
		RequestID: request.RequestId,
		Subjects:  subjectsFromProto(request.Subjects),
		Features:  featureRefsFromProto(request.Features),
		Cutoff:    request.Cutoff.AsTime(),
		MaxAge:    maxAge,
	})
	if err != nil {
		return nil, servingGRPCError(err)
	}
	return responseToProto(response)
}

func (s *GRPCFeatureServer) BatchGetAt(
	ctx context.Context,
	request *featurev1.BatchGetAtRequest,
) (*featurev1.BatchGetResponse, error) {
	principal, err := s.resolvePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.AsOf == nil {
		return nil, status.Error(codes.InvalidArgument, "request and as_of are required")
	}
	if err := request.AsOf.CheckValid(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "valid as_of is required")
	}
	response, err := s.service.BatchGetAt(ctx, BatchGetAtRequest{
		Principal: principal,
		RequestID: request.RequestId,
		Subjects:  subjectsFromProto(request.Subjects),
		Features:  featureRefsFromProto(request.Features),
		AsOf:      request.AsOf.AsTime(),
	})
	if err != nil {
		return nil, servingGRPCError(err)
	}
	return responseToProto(response)
}

func (s *GRPCFeatureServer) resolvePrincipal(ctx context.Context) (string, error) {
	if s == nil || s.service == nil || s.principalResolver == nil {
		return "", status.Error(codes.Internal, "feature serving transport is not configured")
	}
	principal, err := s.principalResolver.ResolvePrincipal(ctx)
	if err != nil || principal == "" {
		return "", status.Error(codes.Unauthenticated, "authenticated service principal is required")
	}
	return principal, nil
}

func subjectsFromProto(subjects []*featurev1.Subject) []Subject {
	result := make([]Subject, 0, len(subjects))
	for _, subject := range subjects {
		if subject == nil {
			result = append(result, Subject{})
			continue
		}
		keys := make(map[string]string, len(subject.Keys))
		for key, value := range subject.Keys {
			keys[key] = value
		}
		result = append(result, Subject{Type: subject.Type, Keys: keys})
	}
	return result
}

func featureRefsFromProto(refs []*featurev1.FeatureRef) []FeatureRef {
	result := make([]FeatureRef, 0, len(refs))
	for _, ref := range refs {
		if ref == nil {
			result = append(result, FeatureRef{})
			continue
		}
		result = append(result, FeatureRef{
			FeatureSetKey:     ref.FeatureSetKey,
			FeatureSetVersion: int(ref.FeatureSetVersion),
			FeatureKey:        ref.FeatureKey,
		})
	}
	return result
}

func responseToProto(response BatchGetResponse) (*featurev1.BatchGetResponse, error) {
	result := &featurev1.BatchGetResponse{RequestId: response.RequestID, Values: make([]*featurev1.FeatureValue, 0, len(response.Values))}
	for _, value := range response.Values {
		encoded, err := valueToProto(value)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "encode feature value: %v", err)
		}
		result.Values = append(result.Values, encoded)
	}
	return result, nil
}

func valueToProto(value FeatureValueResult) (*featurev1.FeatureValue, error) {
	result := &featurev1.FeatureValue{
		Subject: &featurev1.Subject{Type: value.Subject.Type, Keys: value.Subject.Keys},
		Feature: &featurev1.FeatureRef{
			FeatureSetKey: value.Ref.FeatureSetKey, FeatureSetVersion: int32(value.Ref.FeatureSetVersion), FeatureKey: value.Ref.FeatureKey,
		},
		ValueType:          value.ValueType,
		Status:             valueStatusToProto(value.Status),
		SourceRunId:        value.SourceRunID,
		ContentFingerprint: value.ContentFingerprint,
		ErrorCode:          value.ErrorCode,
	}
	if !value.SnapshotAsOf.IsZero() {
		result.SnapshotAsOf = timestamppb.New(value.SnapshotAsOf)
	}
	if !value.ComputedAt.IsZero() {
		result.ComputedAt = timestamppb.New(value.ComputedAt)
	}
	if value.Status != ValueStatusSuccess {
		return result, nil
	}
	switch typed := value.Value.(type) {
	case bool:
		result.Value = &featurev1.FeatureValue_BoolValue{BoolValue: typed}
	case int64:
		result.Value = &featurev1.FeatureValue_IntValue{IntValue: typed}
	case float64:
		result.Value = &featurev1.FeatureValue_DoubleValue{DoubleValue: typed}
	case string:
		result.Value = &featurev1.FeatureValue_StringValue{StringValue: typed}
	case time.Time:
		result.Value = &featurev1.FeatureValue_TimestampValue{TimestampValue: timestamppb.New(typed)}
	case time.Duration:
		result.Value = &featurev1.FeatureValue_DurationValue{DurationValue: durationpb.New(typed)}
	case map[string]any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		result.Value = &featurev1.FeatureValue_JsonValue{JsonValue: string(encoded)}
	default:
		return nil, fmt.Errorf("unsupported successful value type %T", value.Value)
	}
	return result, nil
}

func valueStatusToProto(value ValueStatus) featurev1.ValueStatus {
	switch value {
	case ValueStatusSuccess:
		return featurev1.ValueStatus_VALUE_STATUS_SUCCESS
	case ValueStatusMissing:
		return featurev1.ValueStatus_VALUE_STATUS_MISSING
	case ValueStatusUndefined:
		return featurev1.ValueStatus_VALUE_STATUS_UNDEFINED
	case ValueStatusStale:
		return featurev1.ValueStatus_VALUE_STATUS_STALE
	case ValueStatusError:
		return featurev1.ValueStatus_VALUE_STATUS_ERROR
	default:
		return featurev1.ValueStatus_VALUE_STATUS_UNSPECIFIED
	}
}

func servingGRPCError(err error) error {
	var invalidRequest InvalidRequestError
	if errors.As(err, &invalidRequest) {
		return status.Error(codes.InvalidArgument, invalidRequest.Error())
	}
	var denied FeatureAccessDeniedError
	if errors.As(err, &denied) {
		return status.Error(codes.PermissionDenied, denied.Error())
	}
	var notFound NotFoundError
	if errors.As(err, &notFound) {
		return status.Error(codes.NotFound, notFound.Error())
	}
	var invalidState InvalidStateError
	if errors.As(err, &invalidState) {
		return status.Error(codes.FailedPrecondition, invalidState.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "feature read deadline exceeded")
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, "feature read cancelled")
	}
	return status.Error(codes.Internal, "feature serving failed")
}
