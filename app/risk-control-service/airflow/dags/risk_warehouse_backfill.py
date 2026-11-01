from datetime import datetime, timezone

from airflow.sdk import DAG
from airflow.providers.cncf.kubernetes.operators.pod import KubernetesPodOperator
from kubernetes.client import models as k8s


RISK_IMAGE = "localhost:5001/platform-lab/risk-control-service:20261101190000"
RISK_NAMESPACE = "risk-control-lab"


def secret_env(name, secret_name, secret_key):
    return k8s.V1EnvVar(
        name=name,
        value_from=k8s.V1EnvVarSource(
            secret_key_ref=k8s.V1SecretKeySelector(name=secret_name, key=secret_key)
        ),
    )


with DAG(
    dag_id="risk_warehouse_backfill",
    description="Run or resume an ordered point-in-time risk warehouse backfill.",
    start_date=datetime(2026, 11, 1, tzinfo=timezone.utc),
    schedule=None,
    catchup=False,
    max_active_runs=1,
    tags=["risk", "warehouse", "backfill", "coach"],
) as dag:
    run_backfill = KubernetesPodOperator(
        task_id="run_backfill",
        name="risk-warehouse-backfill",
        namespace=RISK_NAMESPACE,
        image=RISK_IMAGE,
        cmds=["/risk-warehouse-backfill"],
        env_vars=[
            secret_env("POSTGRES_DSN", "risk-control-runtime", "postgres-dsn"),
            k8s.V1EnvVar(
                name="CLICKHOUSE_HTTP_ENDPOINT",
                value="http://risk-clickhouse.risk-control-lab.svc.cluster.local:8123",
            ),
            secret_env("CLICKHOUSE_USER", "risk-warehouse-clickhouse", "username"),
            secret_env("CLICKHOUSE_PASSWORD", "risk-warehouse-clickhouse", "password"),
            k8s.V1EnvVar(name="BACKFILL_ID", value="{{ dag_run.conf['backfill_id'] }}"),
            k8s.V1EnvVar(name="BACKFILL_START_AS_OF", value="{{ dag_run.conf['start_as_of'] }}"),
            k8s.V1EnvVar(name="BACKFILL_END_AS_OF", value="{{ dag_run.conf['end_as_of'] }}"),
            k8s.V1EnvVar(name="BACKFILL_STEP_SECONDS", value="{{ dag_run.conf['step_seconds'] }}"),
            k8s.V1EnvVar(name="FEATURE_SET_VERSION", value="1"),
            k8s.V1EnvVar(name="RISK_POLICY_KEY", value="warehouse_rapid_outflow"),
            k8s.V1EnvVar(name="RISK_POLICY_VERSION", value="1"),
            k8s.V1EnvVar(name="RISK_SUBJECT_TYPE", value="organization"),
            k8s.V1EnvVar(name="RISK_SUBJECT_ID", value="org_warehouse_coach"),
            k8s.V1EnvVar(name="RISK_CURRENCY", value="USD"),
        ],
        get_logs=True,
        is_delete_operator_pod=False,
        in_cluster=True,
    )
