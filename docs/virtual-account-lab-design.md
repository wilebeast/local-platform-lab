# Virtual Account Lab — 技术设计与实证边界

## 目标

本实验独立实现一条通用虚拟收款账户纵向链路，用于验证四个边界：能力目录、异步开户、一个账户多条收款轨道、签名渠道事件到唯一入账事实。所有身份、账户号、路由号、渠道和金额均为合成数据。

## 领域边界

- `Capability`：客观产品能力，不表达某个主体是否已获批。
- `Opening`：用户申请记录，状态为 `requested → active | failed`。
- `ChannelOperation`：外部开户副作用的耐久执行记录。
- `VirtualAccount`：平台稳定资源；一个账户可绑定多条 Rail。
- `ProviderEventInbox`：验签后原样落库的渠道事件。
- `Payin`：平台统一入账单。
- `CreditEntry`：不可重复创建的入账事实；余额只是它的投影。

## 状态与事务

开户 API 只在同一事务创建 Opening 和 Operation，不同步等待渠道。Worker 锁定待处理 Operation，原子创建 Account、两条 Rail，并将 Opening 置为 active。

Webhook Receiver 先用 `timestamp.event_id.raw_body` 做 HMAC-SHA256 验签。验签失败不落 Inbox。Worker 在同一事务中创建 Payin、CreditEntry、更新 Balance，并将 Inbox 置为 processed。

唯一约束：

- `va_openings.idempotency_key`
- `va_channel_operations.opening_id`
- `va_accounts.opening_id`
- `(va_account_rails.account_id, rail_type)`
- `va_provider_event_inbox.event_id`
- `va_payins.event_id`
- `va_credit_entries.payin_id`

## 已验证

- Worker 停止时，开户稳定停在 `requested`；恢复后进入 `active`。
- 一个账户生成 `local_transfer` 与 `wire` 两条 Rail。
- 相同开户请求返回同一 Opening。
- 同一事件重复投递：1 Inbox、1 Payin、1 Credit，余额只增加一次。
- 修改已签名正文：HTTP 401，Inbox 行数为 0。

## 生产差距

实验没有实现真实渠道 SDK、主体准入、开户费用、密钥轮换、时间窗防重放、PII 加密、复式总账、对账、人工审核、重试退避、死信运营台、指标告警和灾难恢复。`va_balances` 是教学投影，不是生产总账。
