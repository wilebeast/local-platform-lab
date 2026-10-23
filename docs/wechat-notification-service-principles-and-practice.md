# 从一封本地邮件开始：亲手拆解通知服务的安全与可靠性

> 邮件通知看起来只是“生成正文，然后调用 SMTP”。真正进入生产环境后，我们还要回答：模板放在哪里、验证码是否明文保存、重复发送如何限制、验证码能否重复使用、邮件服务宕机后如何恢复，以及我们怎样知道系统正在出错。
>
> 这篇文章采用教练式视角，带你在本地 Kind 集群中逐关验证一套通知服务。我们不只看最终代码，而是主动改变系统状态，通过返回值、Redis、PostgreSQL、Mailpit、指标和 Argo CD 观察每一层真正承担的职责。

---

## 一、先明确目标：我们不是在做一个 `SendMail` 函数

本次实践的第一阶段需要跑通两类能力：

1. 根据数据库模板发送普通业务邮件；
2. 签发并校验一次性邮件验证码。

完整链路如下：

```text
业务调用方
    │ gRPC
    ▼
notification-service
    ├── PostgreSQL：布局、多语言模板
    ├── Redis：验证码摘要、冷却、失败计数、临时封禁
    ├── SMTP：投递渲染后的 MIME 邮件
    ├── /healthz：依赖健康状态
    └── /metrics：gRPC 请求指标
              │
              ▼
           Mailpit
       本地邮件接收箱
```

各组件只承担一种主要职责：

| 组件 | 职责 |
|---|---|
| `notification-service` | 编排模板、验证码状态和邮件投递 |
| PostgreSQL | 保存可长期管理的模板数据 |
| Redis | 保存有有效期的安全状态 |
| Mailpit | 在本地接收 SMTP 邮件，不发送到公网 |
| Kubernetes | 运行并恢复工作负载 |
| Argo CD | 让集群实际状态回到 GitOps 声明状态 |

第一阶段刻意没有加入 Kafka、Outbox、重试队列和真实邮件供应商。原因很简单：先把一条同步纵向链路验证清楚，再增加异步可靠性，问题边界最容易理解。

---

## 二、第一关：数据库模板到底解决了什么

如果把邮件标题和 HTML 写死在业务代码里，每次修改文案都需要：

```text
修改代码 → 运行测试 → 构建镜像 → 部署服务
```

本次实践将公共布局和业务正文分开保存：

```text
mail_layouts
├── locale
├── shell_html
└── updated_at

mail_templates
├── template_key
├── locale
├── subject_pattern
├── body_html
├── enabled
└── updated_at
```

同一个业务模板可以有多个语言版本：

```text
workspace_welcome
├── en
└── zh-CN

email_verification
├── en
└── zh-CN
```

布局中保留一个 `{{content}}` 标记：

```html
<html>
  <body>
    <main>
      {{content}}
      <footer>Local Platform Lab</footer>
    </main>
  </body>
</html>
```

服务查询同一语言的布局和模板后进行合并：

```text
公共布局
   +
业务正文
   ↓
完整 HTML
   ↓
变量渲染
   ↓
MIME 邮件
```

模板渲染使用严格模式。如果正文引用了 `{{.DisplayName}}`，调用方却没有传入对应变量，服务会返回错误，而不是悄悄发出一封残缺邮件。

### 手动验收：模板热更新

我们把英文欢迎邮件标题临时修改为：

```text
[Hot Reload Demo] Hello, {{.DisplayName}}
```

随后没有重启 Pod、没有重新构建镜像，直接再次发送邮件。Mailpit 中立即出现：

```text
[Hot Reload Demo] Hello, Coach
```

验证完成后再把标题恢复成：

```text
Welcome, {{.DisplayName}}
```

这证明当前模板读取模型是：

```text
每次发送请求
    ↓
实时查询 PostgreSQL
    ↓
读取当前启用版本
    ↓
渲染并发送
```

它的优点是修改立即生效，缺点是每封邮件都需要查询数据库。高流量环境通常会继续增加模板缓存、版本号、草稿与发布状态、审计记录和回滚能力。

---

## 三、第二关：验证码是怎样生成的

验证码不能使用时间戳、普通伪随机数或可预测的自增值。本次实现使用操作系统安全随机源，在 `000000` 到 `999999` 之间生成六位数字：

```text
crypto/rand
    ↓
均匀随机整数
    ↓
补齐为六位字符串
```

六位验证码只有一百万种可能，因此它不能单独承担全部安全责任。系统还必须同时具备：

- 较短有效期；
- 发送冷却；
- 校验失败次数限制；
- 临时封禁；
- 一次性消费；
- 不在日志和数据库中保存明文。

本次实验采用的参数是：

| 规则 | 数值 |
|---|---:|
| 验证码有效期 | 10 分钟 |
| 重复发送冷却 | 10 秒 |
| 失败统计窗口 | 5 分钟 |
| 失败阈值 | 10 次 |
| 达到阈值后的封禁 | 2 小时 |

这些是实验参数，不应该不经风险评估直接照搬到所有生产业务。登录、提现、修改安全设置等场景可能需要不同策略。

---

## 四、第三关：为什么 Redis 不保存六位明文

用户必须在邮件中看到明文验证码，但 Redis 没必要保存明文。

服务端持有一段只在运行时 Secret 中存在的 Pepper，并计算：

```text
digest = HMAC-SHA256(
    pepper,
    recipient + "\0" + purpose + "\0" + code
)
```

Redis 保存的是十六进制摘要：

```text
mail:code:value:sign_in:<recipient_fingerprint>
    → e45cbe2f...333d6160
```

我们在 Mailpit 中看到验证码：

```text
024236
```

但 Redis 查询结果是长度为 64 的摘要，并且：

```text
plaintext_stored=no
```

这层设计带来三个效果：

1. Redis 只读泄漏时，攻击者不能直接抄走验证码；
2. 同一个验证码用于不同邮箱或不同用途时，摘要不同；
3. Pepper 不在 Git 和 Redis 中，必须同时突破运行时 Secret 才能离线枚举。

需要诚实说明：六位验证码空间很小。如果 Pepper 泄漏，攻击者可以枚举一百万种可能。因此 HMAC 摘要不是失败次数限制的替代品，而是纵深防御的一层。

### 为什么 Redis Key 里也不放完整邮箱

Key 使用邮箱 SHA-256 的前 12 字节作为指纹：

```text
原始邮箱
   ↓ SHA-256
截取 24 个十六进制字符
   ↓
recipient_fingerprint
```

这样可以避免通过 Redis Key 列表直接读取完整邮箱。它不是匿名化：如果攻击者已经拥有候选邮箱列表，仍然可以计算并比对指纹。因此 Redis 的访问控制和网络隔离仍不可省略。

---

## 五、第四关：一次性消费为什么需要原子操作

最朴素的验证码校验会执行：

```text
GET 验证码摘要
    ↓
应用层比较
    ↓
DEL 验证码
```

问题是两个并发请求可能同时在删除前读到相同摘要，导致同一个验证码成功两次。

本次实践使用 Redis Lua 脚本把比较和删除合并成一个原子操作：

```text
读取摘要
  ├── 不存在：missing
  ├── 不匹配：mismatch
  └── 匹配：立即删除并返回 matched
```

手动验收结果如下：

```text
第一次提交 024236
→ valid=true

查询 Redis
→ EXISTS=0
→ TTL=-2

第二次提交 024236
→ valid=false
```

因此验证码的生命周期不是“十分钟内无限使用”，而是：

```text
未使用且未过期 → 最多成功一次
已使用           → 立即失效
已过期           → Redis 自动删除
```

---

## 六、第五关：发送冷却与暴力尝试封禁

仅有六位验证码和十分钟 TTL 还不够。攻击者可以高速枚举，因此服务需要记录失败行为。

本次实践使用三类状态：

```text
mail:code:cooldown:<purpose>:<fingerprint>
→ 限制短时间内重复发送

mail:code:failures:<fingerprint>
→ 记录失败窗口内的错误次数

mail:code:blocked:<fingerprint>
→ 达到阈值后的临时封禁
```

我们先重放已消费的验证码，再补足错误尝试。观察到：

```text
failure count = 10
blocked exists = 1
blocked TTL ≈ 7200 秒
```

封禁后再次请求发送验证码，服务返回：

```text
ResourceExhausted: email verification is temporarily blocked
```

这里保护的是邮箱身份，而不是某一枚验证码：

```text
同一邮箱持续失败
        ↓
生成 blocked Key
        ↓
拒绝继续校验
        ↓
同时拒绝继续发送
```

为什么发送也要拒绝？否则攻击者可以一边爆破、一边持续触发邮件，造成骚扰和供应商费用。

生产环境还应叠加 IP、设备、账号、组织和全局维度的限流，避免攻击者不断更换邮箱绕过单一维度限制。

---

## 七、第六关：SMTP 失败后为什么必须补偿清理

签发验证码并不是一个单步骤动作：

```text
生成验证码
    ↓
保存摘要
    ↓
设置发送冷却
    ↓
通过 SMTP 发送邮件
```

如果最后一步失败，却保留前面的 Redis 状态，就会产生两个问题：

- 用户没有收到邮件，但系统中存在一枚有效验证码；
- 用户立即重试时，被发送冷却错误拦截。

因此同步实现采用补偿逻辑：

```text
SMTP 成功
  → 保留摘要，等待用户验证

SMTP 失败
  → 删除验证码摘要
  → 删除发送冷却
  → 返回 gRPC Unavailable
```

### 手动故障注入

我们把 Mailpit Deployment 暂时缩容为零，然后再次签发验证码，得到：

```text
Unavailable
email delivery failed
connect: connection refused
```

随后检查本次收件人的验证码和冷却 Key：

```text
EXISTS = 0
```

这证明补偿清理确实执行了。邮件服务恢复后，用户可以立即重新请求，而不会被一条失败请求遗留的状态阻塞。

需要注意：同步补偿只能解决当前第一阶段的问题。生产环境如果要求“业务事务提交后邮件最终一定送达”，应该继续引入 Transactional Outbox、消息队列、重试调度和死信处理，而不是让业务请求一直等待 SMTP。

---

## 八、第七关：为什么第一次缩容 Mailpit 没有成功

第一次执行：

```text
kubectl scale deployment/mailpit --replicas=0
```

三秒后却看到一个新的 Mailpit Pod，邮件也成功发出。

这不是命令失效，而是 Argo CD 自愈：

```text
人工把 replicas 改为 0
          ↓
集群实际状态偏离 Git 中声明的 1
          ↓
Argo CD 检测到漂移
          ↓
重新创建 Mailpit Pod
```

为了完成故障实验，我们临时关闭该 Application 的 `selfHeal`，完成验证后再恢复。最终状态为：

```text
selfHeal=true
Synced
Healthy
```

这次意外非常有价值，它帮我们区分了三层恢复能力：

| 层次 | 负责什么 |
|---|---|
| notification-service | 识别投递失败，清理验证码临时状态 |
| Kubernetes | 根据 Deployment 副本数运行和恢复 Pod |
| Argo CD | 纠正人工操作或漂移，让集群回到 GitOps 期望状态 |

不要把这三层混成一个“自动恢复”。它们观察的状态、触发条件和恢复对象完全不同。

---

## 九、第八关：健康检查和指标分别回答什么问题

服务暴露两个 HTTP 接口：

```text
/healthz
→ PostgreSQL 和 Redis 当前是否可用

/metrics
→ 各 gRPC 方法和状态累计出现多少次
```

健康检查返回：

```http
HTTP/1.1 200 OK

ok
```

说明当前服务依赖可用。指标采用 Prometheus 文本格式：

```text
notification_grpc_requests_total{
  method="/notification.v1.NotificationService/IssueEmailCode",
  status="Unavailable"
} 1
```

完整实践中观察到了三种状态：

```text
OK
→ 正常业务结果，包括 valid=false 但未达到封禁阈值

ResourceExhausted
→ 达到安全限制，被业务策略拒绝

Unavailable
→ SMTP 等基础设施依赖不可用
```

为什么错误验证码可能仍显示 `status="OK"`？

因为 gRPC 请求被正常处理，业务响应是：

```json
{
  "valid": false
}
```

只有达到封禁阈值后，服务才返回 `ResourceExhausted`。传输状态和业务结果必须分开理解。

当前指标由 `notification-service` 自己在内存中统计，Pod 重启后会归零。下一阶段可以在 Kind 集群中加入：

```text
notification-service /metrics
              ↑ 定时抓取
          Prometheus
              ↓ 查询
            Grafana
```

Prometheus 负责抓取和持久化时间序列，Grafana 负责仪表盘。之后还可以增加请求耗时直方图、投递成功率、验证码封禁次数和告警规则。

指标标签中不能加入邮箱、验证码、邮件正文或 message ID，否则既会泄露数据，也会制造高基数时间序列。

---

## 十、Secret 放在哪里

本次系统至少有两类运行时敏感信息：

- PostgreSQL 连接信息；
- 计算验证码摘要的 Pepper。

它们由部署脚本生成或注入 Kubernetes Secret，Git 仓库只保存引用关系，不保存真实值：

```text
GitOps YAML
  → 引用 notification-runtime Secret

Kubernetes Secret
  ├── postgres-dsn
  └── email-code-pepper

Pod
  → 以环境变量读取
```

本地 Kubernetes Secret 仍不是完整的生产密钥管理方案。生产环境应考虑云 KMS、External Secrets、Vault、访问审计、轮换和最小权限。

同样需要避免把验证码写入应用日志。Mailpit 能看到明文，是因为它在实验中扮演最终收件箱，而不是因为服务应该记录验证码。

---

## 十一、为什么使用 gRPC 边界

通知服务向内部调用方提供三个明确接口：

```text
SendTemplatedEmail
IssueEmailCode
VerifyEmailCode
```

这比向所有业务服务暴露底层 SMTP 参数更安全：

```text
业务服务只表达意图
  “发送 workspace_welcome”
  “为 sign_in 签发验证码”

notification-service 决定
  使用哪个模板
  如何生成验证码
  如何限流和封禁
  如何连接邮件供应商
```

如果每个业务服务都自行生成验证码、拼接 HTML、连接 SMTP，规则很快就会分叉，审计和安全修复也必须重复进行。

---

## 十二、当前实现已经解决什么，还没有解决什么

### 已完成的第一阶段

- PostgreSQL 模板与多语言布局；
- 运行时模板热更新；
- HTML 邮件和附件；
- 安全随机六位验证码；
- HMAC 摘要存储；
- 发送冷却和验证码 TTL；
- 原子一次性消费；
- 失败统计和临时封禁；
- SMTP 失败补偿；
- 健康检查和基础指标；
- Kubernetes 部署和 Argo CD 自愈。

### 进入生产前仍需补充

- 模板版本、审核、预览和回滚；
- 模板缓存及可靠的失效机制；
- Transactional Outbox；
- Kafka 或其他消息队列；
- 有上限且带退避的重试；
- 死信队列与人工重放；
- 持久化投递记录与幂等键；
- 真实邮件供应商适配器；
- 退信、投诉、退订和抑制名单；
- Secret 托管和轮换；
- Prometheus、Grafana 和 Alertmanager；
- 多维度风控及审计。

Mailpit 和当前 SMTP 适配器是教学工具，不能因为本地成功，就直接推断它们已经满足生产投递要求。

---

## 十三、这次实践真正应该带走的东西

第一，邮件通知不是一个工具函数，而是拥有独立数据、安全状态、投递边界和可观测性的业务能力。

第二，验证码安全不是某一个算法带来的，而是多层约束共同形成的：

```text
安全随机数
  + 短 TTL
  + HMAC 摘要
  + 一次性消费
  + 发送冷却
  + 失败封禁
  + Secret 隔离
```

第三，可靠性不能只测试成功路径。主动停掉邮件服务器后，我们才真正验证了失败补偿是否存在。

第四，Kubernetes 和 Argo CD 不是通知业务的一部分，但它们决定了故障演练时系统会怎样恢复。理解基础设施行为，是理解微服务整体行为的一部分。

最后，好的本地实验不是“接口返回了 200”，而是能够回答：

- 数据具体存在哪里？
- 为什么不是明文？
- 有效期由谁控制？
- 并发时是否仍然安全？
- 依赖失败会留下什么状态？
- 谁负责恢复？
- 我们如何从指标里看见它？

当这些问题都能通过手动实验得到证据时，这套系统才真正从“看起来能用”走向“可以解释、可以验证、可以继续演进”。
