#!/usr/bin/env python3
"""Wait for a particular GitOps commit and image, never just an old rollout."""
import argparse
import json
import subprocess
import time

CONTEXT = 'kind-local-platform'


def release_ready(application, deployment, image, revision):
    app_status = application.get('status', {})
    sync = app_status.get('sync', {})
    if (sync.get('revision') != revision or sync.get('status') != 'Synced'
            or app_status.get('health', {}).get('status') != 'Healthy'):
        return False
    metadata = deployment.get('metadata', {})
    spec = deployment.get('spec', {})
    status = deployment.get('status', {})
    containers = spec.get('template', {}).get('spec', {}).get('containers', [])
    if not any(c.get('name') == 'matching-service' and c.get('image') == image
               for c in containers):
        return False
    generation = metadata.get('generation')
    replicas = spec.get('replicas', 1)
    return (generation is not None and replicas > 0
            and status.get('observedGeneration', 0) >= generation
            and all(status.get(key, 0) == replicas for key in
                    ('replicas', 'updatedReplicas', 'availableReplicas', 'readyReplicas'))
            and not metadata.get('deletionTimestamp'))


def read_resource(namespace, kind, name):
    command = ['kubectl', '--context', CONTEXT, '-n', namespace,
               'get', kind, name, '-o', 'json']
    result = subprocess.run(command, capture_output=True, text=True, timeout=10)
    if result.returncode:
        return {}
    return json.loads(result.stdout)


def wait_for_release(image, revision, timeout=180, interval=2):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            application = read_resource('argocd', 'application', 'matching-service')
            deployment = read_resource('matching-service', 'deployment', 'matching-service')
            if release_ready(application, deployment, image, revision):
                return
        except (subprocess.TimeoutExpired, json.JSONDecodeError):
            pass
        time.sleep(min(interval, max(0, deadline - time.monotonic())))
    raise TimeoutError(f'target release not healthy within {timeout}s: {revision} {image}')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('image')
    parser.add_argument('revision')
    parser.add_argument('--timeout', type=float, default=180)
    args = parser.parse_args()
    wait_for_release(args.image, args.revision, args.timeout)
    print(f'Confirmed GitOps revision {args.revision}, image {args.image}')


if __name__ == '__main__':
    main()
