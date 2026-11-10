# Learner · 具体实体 Workflow 管理

## 2026-11-09 首次创建画像契约

实际提示词现要求先以关键词及有来源的别名跨全部授权数据检索，不先限定某个课程或平台；使用 `data_search` 读完游标，再以 `data_query` 查询关联课表、个人作业、教学节点、资料和讨论参与。Host 当前事项快照不代表完整画像。搜索为有界发现摘要，截断字段需按 dataset/id 回读。

首次 `workflow_create` 同时传入 `initial_private_memories`，原子保存 Workflow PRI 索引和有来源的主题画像。种子每项顶层 `source_refs` 必填；后端赋予新 Workflow 归属，未绑定的 Learner 不因此获得任意 Workflow PRI 权限。`profile_manifest` 说明关键词、检索覆盖和六个画像维度，动态事实放私有记忆，不固化到 Workflow 规则。

Runtime 对 Learner 已开始但尚未完成的关键词分页进行宿主检查，提示继续；未执行写入的 Schema 错误应补齐参数。旧发现页可压缩为引用索引，完整数据仍按稳定ID回查。取消、真正的工具/权限错误及上下文限制仍按真实结果处理。下文早期示例展示旧基础格式，不代表当前完整画像创建请求。


实例ID由安装后端生成，不在模板中固定。`system-prompt.md` 为实际提示词；`definition.template.json` 内嵌提示词须逐字节一致；`prompt.source.md` 为历史原文。Learner 被动管理当前用户 Workflow，不替代 Filter 路由或 Worker 执行业务，不新增调度器或发现接口。

每个新建或可执行的 Workflow definition 必须含 `entity:{kind:'course'|'competition'|'task'|'identity',id,name}`，ID 和名称必须具体、有证据、非空。课程 ID 按 `source=<来源>|connection=<连接>|course=<课程>|class=<班级>` 稳定组合并转义分隔符，course/class 只纳入真实可得维度，`entity_source` 保留原始组成和 evidence_refs。after.parent_id 指向课程时，以 kind=course 且 external_id=parent_id 精确查源连接并读真实 title；缺真标题或必要标识待补充。禁止 generic“网课/课程/考试”或 unknown/default 等占位实体；缺实体先查证，仍缺返回 NEEDS_INFO，不造 ID，也不建占位 draft。

匹配要求 active、entity.kind/id 精确一致、目标适用。既有无 entity Workflow 不可复用为 NO_CHANGE；只有明确实体迁移证据和授权才可 UPDATE，不能把旧泛流程改成某门课而影响其他事项。同课程的作业/考试/资料可复用适用流程，不同课程、班级或具体竞赛不可仅因同名共用。

允许按多个具体实体多次 CREATE、回读并返回多个结果，不设一次调用一个流程或每缺口一次的提示词次数上限。每次先分页查询和读完整 definition，已创建结果先复用，写入结果未知先核查，防止重复。输入保留 host/Filter 的 `item_id,parent_item_id,entity,item,event`；单项调用返回一个管理结果，多实体请求返回 `{"results":[管理结果,...]}`，每项分别保留真实 Workflow ID、整数版本及 verified。

Learner 管理状态仍为 `APPLIED/NEEDS_INFO/CONFLICT/NO_CHANGE/REJECTED/ERROR`，operation 为 `CREATE/UPDATE/DELETE/NONE`。APPLIED 需真实写入并回读；只表示定义完成。创建后调用方重读真实版本，再由 Worker 执行业务。更新提交 name/description/status 和完整 definition；归档用 UPDATE，删除先核实授权、关联与结果，不能承诺跨调用事务。

新 definition 必须写入 entity、goal、applicability、instructions、input_contract、output_contract。Worker 输出四业务状态 `COMPLETED/NEEDS_USER_INPUT/PARTIAL/FAILED`；产物含 `type/title/description/entity_ref/data`，具体到实体、事项、时间/材料和证据，data 保留 entity/item_id。NOTICE 仅应用内草案，不声称已发送或送达；资料核查仅目录元数据，不声称已读正文。不以创建流程冒充业务完成。

固定 Worker 工具能力直接写入提示词，无需 Worker 工具发现或用户确认；Learner 自身缺日程工具不代表 Worker 缺能力。启用 5 个 Workflow 工具、8 个 Memory 工具、3 个数据只读工具。严格区分 change_id/record_id/external_id/connection_id，优先中心 ID 精确查证，保留原始结构化证据，工具失败不当空结果。

模板移除显式 `model_calls/tool_calls` 低次数覆盖，仍服从 Runtime 默认值、期限、循环和其他真实限制。遵守 ctx 取消与用户停止，取消后不新增写入/派发，准确说明已提交、已核验与未知结果。

`workflow_create` 完整参数示例（假定来源、连接、课程/班级及真实标题均已查证；示例值不能当真实 ID 使用）：

```json
{
  "name": "高等数学 · 资料与要求核查",
  "description": "核查指定课程实体的资料实质变化与行动要求。",
  "status": "active",
  "definition": {
    "schema_version": 1,
    "kind": "experience_contract",
    "entity": {
      "kind": "course",
      "id": "source=chaoxing|connection=conn-17|course=course-23|class=class-4",
      "name": "高等数学"
    },
    "entity_source": {
      "source": "chaoxing",
      "connection": "conn-17",
      "course": "course-23",
      "class": "class-4",
      "evidence_refs": [{"kind": "record_id", "id": "真实课程中心记录ID"}]
    },
    "goal": "核查该课程资料变化及行动要求，返回具体有据结果。",
    "applicability": {
      "include": ["entity.kind/id一致且有行动要求或实质更新"],
      "exclude": ["其他实体", "仅首次观察无实质更新的旧目录"]
    },
    "input_contract": {"required": ["item_id", "parent_item_id", "entity", "item", "event"]},
    "instructions": "先get完整active定义与版本，核对entity。created仅首次观察。按中心record_id/源连接查询资料名称、路径、变化和要求，不声称读正文。只执行本item和明确授权，写后回读，遵守ctx取消。无价值目录不强建summary。产物含type/title/description/entity_ref/data，data保留entity/item_id；草案引用null，NOTICE仅应用内DRAFT，不声称送达。",
    "output_contract": {
      "fields": ["run_id", "workflow_id", "workflow_version", "status", "summary", "artifacts", "actions", "evidence_refs", "interaction", "collaboration", "evolution_feedback"],
      "statuses": ["COMPLETED", "NEEDS_USER_INPUT", "PARTIAL", "FAILED"],
      "artifact_required_fields": ["type", "title", "description", "entity_ref", "data"],
      "notice_delivery": "in_app_draft_only"
    }
  }
}
```

name/description/status 是工具外层参数，entity 必须在 definition 内；parent_workflow_id 可省略。创建后 get 回读真实 ID、active、entity 及整数 version 才能交 Worker，不能凭示例 ID 派发。

根目录运行 `python3 agent/learner-agent/install.py`；更新须 `--update-agent-id <existing-learner-id>`。脚本使用既有运行环境认证，不将凭据写入文档。控制台提示词上限为64 KiB UTF-8，Learner上下文预算为4 MiB。此次仅修改文件并验证模板契约，不表示已同步实例、迁移存量 Workflow 或真实模型验证。

`created` 仅表示首次观察，不能据此称刚发布；对照 before/after、发布时间和正文判断实质变化与行动价值。普通旧教材目录/material folder 无行动要求或实质更新可忽略，不为每项强建 summary，也不能仅按 resource 类型忽略。workflow_list 即使返回摘要，也必须 get 完整 definition、entity 与版本后才匹配。
