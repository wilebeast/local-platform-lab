package io.wilebeast.riskshadow;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.time.Instant;
import java.time.format.DateTimeParseException;
import java.util.Locale;

public final class RawFundMovementParser {
    private static final String EVENT_TYPE = "funds.movement.recorded";
    private static final int EVENT_VERSION = 1;

    private final ObjectMapper mapper = new ObjectMapper();

    public FundMovement parse(String raw) {
        try {
            JsonNode root = mapper.readTree(raw);
            require(EVENT_TYPE.equals(text(root, "event_type")), "unsupported event_type");
            require(root.path("event_version").asInt(-1) == EVENT_VERSION, "unsupported event_version");

            String eventId = text(root, "event_id");
            String subjectType = text(root, "subject_type");
            String subjectId = text(root, "subject_id");
            require(!eventId.isBlank(), "event_id is required");
            require(!subjectType.isBlank() && !subjectId.isBlank(), "subject is required");

            long occurredAt = Instant.parse(text(root, "occurred_at")).toEpochMilli();
            JsonNode payload = root.path("payload");
            String direction = text(payload, "direction");
            String currency = text(payload, "currency");
            long amount = payload.path("amount_minor").asLong(0);
            require(direction.equals("inflow") || direction.equals("outflow"), "unsupported direction");
            require(currency.length() == 3 && currency.equals(currency.toUpperCase(Locale.ROOT)), "invalid currency");
            require(amount > 0, "amount_minor must be positive");

            return new FundMovement(eventId, occurredAt, subjectType, subjectId, direction, currency, amount);
        } catch (InvalidFundMovementException error) {
            throw error;
        } catch (DateTimeParseException error) {
            throw new InvalidFundMovementException("invalid occurred_at", error);
        } catch (Exception error) {
            throw new InvalidFundMovementException("invalid fund movement JSON", error);
        }
    }

    private static String text(JsonNode node, String field) {
        return node.path(field).asText("");
    }

    private static void require(boolean condition, String message) {
        if (!condition) {
            throw new InvalidFundMovementException(message);
        }
    }
}
