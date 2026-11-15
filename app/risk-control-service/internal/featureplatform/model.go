package featureplatform

import "time"

type SubjectType struct {
	Key           string
	Description   string
	JoinKeySchema map[string]string
	Owner         string
}

type Source struct {
	Key                string
	Version            int
	Domain             string
	Type               string
	SchemaRef          string
	SchemaVersion      int
	EventTimeField     string
	SubjectMapping     map[string]string
	DataClassification string
	Owner              string
}

type SourceRef struct {
	Key     string
	Version int
}

type FeatureDefinition struct {
	Key                      string
	ValueType                string
	Description              string
	Unit                     string
	Nullable                 bool
	DefinedWhen              string
	MissingSemantics         string
	MaxAgeSeconds            int64
	SupportedEvaluationModes []string
	DataClassification       string
}

type Computation struct {
	Key              string
	Mode             string
	ArtifactRef      string
	ArtifactDigest   string
	WindowSpec       map[string]any
	OutputSchema     map[string]string
	TriggerSpec      map[string]any
	SupportsBackfill bool
	Sources          []SourceRef
}

type QualityContract struct {
	FreshnessSeconds      int64
	MinimumCompleteness   float64
	MaximumReconciliation float64
	RequiredChecks        []string
}

type FeatureSetDraft struct {
	Key         string
	Version     int
	SubjectType string
	Description string
	Owner       string
	WindowType  string
	Features    []FeatureDefinition
	Computation Computation
	Quality     QualityContract
}

type FeatureSetVersion struct {
	FeatureSetDraft
	Status             string
	ContentFingerprint string
	ValidatedAt        *time.Time
	PublishedAt        *time.Time
}

type Publication struct {
	FeatureSetKey      string
	FeatureSetVersion  int
	ContentFingerprint string
	PublishedAt        time.Time
}

type FeatureRef struct {
	FeatureSetKey     string
	FeatureSetVersion int
	FeatureKey        string
	Alias             string
}

type FeatureFactorBinding struct {
	ConsumerType    string
	ConsumerKey     string
	ConsumerVersion int
	FactorKey       string
	FeatureRef      FeatureRef
}

type ConflictError struct{ Message string }

func (e ConflictError) Error() string { return e.Message }

type InvalidRequestError struct{ Message string }

func (e InvalidRequestError) Error() string { return e.Message }

type InvalidStateError struct{ Message string }

func (e InvalidStateError) Error() string { return e.Message }

type NotFoundError struct{ Message string }

func (e NotFoundError) Error() string { return e.Message }
