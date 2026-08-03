# customer-hosted FDE × SRE RCA MVP：可读性重构设计

**状态：** 本文描述已完成的代码边界整理；不改变已验证的线上契约。

## 重构前的真实复杂度

`internal/provider/aliyun.go` 曾同时承担模板选择、selector 校验、阿里云请求
构造、SDK 调用和响应摘要。虽然请求是 allowlist 的，但要 review 一个 SLS 双引号
查询或一个 UModel EntityStore 请求，必须在同一个大文件中穿行。`CMSWriter.Upsert`
也同时分类 EntityStore entity/topology 记录、构造 LogGroup 和写 SLS。

这些不是功能缺失；风险在于以后新增证据场景时，容易把“哪些 selector 可进入云
请求”和“如何把结果缩减为摘要”混在一起，难以验证客户自托管与只读边界。

## 目标边界和依赖方向

```text
Binding + Incident Binding
            |
            v
runtime ----> evidence.Service ----> provider request builder ----> Alibaba SDK
                                        |                                 |
                                        +--> safe summary <---------------+

sre-sync config --> modelsync.Plan --> classified Log Protocol batches --> SLS
                                      |                                      |
                                      +--> CMS GetEntityStoreData <---------+
```

- `runtime` 只组合配置、Binding、Repository 和单一只读 Provider；不拼接云查询。
- `provider/request_builders.go` 是从已审核 selector 到固定 `AliyunRequest` 的唯一
  转换点；不接受 Callback 负载和模型文本。
- `provider/aliyun.go` 保留模板路由、SDK 调用和 Evidence 生成；摘要只读取响应并
  生成最小中文结论与哈希引用。
- `modelsync/writes.go` 先分类和校验一个 `Plan`，生成 entity/topology 两个独立
  SLS 批次；`CMSWriter` 只负责按批次调用写客户端。

这保持依赖单向：HTTP 和 Worker 不依赖云 SDK 细节；Provider 不依赖 HTTP、飞书或
SQLite SQL；UModel 写入不复用只读取证 Provider。

## 关键接口、数据流和状态机

`AliyunProvider.Resolve(binding, selectors, window)`、YAML Binding、CLI 参数和
`domain.Evidence` 没有改动。每次 Resolve 先校验时间窗和 selector，再按固定
`query_template` 调用 request builder，最后返回包含摘要与 `RawRef` 哈希的 Evidence；
原始响应不进入模型上下文或飞书。

`sre-sync` 默认 dry-run。`--apply` 时，端点实例写入
`${workspace}__entity`，`runs_on` 关系写入 `${workspace}__topo`。关系记录必须带
六个 source/destination 身份字段与 `__relation_type__`；验证仍只使用 CMS
`GetEntityStoreData`。

Worker 状态语义保持不变：

```text
RECEIVED -> INVESTIGATING -> COMPLETED
                      \-> AWAITING_AUDIT_EVENT -> (delayed retry)
active incident + RECOVERED -> RECOVERED
job error after bounded retries -> FAILED
```

租约、`CheckActiveClaim` 和同卡更新 fence 继续阻止过期 Worker 覆盖恢复态或其它
Worker 的结果。

## 为什么这样拆分

请求构造是最值得独立审计的安全边界：reviewer 可以直接确认 SLS 字段值全部加
双引号、CloudMonitor CPU 固定 Namespace/Metric/Period、EntityStore 使用 CMS ROA
JSON 读 API。批次构造同样把“实体和拓扑绝不混写”的协议事实变成独立可测单元。

这不是扩展 Provider 的通用框架：注册表仍是有限 allowlist，同一个
`AliyunProvider` 仅以固定名称注册给 evidence Service。没有动态 Provider、任意
模板或反射式 SDK 调用。

## 如何新增已获批的证据场景

1. 先审核 Incident Binding 的稳定 selector；Callback 不能提供 selector。
2. 查当前 Alibaba SDK 实现与官方文档，确认请求形状、区域 endpoint、只读 RAM
   权限和响应语义。
3. 在 `internal/evidence/registry.go` 明确加入 provider/template allowlist。
4. 在 `request_builders.go` 添加固定 request builder，在 Provider 添加最小路由和
   不泄露原始数据的摘要。
5. 先写请求形状与摘要的 characterization tests；特别验证没有选择器时不发云请求。
6. 更新示例 Binding、Runbook 和本文件的状态标记；经 review、测试和客户变更流程
   后才考虑部署。

## 能力状态

| 状态 | 能力 |
| --- | --- |
| 已真实验证 | CloudMonitor CPU Callback、SQLite 幂等/租约、UModel endpoint 与 `runs_on`、CloudMonitor CPU、SLS Nginx access、同张飞书卡更新。 |
| 当前有意不启用 | ActionTrail 安全组变更 Binding；未经审核的 `security_group_id` 不得查询。 |
| 后续候选 | 在每个场景完成 selector 审核、最小权限和真实演练后，增加 ActionTrail、Kubernetes/RDS 或其它 Provider；不代表已完成。 |
