#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUBE_CONTEXT="kind-local-platform"
SERVICE_DIR="${MATCHING_SERVICE_DIR:-$ROOT_DIR/../matching-service}"
kubectl --context "$KUBE_CONTEXT" -n matching-service rollout status deployment/matching-service --timeout=120s
kubectl --context "$KUBE_CONTEXT" -n matching-service port-forward service/matching-service 18089:8080 > /tmp/matching-verify-forward.log 2>&1 &
forward_pid=$!
trap 'kill "$forward_pid" >/dev/null 2>&1 || true' EXIT
python3 - "$SERVICE_DIR/examples/request.json" <<'PYCODE'
import json,sys,time,urllib.request,urllib.error
base='http://127.0.0.1:18089'
for _ in range(60):
    try:
        with urllib.request.urlopen(base+'/readyz',timeout=2) as r: ready=json.load(r)
        break
    except (OSError,urllib.error.URLError):time.sleep(.5)
else:raise SystemExit('service not ready')
request=json.load(open(sys.argv[1]))
def call(path,body,method='POST'):
    req=urllib.request.Request(base+path,data=json.dumps(body).encode(),headers={'Content-Type':'application/json'},method=method)
    try:
        with urllib.request.urlopen(req,timeout=10) as r:return r.status,json.load(r)
    except urllib.error.HTTPError as e:return e.code,json.load(e)
code,result=call('/v1/match',request)
assert code==200,(code,result)
assert result['total']==1 and result['matches'][0]['candidate']['id']=='800000000001',result
assert result['matches'][0]['strategies']==['java-jobs','member-jobs'],result
assert result['rejected_by_eligibility']==1,result
code,_=call('/v1/config',{},'PUT');assert code==401,code
request['context']['user']['skills']='bad-type'
code,_=call('/v1/match',request);assert code==422,code
request['context']['user']['skills']=['java','go'];del request['context']['user']['member']
code,result=call('/v1/match',request);assert code==200 and result['total']==0,(code,result)
print(json.dumps({'status':'passed','version':ready['version'],'checks':['matching','deduplication','eligibility','admin-auth','type-validation','missing-field']},ensure_ascii=False))
PYCODE
