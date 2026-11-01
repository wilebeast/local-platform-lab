package io.wilebeast.riskshadow;

import com.fasterxml.jackson.databind.ObjectMapper;
import java.nio.charset.StandardCharsets;
import org.apache.flink.api.common.serialization.SerializationSchema;

public final class JsonSerializationSchema<T> implements SerializationSchema<T> {
    private transient ObjectMapper mapper;

    @Override
    public byte[] serialize(T value) {
        try {
            if (mapper == null) {
                mapper = new ObjectMapper();
            }
            return mapper.writeValueAsString(value).getBytes(StandardCharsets.UTF_8);
        } catch (Exception error) {
            throw new IllegalStateException("serialize shadow record", error);
        }
    }
}
