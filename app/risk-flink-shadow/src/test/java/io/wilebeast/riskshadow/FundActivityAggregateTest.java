package io.wilebeast.riskshadow;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import org.junit.jupiter.api.Test;

final class FundActivityAggregateTest {
    @Test
    void aggregatesTypedEventsIntoTheSharedFeatureContract() {
        FundActivityAccumulator accumulator = new FundActivityAccumulator();
        accumulator.add(new FundMovement("in", 1, "organization", "org_001", "inflow", "USD", 100_000));
        accumulator.add(new FundMovement("out", 2, "organization", "org_001", "outflow", "USD", 95_000));

        FundActivitySnapshot snapshot = accumulator.snapshot(1_000, 86_401_000, 86_401_000, "shadow-job-1");

        assertEquals("subject_fund_activity_24h", snapshot.featureSetKey());
        assertEquals(1, snapshot.featureSetVersion());
        assertEquals("organization", snapshot.subjectType());
        assertEquals("org_001", snapshot.subjectId());
        assertEquals("USD", snapshot.currency());
        assertEquals(1_000, snapshot.windowStartMillis());
        assertEquals(86_401_000, snapshot.windowEndMillis());
        assertEquals(100_000, snapshot.inflowMinor());
        assertEquals(95_000, snapshot.outflowMinor());
        assertEquals(0.95, snapshot.outflowRatio());
        assertTrue(snapshot.outflowRatioDefined());
        assertEquals(2, snapshot.sourceEventCount());
        assertEquals("shadow-job-1", snapshot.sourceRunId());
    }

    @Test
    void marksRatioUndefinedWhenThereIsNoInflow() {
        FundActivityAccumulator accumulator = new FundActivityAccumulator();
        accumulator.add(new FundMovement("out", 2, "organization", "org_002", "outflow", "USD", 500));

        FundActivitySnapshot snapshot = accumulator.snapshot(0, 60_000, 60_000, "shadow-job-1");

        assertFalse(snapshot.outflowRatioDefined());
        assertEquals(0, snapshot.outflowRatio());
    }

    @Test
    void refusesToMergeAccumulatorsForDifferentSubjects() {
        FundActivityAccumulator left = new FundActivityAccumulator();
        left.add(new FundMovement("left", 1, "organization", "org_001", "inflow", "USD", 100));
        FundActivityAccumulator right = new FundActivityAccumulator();
        right.add(new FundMovement("right", 2, "organization", "org_002", "inflow", "USD", 100));

        assertThrows(IllegalArgumentException.class, () -> left.merge(right));
    }
}
