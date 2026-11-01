package io.wilebeast.riskshadow;

import org.apache.flink.configuration.Configuration;
import org.apache.flink.streaming.api.functions.windowing.ProcessWindowFunction;
import org.apache.flink.streaming.api.windowing.windows.TimeWindow;
import org.apache.flink.util.Collector;

public final class FundActivityWindowFunction
        extends ProcessWindowFunction<FundActivityAccumulator, FundActivitySnapshot, String, TimeWindow> {
    private final String sourceRunId;

    public FundActivityWindowFunction(String sourceRunId) {
        this.sourceRunId = sourceRunId;
    }

    @Override
    public void process(
            String key,
            Context context,
            Iterable<FundActivityAccumulator> values,
            Collector<FundActivitySnapshot> output) {
        FundActivityAccumulator accumulator = values.iterator().next();
        output.collect(accumulator.snapshot(
                context.window().getStart(), context.window().getEnd(),
                context.currentProcessingTime(), sourceRunId));
    }
}
