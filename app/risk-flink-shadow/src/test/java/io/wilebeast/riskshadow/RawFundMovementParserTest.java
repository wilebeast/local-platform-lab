package io.wilebeast.riskshadow;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.time.Instant;
import org.junit.jupiter.api.Test;

final class RawFundMovementParserTest {
    private final RawFundMovementParser parser = new RawFundMovementParser();

    @Test
    void parsesTheVersionedRawContractIntoATypedFundMovement() {
        FundMovement event = parser.parse("""
            {"event_id":"evt_in_001","event_type":"funds.movement.recorded","event_version":1,
             "occurred_at":"2026-11-01T10:00:00Z","produced_at":"2026-11-01T10:00:01Z",
             "subject_type":"organization","subject_id":"org_001",
             "payload":{"direction":"inflow","currency":"USD","amount_minor":100000}}
            """);

        assertEquals("evt_in_001", event.eventId());
        assertEquals(Instant.parse("2026-11-01T10:00:00Z").toEpochMilli(), event.occurredAtMillis());
        assertEquals("organization", event.subjectType());
        assertEquals("org_001", event.subjectId());
        assertEquals("inflow", event.direction());
        assertEquals("USD", event.currency());
        assertEquals(100_000L, event.amountMinor());
    }

    @Test
    void rejectsEventsThatCannotEnterTheShadowState() {
        assertThrows(InvalidFundMovementException.class, () -> parser.parse("""
            {"event_id":"evt_bad","event_type":"funds.movement.recorded","event_version":2,
             "occurred_at":"2026-11-01T10:00:00Z","produced_at":"2026-11-01T10:00:01Z",
             "subject_type":"organization","subject_id":"org_001",
             "payload":{"direction":"outflow","currency":"USD","amount_minor":1}}
            """));

        assertThrows(InvalidFundMovementException.class, () -> parser.parse("""
            {"event_id":"evt_bad","event_type":"funds.movement.recorded","event_version":1,
             "occurred_at":"2026-11-01T10:00:00Z","produced_at":"2026-11-01T10:00:01Z",
             "subject_type":"organization","subject_id":"org_001",
             "payload":{"direction":"sideways","currency":"usd","amount_minor":0}}
            """));
    }
}
