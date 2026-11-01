package io.wilebeast.riskshadow;

import org.apache.flink.api.common.functions.FlatMapFunction;
import org.apache.flink.util.Collector;

public final class RawEventParserFunction implements FlatMapFunction<String, FundMovement> {
    private transient RawFundMovementParser parser;

    @Override
    public void flatMap(String raw, Collector<FundMovement> output) {
        if (parser == null) {
            parser = new RawFundMovementParser();
        }
        try {
            output.collect(parser.parse(raw));
        } catch (InvalidFundMovementException ignored) {
            // S3a consumes a shared raw topic. Other event types are intentionally ignored.
            // A queryable invalid-event DLQ is a separate S3b contract.
        }
    }
}
