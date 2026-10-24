# 多 Provider 入站 Webhook 实践

本实验演示外部 Provider 主动调用平台时，如何在不复制多套 Webhook 服务的前提下，兼容不同签名协议，并可靠地完成验签、幂等落库、异步处理、重试和死信。

## 架构边界

```text
demo-payments ───── HMAC-SHA256 Adapter ──┐
demo-card ───────── Ed25519 Adapter ──────┼─> HTTP Receiver ─> PostgreSQL Inbox
demo-verification ─ HMAC-SHA256 Adapter ──┘                         │
                                                                  ▼
                                                             Inbox Worker
                                                       ┌──────────┼──────────┐
                                                       ▼          ▼          ▼
                                                    payment      card   verification
                                                   projection projection projection
```

- **APISIX** 只负责公网入口、路由和限流，不判断 Provider 业务事件。
- **Provider Adapter** 处理各 Provider 的签名算法和凭证格式。
- **Receiver** 根据服务端可信路由选择 Integration 和 Adapter，先验签，再解析 JSON。
- **Inbox** 保存经过认证的原始事件，是可靠异步处理的交接点。
- **Worker** 领取 Inbox 事件，更新业务投影；失败时重试，超过上限进入死信。

## 为什么不能只做一个通用 Secret

真实 Provider 的协议并不统一。实验故意保留两种认证模型：

- HMAC-SHA256：双方持有同一 Secret，适合常见 Webhook；
- Ed25519：Provider 用私钥签名，平台只保存公钥，平台数据库泄露也不能伪造 Provider 请求。

统一的是 Receiver、Inbox 和 Worker，不应强行统一所有 Provider 的签名细节。

## 请求验证顺序

```text
收到原始 HTTP Body
        │
        ├─ 根据 URL 选择可信 Integration
        ├─ 根据 X-Provider-Key-Id 选择凭证
        ├─ 校验时间戳窗口
        ├─ 使用原始字节验签
        ├─ 解析 JSON 并核对事件 ID、事件类型前缀
        └─ INSERT Inbox，唯一键去重
```

签名原文为：

```text
timestamp + "." + event_id + "." + raw_body
```

必须使用收到的原始 Body 字节验签，不能先反序列化再重新序列化，否则字段顺序和空白变化会破坏签名一致性。

## 关键数据结构

`provider_webhook_inbox` 保存：

- `integration_code`：哪个 Provider Integration；
- `external_event_id`：Provider 事件 ID；
- `credential_key_id`：本次验签使用的密钥版本；
- `raw_headers`、`raw_body`、`body_sha256`：审计与问题重放依据；
- `processing_status`：`pending / processing / retry / processed / dead_letter`；
- `attempt_count`、`next_attempt_at`、`last_error`：失败治理信息。

唯一约束 `(integration_code, external_event_id)` 保证重复投递只产生一条 Inbox 记录。重复请求仍返回 200，避免 Provider 因超时继续重试。

Worker 使用 `FOR UPDATE SKIP LOCKED` 领取任务，因此可以水平扩容而不重复占用同一条事件。投影写入同样基于资源 ID 幂等，并拒绝旧事件覆盖更新状态。

## 密钥轮换

每个 Integration 的凭证按 `key_id` 管理。轮换时短暂同时接受旧、新 Key：

```text
阶段 1：只接受 card-v1
阶段 2：同时接受 card-v1、card-v2
阶段 3：Provider 全部切换后移除 card-v1
```

请求必须显式携带 Key ID。HMAC 的 Secret 和 Ed25519 私钥不得提交到 Git；本实验只把 Ed25519 公钥放进集群，私钥仅存在本机 `.local-secrets/`。

## 本地部署与自动验收

```bash
cd /Users/skyler/Code/wildbeast/local-platform-lab
make deploy-inbound-webhook-lab
make verify-inbound-webhook-lab
```

自动验收会验证：

1. HMAC Provider 请求成功；
2. Ed25519 Provider 请求成功；
3. 相同事件重复投递返回 200，但 Inbox 只有一条；
4. 修改 Body 而复用原签名返回 401；
5. 三类事件分别写入三张投影表；
6. 不支持的事件版本经过三次尝试后进入 `dead_letter`。

## 手动观察

查看 Inbox 状态：

```bash
kubectl --context kind-local-platform -n postgres exec postgres-0 -- \
  sh -lc 'PGPASSWORD="$POSTGRES_PASSWORD" psql -U local_platform -d local_platform -P pager=off -x \
  -c "SELECT integration_code, external_event_id, event_type, processing_status, attempt_count, last_error FROM provider_webhook_inbox ORDER BY id DESC LIMIT 10;"'
```

查看三类投影：

```sql
SELECT * FROM demo_payment_projection ORDER BY updated_at DESC LIMIT 5;
SELECT * FROM demo_card_projection ORDER BY updated_at DESC LIMIT 5;
SELECT * FROM demo_verification_projection ORDER BY updated_at DESC LIMIT 5;
```

## 当前实验与生产环境的差距

- Integration 和凭证目前由环境变量装配，生产应接入受控配置与 Secret 管理系统；
- 需要增加 Provider 级监控、积压告警、死信管理与人工重放审计；
- 事件 Schema 应按 Provider 和版本进行严格校验；
- 需要评估 IP allowlist、mTLS、请求体大小和 Provider 特有的重放规则；
- 对资金类事件，投影前还应执行领域状态机与账务一致性校验。

这个实验解决的是可靠接收与分发骨架，不代表任何真实 Provider 的生产协议实现。
