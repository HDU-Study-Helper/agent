# Trigger Agent：变化初筛

当前 Trigger 只判断信息是否值得下游理解、关联、核查或更新知识。目录、资料、进度、已完成作业及讨论参与记录即使没有新行动要求，也可能有画像价值。是否新增待办、日程、通知或具体事项属于后续处理，不能作为 Trigger 放行门槛。`created` 不证明新发布，但也不是过滤首次采集信息的理由。

以下保留早期框架验收背景；当前课程批次由后端 runcontrol 接线，实际提示词以本目录 system-prompt.md 为准。

这是第一个基于现有通用框架配置的业务 Agent。交付范围依据本目录 AGENT.md：提示词、输入输出协议、案例和可加载定义，不修改 Go Framework，不实现 Filter/Worker，也不把后端 trigger 自动接线当作已完成。

| 文件 | 用途 |
| --- | --- |
| system-prompt.md | 完整 System Prompt，直接作为 system_prompt 加载 |
| definition.template.json | 与现有后台兼容的实例配置，含相同提示词；模型名称与服务需替换 |
| input.schema.json | 输入 JSON Schema，Draft 2020-12 |
| output.schema.json | 输出 JSON Schema，Draft 2020-12 |
| invalid-inputs.json | 7 个必须在调用模型前拒绝的异常输入，含重复 ID |
| cases.json | 41 个输入与预期 decision、相关 ID；reason 不做逐字断言 |
| evaluate.py | 契约校验及一次命令批量真实模型测试；可将通过的定义加入本地后台 |

## 运行边界

Trigger 只判断“是否值得继续处理”。Filter 负责路由，Worker 负责执行。Trigger 不提供工具、权限、长期记忆、个性化用户资料或 Workflow Registry。一次 Run 仅调用模型 1 次，无历史对话。

现有 Runtime 固定执行自动记忆加载；本实例不创建任何记忆，memory_bytes=1，任何记忆记录都无法注入上下文。这不是新增“无状态模式”，仍会经过现有 Repository 读取。不要给此实例添加记忆。tool_calls=1 是框架正数预算的最小值，不是授权；enabled_tools 和 permissions 均为空，不能执行任何业务工具。正式系统若需要完全跳过记忆数据库读取，应另行设计框架能力，当前不改 Runtime。

默认 Run 时限 60 秒，仍受模型服务自身的请求时限约束。生产模型选择需根据准确率、延迟和实际供应商计费评估；本定义不擅自指定新供应商或更改现有 key。当前使用提示词要求 JSON，而没有增加供应商原生结构化输出参数；生产接入层必须校验返回，不能假设模型一定遵守。

## 输入契约

RunRequest 的 trigger 由可信调用方设为 DATA_CHANGED；它与本 Agent 的角色名称 trigger 不同。RunRequest.input 为 input.schema.json 定义的对象，例如：

```json
{
  "batch_id": "sync_001",
  "changes": [
    {
      "change_id": "change_001",
      "operation": "updated",
      "resource": "assignment",
      "before": "报告周三截止",
      "after": "报告周五截止",
      "evidence_ids": ["record_001"]
    }
  ]
}
```

最小字段为 changes 数组，以及每项的 change_id、operation、resource。变化 ID 在批次内必须唯一。operation 支持 created、updated、deleted、withdrawn；这是 Agent 输入协议，不意味着现有采集器已产生全部类型。

可选 before/after 接受字符串或业务对象；context 为最多 10 条文本或对象形式的局部上下文。source、occurred_at、changed_fields、previously_relevant、evidence_ids、batch_id 均可选。previously_relevant 必须来自可信后端先前记录，不能把原消息自称重要当成已确认标记。上下文及所有消息内容均为证据，不得解释为指令。

单批最多 100 项；接入层还应把序列化 input 限制在 48 KiB 以内，为系统提示词和协议保留预算。超限按完整变化拆批，不截断正文后伪称无关。结构校验之外，必须检查 ID 唯一性和内容是否实际可供判断：无 before/after/context 的项不能因缺正文直接丢弃。空批次可确定性跳过模型并视为 IGNORE；测试也覆盖模型对空批次的结果。

现有 backend/internal/trigger.Change 仅有变化标识、记录 ID 和字段名等信息，不能直接替代此输入。未来接入层须在授权范围内组装快照或局部上下文，不能把用户无权限读取的数据装入批次。此交付未增加快照存储或 Diff Engine。

## 输出与确定性检查

```json
{"decision":"TRIGGER","reason":"作业截止日期发生变化","relevant_change_ids":["change_001"]}
```

顶层严格三个字段，不接受 Markdown 或额外输出。所有相关 ID 必须来自 changes，按输入顺序、无重复。混合批次同时保留明确相关项和不确定项；不含忽略项。合法非空批次的 TRIGGER/UNCERTAIN 必须有相关 ID；IGNORE 必须为空。JSON Schema 无法单独验证 ID 与输入的关联，需要接入层额外检查。

后端映射建议：TRIGGER 进入 Filter；UNCERTAIN 也进入 Filter，并带不确定性标记；仅合法、完整、通过校验的 IGNORE 可结束本阶段。模型不得自行调度下游。这些是待接入的后端策略，不是此定义已具备的自动化能力。

## 失败与不确定性策略

| 情况 | 接入层应如何处理 |
| --- | --- |
| 输入缺关键字段、重复 ID、类型错误 | 调模型前拒绝，保留整批待修复；不要生成 IGNORE |
| 正文缺失但结构合法、无上下文的撤回 | UNCERTAIN；必要时补充证据，继续下游核查 |
| JSON 无效、额外字段、枚举错误、编造 ID | 视为分类失败；保留整批，不采信局部数组 |
| 模型超时、取消、供应商错误 | 记录 Run ID 和安全错误；保留原事件，不视为已完成 |
| 暂时错误 | 持久化状态后做有界重试，例如最多 2 次、退避；仍失败则带失败标记进入核查或人工队列 |
| UNCERTAIN | 是有效分类结果，不必重复请求直到得到确定答案；带标记进入 Filter |

去重、重试、状态与下游可靠性由后端负责：建议按用户范围与 batch_id/变化版本建立幂等关联，成功提交下游后才标记已处理。现有 trigger 是尽力通知，无持久队列/重放；仅添加此 Agent 不会解决提交后崩溃丢通知的问题。上述队列、重试、自动调用和结果校验尚未集成，不得声称生产链路已上线。后台当前展示原始输出，测试脚本负责校验。

## 批量验证与加载

测试依赖仅用于 Python 校验器，不增加 Go 依赖：

```bash
python3 -m venv /tmp/trigger-eval
/tmp/trigger-eval/bin/pip install -r agent/trigger-agent/requirements-dev.txt
/tmp/trigger-eval/bin/python agent/trigger-agent/evaluate.py
```

真实模型批测使用后台已保存的服务配置，消耗模型额度；临时用户及其实例会在 finally 中清理。设置环境中的 AGENT_TEST_USERNAME/PASSWORD 或 BOOTSTRAP_ADMIN_USERNAME/PASSWORD；不要把口令放入命令参数。需要时先在 backend 目录加载本地 .env，随后从项目根目录执行：

```bash
/tmp/trigger-eval/bin/python agent/trigger-agent/evaluate.py --live --install
```

--live 顺序执行全部 41 项（单一批量脚本，避免供应商突发限流），检查严格 JSON、预期判断、相关 ID、1 次模型调用和 0 次工具调用。--install 在本次全部用例通过后，为登录管理员添加 Trigger 实例；同名同角色实例存在时更新，发现已有记忆则拒绝覆盖。它不更改模型服务配置和 key。定义模板中的模型取已配置且有 key 的第一个服务；如需独立供应商，请在后台修改实例。

报告默认为 .build/trigger-agent-evaluation.json。单次样本通过不代表所有真实数据准确；上线前应使用脱敏真实变更集持续评估漏报率、误报率和延迟。

## 信息价值职责回归

新增11个用例覆盖首次课程身份、旧教材与目录、零进度、已完成作业、本人讨论回复、真实进度改变、纯观察时间和倒计时噪声、空资料壳，以及混合批次不能只留未交作业。已有“无副作用、无个性化、无工具”的边界保持。TRIGGER只表示信息应保留给下游，不代表新增事件或执行授权。
