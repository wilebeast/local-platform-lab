# 一张企业卡背后的系统：从申请、授权到清算退款的 Kind 实战

> 用户点击“申请企业卡”，几秒钟后看到卡片已经激活；刷卡时系统立即返回批准或拒绝；商户随后发起清算，偶尔还会发生撤销与退款。
>
> 表面上这是几个接口，实际上它同时涉及外部渠道、异步任务、状态机、余额、冻结、幂等、乱序事件和数据库事务。
>
> 这篇文章采用教练式视角，带你在本地 Kind 集群里走完一条通用企业卡链路。所有名称、数据和渠道均为本地模拟，不对应任何真实系统。

---

## 一、先建立正确的事实模型

设计卡系统时，最容易犯的错误是先画状态枚举，或者直接把渠道返回结果写进一张“大而全”的卡表。

更可靠的做法，是先区分四类事实：

| 事实 | 本地负责人 | 典型标识 |
|---|---|---|
| 卡片业务事实 | 卡片表 | `card_id` |
| 渠道操作事实 | 渠道操作表 | `operation_id` |
| 渠道回调事实 | Provider Inbox | `event_id` |
| 资金事实 | 账户、授权、清算、退款和账本 | `authorization_id`、`settlement_id`、`refund_id` |

渠道卡号只是外部映射，不是本地卡片主键；账户余额是资金事实，也不能由卡片展示字段代替。

本实验最终形成的主链路如下：

```text
申请卡
  ↓
本地卡 pending + 渠道操作 pending
  ↓ Worker
模拟渠道创建卡
  ↓
本地卡 active
  ↓
入账 funding
  ↓
授权 authorized
  ├── 撤销 reversal → reversed
  └── 清算 settlement → settled
                              ↓
                         退款 refund → refunded
```

---

## 二、架构总览：同步入口与异步渠道操作分开

```text
客户端
  │ HTTP
  ▼
APISIX
  │
  ▼
enterprise-card-service
  ├── PostgreSQL：卡、操作、账户、账本和事件
  └── 创建渠道操作记录
              │
              ▼
      enterprise-card-worker
              │ HTTP
              ▼
      card-provider-simulator
```

申请接口不会等待渠道完成全部工作。它只保证：

1. 本地申请事实已经落库；
2. 本地卡片处于 `pending`；
3. 后续渠道操作可以由 Worker 恢复执行。

因此，HTTP 接口返回成功不等于渠道已经完成发卡。

```text
HTTP 接受成功 ≠ 渠道最终成功 ≠ 本地业务最终完成
```

---

## 三、第一关：申请卡为什么必须幂等

网络超时后，客户端通常不知道服务端是否已经成功创建卡。如果它换一个请求编号再次提交，可能生成两张卡。

本实验要求客户端提供稳定的 `Idempotency-Key`：

```text
第一次请求
  → 保存申请
  → 创建 card
  → 创建 channel_operation

完全相同的重复请求
  → 返回第一次创建的 card_id
  → 不再创建卡

相同 Key、不同参数
  → HTTP 409
```

数据库通过申请表主键和操作表唯一约束守住最后一道边界。即使两个请求并发到达，也只有一个业务事实能够成立。

---

## 四、第二关：渠道操作为什么需要持久化 Worker

渠道调用可能遇到超时、进程重启或临时失败。不能只把任务放在内存 goroutine 中，否则服务一重启，操作就丢失了。

渠道操作记录至少需要：

```text
operation_id
card_id
operation_type
status
attempt_count
next_attempt_at
last_error
```

Worker 使用数据库领取任务：

```text
pending / retry
      ↓ 原子领取
processing
  ├── 渠道成功 → completed
  ├── 可重试失败 → retry + next_attempt_at
  └── 超过上限 → dead_letter
```

多 Worker 并发时，使用 `FOR UPDATE SKIP LOCKED`，避免两台 Worker 同时执行同一个渠道操作。

生产环境还必须区分“明确失败”和“结果未知”。网络超时不等于渠道失败；如果渠道提供查询接口，应使用原请求编号先查询，再决定是否重发。

---

## 五、第三关：Provider Webhook 的验签、幂等与乱序

渠道状态回调使用以下签名原文：

```text
timestamp + "." + event_id + "." + raw_body
```

以 HMAC-SHA256 为例：

```text
Provider 与平台持有同一个 Secret
Provider 计算签名
平台使用原始请求 Body 重新计算
常量时间比较两个签名
```

接收流程：

```text
收到 HTTP 请求
  ↓
校验时间窗口
  ↓
对原始 Body 验签
  ↓
校验 Header Event ID 与 Body ID 一致
  ↓
按 event_id 幂等写入 Inbox
  ↓
Worker 异步更新卡状态
```

### 为什么还要防乱序

假设渠道先发出 `active`，后发出 `frozen`，但网络导致旧的 `active` 最后才到。如果按到达顺序覆盖，冻结卡会被错误恢复。

本地卡保存两个游标：

```text
provider_status_occurred_at
provider_status_event_id
```

状态更新只接受更晚的业务事件时间：

```text
新事件 occurred_at > 当前游标 → applied
旧事件 occurred_at <= 当前游标 → ignored_stale
```

同时，`provider_card_id` 在数据库中建立非空条件唯一索引，避免一个渠道卡号错误绑定到多张本地卡。

---

## 六、资金模型：Posted、Held 与 Available

本实验使用三个关键金额：

```text
Posted：已经入账的余额
Held：已被授权冻结、尚未清算的金额
Available：当前可继续使用的金额
```

核心公式：

```text
Available = Posted - Held
```

所有金额均使用最小货币单位的整数保存。例如 USD 25.00 表示为：

```text
amount_minor = 2500
```

避免使用浮点数处理资金。

---

## 七、第四关：Funding 入账与幂等冲突

实验先向卡账户加入 USD 1000.00：

```text
Posted = 100000
Held = 0
Available = 100000
```

同一个 `entry_id` 重放时，余额不能再次增加；如果复用同一个 `entry_id`，却把金额从 100000 改为 200000，系统返回 `409 Conflict`。

这说明幂等不是“看见相同编号就返回成功”，而是：

```text
相同编号 + 相同业务参数 → 返回原结果
相同编号 + 不同业务参数 → 拒绝
```

---

## 八、第五关：实时授权如何冻结资金

授权请求进入系统后，依次检查：

```text
HMAC 验签
  ↓
授权编号幂等检查
  ↓
卡片是否 active
  ↓
币种是否匹配
  ↓
Available 是否足够
  ↓
Held += amount
```

一笔 USD 25.00 的授权结果：

```json
{
  "decision": "approved",
  "posted_balance_minor": 100000,
  "held_amount_minor": 2500,
  "available_balance_minor": 97500
}
```

余额不足不是协议错误，而是一个正常业务决策：

```json
{
  "decision": "declined",
  "reason": "insufficient_funds"
}
```

因此实验返回 HTTP 200，并在响应体中表达批准或拒绝。签名错误才返回 401，请求结构错误返回 400。

不同授权编号会在同一账户行上串行加锁，防止两个并发授权同时看到同一份可用余额并导致超额冻结。

---

## 九、第六关：撤销只释放冻结，不改变 Posted

授权撤销适用于交易尚未清算的场景：

```text
撤销前：Posted 100000，Held 2500，Available 97500
撤销后：Posted 100000，Held    0，Available 100000
```

状态变化：

```text
decision = approved             保留原始决策
authorization_status:
authorized → reversed           记录当前生命周期
```

撤销记录使用独立 `reversal_id`。原样重放返回第一次结果；换一个撤销编号再次撤销同一授权，则被拒绝。

---

## 十、第七关：清算把冻结转换为正式扣账

清算不是再次扣减 Available。授权阶段已经降低了可用余额；清算阶段需要同时减少 Posted 和 Held：

```text
清算前：Posted 100000，Held 3000，Available 97000

清算动作：
Posted -= 3000
Held   -= 3000

清算后：Posted 97000，Held 0，Available 97000
```

如果清算时再次减少 Available，就会造成重复扣款。

清算事务同时完成：

```text
锁定原授权
  ↓
更新账户 Posted 与 Held
  ↓
写 settlement 审计记录
  ↓
写 settlement 账本
  ↓
authorization_status = settled
  ↓
统一提交
```

任一步失败，整个事务回滚。

---

## 十一、第八关：退款如何恢复余额

本轮实现全额退款：

```text
退款前：Posted 97000，Held 0，Available 97000
退款：  Posted += 3000
退款后：Posted 100000，Held 0，Available 100000
```

退款通过 `settlement_id` 找到原清算事实，而不是由客户端自由指定退款金额。这样能够在第一阶段天然防止超额退款。

退款事务同时完成：

```text
锁定 settlement_id
  ↓
确认原授权状态为 settled
  ↓
Posted += settled_amount
  ↓
写 refund 审计记录
  ↓
写 refund 账本
  ↓
authorization_status = refunded
```

退款编号原样重放时返回原结果；第二个退款编号试图退款同一清算记录会被拒绝。

---

## 十二、为什么要同时保存“原始决策”和“当前状态”

最终查询可能得到：

```text
decision = approved
authorization_status = refunded
```

二者并不冲突：

- `decision` 表示授权发生时平台确实批准了请求；
- `authorization_status` 表示它后来经历清算和退款后的当前生命周期。

如果直接把 `approved` 改成 `refunded`，就会丢失原始决策，审计时无法回答“这笔交易当初有没有被批准”。

---

## 十三、数据库事务如何守住并发边界

实验使用三类保护：

1. 业务编号主键或唯一约束；
2. PostgreSQL advisory transaction lock；
3. 账户行 `FOR UPDATE` 或带条件的原子更新。

以清算为例：

```text
两个清算请求并发
  ↓
按 authorization_id 串行
  ↓
第一个：authorized → settled，资金生效
  ↓
第二个：读取到 settled，拒绝再次清算
```

进程即使在事务中途崩溃，PostgreSQL 也会回滚未提交的账户、账本和状态变化，不会只扣余额却漏写账本。

---

## 十四、我们亲手验证了什么

本地 Kind 演练覆盖了：

- 申请卡的 HTTP 幂等；
- Worker 领取、渠道调用、重试和死信；
- Provider Webhook HMAC 验签；
- 重复回调和乱序事件；
- Funding 入账与参数冲突；
- 授权批准、余额不足和冻结卡拒绝；
- 授权重复请求不重复冻结；
- 撤销不改变 Posted；
- 清算不重复降低 Available；
- 退款恢复 Posted；
- 重复撤销、清算和退款不产生第二次资金副作用；
- 并发测试及 Go race detector；
- Argo CD `Synced / Healthy`；
- PostgreSQL 审计记录、账本和余额相互印证。

验证一套资金系统，不能只看接口返回 200。至少要形成以下证据链：

```text
HTTP 响应
  ↕
业务状态
  ↕
操作/事件审计
  ↕
账本记录
  ↕
账户最终余额
```

---

## 十五、这仍然不是生产级卡系统

本实验刻意保持范围可理解，生产化仍需补齐：

- 部分清算、多次清算、增量清算；
- 部分退款、多次退款与累计退款上限；
- 授权过期与自动释放；
- 清算金额与授权金额不一致的容差策略；
- 复式账本或双录账务；
- 渠道请求查询、未知结果恢复与对账；
- 多币种、汇率、费用和精度规则；
- 风控、额度、MCC、国家和商户限制；
- PCI DSS 边界与敏感卡数据隔离；
- 管理接口鉴权、审计、指标和告警；
- 人工补偿必须经过审批和双人复核。

本地 Funding 接口仅用于实验造数，不应直接出现在生产入口。

---

## 结语

企业卡系统真正困难的部分，不是“调用渠道创建一张卡”，而是让多类事实在重试、重复、并发、乱序和故障中仍然保持一致。

一条可靠的卡资金链路应该能够清楚回答：

```text
谁发起了操作？
渠道是否已经确认？
本地何时算完成？
资金副作用发生在哪个事务？
同一请求重放会怎样？
系统崩溃后如何恢复？
最终结果能否从审计和账本重建？
```

当这些问题都有持久化证据，而不是只存在于代码注释或日志里，我们才真正开始拥有一套可以演进的企业卡系统。
