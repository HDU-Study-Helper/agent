# Filter · 智能信息拆解与路由

实例ID由安装后端生成，不在模板中固定。`system-prompt.md` 是实际提示词，`definition.template.json` 内嵌内容与其逐字节一致；`prompt.source.md` 保留历史原文。文件修改不会自动同步已安装实例。

host 每次传入一个 change item 和原始 `item_id`，Filter 阅读全部原文后按具体实体、动作、时间拆为完整事项，最终只返回 `{"items":[...]}`。每项必须包含 `item_id,parent_item_id,title,status,reason,evidence_refs,routes,artifacts`，不以泛课程目录代替具体事项。单事项沿用 host ID；多事项按原文顺序使用 `parent_item_id + ':1'`、`':2'` 等稳定子 ID，父 ID 始终来自 host，不能拿 change_id/event_id 代替。缺调用层 ID 时不派发，返回明确待补充项。

状态仅 `IGNORED/COMPLETED/NEEDS_USER_INPUT/PARTIAL/FAILED`。逐项保留完成、忽略、待补充和失败结果，不用单一总状态掩盖缺项。每项可有多条 Learner/Worker route，`route.item_id` 必须匹配所属事项；先 Learner 创建、再 Worker 执行须保留两次真实调用结果。同 Workflow 可处理不同 item 多次，不能仅按 workflow_id 去重。

Workflow 必须 active、版本真实、目标适用，并含与本项一致的具体 `definition.entity:{kind,id,name}`；kind 限 `course/competition/task/identity`。课程 ID 用 `source=<来源>|connection=<连接>|course=<课程>|class=<班级>`，分隔符转义，course/class 只纳入真实可得维度。after.parent_id 指向课程时，用 kind=course 且 external_id=parent_id 精确查询并核对源连接，取得真实 title，缺真标题或必要标识则待补充。禁止 generic“网课/课程/考试”实体，无 entity 旧 Workflow 不匹配，缺实体先查证、仍缺则待补充，不造 ID。不同实体缺流程时可分别多次交 Learner CREATE，同实体先查已创建结果并复用。

固定 Worker 能力直接写在提示词中，不新增发现工具，不向用户询问固定工具目录。启用 12 个工具：Workflow/Memory/平台数据/日程只读及 `agent_dispatch`；无业务直接写入或通知渠道。原始 event、结构化 content、evidence_links 与引用类型完整转交；严格区分 change_id、record_id、external_id、connection_id，优先用中心 record_id 精确查询。Worker 实际调用仍受授权、Schema 和 Runtime 约束。

产物必须具体，含 `type/title/description/entity_ref/data`，保留真实对象/证据及本项 ID；未持久化用 null 引用并标记草案。NOTICE 仅应用内草案，不代表发送或送达。仅创建 Workflow 不表示本项业务完成。

模板设置 `unlimited=true`，不以固定模型/工具次数或总运行时间截断完整调查；上下文、单工具结果、取消、循环、并发与其他真实限制仍有效。收到 ctx 取消或用户停止后不再新增派发/写入，保留已完成及未知状态。

安装使用根目录 `python3 agent/filter-agent/install.py`；更新需显式指定 `--update-agent-id <existing-filter-id>`。脚本读取既有运行环境认证配置，解析当前用户唯一启用的 Worker 和 Learner，并替换提示词目标占位符；目标缺失或不唯一时拒绝安装。不要将凭据写入文件。控制台提示词上限为64 KiB UTF-8，模板必须同步验证；安装只配置实例，不运行模型。

`created` 仅表示首次观察，不能据此称刚发布；对照 before/after、发布时间和正文判断实质变化与行动价值。普通旧教材目录/material folder 无行动要求或实质更新可忽略，不为每项强建 summary，也不能仅按 resource 类型忽略。workflow_list 即使返回摘要，也必须 get 完整 definition、entity 与版本后才匹配。
