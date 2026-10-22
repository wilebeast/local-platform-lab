# 从一把 API Key 开始，亲手拆解 Ed25519 接口认证

> 一次在本地 Kind 集群中完成的教练式实践：从密钥生成、请求签名、公钥验签，到防篡改、防重放、权限撤销和凭证失效。

很多系统把 API Key 简化成一串固定字符串：调用方把它放进请求头，服务端查数据库，存在就放行。

这种方式容易接入，但也留下了一个很现实的问题：一旦这串字符串在日志、配置文件、聊天记录或代理链路中泄露，拿到它的人就拥有了与原调用方完全相同的身份。

这次实践，我们不用“把 Secret 发给服务端”的传统方案，而是使用 Ed25519 非对称签名：

- API 开发者在自己的设备上生成密钥对；
- 私钥只由开发者保存，永远不上传；
- 平台只保存公钥；
- 每次请求都使用私钥对请求关键内容签名；
- 服务端使用公钥验证签名，并继续检查时间戳、Nonce、请求体摘要和权限。

这篇文章不会只给出最终代码。我们将像上课一样，一关一关改变变量、观察结果，并由结果反推系统为什么安全。

---

## 一、先建立正确的心智模型

这套方案里有两个不同的签名链路，不能混为一谈。

### 第一层：开发者对业务请求做 Ed25519 签名

```text
API 调用方
  │
  │ 使用开发者私钥签名
  ▼
APISIX
  │
  │ 转发请求
  ▼
API BFF
  │
  ├─ 根据 key_id 查询 PostgreSQL
  └─ 使用保存的开发者公钥验签
```

这层证明的是：请求确实由持有开发者私钥的一方发出，而且方法、URI、时间戳、Nonce 和 Body 没有被修改。

### 第二层：APISIX 对转发给 BFF 的请求做 HMAC

```text
APISIX
  │
  │ 使用网关共享密钥生成 HMAC
  ▼
API BFF
  │
  └─ 验证请求确实经过可信网关
```

这层防止攻击者绕过 APISIX，直接连接 BFF 并伪造身份头。

二者解决的问题不同：

| 机制 | 证明什么 | 密钥形态 |
|---|---|---|
| Ed25519 | 外部 API 请求来自合法开发者 | 调用方持有私钥，平台保存公钥 |
| 网关 HMAC | 内部请求确实经过 APISIX | APISIX 与 BFF 持有同一共享密钥 |

本文重点是第一层，但测试时必须保留第二层，否则 BFF 的网络边界是不完整的。

---

## 二、系统里到底保存了什么

我们使用一个最小但完整的数据模型：

```text
developer_api_keys
├─ merchant_id             这把 Key 属于哪个调用主体
├─ key_id                  公钥凭证的唯一标识
├─ algo                    ed25519
├─ public_key              32 字节 Ed25519 公钥
├─ public_key_fingerprint  公钥指纹，便于核对
├─ permissions             权限点集合
├─ ip_whitelist            可选的来源 IP 限制
├─ status                  active / inactive
└─ expires_at              到期时间；0 表示当前未设置到期
```

最重要的一点是：数据库不保存开发者私钥。

私钥只存在于开发者控制的设备、CI Secret 或专业密钥管理系统中。服务端即使数据库泄露，攻击者拿到的也只是公钥，无法据此生成合法签名。

公钥指纹不是另一把密钥。它是对公钥计算 SHA-256 后得到的标识，用来确认“本地私钥对应的公钥”和“数据库中的公钥”是否为同一把。

---

## 三、第一关：生成密钥，而不是领取共享 Secret

在本地实验中，客户端生成 Ed25519 私钥：

```bash
openssl genpkey \
  -algorithm ED25519 \
  -out api-key-ed25519-private.pem
```

再从私钥导出公钥：

```bash
openssl pkey \
  -in api-key-ed25519-private.pem \
  -pubout \
  -out api-key-ed25519-public.pem
```

边界应该非常清楚：

```text
私钥：客户端保存，不上传，不可找回
公钥：提交给平台，保存在数据库
key_id：平台用来定位公钥的普通标识符
```

`key_id` 既不是公钥，也不是权限 ID。它类似证件编号：服务端先通过编号找到对应的公钥和凭证信息，再执行验签与授权。

### 手动验收：核对公钥指纹

我们从本地私钥导出公钥原始字节并计算 SHA-256，同时查询数据库中的 `public_key_fingerprint`。两边得到相同结果：

```text
本地公钥指纹：c6ac...83f3
数据库公钥指纹：c6ac...83f3
```

这一步证明：服务端保存的公钥确实对应我们手里的私钥。

---

## 四、第二关：签名的不是“一个请求”，而是一份确定的字符串

数字签名必须建立在完全确定的输入上。客户端和服务端只要有一个字节不一致，验签就会失败。

本次实验采用如下待签名内容：

```text
LOCAL-OPENAPI-V2
local-ed25519-1
GET
/api/v3/open/v2/profile
1792651158
CrvYKhzIHtqbWgW6ERL7Qg
SHA-256=47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=
```

七行内容依次是：

1. 协议版本；
2. `key_id`；
3. HTTP Method；
4. 完整请求 URI；
5. UNIX 秒级时间戳；
6. 128 位随机 Nonce 的 Base64URL 表示；
7. 请求体的 SHA-256 Digest。

然后客户端执行：

```text
signature = Ed25519.Sign(privateKey, signingString)
```

签名经过 Base64 编码后放进请求头：

```http
GET /api/v3/open/v2/profile HTTP/1.1
X-API-Key-ID: local-ed25519-1
X-API-Timestamp: 1792651158
X-API-Nonce: CrvYKhzIHtqbWgW6ERL7Qg
Digest: SHA-256=47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=
Authorization: Ed25519 signature="<Base64签名>"
```

BFF 使用同样的字段重新构造字符串，再执行：

```text
Ed25519.Verify(publicKey, signingString, signature)
```

公钥只能验证，不能签出新的合法请求。这就是它比共享 Secret 更适合开放 API 的核心原因。

第一次请求返回 `HTTP 200`，说明整条链路已经跑通：

```text
私钥签名
 → APISIX 转发
 → BFF 查询公钥
 → Ed25519 验签
 → 权限检查
 → 调用后端服务
```

---

## 五、第三关：为什么还需要 Nonce

签名只能证明请求没有被修改，却不能单独阻止“把原请求完整重发一次”。

我们将第一次成功请求原封不动再次发送，得到：

```json
HTTP 401
{
  "error": "invalid Ed25519 request"
}
```

原因是第一次请求成功后，BFF 在 Redis 中原子占用了 Nonce：

```text
openapi:nonce:local-ed25519-1:CrvYKhzIHtqbWgW6ERL7Qg
```

对应状态为：

```text
EXISTS = 1
TTL    ≈ 11 分钟
```

请求的防重放逻辑是：

```text
校验签名成功
    ↓
Redis SET nonce-key NX EX 660
    ├─ 成功：Nonce 首次出现，继续处理
    └─ 失败：Nonce 已被使用，返回 401
```

为什么既有时间戳，又有 Nonce？

- 时间戳把请求限制在很短的时间窗口内；
- Nonce 保证窗口内的同一请求也只能成功一次。

只有时间戳，攻击者仍可能在五分钟内重放；只有 Nonce，则 Redis 可能需要永久记住所有历史值。二者结合，既安全又能控制存储成本。

还有一个容易忽略的实现细节：应该在签名验证成功后再占用 Nonce。否则攻击者可以抢先提交一个伪造签名，提前消耗合法请求的 Nonce，造成拒绝服务。

---

## 六、第四关：改变 URI，签名为什么马上失效

我们保持签名不变，只把请求 URI 从：

```text
/api/v3/open/v2/profile
```

改为另一个地址。

BFF 会使用实际收到的 URI 重建待签名字符串。此时服务端字符串与客户端签名时的字符串不同，Ed25519 验签失败并返回 `401`。

同理，以下任何变化都应使签名失效：

- 把 `GET` 改成 `POST`；
- 修改路径或 Query String；
- 更换 `key_id`；
- 修改时间戳；
- 修改 Nonce；
- 修改 Digest；
- 修改请求体。

这说明签名不是一个可以复制到任意接口使用的“高级密码”，而是与当前请求上下文紧密绑定的证明。

---

## 七、第五关：Digest 如何保护请求体

对空 Body 计算 SHA-256 后，得到固定摘要：

```text
SHA-256=47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=
```

对于带 JSON Body 的请求，客户端应对收到的原始字节计算摘要：

```text
bodyDigest = Base64(SHA256(rawBodyBytes))
```

BFF 收到请求后也对原始 Body 计算一次，并要求：

```text
请求头 Digest == BFF 实际计算的 Digest
```

同时，Digest 本身又包含在 Ed25519 的待签名字符串中。因此攻击者不能只修改 Body，也不能同时篡改 Body 和 Digest：

```text
篡改 Body
  → 服务端重算 Digest 不一致
  → 401

篡改 Body + Digest
  → 待签名字符串变化
  → Ed25519 验签失败
  → 401
```

需要强调：这里保护的是原始字节，不是“解析后语义相同的 JSON”。空格、换行或字段顺序变化都会产生不同的摘要。因此客户端必须先确定最终发送的 Body 字节，再计算 Digest 和签名，签名后不能让中间件重新格式化请求体。

---

## 八、第六关：认证成功，为什么仍可能返回 403

身份认证和权限授权是两件不同的事情：

```text
认证 Authentication：你是谁？这把 Key 是否可信？
授权 Authorization：你能否调用这个接口？
```

我们的 API Key 记录中配置了权限点：

```json
["profile.read"]
```

BFF 的可信路由配置声明该接口需要 `profile.read`。当我们从 API Key 删除该权限后，使用正确私钥签名的新请求返回：

```json
HTTP 403
{
  "error": "permission denied"
}
```

这个结果非常重要：

- `401 Unauthorized`：凭证本身未通过认证；
- `403 Forbidden`：凭证已通过认证，但没有执行该动作的权限。

权限点必须由服务端路由配置决定，不能由客户端通过 `X-Required-Permission` 一类请求头声明。否则客户端完全可以删除或降低所需权限。

完整关系是：

```text
API Key local-ed25519-1
  └─ 属于调用主体 local-org
       └─ permissions
            └─ profile.read

GET /api/v3/open/v2/profile
  └─ 服务端要求 profile.read
```

恢复 `profile.read` 后，新请求重新返回 `HTTP 200`。

---

## 九、第七关：停用整把 Key 与撤销单个权限有什么区别

接着，我们保留 `profile.read`，只把凭证状态改为：

```text
status = inactive
```

即使请求由正确私钥签名，也会得到 `HTTP 401`。

因为执行顺序是：

```text
根据 key_id 查询凭证
    ↓
检查 status == active
    ├─ 否：凭证无效，返回 401
    └─ 是：继续验证签名
```

两种操作对应不同的运营语义：

| 操作 | 影响范围 | 结果 |
|---|---|---|
| 删除 `profile.read` | 只禁止需要该权限的接口 | 验证成功但授权失败，403 |
| 设置 `status=inactive` | 整把 API Key 的所有接口 | 认证失败，401 |

实际系统中，权限调整适合最小化授权；停用整把 Key 适合密钥疑似泄露、合作终止或紧急封禁。

恢复 `active` 后，新请求再次得到 `HTTP 200`。

---

## 十、第八关：active 不代表永远有效

最后，我们保持：

```text
status = active
permissions = ["profile.read"]
```

但把 `expires_at` 设置为当前时间的一分钟前。

请求返回 `HTTP 401`。因为服务端还会检查：

```text
expires_at > 0 && expires_at <= now
```

凭证虽然处于启用状态，但已经超过生命周期，因此不能继续使用。

在本地实验里，`expires_at=0` 表示未设置到期时间。生产环境更推荐强制设置有效期，并在到期前通过明确的轮换流程创建新 Key，而不是让开发者长期使用永不过期的凭证。

---

## 十一、把八关串起来：BFF 到底按什么顺序处理

最终请求链路可以压缩成下面这张图：

```text
开发者客户端
  │ 生成 timestamp、nonce、digest
  │ 使用 Ed25519 私钥签名
  ▼
APISIX
  │ 执行入口策略
  │ 使用网关共享密钥生成内部 HMAC
  ▼
API BFF
  │
  ├─ 1. 验证 APISIX 网关 HMAC
  ├─ 2. 根据 key_id 查询 API Key
  ├─ 3. 检查 status
  ├─ 4. 检查 expires_at
  ├─ 5. 检查 timestamp 时间窗口
  ├─ 6. 校验 Body Digest
  ├─ 7. 使用公钥验证 Ed25519 签名
  ├─ 8. 在 Redis 原子占用 Nonce
  ├─ 9. 建立 API Key Actor 身份上下文
  ├─ 10. 检查路由所需 permission
  └─ 11. 调用后端业务服务
```

不同失败点应该具有稳定、可观测的分类：

```text
网关不可信、Key 不存在、已停用、已过期、签名错误、重放
    → 401

身份可信，但缺少接口所需权限
    → 403

所有检查通过
    → 进入业务服务
```

对外不一定要暴露过于具体的认证失败原因，否则会帮助攻击者探测有效 `key_id`。但内部日志和审计事件应该记录明确失败阶段，并注意不要记录私钥、完整签名或其他敏感凭证。

---

## 十二、为什么这里要引入 Actor

系统同时支持用户登录和 API Key 调用，二者不应该混成一个模糊的 `user_id`。

BFF 在认证成功后建立统一 Actor：

```text
用户登录 Actor
├─ type = user
├─ user_id
├─ session_id
└─ token_id

API Key Actor
├─ type = api_key
├─ merchant_id
└─ key_id
```

Actor 的价值，是让后续授权和审计明确回答：

- 谁发起了请求？
- 是自然人会话，还是机器凭证？
- 使用了哪一个设备会话或哪一把 API Key？
- 当前主体能执行哪些权限点？

业务服务最终看到的身份，应来自经过验证的服务端上下文，而不是客户端随意填写的 `merchant_id` 或 `user_id`。

---

## 十三、这套方案解决了什么，又没有解决什么

### 已经解决

- 私钥不上传，服务端数据库只保存公钥；
- 请求方法、URI、时间戳、Nonce 和 Body 都被签名绑定；
- 时间戳限制旧请求的可用窗口；
- Redis Nonce 阻止窗口内重放；
- Digest 防止请求体被篡改；
- 权限点支持最小权限授权；
- `status` 支持整把凭证紧急停用；
- `expires_at` 支持凭证生命周期管理；
- APISIX→BFF HMAC 防止绕过网关伪造内部身份。

### 生产化仍需补齐

- API Key 创建、轮换、停用与删除的管理接口；
- 私钥只展示一次的前端交互和明确警告；
- 多把 Key 并存和无中断轮换窗口；
- IP 白名单、租户限流与配额；
- 失败原因、调用主体、`key_id`、源 IP 和请求 ID 的审计；
- 私钥进入 CI Secret、Vault、KMS/HSM 或云端密钥服务；
- PostgreSQL、Redis 故障时的失败关闭策略；
- 时钟同步和客户端 SDK；
- Canonical Query、Header 规范和协议版本升级策略；
- 泄露检测、异常调用告警和自动封禁。

特别要提醒：非对称签名不会神奇地消除私钥泄露风险。它的优势是让服务端不再持有能够冒充开发者的共享 Secret，并让每次请求都具备可验证的完整性。客户端依然必须认真保护私钥。

---

## 十四、给准备实现这套方案的团队一张检查表

上线前至少回答以下问题：

- 私钥在哪里生成，是否从未上传服务端？
- 数据库中是否只保存公钥、状态、有效期和权限？
- `key_id` 是否只用于定位凭证，而不被当作秘密？
- Method、URI、时间戳、Nonce、Digest 是否全部纳入签名？
- Query String 的规范化规则是否唯一且有测试？
- Body Digest 是否基于最终发送的原始字节？
- 时间戳允许偏差是多少？系统是否可靠同步时钟？
- Nonce 是否原子写入、自动过期，并在验签成功后才占用？
- 路由需要的权限是否完全由服务端可信配置决定？
- `inactive`、`expires_at` 和权限撤销是否能即时生效？
- 直接绕过网关访问 BFF 是否一定失败？
- 日志是否避免输出私钥、完整签名和敏感请求体？
- 是否有密钥轮换、审计、限流和异常告警？

---

## 结语：不要只验证“能调用”，要主动证明“不能绕过”

这次实验最有价值的部分，不是第一次拿到 `HTTP 200`，而是后面连续制造的失败：

```text
相同请求重放       → 401
修改 URI           → 401
篡改请求 Body      → 401
撤销权限点         → 403
停用整把 API Key   → 401
凭证超过有效期     → 401
```

安全机制不能只靠架构图证明。真正有效的学习方式，是亲手改变一个变量，观察系统在哪一层拒绝请求，再回到代码和数据结构理解原因。

当你能清楚解释“为什么这次是 401、上一次却是 403”“为什么数据库里只有公钥”“为什么时间戳和 Nonce 缺一不可”，这套 API Key 方案才真正从一组配置，变成了你可以掌控的安全边界。
