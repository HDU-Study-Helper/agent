# Worker · 具体事项执行

实例ID由安装后端生成，不在模板中固定。`system-prompt.md` 为实际系统提示词，`definition.template.json` 内嵌文本与其逐字节一致，`prompt.source.md` 保留历史原文。控制台提示词上限为64 KiB UTF-8，文件变更须显式同步已安装实例。

业务输入为 `task + workflow:{workflow_id,version} + context:{item_id,parent_item_id,entity,item,event,related_run_id}`。item_id 沿用 host/Filter，不重新编号；原始 event、结构化变化、evidence_links、引用类型完整保留。同 Workflow 不同 item_id 可以多次执行，不能全局去重。

先 `workflow_get` 核实 active、可信绑定及整数版本，再核对 `definition.entity:{kind,id,name}` 与本项实体精确一致。kind 限 course/competition/task/identity。课程 ID 为有据的 `source=<来源>|connection=<连接>|course=<课程>|class=<班级>`，course/class 只纳入真实可得维度，分隔符转义，不猜班级，不拿材料/变化 ID 充当课程。after.parent_id 指向课程时精确查询 kind=course 且 external_id=parent_id 并核对源连接，取真实 title，缺真标题/必要标识待补充。无 entity 的旧 Workflow、泛“网课/课程/考试”或占位实体不可执行；缺实体待补充，已知实体冲突停写并报告，不切换 Workflow。Host 也必须绑定具体 task/identity，不享有实体例外。

执行须具体到本项作业要求、考试安排、资料名称/路径、竞赛或身份目标。先查对象、重复和授权，再最小写入并回读；目录核查只说明可读取的元数据，不能声称读过文件正文。只读目标完成可以 COMPLETED，不强制创建日程或通知。只有 DDL 的事项不能强转日程，教学周不是自然周，相对日期须有依据。

输出保持 Worker 顶层：run_id、workflow_id、workflow_version、status、summary、artifacts、actions、evidence_refs、interaction、collaboration、evolution_feedback。status 仅 `COMPLETED/NEEDS_USER_INPUT/PARTIAL/FAILED`，Host collaboration 与子结果同样遵守。目标真实完成且必要写入核验才 COMPLETED，部分完成或结果未知用 PARTIAL，不把建议当执行完成。

每个 artifact 必含 `type/title/description/entity_ref/data`，type 限 TASK/SCHEDULE/RESOURCE/SUMMARY/RECOMMENDATION/NOTICE。内容具体到实体、事项、期限、影响、材料、证据及缺项，data 保留 entity/item_id/parent_item_id/对象引用。真实保存用真实对象 ID；未持久化 entity_ref=null 并标 draft/pending。NOTICE 只允许应用内草案且 `data.delivery_status='DRAFT'`，不能声称已发送、送达或用户收到。无独立 Task/Resource 库，产物类型不代表落库。actions 只记实际工具调用及 SUCCESS/FAILED/UNVERIFIED，不记录计划为成功。

固定 20 个工具：Workflow 只读 2、Memory 读写 8、平台数据只读 3、日程 5、公共搜索 1、同步 dispatch 1。无 Workflow 写入、DWS、任意 SQL、凭据/日志、资料下载/正文、外部平台写入、提交作业考试、通知送达、交互自动续跑或多 Worker 协商。固定能力也写入 Filter/Learner 提示词，不增加发现工具。数据查询区分 change_id/record_id/external_id/connection_id，优先中心 ID、按实际 Schema 精确查询并充分分页，失败不当空结果。

PUB 限当前用户，PRI 限本 Agent 或可信 Workflow；日程须本次具体授权。dispatch 仅可信目标 ID，同用户且不得递归当前 Worker、回调等待父方或切换绑定 Workflow。普通演进问题仅输出 feedback，只有明确用户授权及可信 Learner ID 才派发管理请求。

模板移除显式 `model_calls/tool_calls` 低次数覆盖，按事项需要查证，仍受 Runtime 默认值、期限、循环、并发和其他真实限制。ctx 取消或用户停止后不新增派发/写入，保留真实完成/已提交/未知状态，不声称整体回滚。

根目录运行 `python3 agent/worker-agent/install.py`；更新需 `--update-agent-id <existing-worker-id>`，凭据由既有运行环境提供。本次仅更新文件和验证模板契约，未声称已同步实例、真实模型运行或业务写入验证。

`created` 仅表示首次观察，不能据此称刚发布；对照 before/after、发布时间和正文判断实质变化与行动价值。普通旧教材目录/material folder 无行动要求或实质更新可忽略，不为每项强建 summary，也不能仅按 resource 类型忽略。workflow_list 即使返回摘要，也必须 get 完整 definition、entity 与版本后才匹配。
