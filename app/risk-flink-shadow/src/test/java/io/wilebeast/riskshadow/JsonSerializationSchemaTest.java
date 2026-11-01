package io.wilebeast.riskshadow;

import static org.junit.jupiter.api.Assertions.assertEquals;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.nio.charset.StandardCharsets;
import org.junit.jupiter.api.Test;

final class JsonSerializationSchemaTest {
    @Test
    void serializesTheShadowFeatureWithTheWarehouseFieldNames() throws Exception {
        FundActivitySnapshot snapshot = new FundActivitySnapshot(
                "subject_fund_activity_24h", 1, "organization", "org_001", "USD",
                1, 2, 2, 100_000, 95_000, 0.95, true, 2, "shadow-v1", 3);

        byte[] encoded = new JsonSerializationSchema<FundActivitySnapshot>().serialize(snapshot);
        JsonNode json = new ObjectMapper().readTree(new String(encoded, StandardCharsets.UTF_8));

        assertEquals("subject_fund_activity_24h", json.path("feature_set_key").asText());
        assertEquals(100_000, json.path("inflow_minor").asLong());
        assertEquals(0.95, json.path("outflow_ratio").asDouble());
        assertEquals("shadow-v1", json.path("source_run_id").asText());
    }
}
