package io.wilebeast.riskshadow;

import java.time.Duration;
import org.apache.flink.api.common.eventtime.WatermarkStrategy;
import org.apache.flink.api.common.serialization.SimpleStringSchema;
import org.apache.flink.connector.base.DeliveryGuarantee;
import org.apache.flink.connector.kafka.sink.KafkaRecordSerializationSchema;
import org.apache.flink.connector.kafka.sink.KafkaSink;
import org.apache.flink.connector.kafka.source.KafkaSource;
import org.apache.flink.connector.kafka.source.enumerator.initializer.OffsetsInitializer;
import org.apache.flink.streaming.api.datastream.DataStream;
import org.apache.flink.streaming.api.datastream.SingleOutputStreamOperator;
import org.apache.flink.streaming.api.environment.StreamExecutionEnvironment;
import org.apache.flink.streaming.api.windowing.assigners.SlidingEventTimeWindows;
import org.apache.flink.streaming.api.windowing.time.Time;
import org.apache.flink.util.OutputTag;

public final class RiskFlinkShadowJob {
    static final OutputTag<FundMovement> LATE_EVENTS =
            new OutputTag<>("risk-shadow-late-events") {};

    private RiskFlinkShadowJob() {}

    public static void main(String[] args) throws Exception {
        String brokers = env("KAFKA_BOOTSTRAP_SERVERS", "kafka.kafka.svc.cluster.local:9092");
        String inputTopic = env("RISK_SHADOW_INPUT_TOPIC", "platform.raw.events.v1");
        String outputTopic = env("RISK_SHADOW_OUTPUT_TOPIC", "risk.shadow.feature.subject_fund_activity_24h.v1");
        String lateTopic = env("RISK_SHADOW_LATE_TOPIC", "risk.shadow.late-events.v1");
        String groupID = env("RISK_SHADOW_GROUP_ID", "risk-flink-shadow-v1");
        String sourceRunID = env("RISK_SHADOW_SOURCE_RUN_ID", "flink-shadow-v1");
        long watermarkSeconds = longEnv("RISK_SHADOW_WATERMARK_SECONDS", 120);
        long allowedLatenessSeconds = longEnv("RISK_SHADOW_ALLOWED_LATENESS_SECONDS", 300);
        long windowHours = longEnv("RISK_SHADOW_WINDOW_HOURS", 24);
        long slideMinutes = longEnv("RISK_SHADOW_SLIDE_MINUTES", 1);

        StreamExecutionEnvironment execution = StreamExecutionEnvironment.getExecutionEnvironment();
        execution.enableCheckpointing(10_000);

        KafkaSource<String> source = KafkaSource.<String>builder()
                .setBootstrapServers(brokers)
                .setTopics(inputTopic)
                .setGroupId(groupID)
                .setStartingOffsets(OffsetsInitializer.earliest())
                .setValueOnlyDeserializer(new SimpleStringSchema())
                .build();

        DataStream<FundMovement> movements = execution
                .fromSource(source, WatermarkStrategy.noWatermarks(), "raw-fund-events")
                .flatMap(new RawEventParserFunction())
                .name("validate-fund-movement")
                .assignTimestampsAndWatermarks(
                        WatermarkStrategy.<FundMovement>forBoundedOutOfOrderness(Duration.ofSeconds(watermarkSeconds))
                                .withIdleness(Duration.ofMinutes(1))
                                // Flink windows are [start,end); the warehouse contract is (start,end].
                                .withTimestampAssigner((event, ignored) -> event.occurredAtMillis() - 1))
                .name("event-time-and-watermark");

        SingleOutputStreamOperator<FundActivitySnapshot> snapshots = movements
                .keyBy(FundMovement::subjectKey)
                .window(SlidingEventTimeWindows.of(Time.hours(windowHours), Time.minutes(slideMinutes)))
                .allowedLateness(Time.seconds(allowedLatenessSeconds))
                .sideOutputLateData(LATE_EVENTS)
                .aggregate(new FundActivityAggregateFunction(), new FundActivityWindowFunction(sourceRunID))
                .name("subject-fund-activity-24h-shadow");

        snapshots.sinkTo(jsonSink(brokers, outputTopic)).name("shadow-feature-kafka");
        snapshots.getSideOutput(LATE_EVENTS).sinkTo(jsonSink(brokers, lateTopic)).name("late-event-kafka");

        execution.execute("risk-subject-fund-activity-shadow");
    }

    private static <T> KafkaSink<T> jsonSink(String brokers, String topic) {
        return KafkaSink.<T>builder()
                .setBootstrapServers(brokers)
                .setRecordSerializer(KafkaRecordSerializationSchema.<T>builder()
                        .setTopic(topic)
                        .setValueSerializationSchema(new JsonSerializationSchema<>())
                        .build())
                .setDeliveryGuarantee(DeliveryGuarantee.AT_LEAST_ONCE)
                .build();
    }

    private static String env(String key, String fallback) {
        String value = System.getenv(key);
        return value == null || value.isBlank() ? fallback : value;
    }

    private static long longEnv(String key, long fallback) {
        return Long.parseLong(env(key, Long.toString(fallback)));
    }
}
