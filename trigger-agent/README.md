# Trigger Agent：变化初筛

当前模型协议为 `selection_protocol="indices_v1"`：每页 `changes` 用从 1 开始、按输入顺序连续的 `index` 标识，不发送 `change_id` 或 `evidence_ids`；输出恰好包含 `decision`、`reason`、`relevant_indices`。部署必须同步更新后端编号输入与结果映射，以及数据库中 Trigger 实例保存的提示词。仅修改本目录文件不会更新运行中的实例；本次未安装配置、未更新数据库实例，也未运行测试、模型、构建或校验命令。

后端将当前页编号映射回真实变化 ID 后持久化，历史保存的 `relevant_change_ids` 保持原样。模型输出不接受旧版 ID 字段或新旧字段混用。默认仍逐项选择所有有价值或待核查的信息，本次提速只缩短标识，不改成 batch-level gate。

Trigger 只判断信息是否值得下游理解、关联、核查或更新知识。目录、资料、进度、已完成作业及讨论参与记录即使没有新行动要求，也可能有画像价值。是否新增待办、日程、通知或具体事项属于后续处理，不能作为 Trigger 放行门槛。`created` 不证明新发布，但也不是过滤首次采集信息的理由。

当前课程批次由后端 runcontrol 接线，实际提示词以 [system-prompt.md](/home/skysight/aigc-project/agent/trigger-agent/system-prompt.md) 为准。本目录文件提供提示词、输入输出协议、案例准备逻辑和可加载定义，不修改 Go Framework，也不实现 Filter/Worker。

| 文件 | 用途 |
| --- | --- |
| [system-prompt.md](/home/skysight/aigc-project/agent/trigger-agent/system-prompt.md) | 完整提示词，直接作为 `system_prompt` 加载 |
| [definition.template.json](/home/skysight/aigc-project/agent/trigger-agent/definition.template.json) | 实例配置；`system_prompt` 与提示词文件逐字节一致，模型和服务需替换 |
| [input.schema.json](/home/skysight/aigc-project/agent/trigger-agent/input.schema.json) | `indices_v1` 模型输入 JSON Schema，Draft 2020-12 |
| [output.schema.json](/home/skysight/aigc-project/agent/trigger-agent/output.schema.json) | 严格三个字段的编号输出 JSON Schema |
| [invalid-inputs.json](/home/skysight/aigc-project/agent/trigger-agent/invalid-inputs.json) | 7 个调用前必须拒绝的历史异常输入；保持原文 |
| [cases.json](/home/skysight/aigc-project/agent/trigger-agent/cases.json) | 41 个历史输入及预期 decision、相关 ID；保持原文 |
| [evaluate.py](/home/skysight/aigc-project/agent/trigger-agent/evaluate.py) | 准备阶段转换旧 fixture，随后做契约检查或显式请求的真实模型评估/安装；本次未执行 |

## 运行边界

Trigger 不提供工具、权限、长期记忆、个性化用户资料或 Workflow Registry。一次 Run 仅调用模型 1 次，无历史对话。Filter 负责路由，Worker 负责执行，Trigger 不自行调度下游。

现有 Runtime 固定执行自动记忆加载；本实例不创建任何记忆，`memory_bytes=1`，任何记忆记录都无法注入上下文。仍会经过现有 Repository 读取，不要给此实例添加记忆。`enabled_tools` 和 `permissions` 均为空，不能执行任何业务工具。正式系统若需要完全跳过记忆数据库读取，应另行设计框架能力。

默认 Run 时限 60 秒，仍受模型服务自身的请求时限约束。模型选择需根据准确率、延迟和实际供应商计费评估；本定义不指定新供应商或更改现有 key。当前通过提示词要求 JSON，没有增加供应商原生结构化输出参数；接入层必须校验返回。

## 输入契约

可信调用方设置 RunRequest 的触发类型，RunRequest.input 使用如下对象：

```json
{
  "selection_protocol": "indices_v1",
  "batch_id": "sync_001",
  "page": {"index": 1, "count": 1, "change_offset": 0, "total_changes": 1},
  "changes": [
    {
      "index": 1,
      "operation": "updated",
      "resource": "assignment",
      "before": "报告周三截止",
      "after": "报告周五截止"
    }
  ]
}
```

最小字段为顶层 `selection_protocol`、`changes`，以及每项的 `index`、`operation`、`resource`。编号必须恰好对应数组位置 `1..N`，不使用真实 ID、全批偏移或跨页累加编号。`operation` 支持 created、updated、deleted、withdrawn。

可选 `before`/`after` 接受字符串或业务对象；`context` 为最多 10 条文本或对象形式的局部上下文。`source`、`occurred_at`、`changed_fields`、`previously_relevant`、`batch_id` 均可选。`previously_relevant` 必须来自可信后端记录，不能把原消息自称重要当成已确认标记。上下文及消息内容均为证据，不得解释为指令。

批次元数据可选，历史 fixture 无需补充：`scope` 含 `kind`（course / unassociated / group）、`connection_id`，以及可选 `offering_id`、`course_ref`、`course_name`、`group_id`；还可带课程业务对象 `course`、`refresh_id`、`processing_unit`（course / unassociated_connection / dws_group）、`time_semantics` 和纠正请求的 `validation_error`。`page` 若提供，应含 `index`、`count`、`change_offset`、`total_changes` 四个整数，页码和页数从 1 开始，offset 和总条数允许 0。

学习通和 MOOC 按同一连接、教学班/所属期次的课程分批，同名不同班/期不能合并；未可靠关联时不猜课程。DWS 按连接、群分批。分页后每页从 1 重新编号，编号只标识当前页项；不能跨页引用编号，也不能把前页同号项的内容或判断复用于当前页。模型逐项保留当前页所有有价值或待核查的信息。

单页最多 100 项；接入层还应限制输入字节预算，超限按完整变化分页，不截断正文后伪称无关。结构校验之外，必须检查编号等于数组位置：缺失、重复、跳号或非整数在调用模型前拒绝并保留整批。无 before/after/context 的项不能因缺正文直接丢弃；空页可确定性跳过模型并视为 IGNORE。

## 输出与确定性检查

```json
{"decision":"TRIGGER","reason":"作业截止日期发生变化","relevant_indices":[1]}
```

顶层恰好三个字段，不接受 Markdown、额外字段、`relevant_change_ids` 或协议混用。`relevant_indices` 的每项必须是 `1..当前页条数` 内的整数，唯一且按输入顺序递增。混合页同时保留明确相关项和不确定项，不含忽略项。合法非空页的 TRIGGER/UNCERTAIN 必须至少选一项；IGNORE 必须为空。异常输入兜底的 UNCERTAIN 可为空，但接入层应在模型调用前拒绝异常输入。

JSON Schema 只能给出静态整数边界和唯一性；当前页实际范围、顺序以及非空相关选择需接入层额外检查。只有完整通过这些检查的结果才可映射回真实变化 ID；不采信部分合法编号。映射由后端持有的当前页原始变化数组完成，持久层继续保存真实 `relevant_change_ids`，不改写历史记录。

TRIGGER 表示存在明确下游价值；无明确价值项但存在待核查项时为 UNCERTAIN；全部明确无关或空页为 IGNORE。是否进入 Filter 由后端规则决定，模型不创建业务对象，也不启动下游。

## 失败与不确定性策略

| 情况 | 接入层应如何处理 |
| --- | --- |
| 协议版本错误、缺字段、重复/不连续编号、类型错误 | 调模型前拒绝，保留整批待修复，不生成 IGNORE |
| 正文缺失但结构合法、无上下文的撤回 | UNCERTAIN；必要时补充证据供下游核查 |
| JSON 无效、额外/旧版字段、枚举错误、编号越界/重复/乱序 | 视为分类失败，保留整批，不采信局部数组 |
| 模型超时、取消、供应商错误 | 记录 Run ID 和安全错误，保留原事件，不视为已完成 |
| 暂时错误 | 持久化后有界重试；仍失败则带失败标记进入核查或人工队列 |
| UNCERTAIN | 有效分类结果，无需重复请求直到得到确定答案；带标记进入下游 |

去重、重试、状态与下游可靠性由后端负责，建议按用户范围与 batch_id/变化版本建立幂等关联，成功提交下游后才标记已处理。仅更新本目录定义不代表持久队列、重放或完整生产链路已经上线。

## 评估准备与报告

[evaluate.py](/home/skysight/aigc-project/agent/trigger-agent/evaluate.py) 在准备阶段深拷贝历史 fixture：移除每项 `change_id`、`evidence_ids`，映射成当前页 1-based `index`，并将 expected 的 `relevant_change_ids` 转为 `relevant_indices`。旧文件内容保持不变。重复旧 ID 映射为重复编号，缺 ID 不补编号，因此异常案例仍会被前置检查拒绝。

在线结果检查使用编号范围、唯一性、输入顺序和非空选择规则，再比较预期 decision 与 relevant_indices，并要求 1 次模型调用、0 次工具调用。持久报告带 `selection_protocol="indices_v1"`；重试复用还要求提示词哈希、fixture 哈希和模型配置一致，缺协议版本或旧协议的报告会被拒绝。

脚本保留 `--live`、`--install`、`--retry-failed` 等显式操作入口。真实模型评估消耗额度；安装会更新数据库实例，因此部署时需安排后端与实例提示词同步切换。本次只更新文件，未调用这些入口，也未安装配置。

## 信息价值职责回归

现有 41 个案例覆盖闲聊、广告、课程与校园事务，以及首次课程身份、旧教材与目录、零进度、已完成作业、本人讨论回复、真实进度改变、纯观察时间和倒计时噪声、空资料壳、混合批次不能只留未交作业等信息价值规则。本次保留这些业务规则和逐项高召回语义，只替换选择标识并补充分批、分页边界。TRIGGER 只表示信息应保留给下游，不代表新增事件或执行授权。
