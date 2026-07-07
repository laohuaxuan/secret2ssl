# secret2ssl

`secret2ssl` 是一个运行在 Kubernetes 内外均可的 Go 服务，用于监听指定 TLS Secret（`tls.crt`/`tls.key`）变化，并自动同步到阿里云 SSL（CAS）证书管理。
建议配合cert-manager、alidns-webhook、k8s-replicator完成证书的全托管自理（自签、验证、更换、同步），以下是整体的配合架构图：
<img width="980" height="833" alt="image" src="https://github.com/user-attachments/assets/8ab6b78f-2dd9-4c8f-9de1-612c55116395" />

cert-manager、alidns-webhook、k8s-replicator均为开源工具，请自行查阅和配置。



## 功能特性

- 监听多个 Kubernetes TLS Secret 的新增/更新事件
- 启动时执行一次全量比对：阿里云不存在证书时自动补传
- 证书存在时先删除旧证书再上传新证书，避免同名冲突
- 支持两种运行方式：
  - 集群内（`in_cluster: true`，使用 ServiceAccount）
  - 集群外（`kubeconfig`）
- 支持两种阿里云凭证来源：
  - `credential_secret`（推荐）
  - `config.yaml` 明文（兼容/兜底）
- 支持阿里云 CAS 外网/内网 Endpoint 切换

## 工作流程

1. 加载 `config/config.yaml`
2. 读取阿里云凭证（优先从 `credential_secret`）
3. 初始化阿里云 CAS 客户端
4. 启动前执行一次初始同步（仅同步阿里云不存在的证书）
5. 监听 Secret 变化并实时同步到阿里云

## 目录结构

```text
.
├── config/
│   └── config.yaml
├── deploy/
│   ├── Dockerfile
│   ├── values/dev.yaml
│   └── yamls/
│       ├── configmap.yaml
│       └── serviceaccount.yaml
├── pkg/
│   ├── aliyun/
│   │   └── ssl.go
│   ├── config/
│   │   └── config.go
│   └── kubernetes/
│       └── client.go
└── main.go
```

## 运行要求

- Go `1.25.6+`
- 可访问 Kubernetes API（集群内或 kubeconfig）
- 阿里云账号具备 SSL 证书管理相关权限
- 目标 Secret 必须是 TLS 类型，且包含：
  - `tls.crt`
  - `tls.key`

## 配置说明

主配置文件：`config/config.yaml`

### 示例

```yaml
secrets:
  - name: example-com-tls
    namespace: demo
    ali_ssl_name: example-com-tls

aliyun:
  access_key_id: ""
  access_key_secret: ""
  region: cn-hangzhou
  ssl_endpoint: cas.aliyuncs.com
  ssl_internal_endpoint: ""
  use_internal_endpoint: false
  credential_secret:
    namespace: demo
    name: aliyun-cas-credential
    access_key_id_key: access_key_id
    access_key_secret_key: access_key_secret

kubernetes:
  kubeconfig: "/path/to/kubeconfig"
  in_cluster: false
  resync_period: 30

logging:
  level: info
  format: json
```

### 关键字段

- `secrets[]`：需要监听并同步的 Secret 列表
- `ali_ssl_name`：同步到阿里云后的证书名称
- `credential_secret`：阿里云 AK/SK 的 K8s Secret 引用（推荐）
- `in_cluster`：`true` 时走 Pod ServiceAccount，优先级高于 `kubeconfig`
- `resync_period`：watch 异常后的重连等待时间（秒）

## 本地开发运行

1. 准备配置文件 `config/config.yaml`
2. 确认可访问 Kubernetes（`kubeconfig`）和阿里云 CAS
3. 启动：

```bash
go mod tidy
go run main.go
```

## Kubernetes 部署参考

当前仓库提供了基础清单：

- `deploy/yamls/configmap.yaml`
- `deploy/yamls/serviceaccount.yaml`

可按需扩展 Deployment/Job 清单并挂载配置文件后运行。

### 最小权限说明

`serviceaccount.yaml` 中包含以下权限：

- `ServiceAccount`：`secret2ssl`
- `ClusterRole`：`secrets` 的 `get/list/watch`
- `ClusterRoleBinding`：将上述权限绑定到 `secret2ssl`

这是本服务读取凭证 Secret、查询目标 Secret、watch Secret 变化所需的最小权限集。

## 创建阿里云凭证 Secret（示例）

```bash
kubectl -n demo create secret generic aliyun-cas-credential \
  --from-literal=access_key_id='<你的AK>' \
  --from-literal=access_key_secret='<你的SK>'
```

然后在 `config.yaml` 的 `aliyun.credential_secret` 中引用该 Secret。

## 常见问题

- 启动报错 `credentials are empty`
  - 检查 `credential_secret` 是否存在且 key 名称正确
  - 或补充 `access_key_id/access_key_secret` 作为兜底
- 报错 `does not contain tls.crt or tls.key`
  - 目标 Secret 不是标准 TLS Secret，需包含 `tls.crt` 和 `tls.key`
- 集群内无法访问 K8s API
  - 检查 Pod 使用的 `serviceAccountName` 以及 RBAC 绑定
