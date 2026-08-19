# Kubernetes 部署指南 — 白盒多 Agent 协作平台

本目录提供将 `multi-agent-platform` 部署到 Kubernetes 的最小可用清单（Namespace / ConfigMap / Secret / PVC / Deployment / Service，可选 Ingress）。

镜像由 CI 自动构建并推送到 GitHub Container Registry：

```
ghcr.io/ayanmw/multi-agent-platform:<tag|latest>
```

---

## 1. 前置条件

- 一个 Kubernetes 集群（≥ 1.25），且默认 StorageClass 可用（PVC 用 `ReadWriteOnce`）。
- `kubectl` 与集群上下文已配置。
- （可选）[cert-manager](https://cert-manager.io/) 用于 Ingress 自动 TLS。
- 一个可用的 LLM 端点与 API key（真实 LLM 接入为必需）。

---

## 2. 部署步骤

### 2.1 准备 Secret（凭证不要提交进仓库）

推荐用命令行直接创建，避免把 key 写进 `secret.yaml`：

```bash
kubectl -n multi-agent-platform create secret generic multi-agent-platform \
  --from-literal=LLM_ENDPOINT='https://your-llm-endpoint/v1' \
  --from-literal=LLM_API_KEY='sk-xxx' \
  --from-literal=LLM_MODEL='deepseek-v4-flash'
```

> 如需仅做离线/评估部署，可把 `configmap.yaml` 里的 `LLM_USE_MOCK` 置为 `"true"`，并跳过 Secret 中的真实 key。

### 2.2 用 Kustomize 一键部署

```bash
kubectl apply -k deploy/k8s
```

或逐个文件：

```bash
kubectl apply -f deploy/k8s/namespace.yaml \
  -f deploy/k8s/configmap.yaml \
  -f deploy/k8s/secret.yaml \
  -f deploy/k8s/pvc.yaml \
  -f deploy/k8s/deployment.yaml \
  -f deploy/k8s/service.yaml
```

### 2.3 启用 Ingress（可选）

编辑 `deploy/k8s/ingress.yaml` 取消注释并填入你的域名 / 证书方案，然后在 `kustomization.yaml` 中取消 `- ingress.yaml` 注释后重新 `kubectl apply -k deploy/k8s`。

---

## 3. 验证

```bash
kubectl -n multi-agent-platform rollout status deployment/multi-agent-platform
kubectl -n multi-agent-platform get pods
kubectl -n multi-agent-platform port-forward svc/multi-agent-platform 8080:8080
curl http://localhost:8080/healthz
```

---

## 4. 重要约束：SQLite 单写者

当前默认后端是 `modernc.org/sqlite`（纯 Go 单文件，单写者）。因此：

- **Deployment 必须 `replicas: 1`**（已设），且使用 `Recreate` 策略 + `ReadWriteOnce` PVC。
- 不要横向扩容到多副本，否则第二个 Pod 无法安全写入同一 SQLite 文件。

### 横向扩展路径（多副本）

`N3-04c` 已实现 DB 后端可插拔抽象（`Backend`/`Dialect` 接口 + SQLite 默认 + Postgres 原型）。要支持多副本：

1. 将 `DB_BACKEND` 切到支持并发写者的后端（Postgres 原型）；
2. 在 Secret/ConfigMap 中配置对应的连接串；
3. 此时才可将 `replicas` 提高到 ≥ 2，并把 PVC 换成外部托管数据库（不再需要本地 PVC）。

---

## 5. 安全建议

- 生产环境将 `configmap.yaml` 的 `REQUIRE_AUTH` 设为 `"true"`，并为用户 / 服务账号下发 API key（N3-01 认证加固：特权写路由与敏感读始终要求有效 key）。
- Deployment 已采用非 root、只读根文件系统、丢弃全部 capabilities；`/tmp` 用 `emptyDir`、`/data` 用 PVC 提供可写空间。
- 镜像来自 GHCR 并带 tag / `latest`；生产建议锁定到具体版本 tag（如 `:v0.16.0`），避免使用 `latest`。
