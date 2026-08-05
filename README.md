# customer-hosted FDE × SRE RCA MVP

这是一个部署在**客户自有 ECS 和阿里云账号**中的只读 RCA MVP。它以
CloudMonitor Callback 为唯一 Incident 入口，在客户本地 SQLite 中维护
Incident/Job 状态，读取客户账号内的 UModel、CloudMonitor 和 SLS 证据，并将
经校验的中文 RCA 摘要更新到同一张飞书卡片。它不是中心化 SaaS，也不保存或
汇聚客户原始遥测数据。

## 已真实验证的范围

- CloudMonitor ECS CPU 告警 Callback 返回 HTTP 202；触发、恢复和幂等
  Incident 已验证。
- `sre-gateway -> SQLite -> sre-worker -> Claude Code -> sre-evidence -> Feishu`
  闭环已在客户环境验证；Gateway 与 Worker 是独立 systemd 服务。
- 自定义 `sre.service_endpoint` 实体和
  `sre.service_endpoint --runs_on--> acs.ecs.instance` 关系已通过
  EntityStore 读回验证。
- CPU 演练的正确结论是“瞬时 CPU 峰值、未造成服务异常”；它不能被表述为网站
  故障。

## 当前有意不启用的能力

- ActionTrail 安全组变更证据：`security_group_id` selector 尚未冻结和审核。
  没有证据时保持 `AWAITING_AUDIT_EVENT` 是保守的正确行为。
- 自动修复、任意云 API/SLS 查询、跨客户中心化存储、多云、Kubernetes/RDS
  Provider 都不在本 MVP 范围内。

## 快速代码地图

| 路径 | 责任 |
| --- | --- |
| `cmd/sre-gateway` | Callback HTTP 入口、规范化、幂等入队；不运行模型。 |
| `cmd/sre-worker` | 领取 Job、取证、调用 Claude、更新飞书卡片。 |
| `cmd/sre-evidence` | 仅暴露固定的只读取证命令。 |
| `cmd/sre-sync` | 管理员显式同步 UModel 实体与静态拓扑；不在告警路径运行。 |
| `internal/runtime` | Worker 与 CLI 的运行时组合和 Incident Binding 解析。 |
| `internal/provider` | 固定 Binding 的只读 Alibaba Cloud 请求、SDK 边界和安全摘要。 |
| `internal/modelsync` | EntityStore 实体/拓扑 payload、SLS Log Protocol 写入、只读 inspect。 |
| `internal/store` | SQLite Incident、Job、租约、重试、证据记录。 |
| `internal/worker` | Job 状态机、取证顺序、Claude 结果/证据 ID 校验。 |

`sre-gateway` 不运行模型、取证或轮询 Worker；仅在收到 `RECOVERED` 且配置了飞书
App ID/Secret 时，更新已存在的同一张飞书卡片。创建卡片和所有 RCA 处理仍由
`sre-worker` 完成。

## 推荐阅读顺序

1. [MVP 架构](docs/architecture/customer-hosted-fde-sre-rca-mvp.md)
2. [重构设计](docs/architecture/mvp-refactor-readability.md)
3. [团队开发上手](docs/development/team-onboarding.md)
4. [MVP 演练 Runbook](docs/runbooks/mvp-demo.md)
5. [Evidence 拓扑上下文 Runbook](docs/runbooks/evidence-topology-context.md)
6. [UModel 同步 Runbook](docs/runbooks/sre-sync.md)

`docs/superpowers/specs/` 与 `docs/superpowers/plans/` 是历史设计和实施记录，不是
当前运行手册；以 README、上面的 Runbook 和团队上手文档为准。

## 本地验证与 Linux 构建

```bash
go test ./...
bash scripts/verify-worker-preflight_test.sh
git diff --check

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/sre-gateway.verify ./cmd/sre-gateway
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/sre-worker.verify ./cmd/sre-worker
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/sre-evidence.verify ./cmd/sre-evidence
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/sre-sync.verify ./cmd/sre-sync
```

构建成功不等于获准部署。变更二进制、Binding 或运行配置后，必须先完成本地
验证、同行 review 和客户变更流程；Worker 不支持热加载，获批发布后才可在客户
主机重启它。

## 安全禁止事项

- 不提交 Token、App Secret、Callback Token、模型 Provider 凭据、真实 Callback
  fixture、原始日志、真实 IP、客户账号或本地状态库。
- 不把 Callback、模型输出或飞书输入直接转换成云查询；只有经审核的
  `(workspace, rule_id, resource_id)` Incident Binding 能给出 selector。
- 不让 `sre-evidence`、Claude 或飞书执行写云资源、任意 Shell 或任意 SLS 查询。
- 不用 `UpsertUmodelData` 写 EntityStore 实例；实体写 `${workspace}__entity`、关系
  写 `${workspace}__topo`，并通过 CMS `GetEntityStoreData` 只读验证。
- 不提交 `.tar.gz`、二进制或构建产物。

## 许可证

本项目采用 [Apache License 2.0](LICENSE)。你可以在该许可证的条件下使用、修改和
分发本项目；其中包括保留许可证和相关声明等义务。
