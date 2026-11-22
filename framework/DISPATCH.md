# 通用 Agent 调度

`agent_dispatch` 同步调用同一 owner 下已启用的 Agent。框架提供目标校验、可信执行范围传递与运行预算，不按角色决定业务分工、检索步骤或最终答复格式。

```json
{
  "target_agent_id": "<registered-agent-id>",
  "task": "处理指定子任务",
  "workflow": {"workflow_id": "实际 active Workflow ID", "version": 1},
  "context": {
    "custom_input": {"values": [1, false, null]},
    "related_run_id": null
  }
}
```

`target_agent_id` 和字符串 `task` 必填。`workflow` 与 `context` 都可省略。提供 `workflow` 时，其 ID 和正整数 version 必填；提供 `context` 时，它可以是任意 JSON 对象。是否传递事件、实体或其他业务字段由调用方与宿主协议决定。框架不从 Workflow 定义提取 entity 覆盖模型 context，也不添加实体差异警告。输入中的 owner、权限或 Workflow 文本不构成授权。

调用方需要 `agent.dispatch`。选择 Workflow 还需要 `workflow.read`：工具使用 Runtime 的可信 OwnerID 查询，验证 Workflow 属于当前用户、状态为 active、版本一致。已绑定 Workflow 的父调用不能切换到其他 Workflow。工具为子调用绑定验证后的 scope 和快照，父调用自身及后续工具不会因此新增该 Workflow PRI 权限。

省略 `workflow` 的调用保留 `task/context` 包装，并继承父调用已有的可信 Workflow。`context.workflow`、`context.workflow_id` 或原始 RunRequest JSON 都不能建立新的可信绑定。只有验证后的工具处理器或宿主 `WorkflowContext` 可以绑定执行范围。

宿主可以通过 `WithDispatchEvent` 在可信 Go context 中绑定同 owner 的原始事件。该事件必须为 JSON 对象，框架不规定业务字段。调度时会将完整原始对象转发到 `context.event`；调用方可省略该字段，显式提供时必须与宿主事件全部内容一致，不能删除、添加或改写字段。框架保留数字精度以防不同大整数被误判为相同。没有宿主绑定时，`context.event` 与其他业务字段一样原样传递，不受事件 schema 限制。事件来源不会授予权限。

子 Runtime 重新检查 Workflow 与选定版本，选择与启动之间发生变化会返回 `CONFLICT`。通过后，Runtime 注入完整的可信 Workflow 快照、真实 run_id/parent_run_id 与执行身份，并在 RunRecord 中保存 Workflow ID、版本和快照。模型 context 中的业务字段与这份可信快照分别保留。运行中的快照不随定义更新而变化，后续 `workflow_get` 读取最新定义。同一 owner/Workflow 的运行通过宿主提供的 WorkflowLocker 串行，未提供时使用进程内锁；等待支持 context 取消。归档或删除不会自动撤销已提交操作或取消运行。

返回值通过进程内 Bridge 同步回到父模型的 tool 消息，结构为 `{run_id,output,error}`，output 是子 Agent 的最终文本。角色专属 JSON 输出、业务状态、派发顺序和具体 Workflow 流程属于宿主协议，可由 Agent 配置与宿主输入约定。框架运行成功仅表示运行完成，业务完成由消费结果的宿主判断。

工具输入和输出 schema、owner 与权限隔离、Workflow 版本、取消、结果大小、超时、调度深度、循环检查和父子共享预算继续生效。子任务已执行但返回超时或结果过大时，调用方应依据真实运行记录判断副作用，再决定是否重试。

回归测试覆盖任意 context、无事件调度、业务 entity 不改写、完整宿主事件转发与防替换，以及 owner、权限、版本、scope、循环调用和父子隔离。宿主可在自己的测试中验证具体角色协议与持久化流程。
