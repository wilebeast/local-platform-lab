# Local Platform 本地 Kubernetes + Argo CD 演练

这是一套可随时销毁的本地等价环境，用来演练当前发布链路：

`本地 CI 构建镜像 -> 本地 Registry -> 更新 GitOps 仓库 -> Argo CD 同步 -> Kubernetes 发布`

它不会连接 AWS、ECR、生产数据库、Kafka 或其他业务系统，也不会修改其他代码工作区。本地 Nacos 和 Etcd 都是集群内的独立演练实例，不连接生产环境。

## 资源建议

- Apple Silicon Mac
- 4 CPU / 8 GiB 内存 / 30 GiB 可用磁盘
- Homebrew

## 快速开始

先将代码仓库和 GitOps 仓库克隆到同一父目录：

```text
wildbeast/
├── local-platform-lab/
└── local-platform-gitops/
```

默认脚本读取 `../local-platform-gitops`；如目录不同，可设置 `GITOPS_DIR`。

```bash
make doctor
make bootstrap
make deploy
make verify
```

部署 Go-Zero API BFF：

```bash
make deploy-api-bff
make verify-api-bff
```

部署最小 gRPC 用户服务，并让 API BFF 调用它：

```bash
make deploy-user-service
make deploy-api-bff
make verify-user-service
```

部署服务注册中心：

```bash
make deploy-service-discovery
make verify-service-discovery
make deploy-user-service
make deploy-api-bff
make verify-service-registration
```

两个组件都由 Argo CD 管理，可在 Applications 页面看到：

- Nacos：`nacos.nacos.svc.cluster.local:8848`
- Etcd：`etcd-client.etcd.svc.cluster.local:2379`

`user-service` 启动后会同时注册到 Nacos 和 Etcd；`api-bff` 默认从 Nacos
发现健康实例，并使用 gRPC round-robin。通过 Deployment 的
`DISCOVERY_PROVIDER` 可切换为 `etcd` 或 `dns`，其中 DNS 地址仍保留为
`user-service.user-service.svc.cluster.local:9001`。

服务发现链路为：

```text
HTTP client -> local-alb -> APISIX -> api-bff
  -> Nacos（默认）/ Etcd / Kubernetes DNS
  -> user-service Pod gRPC
```

此时 `/api/v3/profile` 的真实协议链路为：

```text
HTTP client -> local-alb -> APISIX -> api-bff -> gRPC UserService.GetProfile
```

部署本地 ALB 模拟入口和 APISIX：

```bash
make deploy-local-gateway
make verify-local-gateway
```

部署并演练分层用户鉴权链路：

```bash
make deploy-auth
make verify-auth
```

保持本地 HTTPS 入口运行：

```bash
make auth-ui
```

入口地址为 <https://localhost:18443>。当前集群使用本地自签名证书，命令行请求需加
`--insecure`；若重新创建 Kind 集群，`kind/cluster.yaml` 也已预留 18443 端口。

演练账号仅用于本地环境：

- 邮箱：`demo@local.test`
- 密码：`local-demo-password`

`user-service` 签发 30 分钟有效的 HS256 Access Token，并将 7 天有效的
Refresh Token 哈希存入 Redis。刷新采用一次性轮换，旧 Refresh Token 再次使用会
返回 401。JWT 签名密钥和 APISIX 到 BFF 的网关签名密钥在首次部署时随机生成，
只存入 Kubernetes Secret，不写入 Git。

浏览器登录采用 Cookie-only 模式：Access Token 和 Refresh Token 只写入
`Secure + HttpOnly + SameSite=Strict` Cookie，不出现在 JSON 响应中。刷新和退出
还必须通过 Origin 与 CSRF 双重校验；APISIX 对登录路由按来源 IP 限流。

鉴权链路为：

```text
HTTP client
  -> local-alb
  -> APISIX（校验 JWT、清除伪造身份头、签名网关请求）
  -> api-bff（验证网关签名、构造用户上下文）
  -> gRPC user-service
  -> Redis（仅管理 Refresh Token）
```

登录示例：

```bash
curl --insecure -c cookies.txt -X POST https://localhost:18443/api/v3/auth/login \
  -H 'Origin: https://localhost:18443' \
  -H 'Content-Type: application/json' \
  -d '{"email":"demo@local.test","password":"local-demo-password"}'
```

浏览器会自动携带 Cookie；命令行可以使用 `curl --insecure -b cookies.txt` 请求
`GET /api/v3/profile`。

部署后的请求链路为：

```text
https://localhost:18443 -> local-alb (Nginx TLS) -> APISIX -> api-bff
```

`local-alb` 只模拟云上 ALB 的统一公网入口和反向代理位置，不模拟 AWS
控制面、计费、WAF 或云厂商健康检查。APISIX 仍使用 standalone 配置，不依赖 etcd，
适合本地演练；`apisix` 和 `local-alb` 都由 Argo CD 持续同步。

提供两个首轮演练接口：

- `GET /healthz`
- `POST /api/v3/auth/login`
- `POST /api/v3/auth/refresh`
- `POST /api/v3/auth/logout`
- `GET /api/v3/profile`，必须携带有效 Access Token；客户端传入的 `X-User-ID` 会被 APISIX 清除

> 安全约束：所有集群命令都显式使用 `kind-local-platform`，不会依赖当前 kubectl context，也不会操作远端 EKS。

打开 Argo CD：

```bash
make argocd-ui
```

浏览器访问 <http://localhost:8443>。用户名为 `admin`，临时密码由命令直接打印。

修改 `demo/site/index.html` 后重新发布：

```bash
make deploy
make verify
```

演练 GitOps 回滚：

```bash
make rollback
make verify
```

全部销毁：

```bash
make destroy
```

## 和线上链路的对应关系

| 线上 | 本地演练 |
|---|---|
| GitHub Actions | `scripts/deploy.sh` |
| AWS ECR | `localhost:5001` Registry |
| 部署配置仓库 | 独立仓库 `../local-platform-gitops`，并同步到集群内临时 `git-server` |
| Argo CD | `argocd` namespace |
| EKS | Kind 集群 `local-platform` |

第一轮只部署一个无外部依赖的演示服务，验证构建、推送、声明式发布、健康检查和回滚。真实 BFF、gRPC 服务及基础设施依赖放到第二轮，避免把“平台是否通”与“业务配置是否齐”混在一起。

## Kustomize 目录

```text
../local-platform-gitops/apps/platform-demo/
├── base/                  # 各环境共享的 Deployment、Service、Namespace
└── overlays/
    └── local/             # 仅本地使用的镜像、tag、副本数和 NodePort
```

Argo CD 指向 `apps/platform-demo/overlays/local`。`scripts/deploy.sh` 也只更新 local overlay 的镜像 tag，公共 base 不携带本机 Registry 地址。

GitOps 清单位于独立同级仓库 `../local-platform-gitops`。如需使用其他位置，可通过 `GITOPS_DIR` 指定绝对路径。业务代码仓库不再跟踪 Kubernetes 发布清单。

## 本机网络说明

默认使用公共 DNS，并且不配置网络代理。如所在网络需要代理，可在启动前设置：

```bash
export LOCAL_HTTP_PROXY=http://<colima-host-address>:<port>
export COLIMA_DNS_PRIMARY=1.1.1.1
export COLIMA_DNS_SECONDARY=8.8.8.8
make bootstrap
```

代理地址必须是 Kind 节点能够访问的地址，通常不能使用节点内部的
`127.0.0.1`。这些设置只通过环境变量提供，不需要提交个人网络配置。

## 2026-10-17 实测结果

- Kind `local-platform`：运行中
- Argo CD：安装完成
- `platform-demo`：2/2 Pod Ready
- Argo Application：`Synced / Healthy`
- `/healthz`：返回 `{"status":"ok"}`
- v2 发布：成功
- Git revert 回滚到上一镜像：成功，页面内容已恢复

### 通用匹配服务（实验版）

新增 CEL + Roaring Bitmap 单分片匹配服务，支持 Schema、策略选择、候选检索、准入校验和 PVC 快照恢复。`make deploy-matching-service` 发布，`make verify-matching-service` 验证。服务代码已迁至独立仓库 `../matching-service`。部署脚本默认从该相邻目录构建，也可通过 `MATCHING_SERVICE_DIR` 指定位置。接口、示例及规模边界见独立项目 README.md。
