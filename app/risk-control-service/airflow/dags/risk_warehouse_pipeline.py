from datetime import datetime, timezone

from airflow.sdk import DAG
from airflow.providers.cncf.kubernetes.operators.pod import KubernetesPodOperator
from airflow.utils.trigger_rule import TriggerRule
from kubernetes.client import models as k8s


RISK_IMAGE = "localhost:5001/platform-lab/risk-control-service:20261101174500"
RISK_NAMESPACE = "risk-control-lab"
FEATURE_AS_OF = "{{ dag_run.conf['as_of'] }}"
FEATURE_RUN_ID = "{{ dag_run.run_id }}:subject_fund_activity_24h:v1"


def secret_env(name, secret_name, secret_key):
    return k8s.V1EnvVar(
        name=name,
        value_from=k8s.V1EnvVarSource(
            secret_key_ref=k8s.V1SecretKeySelector(
                name=secret_name,
                key=secret_key,
            )
        ),
    )


clickhouse_env = [
    k8s.V1EnvVar(
        name="CLICKHOUSE_HTTP_ENDPOINT",
        value="http://risk-clickhouse.risk-control-lab.svc.cluster.local:8123",
    ),
    secret_env("CLICKHOUSE_USER", "risk-warehouse-clickhouse", "username"),
    secret_env("CLICKHOUSE_PASSWORD", "risk-warehouse-clickhouse", "password"),
]


with DAG(
    dag_id="risk_warehouse_pipeline",
    description="Build a point-in-time feature snapshot and evaluate one published policy.",
    start_date=datetime(2026, 11, 1, tzinfo=timezone.utc),
    schedule=None,
    catchup=False,
    max_active_runs=1,
    tags=["risk", "warehouse", "coach"],
) as dag:
    register_warehouse_run = KubernetesPodOperator(
        task_id="register_warehouse_run",
        name="risk-warehouse-run-start",
        namespace=RISK_NAMESPACE,
        image=RISK_IMAGE,
        cmds=["/risk-warehouse-run"],
        env_vars=[
            secret_env("POSTGRES_DSN", "risk-control-runtime", "postgres-dsn"),
            k8s.V1EnvVar(name="RUN_OPERATION", value="start"),
            k8s.V1EnvVar(name="DAG_RUN_ID", value="{{ dag_run.run_id }}"),
            k8s.V1EnvVar(name="FEATURE_AS_OF", value=FEATURE_AS_OF),
            k8s.V1EnvVar(name="FEATURE_SET_VERSION", value="1"),
            k8s.V1EnvVar(name="RISK_POLICY_KEY", value="warehouse_rapid_outflow"),
            k8s.V1EnvVar(name="RISK_POLICY_VERSION", value="1"),
            k8s.V1EnvVar(
                name="RISK_TRIGGER_TYPE",
                value="{{ dag_run.conf.get('trigger_type', 'manual') }}",
            ),
            k8s.V1EnvVar(
                name="RISK_RUN_REASON",
                value="{{ dag_run.conf.get('reason', '') }}",
            ),
        ],
        get_logs=True,
        in_cluster=True,
    )

    build_feature_snapshot = KubernetesPodOperator(
        task_id="build_feature_snapshot",
        name="risk-feature-snapshot",
        namespace=RISK_NAMESPACE,
        image=RISK_IMAGE,
        cmds=["/risk-feature-snapshot"],
        env_vars=[
            *clickhouse_env,
            secret_env("POSTGRES_DSN", "risk-control-runtime", "postgres-dsn"),
            k8s.V1EnvVar(name="FEATURE_AS_OF", value=FEATURE_AS_OF),
            k8s.V1EnvVar(name="FEATURE_RUN_ID", value=FEATURE_RUN_ID),
        ],
        get_logs=True,
        is_delete_operator_pod=False,
        in_cluster=True,
    )

    validate_feature_snapshot = KubernetesPodOperator(
        task_id="validate_feature_snapshot",
        name="risk-feature-validator",
        namespace=RISK_NAMESPACE,
        image=RISK_IMAGE,
        cmds=["/risk-feature-validator"],
        env_vars=[
            *clickhouse_env,
            k8s.V1EnvVar(name="FEATURE_AS_OF", value=FEATURE_AS_OF),
            k8s.V1EnvVar(name="FEATURE_SET_VERSION", value="1"),
            k8s.V1EnvVar(name="RISK_SUBJECT_TYPE", value="organization"),
            k8s.V1EnvVar(name="RISK_SUBJECT_ID", value="org_warehouse_coach"),
            k8s.V1EnvVar(name="RISK_CURRENCY", value="USD"),
        ],
        get_logs=True,
        is_delete_operator_pod=False,
        in_cluster=True,
    )

    evaluate_risk_policy = KubernetesPodOperator(
        task_id="evaluate_risk_policy",
        name="risk-feature-evaluator",
        namespace=RISK_NAMESPACE,
        image=RISK_IMAGE,
        cmds=["/risk-feature-evaluator"],
        env_vars=[
            *clickhouse_env,
            secret_env("POSTGRES_DSN", "risk-control-runtime", "postgres-dsn"),
            k8s.V1EnvVar(name="FEATURE_AS_OF", value=FEATURE_AS_OF),
            k8s.V1EnvVar(name="FEATURE_RUN_ID", value=FEATURE_RUN_ID),
            k8s.V1EnvVar(name="DAG_RUN_ID", value="{{ dag_run.run_id }}"),
            k8s.V1EnvVar(
                name="RISK_TRIGGER_TYPE",
                value="{{ dag_run.conf.get('trigger_type', 'manual') }}",
            ),
            k8s.V1EnvVar(
                name="RISK_RUN_REASON",
                value="{{ dag_run.conf.get('reason', '') }}",
            ),
            k8s.V1EnvVar(name="RISK_SUBJECT_TYPE", value="organization"),
            k8s.V1EnvVar(name="RISK_SUBJECT_ID", value="org_warehouse_coach"),
            k8s.V1EnvVar(name="RISK_CURRENCY", value="USD"),
            k8s.V1EnvVar(name="RISK_POLICY_KEY", value="warehouse_rapid_outflow"),
            k8s.V1EnvVar(name="RISK_POLICY_VERSION", value="1"),
        ],
        get_logs=True,
        is_delete_operator_pod=False,
        in_cluster=True,
    )

    finalize_warehouse_run_success = KubernetesPodOperator(
        task_id="finalize_warehouse_run_success",
        name="risk-warehouse-run-finalize-success",
        namespace=RISK_NAMESPACE,
        image=RISK_IMAGE,
        cmds=["/risk-warehouse-run"],
        env_vars=[
            secret_env("POSTGRES_DSN", "risk-control-runtime", "postgres-dsn"),
            k8s.V1EnvVar(name="RUN_OPERATION", value="finalize"),
            k8s.V1EnvVar(name="DAG_RUN_ID", value="{{ dag_run.run_id }}"),
            k8s.V1EnvVar(name="RUN_STATUS", value="succeeded"),
            k8s.V1EnvVar(name="RUN_SNAPSHOT_COUNT", value="1"),
            k8s.V1EnvVar(name="RUN_FAILURE_COUNT", value="0"),
        ],
        get_logs=True,
        in_cluster=True,
    )

    finalize_warehouse_run_failure = KubernetesPodOperator(
        task_id="finalize_warehouse_run_failure",
        name="risk-warehouse-run-finalize-failure",
        namespace=RISK_NAMESPACE,
        image=RISK_IMAGE,
        cmds=["/risk-warehouse-run"],
        env_vars=[
            secret_env("POSTGRES_DSN", "risk-control-runtime", "postgres-dsn"),
            k8s.V1EnvVar(name="RUN_OPERATION", value="finalize"),
            k8s.V1EnvVar(name="DAG_RUN_ID", value="{{ dag_run.run_id }}"),
            k8s.V1EnvVar(name="RUN_STATUS", value="failed"),
            k8s.V1EnvVar(name="RUN_SNAPSHOT_COUNT", value="0"),
            k8s.V1EnvVar(name="RUN_FAILURE_COUNT", value="1"),
            k8s.V1EnvVar(name="FAIL_AFTER_FINALIZE", value="true"),
        ],
        trigger_rule=TriggerRule.ONE_FAILED,
        get_logs=True,
        in_cluster=True,
    )

    (
        register_warehouse_run
        >> build_feature_snapshot
        >> validate_feature_snapshot
        >> evaluate_risk_policy
        >> finalize_warehouse_run_success
    )
    [build_feature_snapshot, validate_feature_snapshot, evaluate_risk_policy] >> finalize_warehouse_run_failure
