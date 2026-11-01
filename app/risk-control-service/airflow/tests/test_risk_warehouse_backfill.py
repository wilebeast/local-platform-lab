import os
import unittest

from airflow.models import DagBag


class RiskWarehouseBackfillContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.dag_bag = DagBag(
            dag_folder=os.path.join(os.path.dirname(os.path.dirname(__file__)), "dags"),
            include_examples=False,
        )

    def test_backfill_passes_explicit_range_to_persistent_coordinator(self):
        self.assertEqual({}, self.dag_bag.import_errors)
        dag = self.dag_bag.dags.get("risk_warehouse_backfill")
        self.assertIsNotNone(dag)
        self.assertEqual({"run_backfill"}, set(dag.task_ids))
        self.assertEqual(1, dag.max_active_runs)
        task = dag.get_task("run_backfill")
        self.assertEqual(["/risk-warehouse-backfill"], task.cmds)
        self.assertEqual("{{ dag_run.conf['backfill_id'] }}", self._env(task, "BACKFILL_ID"))
        self.assertEqual("{{ dag_run.conf['start_as_of'] }}", self._env(task, "BACKFILL_START_AS_OF"))
        self.assertEqual("{{ dag_run.conf['end_as_of'] }}", self._env(task, "BACKFILL_END_AS_OF"))
        self.assertEqual("{{ dag_run.conf['step_seconds'] }}", self._env(task, "BACKFILL_STEP_SECONDS"))

    @staticmethod
    def _env(task, name):
        return next(item.value for item in task.env_vars if item.name == name)


if __name__ == "__main__":
    unittest.main()
