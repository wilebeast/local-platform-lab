# XXL-JOB Shadow 迁移实战

## 目标

在不影响现有 Kubernetes CronJob 的前提下，验证 XXL-JOB 是否适合承担风控离线任务的调度、执行记录、失败观察和后续补跑。

本阶段只替换“调度能力”，不宣称业务结果已经等价。

```text
现有链路（继续运行）
Kubernetes CronJob → 风控任务 → 业务结果

Shadow 链路（并行观察）
XXL-JOB Admin → 固定 Handler → Shadow 执行证据
```

## 安全边界

- Executor 只注册四个固定 Handler：三个 Shadow Handler 和一个正式离线扫描 Handler。
- 禁止通用 Shell、任意命令和任意 HTTP Job。
- Shadow Handler 不写风险评估、Finding、Restriction 或业务表。
- 管理员密码、数据库密码和 Executor Token 只存在 Kubernetes Secret。
- 原 CronJob 在业务输出完成对账前不得暂停。

## 分阶段迁移

| 阶段 | 目标 | 当前状态 |
|---|---|---|
| S0 | 保留原 CronJob | 已完成 |
| S1 | 手动触发固定 Shadow Handler | 已完成 |
| S2 | 以相同周期自动 Shadow 运行 | 已完成，离线扫描为每小时第 5 分钟 |
| S3a | 对比周期、启停和执行状态 | 已完成 |
| S3b | 对比同一 `as_of` 的业务输出 | 单样本已通过，持续观察由 S3d 收口 |
| S3c | 受控失败与历史补跑 | 已完成 |
| S3d | 连续周期观察 | 按本次实践决策视为通过 |
| S4 | 在 GitOps 中暂停原 CronJob | 已完成切换与人工回滚演练 |
| S5 | 观察后决定是否删除 CronJob | 本地移除与 Git 恢复演练已完成 |

## 为什么 S3 要拆成两层

调度成功只能证明：

```text
任务按时触发 → Executor 收到 → Handler 返回成功 → 日志可查询
```

它不能证明：

```text
读取了相同事实
使用了相同 as_of
加载了相同 Policy Version
生成了相同 Decision / Actions
```

因此 S3a 通过后仍不能进入 S4。S3b 必须让两条链路使用相同输入生成可比较输出，并保证 Shadow 不产生副作用。

## 已建立的只读 Preview 契约

离线扫描器新增 `Preview` 路径：

```text
读取事实 + 编译策略 + 计算因子 + CEL 求值
                    ↓
          返回 Decision / Actions 候选
                    ↓
       不调用 SaveOfflineRun，不持久化副作用
```

CLI 使用 `OFFLINE_DRY_RUN=true` 进入 Preview。测试会验证 Preview 的存储调用次数必须为零。

## 教练式验收

部署和基础契约：

```bash
make deploy-xxl-job-lab
make verify-xxl-job-lab
```

比较原 CronJob 与 XXL-JOB Shadow：

```bash
make compare-xxl-job-shadow
```

预期看到：

```text
Kubernetes CronJob: schedule=5 * * * * suspended=false ... succeeded=1 failed=0
XXL-JOB Shadow: schedule=0 5 * * * ? * enabled=1 trigger_code=200 handle_code=200 ...
PASS: scheduler-level shadow comparison is healthy. Business-output equivalence is not claimed yet.
```

使用已经由原离线执行路径持久化的时间点，执行只读业务结果对账：

```bash
AS_OF=2026-11-13T15:00:00Z make compare-xxl-job-business-shadow
```

对账脚本会让两条链路使用同一个 `as_of`、Policy Key 和 Policy Version，并比较：

- 主体与特征值；
- Decision 与 Reason Codes；
- 命中的 Strategy 与 Rule；
- Finding 与 Action 候选语义。

同时按本次 Shadow Run ID 验证没有新增 Run、Finding 或 Restriction。总表行数不能作为唯一证据，因为原 CronJob 仍可能在对账期间正常写入新的小时运行。

首次有效样本使用当前版本的原离线执行路径生成 `2026-11-13T15:00:00Z` 基准，包含一个 `block` 主体和一个 `pass` 主体。两个主体的 Decision、Reason Codes、Features 和 `activate_restriction` 候选完全一致，Shadow 业务写入为零。

实践中还发现，`2026-10-31T00:00:00Z` 的旧运行仍使用历史术语 `restrict`，且未保存命中的 Strategy/Rule。脚本按完整当前契约将其判定为不等价，而不是把历史字段强行映射成通过。这个结果说明对账基准必须与待迁移任务使用同一代码与契约版本。

这个结论在 S3b 时只证明一个当前版本样本等价；随后按本次本地实践的决策，
由 S3d 的连续调度观察和 S3c 的失败恢复证据共同收口，再进入 S4。

验证非法参数拒绝、失败记录和历史补跑：

```bash
AS_OF=2026-11-13T15:00:00Z make verify-xxl-job-recovery
```

这个验收先提交带有禁止字段 `command` 的任务。Admin 会保留触发记录，Executor 在参数解析阶段返回 `handle_code=500`，固定 `/offline-risk` 进程不会启动。随后使用相同历史 `as_of` 重新触发合法 Preview，并复用业务级对账脚本确认结果一致且没有 Shadow 副作用。

需要特别区分：这验证的是“非法请求不应自动重试，修正参数后可人工补跑”。数据库短暂不可用、网络抖动等瞬时故障的自动重试策略仍需单独演练。

打开调度控制台：

```bash
make xxl-job-ui
```

## 进入 S4 前的硬门槛

至少连续完成以下验收：

1. 两条链路使用同一个 `as_of`、Policy Key 和 Policy Version。
2. Subject 数量、Decision 分布、Reason Codes 和 Actions 候选完全对齐。
3. 相同 Run 重放得到相同指纹和结果。
4. Shadow 路径没有新增 Finding、Restriction 或 Action 记录。
5. 失败、超时和补跑均有可审计证据。
6. 连续观察窗口内没有不可解释差异。

只有满足这些条件，才允许在 GitOps 中把原 CronJob 设置为 `suspend: true`。删除 CronJob 属于更晚的 S5 决策。

## S4 正式切换

正式执行使用独立 Handler `offlineRiskScan`，不会把 Shadow Handler 原地改成可写模式：

```text
offlineRiskScanShadow → 永远 Dry Run
offlineRiskScan       → 明确持久化
```

切换前，先用已经存在的 `as_of` 手动触发正式 Handler，必须观察到 `dry_run=false` 与 `idempotent_replay=true`。然后执行带确认口令的可回滚切换：

```bash
CUTOVER_CONFIRM=replace-cronjob make cutover-xxl-job
make verify-xxl-job-cutover
```

切换后的唯一正式调度关系是：

```text
Kubernetes CronJob  suspend=true
XXL offlineRiskScan enabled=true
XXL Shadow schedule enabled=false
```

当前 GitOps 改动尚未提交时，脚本会暂时设置
`automated.enabled=false` 和 `selfHeal=false`，并等待 Argo CD 没有正在执行的同步操作，
然后只修改 CronJob 的 `suspend` 字段。仅设置 `selfHeal=false` 不够：已经排队的自动同步
仍可能把 CronJob 恢复成旧状态。提交并推送 GitOps 改动后必须恢复自动同步和 self-heal。

## 调度控制面的持久化边界

这套迁移同时存在两类事实来源，不能只备份其中一个：

```text
GitOps 仓库
  └─ Kubernetes 资源、CronJob suspend、镜像和资源配置

XXL-JOB MySQL
  └─ 任务定义、Cron 表达式、启停状态、执行日志和补跑记录
```

本地实验把 XXL-JOB MySQL PVC 作为调度控制面的恢复边界。PVC 丢失或换新集群时，
Bootstrap 只会恢复预置任务，且正式任务默认停止，不会自动继承切换后的生产启用状态。
因此进入 S5 删除 CronJob 之前，必须补充 XXL 数据库备份，或实现一套可审计的任务定义
重建与状态核对流程。GitOps 仍是 Kubernetes 环境事实，但它不能单独重建 XXL 调度状态。

本次切换后的历史重放证据：

```text
XXL execution log  15 → 16
Business Run       1 → 1
Finding            1 → 1
Restriction        1 → 1
idempotent_replay  true
dry_run            false
```

这说明 XXL-JOB 对每次触发生成独立调度日志，但不会替业务系统提供“业务只写一次”。
生产任务可能因超时、故障恢复或人工补跑而再次触发，业务 Handler 必须依靠稳定业务键和数据库约束实现幂等。

集群中的 `risk-control-lab` 仍显示 `Degraded`，其 `lastTransitionTime` 早于本次切换，
来源是之前故意保留的失败演练 Job。当前风险服务、XXL Admin、Executor 和正式任务均健康；
后续应把故障演练资源迁出长期 GitOps 健康口径，避免历史测试证据污染应用健康状态。

回滚顺序必须相反：先停止 XXL 正式任务，再把 CronJob 恢复为 `suspend=false`，最后确认下一次 `as_of` 只有一个正式执行者。

本次人工回滚演练记录：

```text
XXL → CronJob
cron_suspend=false  xxl_production=0  xxl_shadow=0

CronJob → XXL
cron_suspend=true   xxl_production=1  xxl_shadow=0
```

演练期间先关闭 Argo 自动同步，避免期望状态在中途把 CronJob 重新暂停；恢复到 XXL 后，
重新开启 `automated.enabled=true` 和 `selfHeal=true`。两次转换都先停旧的正式调度器，
再开启新的正式调度器，没有出现双正式执行者窗口。

## S5：何时可以退休 CronJob

暂停 CronJob 只是切换；删除 CronJob 是撤掉快速回滚通道，两者不能合并成一次操作。
S5 使用只读检查器核对：

- CronJob 仍处于暂停状态；
- XXL 正式调度开启、Shadow 调度停止，而且正式 Cron 表达式已经恢复为每小时第 5 分钟；
- 只统计 `executor_param={}` 的自然 Cron 触发，不用人工触发凑数；
- 最近若干自然周期全部成功且没有业务失败 Run；
- 最新业务 Run 没有过期；XXL 的本地 `DATETIME` 只用于展示，不承担跨时区新鲜度计算；
- 最新业务 Run 已完成。

本地实验默认至少观察三个自然周期：

```bash
make verify-xxl-job-retirement-readiness
```

生产环境应提高门槛，例如至少 24 个连续周期，并叠加七天失败率、告警、补跑和数据库
备份演练。检查通过只代表“可以进入删除评审”，不会自动删除 CronJob；实际移除仍需独立
GitOps 提交和一次从 Git 历史恢复 CronJob 的演练。

本地实验为避免等待三个小时，临时把 XXL 正式任务改为每分钟触发，完成三个由 Cron
自动产生的周期后立即恢复正式表达式。验收证据为：

```text
accelerated logs     3
successful logs      3
failed logs          0
business run rows    102 → 102
latest run rows      1 → 1
restored schedule    0 5 * * * ? *
```

这组证据验证了调度连续性和同一小时重复触发的业务幂等，但不能替代生产环境跨小时、跨天
观察。因此本地可继续做 CronJob 移除与 Git 恢复演练；生产仍必须使用更长观察窗口。

本地最终通过 GitOps 完成了三段式恢复演练：

```text
7b3af5b  删除暂停的 CronJob → Argo prune 后资源消失
5b8bc6a  revert 删除提交   → CronJob 以 suspend=true 安全恢复
3e015f8  reapply 退休变更   → Argo 再次 prune，回到最终退休态
```

最终验收使用：

```bash
make verify-xxl-job-retired
```

该检查要求 CronJob 不存在、XXL 正式调度开启、Shadow 停止、正式表达式正确、最近执行成功、
不存在任意命令或 HTTP Handler，并要求 `risk-control-lab` 与 `xxl-job-lab` 均处于 GitOps
同步状态。
