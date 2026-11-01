package io.wilebeast.riskshadow;

import java.io.Serializable;

public final class FundActivityAccumulator implements Serializable {
    private String subjectType;
    private String subjectId;
    private String currency;
    private long inflowMinor;
    private long outflowMinor;
    private long eventCount;

    public void add(FundMovement event) {
        if (eventCount == 0) {
            subjectType = event.subjectType();
            subjectId = event.subjectId();
            currency = event.currency();
        } else if (!subjectType.equals(event.subjectType()) ||
                !subjectId.equals(event.subjectId()) || !currency.equals(event.currency())) {
            throw new IllegalArgumentException("one accumulator may only contain one subject and currency");
        }
        if ("inflow".equals(event.direction())) {
            inflowMinor += event.amountMinor();
        } else if ("outflow".equals(event.direction())) {
            outflowMinor += event.amountMinor();
        } else {
            throw new IllegalArgumentException("unsupported direction: " + event.direction());
        }
        eventCount++;
    }

    public FundActivityAccumulator merge(FundActivityAccumulator other) {
        if (other.eventCount == 0) {
            return this;
        }
        if (eventCount == 0) {
            subjectType = other.subjectType;
            subjectId = other.subjectId;
            currency = other.currency;
        } else if (!subjectType.equals(other.subjectType) ||
                !subjectId.equals(other.subjectId) || !currency.equals(other.currency)) {
            throw new IllegalArgumentException("cannot merge accumulators for different subjects or currencies");
        }
        inflowMinor += other.inflowMinor;
        outflowMinor += other.outflowMinor;
        eventCount += other.eventCount;
        return this;
    }

    public FundActivitySnapshot snapshot(long windowStart, long windowEnd, long computedAt, String sourceRunId) {
        boolean ratioDefined = inflowMinor > 0;
        double ratio = ratioDefined ? (double) outflowMinor / (double) inflowMinor : 0;
        return new FundActivitySnapshot(
                "subject_fund_activity_24h", 1, subjectType, subjectId, currency,
                windowStart, windowEnd, windowEnd, inflowMinor, outflowMinor,
                ratio, ratioDefined, eventCount, sourceRunId, computedAt);
    }
}
