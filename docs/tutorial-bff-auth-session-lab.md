# BFF 认证与会话治理：本地集群教练式实践

本文是 Passkey 实践的前置阶段。目标不是先做一个能点击的 Passkey 页面，而是先建立所有登录方式共同依赖的认证与会话底座。

## 1. 本阶段完成什么

```text
浏览器
  │ HTTPS + access_token Cookie
  ▼
APISIX
  │ 清理客户端身份头 + 网关 HMAC
  ▼
API BFF
  ├─ 使用 Ed25519 公钥独立验证 JWT
  ├─ 从 JWT 读取 sub / sid / jti / ver
  ├─ 查询 Redis 设备会话
  └─ 比对 Redis 用户版本
       │
       ▼
业务 gRPC 服务
```

Passkey、密码和 TOTP 的差异只存在于“用户如何证明身份”。认证成功以后，它们都进入相同的会话创建和 Token 签发流程。

## 2. 为什么先做这一层

- `EdDSA` 是 JWT Header 中的算法标识，本项目具体使用 Ed25519 密钥签名。
- user-service 独占私钥并签发 Token；BFF 只持有公钥，无法伪造 Token。
- `sid` 表示一次设备会话。刷新 Access Token 时保持不变。
- `jti` 表示单枚 Access Token。每次刷新都会变化。
- `ver` 表示用户认证版本。服务端递增版本即可让该用户的旧 Token 全部失效。
- BFF 独立验证 Token，因此伪造 `X-User-ID` 或绕过 APISIX 直连 BFF 都不能冒充用户。

## 3. 关键配置在哪里

| 配置 | 所在位置 | 用途 |
|---|---|---|
| `JWT_PRIVATE_KEY_B64` | user-service 的 Kubernetes Secret | PKCS#8 Ed25519 私钥，只允许签发服务读取 |
| `JWT_PUBLIC_KEY_B64` | api-bff 的 Kubernetes Secret | PKIX Ed25519 公钥，用于验签和生成 JWKS |
| `JWT_KEY_ID` | 两个服务的 Kubernetes Secret | JWT Header `kid`，用于选择验签公钥 |
| `REDIS_ADDR` | user-service、api-bff Deployment | 会话与用户版本存储 |
| `GATEWAY_SIGNATURE_KEY` | APISIX、api-bff Secret | 证明请求经过本地网关；不代替 JWT 验签 |

密钥由 `scripts/ensure-auth-secrets.sh` 在本地生成并写入 Kubernetes Secret。Git 仓库只保存 Secret 的引用，不保存密钥明文。

## 4. JWT 数据结构

JWT Header：

```json
{
  "alg": "EdDSA",
  "kid": "local-auth-1",
  "typ": "JWT"
}
```

JWT Payload 示例：

```json
{
  "iss": "local-platform-lab",
  "aud": "api-bff",
  "sub": "local-user",
  "sid": "device-session-id",
  "jti": "access-token-id",
  "ver": 1,
  "amr": ["pwd"],
  "auth_time": 1792243812,
  "iat": 1792243812,
  "exp": 1792245612
}
```

`auth_time`、`iat` 和 `exp` 都是 Unix 秒，不是毫秒。

## 5. Redis 数据结构

```text
auth:user-version:{user_id} = 1

auth:session:{sid} = {
  "ID": "device-session-id",
  "UserID": "local-user",
  "UserVersion": 1,
  "AuthMethods": ["pwd"],
  "AuthenticatedAt": 1792243812,
  "Status": "active"
}

auth:refresh:{sha256(refresh_token)} = local-user|device-session-id
```

Refresh Token 明文只存在于客户端 Cookie。Redis 保存其 SHA-256 索引和会话关联，不保存明文。

## 6. 部署

在源码仓库执行：

```bash
make deploy-auth
```

它会完成：生成本地密钥、构建并推送本地镜像、更新本地 GitOps 仓库、触发 Argo CD 同步和重启 APISIX。

## 7. 手动验收

### 7.1 打开 HTTPS 入口

```bash
make auth-ui
```

保持该终端运行，入口为 `https://localhost:18443`。

### 7.2 查看 JWKS

```bash
curl -ks https://localhost:18443/.well-known/jwks.json | jq
```

预期看到：

```json
{
  "kty": "OKP",
  "crv": "Ed25519",
  "alg": "EdDSA",
  "kid": "local-auth-1",
  "use": "sig"
}
```

`x` 是公钥材料，可以公开；不应出现私钥字段 `d`。

### 7.3 登录并取得 HttpOnly Cookie

```bash
COOKIE_JAR=/tmp/local-platform-auth-cookies.txt
curl -ksS -c "$COOKIE_JAR" \
  -H 'Origin: https://localhost:18443' \
  -H 'Content-Type: application/json' \
  -d '{"email":"demo@local.test","password":"local-demo-password"}' \
  https://localhost:18443/api/v3/auth/login | jq
```

响应体不应包含 Access Token 或 Refresh Token；它们通过 `Set-Cookie` 响应头写入 Cookie Jar。

### 7.4 手动解码 JWT

```bash
ACCESS_TOKEN="$(awk '$6 == "access_token" {print $7}' "$COOKIE_JAR")"
HEADER="$(printf '%s' "$ACCESS_TOKEN" | cut -d. -f1 | tr '_-' '/+')"
PAYLOAD="$(printf '%s' "$ACCESS_TOKEN" | cut -d. -f2 | tr '_-' '/+')"
decode_base64url() {
  value="$1"
  case $((${#value} % 4)) in
    2) value="${value}==" ;;
    3) value="${value}=" ;;
  esac
  printf '%s' "$value" | openssl base64 -d -A
}
decode_base64url "$HEADER" | jq
decode_base64url "$PAYLOAD" | jq
```

手动确认：

- Header 的 `alg` 是 `EdDSA`，`kid` 是 `local-auth-1`。
- Payload 有 `sub`、`sid`、`jti`、`ver`、`amr`、`auth_time`、`iat`、`exp`。
- Payload 不含邮箱、密码、TOTP Secret 或权限全集。

### 7.5 查看 Redis 会话

```bash
SID="$(decode_base64url "$PAYLOAD" | jq -r .sid)"
kubectl --context kind-local-platform -n redis exec deploy/redis -- \
  redis-cli GET "auth:session:$SID"
kubectl --context kind-local-platform -n redis exec deploy/redis -- \
  redis-cli GET 'auth:user-version:local-user'
```

预期会话状态为 `active`，会话中的 `UserVersion` 与用户版本一致。

### 7.6 验证正常访问

```bash
curl -ksS -b "$COOKIE_JAR" https://localhost:18443/api/v3/profile | jq
```

预期返回当前演练用户资料。

### 7.7 验证伪造身份头无效

不带 JWT，只伪造身份头：

```bash
curl -ksi \
  -H 'X-User-ID: administrator' \
  -H 'X-Authenticated: true' \
  https://localhost:18443/api/v3/profile
```

预期为 `401`。BFF 的 Actor 只能来自已验证 JWT，不能来自客户端 Header。

### 7.8 撤销单设备会话

```bash
SESSION_JSON="$(kubectl --context kind-local-platform -n redis exec deploy/redis -- redis-cli GET "auth:session:$SID")"
REVOKED_JSON="$(printf '%s' "$SESSION_JSON" | jq -c '.Status="revoked"')"
kubectl --context kind-local-platform -n redis exec deploy/redis -- \
  redis-cli SET "auth:session:$SID" "$REVOKED_JSON" KEEPTTL
curl -ksi -b "$COOKIE_JAR" https://localhost:18443/api/v3/profile
```

预期为 `401`。JWT 的密码学签名仍然有效，但服务端会话已撤销。

### 7.9 验证用户版本全局失效

重新登录获取新会话，然后执行：

```bash
kubectl --context kind-local-platform -n redis exec deploy/redis -- \
  redis-cli INCR 'auth:user-version:local-user'
curl -ksi -b "$COOKIE_JAR" https://localhost:18443/api/v3/profile
```

预期为 `401`。JWT 中的 `ver` 已落后于 Redis 当前版本。

## 8. 第一阶段通过标准

- user-service 签发 `alg=EdDSA`、带 `kid` 的 JWT。
- BFF 独立验证签名、Issuer、Audience、过期时间、`sid` 和 `ver`。
- APISIX 不再向 BFF 提供可直接信任的用户身份。
- Refresh 后 `sid` 不变而 `jti` 改变。
- 伪造身份头、撤销会话、递增版本三个负向用例均返回 `401`。
- Git 和 Kubernetes ConfigMap 中没有私钥明文。

达到这些标准后，再进入第二阶段：Passkey 注册、WebAuthn Challenge、Assertion 验签，并复用本阶段的会话和 Token 签发逻辑。
