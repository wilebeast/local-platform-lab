package io.wilebeast.riskshadow;

import org.apache.flink.api.common.functions.AggregateFunction;

public final class FundActivityAggregateFunction
        implements AggregateFunction<FundMovement, FundActivityAccumulator, FundActivityAccumulator> {
    @Override
    public FundActivityAccumulator createAccumulator() {
        return new FundActivityAccumulator();
    }

    @Override
    public FundActivityAccumulator add(FundMovement event, FundActivityAccumulator accumulator) {
        accumulator.add(event);
        return accumulator;
    }

    @Override
    public FundActivityAccumulator getResult(FundActivityAccumulator accumulator) {
        return accumulator;
    }

    @Override
    public FundActivityAccumulator merge(FundActivityAccumulator left, FundActivityAccumulator right) {
        return left.merge(right);
    }
}
