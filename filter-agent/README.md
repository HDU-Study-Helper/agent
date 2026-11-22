# Filter · 智能信息拆解与路由

Filter 结合新信息、用户记忆和已有 Workflow，理解事项对象、用户关系和处理目标，选择 Learner 或 Worker。提示词按工作背景、角色、输入、记忆、查询边界、路由、交接与输出组织。

## 提示词与格式约定

[system-prompt.md](system-prompt.md) 是完整提示词，[definition.template.json](definition.template.json) 的 system_prompt 保存相同文本；[prompt.source.md](prompt.source.md) 保留历史原文。修改文件不会自动更新数据库中已安装的实例。

工具调用的正式 JSON Schema 由 Runtime 通过 tools 单独提供，提示词中用缩进、换行的 JSON 说明 Worker/Learner 参数布局。最终答复采用提示词末尾的格式化 JSON 示例与字段规则，不在提示词中重复一整份输出 Schema。实际模型答复只返回 JSON 对象，不能包含代码围栏或示例占位值。现有后端继续解析结果，并核验真实事项、证据、子运行、Workflow 归属和业务状态；没有新增供应商原生 response_format。

## 记忆与查询边界

先利用已注入记忆与宿主快照；缺少路由依据时，用 memory_search、memory_get 或 memory_get_relations 定向补查，必要时按分类浏览。记忆用于理解身份、课程别名、群映射、用户决定和路由经验。区分用户确认、来源观察和模型推断，检查有效期与适用范围；新旧冲突保留依据，不把缺结果当作不存在。

Filter 只有记忆读取权限。可见用户 PUB、自身 Agent PRI 及可信绑定 Workflow PRI；不能读取其他 Agent 或未绑定 Workflow 的 PRI。需要维护记忆的内容交给下游，并明确传递结论、来源与不确定性，不假设子 Agent 共享完整对话和父方私有记忆。

补查只为确定具体对象、用户关系、已有事项关联或适用 Workflow。能够正确交接即停；业务细节交 Worker，流程调查与管理策略交 Learner。不强制全数据集检索、固定画像、profile_manifest 或初始化记忆回执。

## 输入与输出

宿主提供真实 item_id、content 变化数组、来源及证据映射，也可能提供课程变化组或群范围。一次变化不等于一个事项，同一语义目标可合并，独立目标可拆分。当前后端仍有单条路径和课程组路径，不将 DWS 按群封批描述成已实现整群 Filter 调用。

单事项沿用宿主 item_id，parent_item_id=null；多事项按原文顺序用原 ID 加 :1、:2 等后缀，parent_item_id 为宿主原 ID，不同时输出父容器。

每项必须包含 item_id、parent_item_id、title、status、reason、evidence_refs、routes、artifacts。状态仅 IGNORED、COMPLETED、NEEDS_USER_INPUT、PARTIAL、FAILED。每次真实 Learner/Worker 调用保留对应 route，未调用用空数组。Filter 的 artifacts 固定为空，宿主从子运行提取产物。COMPLETED 必须有实际业务完成的 Worker；仅建流程或完成路由不能称业务完成。

## 能力与交接

模板保留原有13个工具：Workflow只读2个、记忆只读4个、数据只读4个、日程只读2个及 agent_dispatch。权限与运行限制未扩展。

Worker 派发在顶层传 workflow_id 和整数 version，context 显式传递 item_id、parent_item_id、entity 和目标。Learner 不绑定顶层 workflow，传递管理意图、可复用目标、候选缺口及证据。原始 event 在宿主已有绑定时由工具自动转交；框架不从 Workflow 自动覆盖 context.entity。课程身份沿用可信实体，需构造时使用已查证 source、connection、course_ref，不按跨平台同名猜映射。

## 安装与当前状态

根目录安装入口为 `python3 agent/filter-agent/install.py`；更新现有实例需显式指定 `--update-agent-id <existing-filter-id>`。安装器从当前用户实例解析唯一启用的 Worker/Learner，替换提示词占位符；目标缺失或不唯一时拒绝安装。凭据由既有环境提供，不写入提示词或模板。

本次仅重写提示词并同步配置模板；未执行安装、测试或模型调用，未更改数据库实例、工具权限或运行限制。
