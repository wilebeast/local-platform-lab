# 风险数据面教练实战：从 DWD 明细到 DWS 汇总

> 固定数据时间：`as_of = 2026-11-08T00:00:00Z`
>
> 观察窗口：`(2026-11-07T00:00:00Z, 2026-11-08T00:00:00Z]`
> 数据性质：本地固定演练数据，不含真实客户或渠道信息

## 1. 这次实践要看懂什么

```text
业务事件
   ↓ 规范化为一种稳定事实
DWD 明细表
   ↓ 按主体、主题和窗口聚合
DWS History
   ↓ 同一业务键的最新投影
DWS Current
   ↓ 形成标准 Feature Snapshot
Feature Provider → Factor → Rule → Decision
```

重点不是表的数量，而是三条边界：

1. DWD 一行是一件业务事实，不能把卡授权和资金移动混成一种事实；
2. DWS 一行是一个主体在一个风险主题、一个窗口、一个 `as_of` 下的汇总；
3. 每组 DWS 都有独立 `source_run_id`，并在 PostgreSQL 注册 Run 和内容指纹。

## 2. 业务域与表的对应关系

| 风险主题 | DWD 明细 | DWS 汇总 |
|---|---|---|
| 资金活动 | `dwd_fund_movements_v2` | `dws_subject_fund_activity_*` |
| 商户收单 | `dwd_acquiring_payment_attempts_v1` | `dws_merchant_acquiring_activity_*` |
| 企业卡 | `dwd_card_authorizations_v1` | `dws_subject_card_activity_*` |
| 提现 | `dwd_withdrawal_orders_v1` | `dws_subject_withdrawal_activity_*` |
| 账户安全 | `dwd_account_security_events_v1` | `dws_subject_security_activity_*` |
| 客户认证 | `dwd_customer_verification_events_v1` | `dws_subject_profile_risk_*` |
| 主体关联 | `dwd_subject_relationship_edges_v1` | `dws_subject_relationship_risk_*` |
| 渠道质量 | `dwd_provider_channel_events_v1` | `dws_provider_channel_quality_*` |

每个 `*` 都包含 `history` 和 `current` 两张物理表。History 保存每次 Run 的证据；Current 是可从 History 重建的快速查询投影。

## 3. 准备查询入口

```bash
export CLICKHOUSE_POD="$(
  kubectl --context kind-local-platform \
    -n risk-control-lab \
    get pods \
    -l app=risk-clickhouse \
    -o jsonpath='{.items[0].metadata.name}'
)"
```

## 4. 第一关：观察 DWD 逐笔事实

先看同一个企业来自不同业务域的资金事实：

```bash
kubectl --context kind-local-platform \
  -n risk-control-lab exec "$CLICKHOUSE_POD" -- \
  clickhouse-client --query "
    SELECT
      event_id,
      source_domain,
      aggregate_type,
      direction,
      amount_minor,
      toDateTime(occurred_at_millis / 1000, 'UTC') AS occurred_at
    FROM risk_warehouse.dwd_fund_movements_v2 FINAL
    ORDER BY occurred_at_millis
    FORMAT PrettyCompact
  "
```

预期看到四条事实：收单结算和入金形成 inflow，卡清算和提现完成形成 outflow。

再看为什么卡授权没有直接进入资金移动表：

```bash
kubectl --context kind-local-platform \
  -n risk-control-lab exec "$CLICKHOUSE_POD" -- \
  clickhouse-client --query "
    SELECT
      authorization_id,
      merchant_country,
      amount_minor,
      decision,
      decline_reason
    FROM risk_warehouse.dwd_card_authorizations_v1 FINAL
    ORDER BY occurred_at_millis
    FORMAT PrettyCompact
  "
```

卡授权表达“批准或拒绝”，资金移动表达“钱已经流入或流出”，二者生命周期不同，所以必须分表。

## 5. 第二关：观察 DWD 如何变成 DWS

资金主题的真实聚合公式是：

```text
inflow_minor  = sumIf(amount_minor, direction = inflow)
outflow_minor = sumIf(amount_minor, direction = outflow)
ratio         = outflow_minor / inflow_minor
```

查询结果：

```bash
kubectl --context kind-local-platform \
  -n risk-control-lab exec "$CLICKHOUSE_POD" -- \
  clickhouse-client --query "
    SELECT
      subject_id,
      window_type,
      inflow_minor,
      outflow_minor,
      round(outflow_ratio, 4) AS outflow_ratio,
      source_event_count,
      source_run_id
    FROM risk_warehouse.dws_subject_fund_activity_current_v2 FINAL
    FORMAT PrettyCompact
  "
```

预期：

```text
inflow_minor       = 200000
outflow_minor      = 175000
outflow_ratio      = 0.875
source_event_count = 4
```

## 6. 第三关：比较不同风险主题

### 收单

```sql
SELECT total_attempts, succeeded_attempts, failed_attempts,
       attempted_amount_minor, succeeded_amount_minor,
       round(success_rate, 4) AS success_rate, unique_payers
FROM risk_warehouse.dws_merchant_acquiring_activity_current_v1 FINAL;
```

预期成功率为 `3 / 5 = 0.6`。

### 企业卡

```sql
SELECT total_authorizations, approved_authorizations,
       declined_authorizations, cross_border_count,
       unique_merchants, round(decline_rate, 4) AS decline_rate
FROM risk_warehouse.dws_subject_card_activity_current_v1 FINAL;
```

### 提现

```sql
SELECT total_withdrawals, completed_withdrawals, failed_withdrawals,
       pending_withdrawals, new_destination_count,
       round(failure_rate, 4) AS failure_rate
FROM risk_warehouse.dws_subject_withdrawal_activity_current_v1 FINAL;
```

### 账户安全

```sql
SELECT total_events, failed_login_count, new_device_login_count,
       password_reset_count, administrator_add_count,
       high_risk_event_count
FROM risk_warehouse.dws_subject_security_activity_current_v1 FINAL;
```

### 客户认证与关联风险

```sql
SELECT total_events, verification_failed_count,
       material_change_count, mismatch_field_count,
       current_verification_status, current_risk_level
FROM risk_warehouse.dws_subject_profile_risk_current_v1 FINAL;

SELECT active_relation_count, shared_device_count, shared_ip_count,
       shared_destination_count, high_risk_related_subject_count
FROM risk_warehouse.dws_subject_relationship_risk_current_v1 FINAL;
```

### 渠道质量

```sql
SELECT provider_code, channel_domain, total_operations,
       succeeded_operations, failed_operations, timeout_operations,
       round(success_rate, 4) AS success_rate,
       round(p95_latency_ms, 2) AS p95_latency_ms,
       duplicate_callback_count, reconciliation_mismatch_count
FROM risk_warehouse.dws_provider_channel_quality_current_v1 FINAL
ORDER BY provider_code, channel_domain;
```

## 7. 第四关：查看 History、Current 和血缘

同一个逻辑 DWS 的两个物理用途：

```sql
SELECT source_run_id, content_fingerprint, computed_at_millis
FROM risk_warehouse.dws_subject_fund_activity_history_v2 FINAL;

SELECT source_run_id, content_fingerprint, computed_at_millis
FROM risk_warehouse.dws_subject_fund_activity_current_v2 FINAL;
```

再去 PostgreSQL 查询 Run 注册：

```bash
kubectl --context kind-local-platform \
  -n postgres exec postgres-0 -- \
  sh -lc 'PGPASSWORD="$POSTGRES_PASSWORD" psql \
    -U local_platform \
    -d local_platform \
    -P pager=off \
    -x \
    -c "SELECT
          r.run_id,
          r.feature_set_key,
          r.as_of,
          r.status,
          r.snapshot_count,
          f.content_fingerprint
        FROM risk_control.feature_runs r
        JOIN risk_control.feature_run_fingerprints f
          ON f.source_run_id = r.run_id
        WHERE r.run_id LIKE '\''risk-domain-demo-20261108-%'\''
        ORDER BY r.feature_set_key;"'
```

这条查询证明：ClickHouse 的每次历史汇总不是一批无法追溯的数字，而是对应一条 PostgreSQL 权威 Run 注册记录。

## 8. 第五关：查看 Feature Manifest

```sql
SELECT
  feature_set_key,
  subject_type,
  subject_id,
  as_of_millis,
  source_run_id,
  values_json
FROM risk_warehouse.feature_snapshot_history_v2 FINAL
ORDER BY feature_set_key, subject_id;
```

类型化 DWS 负责高效计算和查询；Feature Manifest 负责统一血缘、内容指纹和下游发现。决策引擎不会直接扫描所有 DWD，而是由 Feature Provider 从这些稳定快照中解析出类型化 Factor Value。

## 9. 一键重放与验收

文件顺序：

```text
001_risk_warehouse_v2.sql          基础资金 DWD/DWS
002_risk_domain_tables.sql         其他业务域 DWD/DWS
003_seed_risk_domain_dwd.sql       固定 DWD 明细
004_build_risk_domain_dws.sql      INSERT SELECT 聚合
005_verify_risk_domain_data.sql    行数、指标、血缘口径断言
```

Seed 与 Build 使用固定事件 ID、业务键、`source_run_id` 和 ReplacingMergeTree。重复执行后，`FINAL` 查询仍得到同一组逻辑事实和指标；这用于演练幂等，不代表生产环境可以绕过 PostgreSQL Run 指纹注册直接写入 History。
