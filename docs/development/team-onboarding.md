# 团队开发上手：customer-hosted FDE × SRE RCA MVP

## 本地开发与测试

使用 Go 工具链后，在仓库根目录运行：

```bash
go test ./...
bash scripts/verify-worker-preflight_test.sh
git diff --check
```

预检测试验证脚本的固定路径、安全 incident reference 和输出校验逻辑；它不要求、
也不应连接客户生产环境。需要验证 Linux 可执行文件时使用 README 中的四条
`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build` 命令，输出写到 `/tmp`。

## Provider 开发规范

- 证据只能由 YAML 中已 allowlist 的 provider/template 启用；新增模板先改
  `internal/evidence/registry.go`，不能做动态名字解析。
- 云请求只能由经过审核的 Incident Binding selector 生成。不要读取 Callback 中的
  URL、查询、自由文本或模型参数。
- 把固定请求形状放进 `internal/provider/request_builders.go`，让
  `AliyunProvider` 只负责路由、执行和生成最小摘要。
- 请求前校验 window 与所有 selector；缺 selector 或值不安全时必须在调用云 SDK
  前返回错误。
- Evidence 的 `Summary` 只写可读的最小事实，`RawRef` 只保留哈希引用；不要把原始
  日志、指标、事件或账号信息传给 Claude/飞书。
- 每个场景至少有：固定请求形状、拒绝不安全 selector、摘要不暴露原文三类测试。

如遇 UModel/CMS/SLS/ActionTrail SDK 或 API 变更，先核对当前 SDK 源码和官方文档，
再写测试和代码；不要通过反复试错探测客户账号。

## 配置与秘密管理

运行配置和 Binding 位于客户 ECS 的 `/etc/sre-rca`，不是仓库中的生产副本。
`sre.yaml` 与 Binding 可以使用环境变量展开，但 Callback Token、飞书 App Secret、
模型 Provider 凭据只能位于受限的 `sre.env` 或等价秘密存储，不能进入 Git、fixture
或日志。阿里云调用使用 ECS RAM Role 临时凭据，不保存 AK/SK。

Worker 在进程内组合 Provider，因此二进制、Evidence Binding 或运行配置变更不会热
加载。获得客户批准并完成验证后，才按变更流程重启 Worker；本仓库的测试与构建都
不授权部署。

## 发布前检查

1. 运行完整测试、预检测试、`git diff --check` 和四个 Linux 构建。
2. 审查 staged diff、配置、文档和测试样例：不得有 Token、Secret、真实 Callback
   fixture、原始日志、真实 IP、客户账号、二进制、`.tar.gz` 或 SQLite 状态库。
3. 确认 Gateway 仍只做 HTTP 入口；Worker 仍通过 Job 租约执行；CLI 参数、YAML key、
   Incident/Evidence Binding 与飞书卡片字段均保持兼容。
4. 在客户变更记录中列出验证命令、批准人、可回滚版本和是否需要重启 Worker。没有
   已验证包和批准时，**不得部署**。

## UModel/CMS/SLS 关键约束与常见踩坑

| 主题 | 必须遵守 |
| --- | --- |
| Schema 与实例 | `UpsertUmodelData` 只处理 UModel schema/model，不能写 EntityStore 实例。 |
| 实体/拓扑写入 | 实体写 `${workspace}__entity`；关系写 `${workspace}__topo`。 |
| 关系 payload | 必须有六个 source/destination 身份字段及 `__relation_type__`；`runs_on` 类型和 schema 完全一致。 |
| 只读验证 | 用 CMS `GetEntityStoreData`，不要改为受限图 API。 |
| SLS | 使用 `<region>.log.aliyuncs.com` 与生成 SDK `GetLogs`；不要混用 classic 与 OpenAPI SLS SDK。 |
| 查询 | SLS 字段值必须双引号，例如 `endpoint_id:"blog-http" AND log_kind:"access"`。 |
| ActionTrail | selector 未冻结时不启用；“等待审计事件”不是可用猜测填补的错误。 |

## 代码 review 提示

优先 review 三件事：新 selector 是否经过 Incident Binding 审核、请求 builder 是否仍是
固定只读形状、摘要和测试是否避免泄露原始数据。随后检查所有状态变化是否仍受 Job
lease 和 Incident active fence 保护。任何涉及生产 Gateway、Nginx、CloudMonitor
Webhook、RAM、飞书或 systemd 的动作都超出普通代码重构范围。
