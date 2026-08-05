# 客户自托管 FDE × SRE RCA MVP：设计与工程落地参考

**状态：** 已在客户自有 ECS、阿里云账号与飞书测试群完成真实闭环验证
**目标读者：** FDE、SRE、平台工程、云治理与交付团队
**适用范围：** 一个服务端点关联一个或多个云资源的只读 RCA；以 CloudMonitor 告警为唯一入口
**不适用范围：** 自动修复、跨客户中心化 SaaS、任意云 API/任意日志查询 Agent

本文把已验证的 MVP 固化为一个可复制的工程模式。它既说明已落地的实现，也规定向其他服务、ACK、RDS 或安全组变更等场景扩展时必须保持的边界。

## 1. 已验证结果与验收边界

本 MVP 在客户自己的阿里云账号中完成了以下真实路径：

```text
CloudMonitor ECS CPU 告警
  -> HTTPS Webhook
  -> 客户 ECS 上的 sre-gateway
  -> SQLite Incident / Job
  -> sre-worker
  -> 本地 Claude Code（使用客户本地配置的模型提供方）
  -> 受控 sre-evidence
  -> UModel + CloudMonitor + SLS
  -> 同一张飞书卡片更新
```

最后一次受控 CPU 演练的飞书卡片已包含四条证据引用：

1. `sre.service_endpoint` 的 EntityStore 实体上下文；
2. `sre.service_endpoint --runs_on--> acs.ecs.instance` 的 UModel 拓扑；
3. CloudMonitor 的 ECS CPU 窗口数据（卡片中显示最高 100%）；
4. SLS 的 Nginx access 日志窗口数据（卡片中显示 5xx 为 0）。

因此，本 MVP 验证的是“基于真实、只读证据的 RCA 协助闭环”，不是证明模型可以自动处置生产故障。此次注入的是低优先级 CPU 压测；卡片显示“瞬时 CPU 抖动、未造成服务异常”是与实验事实一致的结论。

## 2. 目标、非目标与不可突破的边界

### 2.1 目标

- 接收一个已审核的 CloudMonitor 告警，创建可幂等合并的 Incident；
- 从稳定业务拓扑解析目标资源，再实时读取指标和日志；
- 让模型只能使用固定证据命令，输出严格、可校验的 RCA JSON；
- 将状态、结论、置信度、证据引用与下一步动作更新到同一张飞书卡片；
- 所有遥测原文仍留在客户阿里云账号；本地仅保存 Incident 状态和受控证据记录；
- 将端点、资源、证据 Binding 和告警选择器配置化，以便复制到新场景。

### 2.2 非目标

- 不允许 Claude Code、飞书或 Callback 直接调用任意阿里云 API；
- 不让模型执行 Bash 修复命令、任意 SLS SPL/SQL 或写云资源；
- 不把原始日志、指标、ActionTrail 事件汇聚到供应商的中心化存储；
- 不重新维护 ECS、VPC、安全组等原生资产的第二份 CMDB；
- 不将飞书作为事件总线或状态数据库；
- 不在没有可验证证据时给出确定性根因。

### 2.3 数据边界

| 数据 | 主存储位置 | 本系统保存内容 | 是否离开客户阿里云账号 |
| --- | --- | --- | --- |
| 指标 | CloudMonitor | 受控摘要、哈希引用、Incident 关联 | 否，除飞书展示摘要 |
| Nginx 原始日志 | 客户 SLS Project/Logstore | 受控摘要、哈希引用 | 否，除飞书展示摘要 |
| UModel 实体/拓扑 | CloudMonitor 2.0 EntityStore | EntityStore 查询结果的最小引用 | 否，除飞书展示摘要 |
| ActionTrail 原始事件 | 客户 ActionTrail | 受控摘要、哈希引用 | 否，除飞书展示摘要 |
| Incident/Job | 客户 ECS SQLite | 状态、幂等键、任务、卡片 message ID | 否 |
| 飞书 | 客户飞书租户 | RCA 摘要、证据 ID、下一步动作 | 不含原始遥测 |

## 3. 架构与职责分界

```text
                    客户阿里云账号

CloudMonitor ---------------------> sre-gateway (127.0.0.1:8080)
  OCCURRED / RECOVERED HTTPS                 |
                                               | SQLite: incidents / jobs
                                               v
                                           sre-worker
                                               |
                       +-----------------------+----------------------+
                       |                       |                      |
                  UModel EntityStore      CloudMonitor            SLS
                  静态实体与拓扑            指标窗口              Nginx 日志窗口
                       \                       |                      /
                        \                      |                     /
                         +---- sre-evidence / Provider -----------+
                                               |
                                    Claude Code + rca-investigate Skill
                                               |
                                          Feishu App Bot
```

| 组件 | 职责 | 不负责什么 |
| --- | --- | --- |
| `sre-gateway` | 校验 Callback、规范化、幂等 Incident、入队、处理恢复；在配置飞书凭据时更新已有恢复卡片 | 不执行模型与云证据查询，也不创建卡片 |
| SQLite State Store | Incident、Job、证据记录、飞书 message ID、重试租约 | 不保存遥测原文 |
| `sre-worker` | 领取 Job、收集证据、调用 Claude、校验结果、更新卡片 | 不接收公网请求 |
| `sre-evidence` / Provider | 对固定 Binding 执行只读云查询，生成统一 Evidence | 不接受 Agent 传入的任意查询 |
| `sre-sync` | 管理员显式同步 SRE 端点实体和静态拓扑 | 不在告警路径运行 |
| Claude Skill | 解释已给证据并输出 JSON | 无云凭据、无飞书凭据、无写能力 |
| 飞书 | 人工可见的状态与结论载体 | 不是状态机或事件队列 |

## 4. UModel：静态业务语义与实时数据分离

### 4.1 端点实体

自定义实体类型为 `sre.service_endpoint`，主键为 `endpoint_id`。稳定字段至少包括：

```text
endpoint_id, service_name, service_url, environment,
instance_id, ecs_entity_id, region_id, account_id
```

实体的作用是业务语义和稳定关联键，不承载不断变化的 CPU、日志或变更事实。

### 4.2 端点到 ECS 的静态关系

关系 Schema 是：

```text
sre.service_endpoint --runs_on--> acs.ecs.instance
```

注意 Schema 与关系实例是两件事。EntitySetLink 只定义允许的关系类型；运行时关系实例必须写入 EntityStore 拓扑数据。

### 4.3 官方支持的 EntityStore 写入与验证路径

| 对象 | 写入目标 | 必需特殊字段 | 验证方式 |
| --- | --- | --- | --- |
| Schema / Model | CMS `UpsertUmodelData` | 由 UModel schema 定义 | 控制台与读 API |
| 实体实例 | `${workspace}__entity` SLS Log Protocol | `__domain__`、`__entity_type__`、`__entity_id__`、`__last_observed_time__` | CMS `GetEntityStoreData` |
| 拓扑关系实例 | `${workspace}__topo` SLS Log Protocol | 六个 source/destination 字段、`__relation_type__` | CMS `GetEntityStoreData` |

`sre-sync` 的约束：

- 默认 dry-run；只有显式 `--apply` 才写入；
- 实体使用确定性的 endpoint ID，因此重复同步是更新同一条记录；
- 关系写入只有在 `relation.type` 非空时启用；
- 关系类型必须与已创建 EntitySetLink 完全一致；
- 查询必须使用 `GetEntityStoreData`，不依赖受限图 API；
- 错误关系用 `__method__=Expire` 退役，不能猜测 `Delete` 语义。

## 5. Incident 契约、状态机与幂等性

### 5.1 Incident Binding 是入口白名单

CloudMonitor Callback 不被信任为完整 RCA 上下文。`incident-bindings.yaml` 通过三元组精确匹配：

```yaml
incident_bindings:
  - workspace: <cloudmonitor-workspace>
    rule_id: <reviewed-rule-id>
    resource_id: <reviewed-entitystore-resource-id>
    selectors:
      endpoint_id: <service-endpoint-id>
      instance_id: <ecs-instance-id>
      region_id: <region>
      account_id: <account-id>
```

只有审核过的三元组才能把稳定 selector 传给证据系统。Callback 负载中的 URL、命令、自由文本或查询条件不得成为 Provider 输入。

### 5.2 状态机

```text
OCCURRED
  -> RECEIVED
  -> INVESTIGATING
  -> COMPLETED
   \-> AWAITING_AUDIT_EVENT --(延迟重试)--> INVESTIGATING

任一活动状态 + RECOVERED Callback -> RECOVERED
任务异常（最多三次）                 -> FAILED
```

- 同一活动 Incident Key 的重复 `OCCURRED` 合并，不重复建卡；
- `RECOVERED` 更新同一张飞书卡；
- `AWAITING_AUDIT_EVENT` 仅用于“关键变更事件尚未可读”，不是允许模型无限猜测；
- Worker 用 SQLite 租约原子领取 Job，防止多个进程重复执行。

## 6. Evidence Binding、Provider 与统一证据契约

### 6.1 统一 Evidence

每个 Provider 返回相同的可追溯结构：

```text
Evidence {
  id, type, observed_at, source, query_ref,
  summary, raw_ref
}
```

其中 `summary` 用于飞书和模型上下文；`raw_ref` 是受控原始响应的哈希引用。模型只可引用本轮发给它的 `Evidence.id`，Worker 会拒绝未知 ID 或空证据引用。

### 6.2 本 MVP 启用的 Binding

| Binding | 目标 | Provider | 真实查询 | 已验证输出 |
| --- | --- | --- | --- | --- |
| `endpoint_context` | `sre.service_endpoint` | `aliyun.umodel` | EntityStore 端点实体 | 端点、ECS ID、环境等上下文 |
| `endpoint_runs_on_topology` | `sre.service_endpoint` | `aliyun.umodel` | EntityStore 拓扑邻居 | `runs_on` 到原生 ECS |
| `ecs_cpu_window` | `acs.ecs.instance` | `aliyun.cloudmonitor` | `DescribeMetricList` | CPU 数据点、均值/最大/最小 |
| `nginx_access_log` | `sre.service_endpoint` | `aliyun.sls` | SLS `GetLogs` | access 条数与 5xx 数 |

CloudMonitor ECS CPU Binding 固定使用审核过的 Namespace、MetricName、Period 和 instanceId 维度；不从模型输出拼接。

Nginx SLS 查询固定使用字段精确匹配，字段值必须加引号：

```text
endpoint_id:"<endpoint-id>" AND log_kind:"access" AND instance_id:"<instance-id>"
```

SLS SDK 必须使用区域 endpoint `<region>.log.aliyuncs.com`，并调用生成 SDK 的 `GetLogs`，不能用不兼容的泛化 HTTP POST 代替。

### 6.3 ActionTrail 的正确扩展方式

当前 MVP 没有启用变更 Binding，因为可用 Callback/实体选择器中没有经过审核的 `security_group_id`。这是一项明确的安全限制，不是缺陷补偿。

启用 `security_group_change` 前必须同时完成：

1. 将具体安全组 ID 作为稳定、审核过的 selector；
2. 为该资源补齐只读 ActionTrail 权限；
3. 用真实的、可恢复变更验证事件延迟、分页和时间窗；
4. 只有事件真实可读时才让模型作出变更归因；否则保留 `AWAITING_AUDIT_EVENT`。

## 7. Claude Code、Skill 与输出围栏

Worker 启动一次性 Claude Code headless 进程；不使用常驻 Agent。运行用户必须是 `sre-rca`，并拥有自己的 `HOME=/var/lib/sre-rca`、Claude 配置与受限运行目录。

`rca-investigate` Skill 的不可变约束：

- 只能调用 `/opt/sre-rca/bin/sre-evidence` 的固定子命令；
- 必须获取 context、metrics、logs、changes 四类 collection；没有 Binding 的 collection 可以为空；
- 输出一个严格 JSON envelope，不能用 Markdown 围栏或解释文字包裹 JSON；
- `summary`、`root_cause`、每个 `next_actions` 项均使用简体中文；
- `evidence_ids` 必须是非空数组且全部属于 `ALLOWED_EVIDENCE_IDS`；
- 不能把“无数据”改写成确定性根因；需要变更证据但尚不可见时设置 `pending_audit=true`。

Worker 负责验证 JSON、证据 ID、超时和最大轮数。模型没有 Feishu secret、RAM 写权限或状态库写权限。

## 8. 配置、凭据与最小权限

### 8.1 配置分层

| 文件 | 内容 | 权限建议 |
| --- | --- | --- |
| `/etc/sre-rca/sre.yaml` | 非敏感运行参数、Binding/Incident Binding 路径 | `root:sre-rca 0640` |
| `/etc/sre-rca/evidence-bindings.yaml` | 允许的 Evidence 取证模板 | `root:sre-rca 0640` |
| `/etc/sre-rca/incident-bindings.yaml` | 告警三元组到 selector 的映射 | `root:sre-rca 0640` |
| `/etc/sre-rca/sre.env` | Callback Token、飞书 App ID/Secret/Chat ID | `root:root 0600` |
| `/var/lib/sre-rca/state.db` | 客户本地 Incident/Job 状态 | `sre-rca` 可读写 |

配置中的 `${VARIABLE}` 仅在进程启动时展开。任何改动 `sre.yaml`、Binding 或运行二进制后，都必须重启相应的 systemd 服务；特别是 Worker 在进程内创建 Provider，不会热加载证据逻辑。

### 8.2 云权限

所有阿里云调用使用 ECS Instance RAM Role 临时凭据；不保存长期 AK/SK。

| 职能 | 最小能力原则 |
| --- | --- |
| UModel schema 管理 | `cms:UpsertUmodelData`，仅目标 workspace 的 UModel 资源 |
| Entity/Topo 同步 | `log:PostLogStoreLogs`，仅 `${workspace}__entity` 与 `${workspace}__topo` |
| EntityStore 读取 | `cms:GetEntityStoreData`，仅目标 workspace/entitystore |
| 指标 | CloudMonitor 只读，限制到必要地域、Namespace/Metric/资源 |
| 日志 | SLS 只读，限制到目标 project/logstore |
| 变更 | ActionTrail 只读，只有启用变更 Binding 后才授予 |

Callback Token 与飞书 App Secret 绝不提交到 Git、不会写进飞书卡片或日志。Feishu 卡片接口只通过 tenant access token 调用，且卡片内容使用其 API 要求的正确 JSON 类型。

## 9. 部署拓扑与生产保护

### 9.1 服务隔离

- `sre-gateway` 仅监听 `127.0.0.1:8080`，由既有 Nginx 仅反代 `/healthz`、`/v1/inbound/cloudmonitor` 和临时 `/capture`；
- `sre-worker` 不监听端口、不暴露公网入口；
- 两者均以非特权 `sre-rca` 用户运行，systemd 启用 `NoNewPrivileges`、`PrivateTmp`、`ProtectHome`、`ProtectSystem=strict`；
- 只允许写入 `/var/lib/sre-rca`；
- Worker 的 `/opt/sre-rca/runtime` 归 `sre-rca`，用于 Skill 与 Claude 运行态。

部署、回滚和证据验证绝不修改现有 Blog 的 Nginx 虚拟主机、网站进程或已运行的 Gateway 路由。

### 9.2 构建与发布契约

在可信构建机上执行：

```bash
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/sre-worker ./cmd/sre-worker
tar -czf sre-worker-linux-amd64.tar.gz bin/sre-worker
sha256sum sre-worker-linux-amd64.tar.gz
```

客户 ECS 上的发布顺序固定为：校验 SHA256 -> 解压到带日期的 release 目录 -> 备份当前二进制 -> 停止单个服务 -> 安装到临时文件 -> `cmp` -> 原子 `mv` -> 启动单个服务 -> 检查 gateway/worker 状态与 journal。

发布包放在客户服务器 `/root/` 仅是传输约定；源码仓库不提交 `.tar.gz`。可复建包输出到
`/tmp` 或外部制品仓库，不能依赖仓库内的 `dist/` 目录。

## 10. 分阶段落地 Runbook

### Phase 0：准入与基线

1. 明确一个业务端点、一个可恢复告警和一个资源对象；
2. 确认 `/healthz`、Nginx、现有网站与 gateway 正常；
3. 创建/确认 Callback capture，采样真实告警和恢复负载并脱敏保存；
4. 冻结 Incident Binding 三元组与 selector；
5. 明确不可触碰的生产组件和回滚责任人。

### Phase 1：UModel 静态上下文

1. 创建 SRE EntitySet 与 EntitySetLink Schema；
2. `sre-sync` dry-run 审核实体和关系 payload；
3. 仅为 `${workspace}__entity`/`__topo` 添加最小写权限；
4. `--apply` 后用 `--inspect-schema` 与 `--inspect-relation` 读回；
5. 控制台拓扑必须看见预期的 `runs_on` 边。

### Phase 2：动态遥测证据

1. 让 Logtail 将结构化 Nginx access/error 日志写入客户 SLS；
2. 以 quoted-field 查询在 SLS 控制台验证 endpoint、log_kind、instance_id；
3. 用 `sre-evidence metrics query <incident>` 验证 CloudMonitor 只读调用；
4. 用 `sre-evidence logs query <incident>` 验证 SLS 只读调用；
5. 只有这些真实返回均成功后，才把 Binding 指向 worker 的活动配置。

### Phase 3：模型与飞书预检

1. 为 `sre-rca` 设置独立的 Claude Provider 配置，不依赖 root 的交互登录；
2. 运行 `scripts/verify-worker-preflight.sh`，验证 Claude、Skill、Evidence CLI、状态库与环境；
3. 请求飞书 tenant access token 并发送一张预检卡；
4. 更新同一张卡以验证卡片更新权限；
5. `worker.enabled: false` 时必须拒绝 Worker 启动；预检通过后才启用服务。

### Phase 4：受控故障演练

1. 使用预先审核、自动释放的低风险故障，例如两个 `nice 19` CPU worker；
2. 全程每 10 秒调用本地 `/healthz`；
3. 确认 CloudMonitor Callback 创建新 Incident；
4. 检查飞书卡片包含 context、topology、metric、log 四类引用；
5. 验证卡片中的数字与 CloudMonitor/SLS 控制台一致；
6. 收到恢复 Callback 后确认同一 Incident/卡片更新为恢复状态；
7. 收集 journal、状态库摘要和控制台截图作为验收凭据。

## 11. 验收矩阵

| 层级 | 验收 | 失败时先检查 |
| --- | --- | --- |
| Callback | CloudMonitor 真实投递返回 202 | Nginx 路由、Token、Callback payload |
| 幂等 | 重复告警不创建重复活动 Incident | incident key、workspace/rule/resource mapping |
| UModel | 实体与 `runs_on` 边可读回 | Log Protocol 目标、特殊字段、RAM 范围 |
| 指标 | 输出 CPU 点数/均值/最大/最小 | Namespace、MetricName、Period、instanceId |
| 日志 | 输出日志条数与 5xx 数 | SLS endpoint、GetLogs、字段引号、Logtail 字段 |
| Claude | 只输出通过 schema/证据 ID 校验的 JSON | `sre-rca` HOME、Provider 配置、Skill 输出围栏 |
| 飞书 | 预检卡与同卡更新均成功 | App Secret、chat ID、机器人入群、卡片 JSON 类型 |
| Worker | 动态证据真实出现在卡片与 evidence record | 活动 Binding 路径、Worker 是否已重启、内嵌 Provider 二进制版本 |
| 恢复 | 同一张卡片更新为恢复 | RECOVERED Callback 与活动 Incident Key |

## 12. 常见故障与不可重复的教训

1. **Schema API 不写 EntityStore 实例。** `UpsertUmodelData` 用于 UModel schema/model；实体和拓扑实例必须分别进入 `__entity`、`__topo`。
2. **SLS 字段查询必须加引号。** 使用 `endpoint_id:"..."`，不要写 `key:value` 后混入未引用值。
3. **SLS 使用生成 SDK `GetLogs`。** 错误 HTTP Method/路径会导致 `OLSInvalidMethod` 或 405。
4. **Classic SLS 与 OpenAPI SLS SDK 不应在同一同步二进制混用。** 会出现 protobuf duplicate registration panic；同步器与 Provider 的依赖边界必须隔离。
5. **Worker 不会热加载。** `sre-worker` 在进程内构造证据 Provider；改 Evidence 代码、活动 Binding 或运行配置后必须构建/部署并重启 worker。
6. **发布包解压路径要先检查。** `tar --strip-components=1` 会改变二进制路径；安装前用 `tar -tzf` 和 `test -x` 确认，避免把不存在的 `bin/...` 路径写进脚本。
7. **不把 root 的 Claude 登录当成服务配置。** `sre-rca` 用户要有自己的受限 Provider 配置；使用最小环境执行预检。
8. **飞书卡片中的布尔值必须是 JSON boolean。** 把 `true` 序列化成字符串会被卡片 API 拒绝。
9. **模型结果必须验证证据 ID。** 不接受未知 ID、空 `evidence_ids` 或 Markdown 包裹的 JSON。
10. **等待审计事件不是失败。** 它表示证据不足并触发延迟重查；应显示缺失证据，而不是伪造变更根因。

## 13. 复制到其他场景的最小变更单元

不要以“换一个 Prompt”作为扩展方式。一个新场景必须新增或审核以下五项：

1. **业务实体与静态拓扑：** 新端点/服务到原生资源的稳定关系；
2. **Incident Binding：** 真实告警的 workspace、rule ID、resource ID 和只读 selector；
3. **Evidence Binding：** 固定 Provider、查询模板、字段映射和时间窗；
4. **RAM 权限：** 仅覆盖新 Evidence 的只读资源；
5. **演练与 Golden Case：** 一个可恢复故障、期望证据、可接受结论与人工验收记录。

### 13.1 场景模板

| 场景 | 静态关系 | 动态证据 | 典型结论边界 |
| --- | --- | --- | --- |
| ECS CPU / 内存 | endpoint -> ECS | CPU、内存、Nginx/应用日志 | 容量瞬时抖动或资源饱和，需日志印证 |
| HTTP 可用性 | endpoint -> ECS/SLB | 拨测、5xx、延迟、access/error 日志 | 先区分探测、网络、Nginx、应用 |
| 安全组变更 | endpoint -> ECS -> security group | ActionTrail、拨测、流量、日志 | 仅在审计事件可读时归因变更 |
| ACK Pod 异常 | service -> workload -> pod/node | ARMS/Prometheus、K8s Event、应用日志 | 区分调度、OOM、镜像、依赖、代码错误 |
| RDS 延迟 | service -> RDS | 连接数、慢 SQL、CPU/IO、应用错误 | 不执行 SQL 修复，仅提出审核动作 |

## 14. 进入下一成熟度前的门槛

当前能力是 **L1 只读 RCA 助手**。进入“人工批准的修复建议”或更高自治等级之前，必须额外具备：

- 针对每个事故类型的 Golden Dataset 与离线评测；
- 写操作的风险分级、dry-run、审批、回滚、速率限制和 kill switch；
- 完整工具调用审计与人工接管；
- 明确的 SLO、告警质量、误报/漏报和 RCA 采纳率度量；
- 多场景、多次演练后证明结论不会越权、不会伪造证据。

在这些条件满足前，RCA 系统只能建议，不能自动执行任何生产变更。

## 15. 交付清单

一个客户环境的交付完成应至少包含：

- [ ] 已审核的 Callback 样本（脱敏且不提交 Git）；
- [ ] Incident Binding 与 Evidence Binding；
- [ ] UModel Schema、Entity 与 Topology read-back 证据；
- [ ] RAM 最小权限策略与资源范围评审；
- [ ] CloudMonitor/SLS/ActionTrail 的只读预检记录；
- [ ] Claude `sre-rca` 服务用户预检记录；
- [ ] 飞书发卡与同卡更新预检记录；
- [ ] SHA256、发布备份、回滚命令和 systemd 状态；
- [ ] 一次可恢复故障演练的卡片、日志与状态库证据；
- [ ] 场景 owner、运行 Runbook、复盘结论与下一阶段范围。

这份清单是“可交付、可复核、可迁移”的最低标准；没有真实演练和读回验证的配置，不视为完成。
