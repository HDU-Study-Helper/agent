# Host · 具体协调实体的经验契约

Host 由通用 Worker 承载，不是独立 Agent。Workflow及实例ID由当前环境生成。导入前将 `execution_agent_id` 的 `{{WORKER_AGENT_ID}}` 替换为当前用户已核实的 Worker ID；文件变更不会自动修改数据库。

`instructions.md` 是实际经验契约，`definition.json` 中 instructions 与其逐字节一致；`prompt.source.md` 保留历史原文。导入模板现为 **draft 且 entity=null**：缺真实协调实体不能创建可执行流程，也不能把 null 视为合法实体。启用前必须根据业务证据填入 `definition.entity:{kind:'task'|'identity',id,name}`，具体到协调任务或身份，由 Learner/授权管理路径创建或更新、回读后才可 active。不能编造通用 Host ID，不能用“课程/考试/网课”替代实体。模板明确记录 required_before_activation；这是提示词/定义契约，并未新增后端校验器。

新规则下，旧无 entity Host 不可被 Filter 匹配；迁移须核实具体实体及授权，不能直接把泛 Host 改成某门课。专业参与者分别核对自己的具体 entity；课程 ID 使用有据的 source/course/class 组合。Host 绑定自身 task/identity，不能切换到其他专业 Workflow、读取其 PRI 或以 Host 名义扩大权限。

输入保留 host/Filter 的 item_id、parent_item_id、entity、item 和完整 event；Filter 多事项子 ID 用原始 parent_item_id 加 ':1' 等稳定编号，Host 不重编号。多课程事项逐项保留事实、证据、结果及待处理项，同 Workflow 不同 item 不全局去重，不以笼统课程目录代替具体成果。

当前仅可查询共享业务证据、整合真实输入意见与方案；同一 Worker 调用链禁止递归，绑定 Workflow 不能切换，也没有 Agent Registry、Host 会话服务、通知送达或自动续跑。不能虚构专业 Agent 发言、意见共识或已执行操作。未来讨论策略不再设固定领域数/轮数/每人一次上限；当前真实循环与权限限制仍必须遵守。

Worker 顶层与 collaboration.status 只用 `COMPLETED/NEEDS_USER_INPUT/PARTIAL/FAILED`。产物必含 `type/title/description/entity_ref/data`，说明具体实体、各事项、冲突时间、方案影响和证据，data 保留 item/parent/entity 引用。未落库引用为 null 并标草案；NOTICE 及 NOTICE_WITH_ASK 仅应用内草案，不能声称发送或送达。用户回复须调用层重新发起并重新查证，不声称自动等待或恢复旧 Run。

不设置模型/工具低次数策略覆盖，仍遵守 Runtime 真实限制和 ctx 取消；取消后不新增派发/写入，不假称已提交操作回滚。execution/required_tools/entity_requirements 等为描述元数据，不是 DSL 执行引擎或新增硬性门禁。

已有同名 Workflow 不应重复导入；需要更新时先取最新整数版本、实体证据和完整 definition，经授权做版本化更新及回读。`dispatch.example.json` 仅保留历史调用形状，缺实体的旧目标不应直接运行，需先迁移/创建具体实体流程并替换真实 ID/版本/上下文。本次只改文件与验证模板契约，未执行数据库迁移、导入或真实模型任务，也未改 framework/backend。

`created` 仅表示首次观察，不能据此称刚发布；对照 before/after、发布时间和正文判断实质变化与行动价值。普通旧教材目录/material folder 无行动要求或实质更新可忽略，不为每项强建 summary，也不能仅按 resource 类型忽略。workflow_list 即使返回摘要，也必须 get 完整 definition、entity 与版本后才匹配。
