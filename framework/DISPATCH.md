# 统一 Worker 调用参数

继续使用既有 `agent_dispatch`，没有新增 `workflow_run`，不改变Workflow的经验契约定位。

```json
{
  "target_agent_id": "<verified-worker-agent-id>",
  "task": "仅处理该Workflow所属的原始事件；先查询再执行，写后核验。",
  "workflow": {"workflow_id": "实际active Workflow ID", "version": 1},
  "context": {
    "event": {
      "event_id": "真实原始事件ID或null",
      "content": "原始正文，不改写",
      "source": {"platform": "manual", "source_id": "真实来源ID"},
      "occurred_at": "2026-11-05T10:00:00+08:00",
      "evidence_refs": []
    },
    "related_run_id": null
  }
}
```

`target_agent_id`、字符串`task`始终必填。指定`workflow`时，ID及正整数version必填，`context.event`必须包含event_id（可null）、content（非空正文字符串，或最多100项的结构化变化对象数组）、source对象及evidence_refs数组；occurred_at可省略/null或RFC3339。原事件和来源保持原样，子任务边界写task。输入字段不是身份凭证。

工具验证Schema后，使用Runtime可信OwnerID查询Workflow；必须属于当前用户、状态active且版本一致。调用方同时需要`agent.dispatch`和`workflow.read`。已绑定其他Workflow的父调用禁止切换。后端只为子Run绑定经验证的scope，Filter自身和后续工具不会获得该Workflow PRI权限。工具不按agent_type硬编码，其他通用Agent也可使用同一契约。

子Runtime重新检查当前Workflow与选定版本，若选择与启动之间变化返回CONFLICT。通过后注入完整Workflow快照、真实run_id/parent_run_id及可信身份；RunRecord的JSONB中保存workflow_id/version/snapshot，无新数据库表或迁移。运行中的快照不随定义更新而变化；后续workflow_get仍读取最新定义，Worker需比较版本，发生变化停相关写入并重新评估。这里没有跨整个Run的数据库锁，归档/删除不会自动撤销已提交操作或取消运行。

返回值沿现有进程内Bridge同步回到父模型的tool消息，结构仍为`{run_id,output,error}`。output为子Agent最终文本，Worker应为JSON；父方必须解析业务status，Runtime成功不等于业务完成。工具结果大小、超时、调度深度、循环检查和共享预算继续生效。子任务已执行但返回超时/结果过大时不能假定无副作用，不能盲重试。

不带workflow的其他Agent调用保留原`task/context`包装，继承父方已有可信Workflow；`context.workflow`或`context.workflow_id`不会新增授权。不把原始RunRequest JSON当成可信绑定入口；只有经校验的工具处理器或宿主`WorkflowContext`可以绑定。

Filter实例已授权agent_dispatch，对确认的Worker目标实际调用。缺少适用流程时先调用Learner评估，创建或复用后workflow_get核验实际active版本，再调Worker；独立Workflow可并行，依赖顺序必须保留。事件变化数组和evidence_links保持结构化，change_id不代替platform_records.id。保留真实子结果，出现部分失败返回PARTIAL。Worker按统一输入解包，使用真实RunID及绑定版本；无需新Workflow执行引擎。Filter的模型调用预算由父子共享，默认32轮覆盖首次建流程与最终汇总。

验证：框架单元/race/vet覆盖统一输入、旧调用兼容、版本冲突、归属、状态、额外权限、上下文切换及父子scope隔离。后端`TestUnifiedWorkerDispatchPersisted`在独立PostgreSQL测试库中运行真实Runtime→工具→内部Bridge→子Runtime，验证原事件/私有记忆范围/结果回传及快照审计持久化。测试使用Mock模型，不调用真实教学平台或写生产业务数据。
