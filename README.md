# Local Platform 本地 Kubernetes + Argo CD 演练

这是一套可随时销毁的本地等价环境，用来演练当前发布链路：

`本地 CI 构建镜像 -> 本地 Registry -> 更新 GitOps 仓库 -> Argo CD 同步 -> Kubernetes 发布`

它不会连接 AWS、ECR、生产数据库、Kafka 或其他业务系统，也不会修改其他代码工作区。本地 Nacos 和 Etcd 都是集群内的独立演练实例，不连接生产环境。

## 资源建议

- Apple Silicon Mac
- 4 CPU / 8 GiB 内存 / 30 GiB 可用磁盘
- Homebrew

## 快速开始

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

部署后的请求链路为：

```text
http://localhost:18080 -> local-alb (Nginx) -> APISIX -> api-bff
```

`local-alb` 只模拟云上 ALB 的统一公网入口和反向代理位置，不模拟 AWS
控制面、计费、WAF 或云厂商健康检查。APISIX 仍使用 standalone 配置，不依赖 etcd，
适合本地演练；`apisix` 和 `local-alb` 都由 Argo CD 持续同步。

提供两个首轮演练接口：

- `GET /healthz`
- `GET /api/v3/profile`，可通过 `X-User-ID` 传入本地测试用户

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
| 部署配置仓库 | 集群内临时 `git-server` |
| Argo CD | `argocd` namespace |
| EKS | Kind 集群 `local-platform` |

第一轮只部署一个无外部依赖的演示服务，验证构建、推送、声明式发布、健康检查和回滚。真实 BFF、gRPC 服务及基础设施依赖放到第二轮，避免把“平台是否通”与“业务配置是否齐”混在一起。

## Kustomize 目录

```text
gitops/apps/platform-demo/
├── base/                  # 各环境共享的 Deployment、Service、Namespace
└── overlays/
    └── local/             # 仅本地使用的镜像、tag、副本数和 NodePort
```

Argo CD 指向 `apps/platform-demo/overlays/local`。`scripts/deploy.sh` 也只更新 local overlay 的镜像 tag，公共 base 不携带本机 Registry 地址。

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
