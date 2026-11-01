package io.wilebeast.riskshadow;

import com.fasterxml.jackson.annotation.JsonProperty;
import java.io.Serializable;

public record FundActivitySnapshot(
        @JsonProperty("feature_set_key") String featureSetKey,
        @JsonProperty("feature_set_version") int featureSetVersion,
        @JsonProperty("subject_type") String subjectType,
        @JsonProperty("subject_id") String subjectId,
        String currency,
        @JsonProperty("window_start_millis") long windowStartMillis,
        @JsonProperty("window_end_millis") long windowEndMillis,
        @JsonProperty("as_of_millis") long asOfMillis,
        @JsonProperty("inflow_minor") long inflowMinor,
        @JsonProperty("outflow_minor") long outflowMinor,
        @JsonProperty("outflow_ratio") double outflowRatio,
        @JsonProperty("outflow_ratio_defined") boolean outflowRatioDefined,
        @JsonProperty("source_event_count") long sourceEventCount,
        @JsonProperty("source_run_id") String sourceRunId,
        @JsonProperty("computed_at_millis") long computedAtMillis) implements Serializable {
}
