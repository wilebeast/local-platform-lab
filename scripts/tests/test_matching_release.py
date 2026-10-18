import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('release', Path(__file__).parents[1] / 'wait-matching-release.py')
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


def state(image='new-image', revision='new-commit'):
    app = {'status': {'sync': {'status': 'Synced', 'revision': revision},
                      'health': {'status': 'Healthy'}}}
    deployment = {'metadata': {'generation': 3},
                  'spec': {'replicas': 1, 'template': {'spec': {'containers': [
                      {'name': 'matching-service', 'image': image}]}}},
                  'status': {'observedGeneration': 3, 'replicas': 1,
                             'updatedReplicas': 1, 'availableReplicas': 1,
                             'readyReplicas': 1}}
    return app, deployment


class MatchingReleaseTests(unittest.TestCase):
    def test_old_healthy_release_is_not_success(self):
        app, deployment = state(image='old-image', revision='old-commit')
        self.assertFalse(release.release_ready(app, deployment, 'new-image', 'new-commit'))

    def test_target_image_but_stale_commit_is_not_success(self):
        app, deployment = state(revision='old-commit')
        self.assertFalse(release.release_ready(app, deployment, 'new-image', 'new-commit'))

    def test_target_still_rolling_is_not_success(self):
        app, deployment = state()
        deployment['status']['observedGeneration'] = 2
        self.assertFalse(release.release_ready(app, deployment, 'new-image', 'new-commit'))

    def test_target_release_ready(self):
        self.assertTrue(release.release_ready(*state(), 'new-image', 'new-commit'))

    def test_unavailable_target_is_not_success(self):
        app, deployment = state()
        deployment['status']['availableReplicas'] = 0
        self.assertFalse(release.release_ready(app, deployment, 'new-image', 'new-commit'))

    def test_old_release_times_out(self):
        with patch.object(release, 'read_resource', side_effect=lambda ns, *args: state(image='old-image', revision='old-commit')[0 if ns == 'argocd' else 1]):
            with self.assertRaises(TimeoutError):
                release.wait_for_release('new-image', 'new-commit', timeout=.02, interval=.005)


if __name__ == '__main__':
    unittest.main()
