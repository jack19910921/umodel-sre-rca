# UModel SRE RCA MVP 设计

**日期：** 2026-07-20
**状态：** 已确认设计，待实施规划
**范围：** 独立 MVP；不复用或修改 ARA

## 1. 目标

在一个客户自有的阿里云账号内，验证以下闭环：

1. CloudMonitor 2.0 的 HTTP 拨测发现 ECS 上 Nginx `/health` 不可用。
2. 告警经 HTTP Callback 进入客户 ECS 上的 SRE Gateway。
3. Gateway 以 `sre` UModel domain 为入口，关联 `acs` 原生资源、指标、日志与变更证据。
4. Claude Code 以一次性、只读的 `rca-investigate` Skill 进行根因分析。
5. Gateway 将结论和证据链更新到同一张飞书卡片。
6. 恢复服务后，更新同一 Incident 和同一张卡片。

演示故障为：**误删除安全组 TCP/80 入方向规则，导致公网 HTTP 健康检查失败。**

## 2. 成功标准

- 删除 TCP/80 后，云拨测告警能够抵达 Gateway，且 Gateway 在 10 秒内创建“分析中”飞书卡片。
- 同一告警的重复通知不会创建重复 Incident 或重复卡片。
- 最终 RCA 至少引用四类证据：拨测失败、ECS 健康/指标、Nginx 日志、ActionTrail 安全组变更。
- 结论能识别“安全组规则撤销”而非误判为 ECS 容量/进程故障。
- 恢复 TCP/80 后，恢复事件更新原卡片。
- 原始指标、日志和审计数据始终保留在客户账号的 CloudMonitor/SLS/ActionTrail 中；产品仅保存 Incident 状态、证据摘要和查询引用。
- Gateway 不保存阿里云长期 AK/SK；使用 ECS Instance RAM Role 的临时凭据。

## 3. 不做什么

- 不复制或重建 `acs.ecs.instance`、安全组、VPC 等云资源实体。
- 不把飞书作为机器事件总线；飞书只用于展示与人工交互。
- 不让模型执行修复操作、任意 Bash、任意 SLS SQL 或任意云 API。
- 不在 MVP 中建设 AWS Provider、自动修复、统一多客户控制面或全量 CMDB。
- 不使用 MCP 作为 MVP 的 Agent 工具协议。

## 4. 总体架构

```text
CloudMonitor HTTP Callback
  -> SRE Gateway: 验证、归一化、Incident 幂等、任务入队
  -> Feishu: 发送/更新 Incident 卡片
  -> RCA Worker: 为一个 Incident 启动一次 CC headless 进程
  -> rca-investigate Skill: 调用受控 sre-evidence CLI
  -> Evidence Providers: UModel / CMS / SLS / ActionTrail
  -> Worker: 校验 RCA JSON、保存证据快照索引、更新飞书卡片
```

部署均在客户当前阿里云账号：

| 组件 | 位置 | 职责 |
|---|---|---|
| CloudMonitor 2.0 与 UModel | 当前 workspace | 告警、原生 `acs/infra` 实体、拓扑、指标/日志关联 |
| `sre` domain | 当前 workspace | 业务语义、端点、可靠性与自定义数据语义 |
| SRE Gateway + Worker | 现有公网 ECS | 回调、队列、Provider、CC 调度、飞书更新 |
| Incident State Store | Gateway 本地 SQLite（WAL） | 幂等、任务状态、消息 ID、证据索引 |
| SLS Logstore | 当前账号 | Nginx access/error 原始日志 |
| 飞书自建应用机器人 | 飞书 | 发送并更新卡片 |

## 5. UModel 建模

### 5.1 `sre` domain 的实体与数据集

`sre` 是 SRE 业务语义层，不是第二套 IaaS CMDB。

| UModel 类型 | 名称 | 主要字段 |
|---|---|---|
| EntitySet | `sre.service` | `service_id`、`name`、`owner`、`env`、`criticality`、`rto_minutes` |
| EntitySet | `sre.service_endpoint` | `endpoint_id`、`url`、`protocol`、`expected_status`、`probe_task_id`、`account_id`、`region_id`、`instance_id` |
| EventSet | `sre.availability_event` | `endpoint_id`、`observed_at`、`probe_result`、`status_code`、`latency_ms` |
| LogSet | `sre.nginx_access_log` | `instance_id`、`host`、`request_uri`、`status`、`request_time`、`time` |
| LogSet | `sre.nginx_error_log` | `instance_id`、`host`、`level`、`message`、`time` |
| EventSet | `sre.change_event` | `account_id`、`region_id`、`resource_id`、`resource_type`、`event_name`、`operator`、`event_time`、`request_id`、`change_summary` |

### 5.2 关系与数据关联

```text
sre.service --serves--> sre.service_endpoint
sre.service_endpoint --hosted_by--> acs.ecs.instance

sre.service_endpoint --DataLink(probe_task_id)--> sre.availability_event
sre.service_endpoint --DataLink(instance_id + host)--> sre.nginx_access_log
sre.service_endpoint --DataLink(instance_id + host)--> sre.nginx_error_log
acs.ecs.securitygroup --DataLink(account_id + region_id + resource_id)--> sre.change_event
```

`acs.ecs.instance` 到 VPC、安全组等关系使用 CloudMonitor 已接入的原生拓扑。RCA 从 `sre.service_endpoint` 走到 ECS，再走到安全组，无需在 `sre` 中重复资源事实。

### 5.3 静态与动态数据

- 静态关系：端点到 ECS、服务归属等，由建模配置和资产增量同步维护。
- 原生动态数据：使用 `acs` 已有关联的指标、日志、事件。
- 自定义动态数据：Nginx 日志进入客户 SLS；ActionTrail 变更由 Provider 直接查询，并可按需要同步至 `sre.change_event`。
- Incident 运行态：不建成 UModel 实体；保存在 Gateway 的 State Store，因为它需要幂等、任务状态、卡片消息 ID 与重试控制。

## 6. Evidence Binding 与 Provider

### 6.1 决策

不使用“实体字段中配置 URL”作为取数机制，也不让 DataLink 单独承担运行时数据访问。

- **字段** 保存稳定身份和关联键，如 `instance_id`、`probe_task_id`、账号、地域、端点 host。
- **DataLink / StorageLink** 定义实体与数据集、数据集与客户侧存储的语义关系。
- **Evidence Binding Registry** 定义可执行取证：Provider、查询模板、字段映射、时间窗、数据保留期、脱敏规则和给人的跳转链接模板。
- **Provider** 执行 API/SDK 查询并返回统一 Evidence；凭据只由 Provider 使用。

URL 只允许用于飞书卡片中的 `console_link_template`，供人工跳转；不得作为 Agent 查询输入。

### 6.2 Binding 示例

```yaml
- id: endpoint_availability
  target: sre.service_endpoint
  provider: aliyun.synthetic_probe
  selector_mapping:
    probe_task_id: probe_task_id
  query_template: availability_window_v1

- id: nginx_access_log
  target: sre.service_endpoint
  provider: aliyun.sls
  selector_mapping:
    instance_id: instance_id
    endpoint_host: host
  query_template: nginx_access_by_window_v1

- id: security_group_change
  target: acs.ecs.securitygroup
  provider: aliyun.actiontrail
  selector_mapping:
    account_id: account_id
    region_id: region_id
    security_group_id: resource_id
  query_template: security_group_change_window_v1
```

### 6.3 Provider 契约

```text
resolve(binding_id, incident_id, time_window) -> Evidence[]

Evidence {
  id, type, observed_at, source, query_ref,
  summary, raw_ref, provenance, redaction_applied
}
```

首批 Provider：

- `aliyun.synthetic_probe`：取 HTTP 拨测失败与恢复。
- `aliyun.cloudmonitor`：取 ECS 状态、CPU、网络等排除性证据。
- `aliyun.sls`：取 Nginx access/error log。
- `aliyun.actiontrail`：取安全组变更、操作者和时间；若审计事件尚未可见，返回“待补齐”并触发延迟重查。

## 7. Agent 与 CLI

### 7.1 为什么不用 MCP

MVP 只有一个本地 Agent Runtime（Claude Code）和一个客户自有的 Gateway。MCP 会增加 Server 生命周期、协议、工具注册与排障面，不能增加当前验证价值。

使用受控 CLI：

```text
sre-evidence incident context <incident_id>
sre-evidence metrics query <incident_id>
sre-evidence logs query <incident_id>
sre-evidence changes query <incident_id>
sre-evidence evidence open <evidence_id>
```

CLI 根据 Incident、Binding 与模板生成固定查询；不接受任意 SLS SQL、任意 URL 或任意云 API 动作。

### 7.2 CC 调度

HTTP Handler 绝不直接等待模型：

1. 校验并归一化 CloudMonitor callback。
2. 根据 workspace、规则、资源、告警指纹计算 Incident 幂等键。
3. 在 SQLite 事务中创建/合并 Incident 和待执行 Job。
4. 创建或更新飞书“分析中”卡片后，立即返回 HTTP 200。
5. Worker 原子领取 Job，创建独立目录 `/var/lib/sre-rca/runs/<incident_id>/`。
6. Worker 启动一次性 Claude Code headless 进程。

概念命令：

```text
claude -p "/rca-investigate <incident_id>" \
  --output-format json \
  --max-turns 8 \
  --allowedTools "Bash(/opt/sre-rca/bin/sre-evidence:*)"
```

生产实现必须使用进程参数数组，而不是 shell 拼接。任务使用固定 Claude 二进制路径、固定工作目录、固定 Skill 版本、最大轮数和最大运行时长。

### 7.3 `rca-investigate` Skill

位置：

```text
/opt/sre-rca/runtime/.claude/skills/rca-investigate/SKILL.md
```

Skill 步骤：

1. 获取 Incident Context 与 `sre -> acs` 子图。
2. 获取指标、日志、变更证据。
3. 先验证确定性假设，再生成自然语言结论。
4. 输出严格 `RCAResult` JSON。

Skill 没有飞书 Token、云凭据或写入权限。Gateway 验证 JSON 后才更新飞书。

```json
{
  "summary": "公网健康检查失败由安全组 80 端口规则撤销导致",
  "confidence": 0.93,
  "root_cause": {
    "type": "security_group_rule_revoked",
    "resource": "sg-xxx",
    "event_time": "..."
  },
  "evidence_ids": ["ev_probe_01", "ev_metric_02", "ev_log_03", "ev_change_04"],
  "next_actions": ["恢复 TCP/80 入方向规则", "确认拨测连续成功"]
}
```

## 8. 飞书交互

使用飞书自建应用机器人，而不是一次性 webhook 机器人。Gateway 保存它自己发送的卡片 `message_id` 并负责更新。

卡片阶段：

```text
RECEIVED -> CONTEXT_READY -> INVESTIGATING ->
AWAITING_AUDIT_EVENT -> COMPLETED | FAILED | TIMEOUT -> RECOVERED
```

只显示阶段、证据摘要、置信度、人工跳转链接与建议动作；不输出模型的 token 流或思维过程。

## 9. 故障演练

### 前置条件

- ECS 有公网 IP，Nginx 已部署，`/health` 正常返回 `200`。
- 保留 SSH/22 入方向规则。
- TCP/80 初始允许公网 HTTP 拨测。
- 安装 Logtail，将 Nginx access/error log 投递到客户账号的 SLS Logstore。
- 通过 Instance RAM Role 给 Gateway 最小只读权限；不保存长期 AK/SK。

### 执行

1. 创建/确认 HTTP 拨测，连续两次失败触发；启用恢复事件。
2. 创建 CloudMonitor 通知策略，将触发、重复、恢复事件通过 HTTP Callback 发送到 Gateway。
3. 创建 `sre` domain 模型和上述绑定。
4. 基线确认：拨测成功、Nginx 日志可检索、ActionTrail 可查询。
5. 在控制台删除目标安全组的 TCP/80 入方向规则。
6. 验证飞书卡片的接收、取证、变更证据补齐与最终 RCA。
7. 恢复 TCP/80，验证恢复事件更新原卡片。

## 10. 安全与运行约束

- 禁止 `--dangerously-skip-permissions`。
- Claude Code 仅允许调用固定的 `sre-evidence` CLI，不允许任意 Bash、Edit、Write。
- Provider 是唯一使用 Instance RAM Role 的组件；权限全部为读。
- Provider 在把日志交给 CC 前做长度限制和敏感字段脱敏。
- 每次执行记录：Incident ID、Skill 版本、Binding 版本、证据查询引用、模型、结果、飞书消息 ID。
- CC 进程失败、超时或审计未到达时，原告警仍可见；卡片明确显示分析状态而不伪造结论。

## 11. 多云演进

不改变 `sre.service`、`sre.service_endpoint`、Evidence 契约、Skill 或飞书/Incident 状态机。

AWS 只新增：

```text
aws.synthetic_probe / CloudWatch Synthetics
aws.metrics / CloudWatch Metrics
aws.logs / CloudWatch Logs
aws.changes / CloudTrail
```

阿里云 `acs` domain 是当前账号的资源事实锚点；多云产品层的稳定抽象是 `sre` domain 与 Binding/Provider 契约。

## 12. 实施前唯一待确认项

确认 ECS 上 Claude Code 的非交互认证方式：推荐由 Worker 的独立 Linux 服务账号使用客户管理的 API 凭据；不应依赖一个交互式个人登录会话。
