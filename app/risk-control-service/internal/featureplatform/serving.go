package featureplatform

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ValueStatus string

const (
	ValueStatusSuccess   ValueStatus = "success"
	ValueStatusMissing   ValueStatus = "missing"
	ValueStatusUndefined ValueStatus = "undefined"
	ValueStatusStale     ValueStatus = "stale"
	ValueStatusError     ValueStatus = "error"
)

type Subject struct {
	Type string
	Keys map[string]string
}

type ServingFeatureDefinition struct {
	Ref       FeatureRef
	ValueType string
	MaxAge    time.Duration
}

type FeatureSnapshot struct {
	FeatureSetKey      string
	FeatureSetVersion  int
	Subject            Subject
	AsOf               time.Time
	ComputedAt         time.Time
	SourceRunID        string
	ContentFingerprint string
	QualityStatus      string
	Values             map[string]any
	Undefined          map[string]bool
}

type SnapshotReadRequest struct {
	RequestID         string
	FeatureSetKey     string
	FeatureSetVersion int
	FeatureKeys       []string
	Subjects          []Subject
	Cutoff            time.Time
	AsOf              time.Time
}

type FeatureCatalog interface {
	ResolveFeatures(context.Context, []FeatureRef) ([]ServingFeatureDefinition, error)
}

type FeatureSnapshotReader interface {
	ReadLatestBefore(context.Context, SnapshotReadRequest) ([]FeatureSnapshot, error)
	ReadAt(context.Context, SnapshotReadRequest) ([]FeatureSnapshot, error)
}

type FeatureAuthorizer interface {
	Authorize(context.Context, string, []ServingFeatureDefinition) error
}

type BatchGetLatestBeforeRequest struct {
	Principal string
	RequestID string
	Subjects  []Subject
	Features  []FeatureRef
	Cutoff    time.Time
	MaxAge    time.Duration
}

type BatchGetAtRequest struct {
	Principal string
	RequestID string
	Subjects  []Subject
	Features  []FeatureRef
	AsOf      time.Time
}

type FeatureValueResult struct {
	Subject            Subject
	Ref                FeatureRef
	ValueType          string
	Status             ValueStatus
	Value              any
	SnapshotAsOf       time.Time
	ComputedAt         time.Time
	SourceRunID        string
	ContentFingerprint string
	ErrorCode          string
}

type BatchGetResponse struct {
	RequestID string
	Values    []FeatureValueResult
}

type ServingService struct {
	catalog    FeatureCatalog
	reader     FeatureSnapshotReader
	authorizer FeatureAuthorizer
}

func NewServingService(
	catalog FeatureCatalog,
	reader FeatureSnapshotReader,
	authorizer FeatureAuthorizer,
) *ServingService {
	return &ServingService{catalog: catalog, reader: reader, authorizer: authorizer}
}

func (s *ServingService) BatchGetLatestBefore(
	ctx context.Context,
	request BatchGetLatestBeforeRequest,
) (BatchGetResponse, error) {
	if err := validateServingRequest(s, request.Principal, request.RequestID, request.Subjects, request.Features); err != nil {
		return BatchGetResponse{}, err
	}
	if request.Cutoff.IsZero() {
		return BatchGetResponse{}, InvalidRequestError{Message: "cutoff is required"}
	}
	if request.MaxAge < 0 {
		return BatchGetResponse{}, InvalidRequestError{Message: "max_age cannot be negative"}
	}
	definitions, err := s.catalog.ResolveFeatures(ctx, request.Features)
	if err != nil {
		return BatchGetResponse{}, fmt.Errorf("resolve feature definitions: %w", err)
	}
	if err := validateResolvedFeatures(request.Features, definitions); err != nil {
		return BatchGetResponse{}, err
	}
	if err := s.authorizer.Authorize(ctx, request.Principal, definitions); err != nil {
		return BatchGetResponse{}, fmt.Errorf("authorize feature read: %w", err)
	}
	snapshots, err := s.readGrouped(ctx, request.RequestID, request.Subjects, definitions, request.Cutoff, time.Time{}, false)
	if err != nil {
		return BatchGetResponse{}, err
	}
	return buildFeatureResponse(request.RequestID, request.Subjects, request.Features, definitions, snapshots, request.Cutoff, request.MaxAge, false), nil
}

func (s *ServingService) BatchGetAt(
	ctx context.Context,
	request BatchGetAtRequest,
) (BatchGetResponse, error) {
	if err := validateServingRequest(s, request.Principal, request.RequestID, request.Subjects, request.Features); err != nil {
		return BatchGetResponse{}, err
	}
	if request.AsOf.IsZero() {
		return BatchGetResponse{}, InvalidRequestError{Message: "as_of is required"}
	}
	definitions, err := s.catalog.ResolveFeatures(ctx, request.Features)
	if err != nil {
		return BatchGetResponse{}, fmt.Errorf("resolve feature definitions: %w", err)
	}
	if err := validateResolvedFeatures(request.Features, definitions); err != nil {
		return BatchGetResponse{}, err
	}
	if err := s.authorizer.Authorize(ctx, request.Principal, definitions); err != nil {
		return BatchGetResponse{}, fmt.Errorf("authorize feature read: %w", err)
	}
	snapshots, err := s.readGrouped(ctx, request.RequestID, request.Subjects, definitions, time.Time{}, request.AsOf, true)
	if err != nil {
		return BatchGetResponse{}, err
	}
	return buildFeatureResponse(request.RequestID, request.Subjects, request.Features, definitions, snapshots, request.AsOf, 0, true), nil
}

type featureSetGroup struct {
	key         string
	version     int
	featureKeys []string
}

func (s *ServingService) readGrouped(
	ctx context.Context,
	requestID string,
	subjects []Subject,
	definitions []ServingFeatureDefinition,
	cutoff time.Time,
	asOf time.Time,
	exact bool,
) ([]FeatureSnapshot, error) {
	groups := make([]featureSetGroup, 0)
	groupIndexes := make(map[string]int)
	for _, definition := range definitions {
		identity := featureSetIdentity(definition.Ref.FeatureSetKey, definition.Ref.FeatureSetVersion)
		index, exists := groupIndexes[identity]
		if !exists {
			index = len(groups)
			groupIndexes[identity] = index
			groups = append(groups, featureSetGroup{key: definition.Ref.FeatureSetKey, version: definition.Ref.FeatureSetVersion})
		}
		groups[index].featureKeys = append(groups[index].featureKeys, definition.Ref.FeatureKey)
	}
	var snapshots []FeatureSnapshot
	for _, group := range groups {
		query := SnapshotReadRequest{
			RequestID:         requestID,
			FeatureSetKey:     group.key,
			FeatureSetVersion: group.version,
			FeatureKeys:       group.featureKeys,
			Subjects:          subjects,
			Cutoff:            cutoff,
			AsOf:              asOf,
		}
		var rows []FeatureSnapshot
		var err error
		if exact {
			rows, err = s.reader.ReadAt(ctx, query)
		} else {
			rows, err = s.reader.ReadLatestBefore(ctx, query)
		}
		if err != nil {
			return nil, fmt.Errorf("read feature set %s version %d: %w", group.key, group.version, err)
		}
		snapshots = append(snapshots, rows...)
	}
	return snapshots, nil
}

func buildFeatureResponse(
	requestID string,
	subjects []Subject,
	refs []FeatureRef,
	definitions []ServingFeatureDefinition,
	snapshots []FeatureSnapshot,
	cutoff time.Time,
	requestMaxAge time.Duration,
	exactAsOf bool,
) BatchGetResponse {
	definitionByRef := make(map[string]ServingFeatureDefinition, len(definitions))
	for _, definition := range definitions {
		definitionByRef[featureRefIdentity(definition.Ref)] = definition
	}
	snapshotBySubjectAndSet := make(map[string]FeatureSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		identity := subjectIdentity(snapshot.Subject) + "\x00" + featureSetIdentity(snapshot.FeatureSetKey, snapshot.FeatureSetVersion)
		snapshotBySubjectAndSet[identity] = snapshot
	}
	response := BatchGetResponse{RequestID: requestID, Values: make([]FeatureValueResult, 0, len(subjects)*len(refs))}
	for _, subject := range subjects {
		for _, ref := range refs {
			definition := definitionByRef[featureRefIdentity(ref)]
			result := FeatureValueResult{Subject: subject, Ref: ref, ValueType: definition.ValueType, Status: ValueStatusMissing}
			snapshot, exists := snapshotBySubjectAndSet[subjectIdentity(subject)+"\x00"+featureSetIdentity(ref.FeatureSetKey, ref.FeatureSetVersion)]
			if !exists {
				response.Values = append(response.Values, result)
				continue
			}
			result.SnapshotAsOf = snapshot.AsOf
			result.ComputedAt = snapshot.ComputedAt
			result.SourceRunID = snapshot.SourceRunID
			result.ContentFingerprint = snapshot.ContentFingerprint
			if exactAsOf && !snapshot.AsOf.Equal(cutoff) {
				result.Status = ValueStatusError
				result.ErrorCode = "snapshot_as_of_mismatch"
			} else if snapshot.AsOf.After(cutoff) {
				result.Status = ValueStatusError
				result.ErrorCode = "snapshot_after_cutoff"
			} else if snapshot.QualityStatus != "passed" {
				result.Status = ValueStatusError
				result.ErrorCode = "quality_not_passed"
			} else if isStale(cutoff, snapshot.AsOf, definition.MaxAge, requestMaxAge) {
				result.Status = ValueStatusStale
				result.ErrorCode = "max_age_exceeded"
			} else if snapshot.Undefined[ref.FeatureKey] {
				result.Status = ValueStatusUndefined
			} else if value, found := snapshot.Values[ref.FeatureKey]; found {
				if valueMatchesType(value, definition.ValueType) {
					result.Status = ValueStatusSuccess
					result.Value = value
				} else {
					result.Status = ValueStatusError
					result.ErrorCode = "value_type_mismatch"
				}
			}
			response.Values = append(response.Values, result)
		}
	}
	return response
}

func validateServingRequest(
	service *ServingService,
	principal string,
	requestID string,
	subjects []Subject,
	features []FeatureRef,
) error {
	if service == nil || service.catalog == nil || service.reader == nil || service.authorizer == nil {
		return fmt.Errorf("feature catalog, snapshot reader and authorizer are required")
	}
	if principal == "" || requestID == "" || len(subjects) == 0 || len(features) == 0 {
		return InvalidRequestError{Message: "principal, request_id, subjects and features are required"}
	}
	for _, subject := range subjects {
		if subject.Type == "" || len(subject.Keys) == 0 {
			return InvalidRequestError{Message: "complete feature subject is required"}
		}
	}
	for _, ref := range features {
		if ref.FeatureSetKey == "" || ref.FeatureSetVersion <= 0 || ref.FeatureKey == "" {
			return InvalidRequestError{Message: "complete versioned feature references are required"}
		}
	}
	return nil
}

func validateResolvedFeatures(refs []FeatureRef, definitions []ServingFeatureDefinition) error {
	if len(refs) != len(definitions) {
		return fmt.Errorf("feature catalog returned %d definitions for %d references", len(definitions), len(refs))
	}
	resolved := make(map[string]ServingFeatureDefinition, len(definitions))
	for _, definition := range definitions {
		if definition.ValueType == "" || definition.MaxAge <= 0 {
			return fmt.Errorf("resolved feature requires type and positive max age")
		}
		resolved[featureRefIdentity(definition.Ref)] = definition
	}
	for _, ref := range refs {
		if _, exists := resolved[featureRefIdentity(ref)]; !exists {
			return fmt.Errorf("feature catalog did not resolve %s", featureRefIdentity(ref))
		}
	}
	return nil
}

func isStale(cutoff, asOf time.Time, definitionMaxAge, requestMaxAge time.Duration) bool {
	maxAge := definitionMaxAge
	if requestMaxAge > 0 && requestMaxAge < maxAge {
		maxAge = requestMaxAge
	}
	return cutoff.Sub(asOf) > maxAge
}

func valueMatchesType(value any, valueType string) bool {
	switch valueType {
	case "bool":
		_, ok := value.(bool)
		return ok
	case "int":
		_, ok := value.(int64)
		return ok
	case "double", "decimal":
		_, ok := value.(float64)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "timestamp":
		_, ok := value.(time.Time)
		return ok
	case "duration":
		_, ok := value.(time.Duration)
		return ok
	case "json":
		_, ok := value.(map[string]any)
		return ok
	default:
		return false
	}
}

func featureRefIdentity(ref FeatureRef) string {
	return featureSetIdentity(ref.FeatureSetKey, ref.FeatureSetVersion) + "\x00" + ref.FeatureKey
}

func featureSetIdentity(key string, version int) string {
	return fmt.Sprintf("%s\x00%d", key, version)
}

func subjectIdentity(subject Subject) string {
	keys := make([]string, 0, len(subject.Keys))
	for key := range subject.Keys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(subject.Type)
	for _, key := range keys {
		builder.WriteByte(0)
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(subject.Keys[key])
	}
	return builder.String()
}
