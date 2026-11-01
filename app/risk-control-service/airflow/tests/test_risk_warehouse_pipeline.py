import os
import unittest

from airflow.models import DagBag


class RiskWarehousePipelineContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        dag_folder = os.path.join(
            os.path.dirname(os.path.dirname(__file__)),
            "dags",
        )
        cls.dag_bag = DagBag(dag_folder=dag_folder, include_examples=False)

    def test_pipeline_passes_one_fixed_as_of_through_snapshot_and_evaluation(self):
        self.assertEqual({}, self.dag_bag.import_errors)

        dag = self.dag_bag.dags.get("risk_warehouse_pipeline")
        self.assertIsNotNone(dag)
        self.assertEqual(
            {
                "register_warehouse_run",
                "build_feature_snapshot",
                "validate_feature_snapshot",
                "evaluate_risk_policy",
                "finalize_warehouse_run_success",
                "finalize_warehouse_run_failure",
            },
            set(dag.task_ids),
        )

        register = dag.get_task("register_warehouse_run")
        snapshot = dag.get_task("build_feature_snapshot")
        validator = dag.get_task("validate_feature_snapshot")
        evaluator = dag.get_task("evaluate_risk_policy")
        success_finalizer = dag.get_task("finalize_warehouse_run_success")
        failure_finalizer = dag.get_task("finalize_warehouse_run_failure")

        self.assertEqual({"build_feature_snapshot"}, register.downstream_task_ids)
        self.assertEqual({"register_warehouse_run"}, snapshot.upstream_task_ids)
        self.assertEqual({"validate_feature_snapshot", "finalize_warehouse_run_failure"}, snapshot.downstream_task_ids)
        self.assertEqual({"build_feature_snapshot"}, validator.upstream_task_ids)
        self.assertEqual({"evaluate_risk_policy", "finalize_warehouse_run_failure"}, validator.downstream_task_ids)
        self.assertEqual({"validate_feature_snapshot"}, evaluator.upstream_task_ids)
        self.assertEqual({"finalize_warehouse_run_success", "finalize_warehouse_run_failure"}, evaluator.downstream_task_ids)
        self.assertEqual("all_success", success_finalizer.trigger_rule)
        self.assertEqual("one_failed", failure_finalizer.trigger_rule)
        self.assertEqual(["/risk-warehouse-run"], register.cmds)
        self.assertEqual(["/risk-warehouse-run"], success_finalizer.cmds)
        self.assertEqual(["/risk-warehouse-run"], failure_finalizer.cmds)
        self.assertEqual(["/risk-feature-snapshot"], snapshot.cmds)
        self.assertEqual(["/risk-feature-validator"], validator.cmds)
        self.assertEqual(["/risk-feature-evaluator"], evaluator.cmds)

        expected_as_of = "{{ dag_run.conf['as_of'] }}"
        expected_feature_run_id = (
            "{{ dag_run.run_id }}:subject_fund_activity_24h:v1"
        )
        self.assertEqual(expected_as_of, self._env(snapshot, "FEATURE_AS_OF"))
        self.assertEqual(expected_as_of, self._env(validator, "FEATURE_AS_OF"))
        self.assertEqual(expected_as_of, self._env(evaluator, "FEATURE_AS_OF"))
        self.assertEqual(
            expected_feature_run_id,
            self._env(snapshot, "FEATURE_RUN_ID"),
        )
        self.assertEqual(
            expected_feature_run_id,
            self._env(evaluator, "FEATURE_RUN_ID"),
        )
        self.assertEqual(
            "{{ dag_run.run_id }}",
            self._env(evaluator, "DAG_RUN_ID"),
        )
        self.assertEqual(
            "{{ dag_run.conf.get('trigger_type', 'manual') }}",
            self._env(evaluator, "RISK_TRIGGER_TYPE"),
        )
        self.assertEqual(
            "{{ dag_run.conf.get('reason', '') }}",
            self._env(evaluator, "RISK_RUN_REASON"),
        )
        self.assertEqual("start", self._env(register, "RUN_OPERATION"))
        self.assertEqual("finalize", self._env(success_finalizer, "RUN_OPERATION"))
        self.assertEqual("finalize", self._env(failure_finalizer, "RUN_OPERATION"))
        self.assertEqual("succeeded", self._env(success_finalizer, "RUN_STATUS"))
        self.assertEqual("failed", self._env(failure_finalizer, "RUN_STATUS"))
        self.assertEqual("true", self._env(failure_finalizer, "FAIL_AFTER_FINALIZE"))

    @staticmethod
    def _env(task, name):
        return next(item.value for item in task.env_vars if item.name == name)


if __name__ == "__main__":
    unittest.main()
