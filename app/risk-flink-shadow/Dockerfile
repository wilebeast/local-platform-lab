FROM maven:3.9.9-eclipse-temurin-17 AS build
WORKDIR /workspace
COPY pom.xml ./
COPY src ./src
RUN mvn -q test package

FROM flink:1.20.1-java17
COPY --from=build /workspace/target/risk-flink-shadow-1.0.0.jar /opt/flink/usrlib/risk-flink-shadow.jar
