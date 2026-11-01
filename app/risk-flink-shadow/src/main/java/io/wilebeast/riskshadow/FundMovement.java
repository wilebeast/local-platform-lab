package io.wilebeast.riskshadow;

import java.io.Serializable;

public record FundMovement(
        String eventId,
        long occurredAtMillis,
        String subjectType,
        String subjectId,
        String direction,
        String currency,
        long amountMinor) implements Serializable {

    public String subjectKey() {
        return subjectType + "\u001f" + subjectId + "\u001f" + currency;
    }
}
