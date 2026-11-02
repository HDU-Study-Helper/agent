# Academic Agent — 通用智能体框架第一阶段开发任务

## 一、项目背景与目标

我们正在开发 Academic Agent，一套面向大学生的智能学业辅助系统。

未来系统的核心智能体包括：

- **Filter Agent**：结合路由记忆，识别新增事件并将其分发至对应 Workflow。
- **Worker Agent**：执行 Workflow，调用工具完成具体任务。
- **Learner Agent**：分析用户反馈和执行结果，持续创建、修改和强化 Workflow。

系统未来还可能增加总结、资源分析等辅助 Agent。

**本次任务仅实现这些 Agent 共同依赖的通用 Agent Framework，不实现任何具体业务 Agent。**

框架必须支持任意数量的 Agent 实例，并提供统一的运行环境、工具系统、记忆系统、通知能力、数据查询、联网搜索、智能体调度和 Workflow 定义管理。

核心设计要求：

1. 新增 Agent 实例不需要修改框架代码。
2. 新增工具不需要修改 Agent Runtime 主循环。
3. 每个 Agent 有独立的身份、配置、记忆和工具权限。
4. 内部模块通过 Go Interface 和现有 `internal/bridge` 通信。
5. Agent Framework 不承担后端 HTTP API 层的职责。
6. 为未来接入 Filter、Worker、Learner 保留清晰的扩展接口。

---

## 二、架构边界与现有项目约束

现有项目采用：

- Go
- chi
- PostgreSQL
- 模块化单体
- `internal/bridge`
- 现有 `dbbridge` 数据访问机制

本次开发需要严格遵守已有项目结构。
本次开发内容请放在 agent/framework下。

### 2.1 Agent Framework 的职责

Agent Framework 负责：

- Agent 实例管理。
- 模型调用与 Tool Calling。
- 运行上下文管理。
- 工具注册、权限检查和执行。
- Agent 私有记忆。
- Agent 主动通知工具。
- 数据查询工具。
- 联网搜索工具。
- Agent 间调度。
- Workflow 定义管理。
- Agent 运行记录与错误处理。

### 2.2 Agent Framework 不负责的内容

以下属于后端其他模块或未来业务系统，不属于本次 Agent Framework 的开发范围：

- Android App 的 HTTP API 设计。
- chi 路由注册。
- 面向客户端的 REST 接口。
- 用户登录与 SSO 认证流程。
- Android 通知界面和推送客户端。
- 校园平台的数据采集和轮询。
- Diff 检测。
- 课程、作业、考试等具体业务模型。
- Filter、Worker、Learner 的业务逻辑。
- 完整 Workflow 执行引擎。

框架只需要提供清晰的内部 Go 服务接口，供后端其他模块调用。

例如，后端的业务模块可以通过内部接口请求运行某个 Agent，但 Agent Framework 不关心这次请求最初来自 HTTP、定时任务、消息事件还是其他来源。

### 2.3 Internal Bridge

项目已经规划使用 `internal/bridge` 进行内部模块通信。

本次开发必须优先沿用这一机制。

Agent Framework 与后端其他模块之间通过 Bridge 暴露或调用服务能力。

Agent 间调度也必须通过内部 Bridge 和 Agent Runtime 完成。

**禁止为了内部通信请求本机 HTTP 接口。**

不得为 Agent 创建独立 HTTP 服务，也不应额外引入微服务或 RPC 框架。

同步调用优先采用 Go Interface；需要异步执行时，可以复用项目已有 Job/Event 机制。

### 2.4 数据库访问

持久化操作应通过现有 `dbbridge` 或项目已有 Repository 机制实现。

不要为了 Agent Framework 建立第二套平行的数据库访问基础设施。

### 2.5 开发前检查

开始实现前，必须阅读现有项目中的：

- Agent 相关代码（如存在）。
- `internal/bridge`。
- `dbbridge`。
- 模型调用相关代码。
- 数据库迁移与配置机制。
- 通知服务。
- 用户身份与权限相关的数据结构。

在此基础上选择最小兼容实现，不要未经分析进行大规模重构。

---

## 三、总体组件设计

Agent Framework 包含以下逻辑组件。

### Agent Registry

管理 Agent 定义、实例、提示词、模型配置及权限。

### Agent Runtime

负责启动 Agent、组装上下文、调用模型、处理 Tool Calling 及返回结果。

### Tool Registry

统一注册、描述、发现和执行 Agent 工具。

### Memory Service

管理每个 Agent 独立的持久化记忆。

### Notification Tool

允许 Agent 通过已有后端通知能力向所属用户发起通知。

### Data Query Tool

允许 Agent 在当前用户授权范围内发现数据库结构并查询数据。

### Web Search Tool

封装 DeepSeek Search / Fetch 能力。

### Agent Dispatch Service

通过内部 Bridge 调度同一用户所属的其他 Agent。

### Workflow Service

提供 Workflow 定义管理及未来执行能力的接口占位。

### Execution Context

统一管理 Agent 身份、用户归属、权限、调用链、超时和资源预算。

以上均为逻辑组件，不要求创建独立进程。

---

## 四、Agent Registry

### 4.1 Agent 实例

Agent 数量不应存在框架级硬编码限制。

每个 Agent 实例拥有全局唯一的 `agent_id`。

建议区分：

- `AgentDefinition`：可复用的预设智能体配置。
- `AgentInstance`：某个用户实际拥有的智能体实例。

Agent 实例至少包含：

- `agent_id`
- `owner_user_id`
- `name`
- `agent_type`
- `system_prompt`
- `model_config`
- `enabled_tools`
- `status`
- `created_at`
- `updated_at`

`agent_type` 用于描述角色，不得成为 Runtime 中的硬编码分支条件。

不同用户可以拥有相同角色的 Agent，但其实例、记忆与权限相互隔离。

### 4.2 实例配置

每个 Agent 可以独立配置：

- 系统提示词。
- 模型 Provider。
- 模型名称与调用参数。
- 可用工具及对应权限。
- 运行限制。
- 启用状态。

这些配置应通过内部服务管理，而非要求新增 Agent 时修改代码。

### 4.3 隔离

所有 Agent 实例绑定 `owner_user_id`。

不同用户的 Agent 之间不得共享私有记忆或执行上下文。

同一用户的多个 Agent 也默认拥有独立记忆。

---

## 五、Agent Runtime

Agent Runtime 是整个框架的核心。

### 5.1 统一运行入口

提供内部 Go 接口，使后端其他模块能够发起 Agent Run。

建议运行请求包含：

- `agent_id`
- `trigger`
- `input`
- `execution_context`

其中 Trigger 可标记：

- `USER_MESSAGE`
- `DATA_CHANGED`
- `AGENT_DISPATCH`
- `WORKFLOW_EVENT`
- `SYSTEM_EVENT`

这些标记用于统一执行语义，不意味着当前阶段需要实现对应的全部业务触发器。

Runtime 不应假设每次运行都是一段用户聊天。

### 5.2 运行流程

一次运行至少包括：

1. 获取目标 Agent 实例及配置。
2. 验证运行身份与所属关系。
3. 加载该 Agent 的系统提示词。
4. 自动加载该 Agent 的持久化记忆。
5. 查询已授权工具。
6. 组装模型上下文。
7. 调用模型 Provider。
8. 解析模型输出及 Tool Calls。
9. 通过 Tool Registry 验证并执行工具。
10. 将工具结果回传模型。
11. 根据需要继续模型调用。
12. 保存运行记录。
13. 返回最终输出或明确的错误结果。

支持同一轮多个 Tool Call，正确维护 `tool_call_id` 与工具结果的对应关系。

### 5.3 模型接口

建立独立的 `ModelProvider` 抽象。

第一阶段优先兼容 OpenAI Chat Completions 的消息与 Function Calling 协议，但不要将具体 SDK 类型渗透到 Runtime 及其他组件。

至少支持：

- 模型配置。
- 消息历史。
- Tool Calling。
- API Key 与 Base URL。
- 超时。
- 错误处理。
- Token 使用量统计（若 Provider 提供）。

不同供应商的参数差异由 Provider Adapter 负责适配。

### 5.4 执行限制

支持可配置的：

- 最大模型调用轮数。
- 最大工具调用次数。
- 单工具超时。
- 单次运行超时。
- Agent 调度深度。
- 上下文预算。
- 最大并发数量。

超出限制必须返回结构化错误，禁止无限循环和无限递归调度。

---

## 六、Tool Registry

所有工具必须通过统一 Tool Registry 注册。

### 6.1 工具契约

每个工具包含：

- 唯一名称。
- 描述。
- JSON Schema 输入定义。
- 输出契约。
- 执行接口。
- 权限要求。
- 超时配置。

新增工具时，不应需要修改 Agent Runtime 主循环。

### 6.2 工具权限

框架根据 Agent 实例配置，为模型提供当前允许调用的工具集合。

每次实际执行 Tool Call 时，仍必须进行服务端权限检查。

不允许模型通过伪造参数获得额外工具或访问权限。

### 6.3 执行上下文

工具执行时必须携带可信的：

- 用户 ID。
- Agent ID。
- Run ID。
- 权限集合。
- 父级运行信息。
- 超时和取消上下文。

这些信息由 Runtime 创建或从可信后端调用上下文传递，不能直接信任模型生成的值。

### 6.4 统一工具返回

成功：

```json
{
  "ok": true,
  "data": {},
  "error": null
}
```

失败：

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "PERMISSION_DENIED",
    "message": "当前智能体无权执行此操作",
    "retryable": false
  }
}
```

至少支持：

- `INVALID_ARGUMENT`
- `PERMISSION_DENIED`
- `RESOURCE_NOT_FOUND`
- `AGENT_NOT_FOUND`
- `TOOL_NOT_FOUND`
- `TIMEOUT`
- `RATE_LIMITED`
- `PROVIDER_ERROR`
- `EXECUTION_LIMIT_EXCEEDED`
- `NOT_IMPLEMENTED`
- `INTERNAL_ERROR`

---

## 七、Memory Service

### 7.1 记忆隔离

每个 Agent 拥有独立的持久化记忆。

以唯一 `agent_id` 为主要索引，同时绑定 `owner_user_id` 进行权限验证。

同一用户的不同 Agent 默认不能读取或修改彼此的记忆。

所有记忆通过现有数据库机制存储至 PostgreSQL。

### 7.2 自动加载

**每次 Agent Run 自动加载该 Agent 的有效记忆，并提供给模型。**

记忆应作为明确标识的上下文注入，不应与框架级系统指令混淆。

需要支持：

- 自动加载长度预算。
- 优先级。
- 确定性排序。
- 超限截断或选择。

上下文容量不足时，不得删除数据库中的原始记忆。

本阶段不要求向量数据库、复杂摘要算法或自动遗忘机制。

### 7.3 记忆工具

提供：

- `memory_create`
- `memory_get`
- `memory_search`
- `memory_update`
- `memory_delete`

可增加 `memory_list`。

记忆记录建议包括：

- `memory_id`
- `owner_user_id`
- `agent_id`
- `title`
- `content`
- `category`
- `priority`
- `created_at`
- `updated_at`

所有修改、删除、查询操作均检查当前 Agent 的归属权限。

---

## 八、Notification Tool

Agent Framework 应当提供主动通知能力，但**不负责重新建设后端通知系统或 Android 推送基础设施**。

工具名称：

`notification_send`

### 8.1 职责

工具负责：

1. 接收 Agent 发起的通知请求。
2. 检查 Agent 是否具有通知权限。
3. 从可信上下文确定所属用户。
4. 校验通知参数。
5. 通过 Internal Bridge 调用后端已有通知服务。
6. 返回通知操作结果。

### 8.2 参数

建议包含：

- `title`
- `body`
- `category`
- 可选关联对象。
- 可选幂等键。

不得允许模型任意指定其他用户作为接收者。

### 8.3 实现边界

通知的具体持久化、推送渠道与 Android 交付逻辑属于后端通知模块。

如果已有 Notification Service，应直接通过 Bridge 复用。

如果尚不存在，需要提供清晰的内部通知服务接口，并在本次测试中使用 Mock 实现；不要求为了智能体框架完整开发移动推送系统。

不得把「通知工具调用成功」与「移动端已经收到推送」混淆。

---

## 九、Data Query Tool

允许 Agent 查询当前学生授权范围内的数据库结构和数据。

### 9.1 工具

- `data_list_tables`
- `data_describe_table`
- `data_query`

### 9.2 职责

工具负责：

- 接收 Agent 的结构化查询需求。
- 验证 Agent 数据访问权限。
- 将用户身份和访问范围传递给数据访问服务。
- 通过 Bridge 调用后端数据服务。
- 将查询结果转换成适合模型使用的格式。

### 9.3 权限要求

不能因为拥有数据查询工具，就可以查询 PostgreSQL 中的任意表。

必须具备受控的数据集目录与字段访问白名单。

支持：

- 表或数据集发现。
- 字段结构查询。
- 条件过滤。
- 分页。
- 行数限制。
- 查询超时。
- 用户范围约束。
- 只读访问。

个人数据必须限制在当前用户范围内。

课程公共数据需要根据学生课程归属或其他授权关系判断可见性。

Agent 私有记忆不能通过通用数据库查询工具绕过 Memory Service 的隔离。

优先使用结构化查询，由服务端生成安全 SQL，不允许直接执行模型输出的任意 SQL。

具体 SQL 生成、数据集授权和数据库访问可以由现有后端数据模块承担；Agent Framework 只定义统一工具契约与调用路径。

---

## 十、Web Search Tool

当前联网搜索采用 DeepSeek Search / Fetch API。

官方文档：

https://api-docs.deepseek.com/zh-cn/guides/search

请在实现时以官方文档实际 Schema 为准，不要凭假设硬编码请求或响应格式。

### 10.1 工具

- `web_search`
- `web_fetch`

### 10.2 Provider 抽象

建立独立 Search Provider，使工具层不依赖特定搜索服务商。

未来替换搜索服务商时，不需要修改 Agent Runtime。

### 10.3 要求

支持：

- 服务端 API Key 配置。
- 请求超时。
- 合理重试。
- 限流。
- 搜索结果数量限制。
- 网页正文长度限制。
- 错误处理。
- 搜索来源与 URL 保留。

外部搜索内容属于不可信信息，不得当作系统指令执行。

`web_fetch` 必须限制对内网、回环、云元数据服务等内部目标的访问，包括重定向后地址。

不得向搜索服务传输非必要的用户私有数据或凭据。

本阶段不实现浏览器自动化和通用网页爬虫。

---

## 十一、Agent Dispatch Service

拥有调度权限的 Agent 可以调用当前用户所属的其他 Agent。

工具名称：

`agent_dispatch`

### 11.1 参数

- `target_agent_id`
- `task`
- 可选结构化任务上下文

### 11.2 内部调度链路

必须采用：

`Agent A → Tool Registry → Internal Bridge → Agent Runtime → Agent B`

Agent B 执行完成后，结果沿内部调用链返回 Agent A。

**不得使用 HTTP 进行 Agent 间通信。**

### 11.3 权限

调度前必须验证：

- 调度 Agent 是否拥有 `agent.dispatch` 权限。
- 目标 Agent 是否属于同一用户。
- 目标 Agent 是否已启用。
- 调度深度是否超过限制。
- 当前执行预算是否足够。

Agent B 使用自身的系统提示词、模型配置、私有记忆和工具权限。

不得直接继承 Agent A 的额外工具权限。

### 11.4 调用链

需要记录：

- 父 Run ID。
- 子 Run ID。
- 调度者 Agent ID。
- 目标 Agent ID。
- 开始及结束时间。
- 执行状态。
- 错误信息。

必须防止循环调度和无限递归。

本阶段可优先采用同步调度，但应保留未来异步扩展接口。

---

## 十二、Workflow Service

Workflow Service 是未来 Workflow 执行与演进系统的占位。

本阶段**只实现 Workflow 定义的持久化管理，不实现执行引擎**。

### 12.1 工具

提供：

- `workflow_create`
- `workflow_list`
- `workflow_get`
- `workflow_update`
- `workflow_delete`

### 12.2 定义模型

建议包含：

- `workflow_id`
- `owner_user_id`
- `name`
- `description`
- `parent_workflow_id`
- `definition`
- `version`
- `status`
- `created_by_agent_id`
- `created_at`
- `updated_at`

### 12.3 未来架构约束

未来 Workflow 系统包括：

- 公共基础 Workflow。
- 用户私有专业 Workflow。
- 专业 Workflow 内部的 Sub-Workflow。
- Learner 驱动的 Workflow 创建与演进。
- Worker 驱动的 Workflow 执行。

当前阶段应保留以下约束：

- 用户私有 Workflow 只属于单个用户。
- 每个 Sub-Workflow 只能有一个父 Workflow。
- 不支持跨用户共享私有 Workflow。
- 不要求跨专业 Workflow 直接复用 Sub-Workflow。
- 原子能力可以共享。

### 12.4 实现范围

本阶段需要真正完成 Workflow 定义的数据库 CRUD、版本记录与归属验证。

不实现：

- Workflow 节点调度。
- Workflow 自动执行。
- Workflow 定时触发。
- Learner 自动修改算法。
- 完整的 Workflow 状态机。

未实现的功能不得伪造成功。

---

## 十三、权限与执行安全

Agent Framework 必须建立统一权限机制。

权限至少包括：

- `memory.read`
- `memory.write`
- `notification.send`
- `data.read`
- `web.search`
- `web.fetch`
- `agent.dispatch`
- `workflow.read`
- `workflow.write`
- `workflow.delete`

权限来自服务端 Agent 配置。

工具调用时必须检查：

1. 工具是否存在。
2. 当前 Agent 是否获得调用权限。
3. 操作对象是否处于所属用户的授权范围。
4. 输入参数是否合法。
5. 是否超过执行预算。
6. 是否触发其他限制。

所有操作采用默认拒绝原则。

模型输出、记忆、数据库内容、网页文本及其他 Agent 返回的内容，都不能自行扩大权限。

---

## 十四、运行记录与可观测性

Agent Framework 应记录基本运行信息。

至少包括：

- Run ID。
- Agent ID。
- 用户 ID。
- 触发类型。
- 执行状态。
- 开始与结束时间。
- 模型调用次数。
- 工具调用次数。
- 父子运行关系。
- 执行耗时。
- 错误类型。
- 结束原因。

如果模型 Provider 提供 Token 使用量，可以记录相关统计。

日志中不得泄露 API Key、用户凭据及非必要的敏感数据。

---

## 十五、测试要求

### Runtime

- Agent 能够通过内部接口启动。
- 多轮 Tool Calling 正常。
- 单轮多个 Tool Call 正常。
- 未知工具处理正常。
- 模型超时和执行限制有效。

### Memory

- 记忆 CRUD 正常。
- 每次 Run 自动注入已有记忆。
- 跨用户隔离有效。
- 同用户不同 Agent 的记忆隔离有效。

### Notification

- 通知权限验证有效。
- 通过 Bridge 正确调用通知服务。
- 不允许向其他用户发送通知。
- 工具结果能够反映通知服务真实状态。

### Data Query

- 可发现允许访问的数据集和字段。
- 不允许越权查询其他用户数据。
- 不允许绕过记忆隔离。
- 查询数量、字段和超时限制有效。

### Web Search

- Provider 接口和结果转换正常。
- 外部 API 失败能够正确处理。
- API Key 不泄露。
- 网页正文长度限制有效。

### Agent Dispatch

- 同用户 Agent 可以受控调度。
- 跨用户调度被拒绝。
- 无权限调用被拒绝。
- 子 Agent 不继承父 Agent 权限。
- 递归调度能够终止。
- 整个调度过程不经过 HTTP。

### Workflow

- Workflow CRUD 正常。
- 工作流版本记录正常。
- 用户归属校验有效。
- Sub-Workflow 父级归属约束有效。
- 未实现功能返回明确的不支持错误。

尽可能使用 Mock Model、Mock Search Provider 和 Mock Notification Service 测试。

---

## 十六、开发顺序

建议按照以下顺序实现：

### A. 框架内核

- Agent Registry。
- Execution Context。
- Model Provider。
- Agent Runtime。
- Tool Registry。
- 权限检查。

### B. 内部能力

- Memory Service。
- Notification Tool。
- Data Query Tool。

### C. 外部能力与调度

- DeepSeek Search Provider。
- Agent Dispatch Service。

### D. Workflow 占位

- Workflow Definition。
- Workflow CRUD。
- Workflow 版本管理。

### E. 测试与验证

- 单 Agent 运行。
- Tool Calling。
- Agent 间调度。
- 数据隔离。
- 错误处理。
- 集成测试。

---

## 十七、明确不在本次任务范围内的内容

不得提前实现：

- HTTP API 路由。
- Android App。
- 用户登录与 SSO。
- 课程事件采集。
- Diff Engine。
- Filter Agent 的业务路由逻辑。
- Worker Agent 的正式 Workflow 执行逻辑。
- Learner Agent 的工作流演进算法。
- 作业、考试及课程管理业务。
- 复杂 Workflow 编排器。
- 跨用户 Workflow 共享。
- 向量数据库。
- 智能体自行修改代码。
- 新的微服务架构。

---

## 十八、最终交付要求

完成开发后，请提供：

1. 新增和修改的文件清单。
2. Agent Framework 内部模块架构图。
3. Agent Runtime 执行链路说明。
4. Tool Registry 的工具注册方式。
5. 六类工具能力的定义与 JSON Schema。
6. 内部 Bridge 的集成方式。
7. 数据库表结构与迁移说明。
8. Agent 创建和运行的内部调用示例。
9. Agent A 调度 Agent B 的内部调用示例。
10. 自动化测试结果。
11. 尚未实现的功能及后续扩展入口。

### 最小验收演示

在测试环境中：

1. 创建测试用户 A 对应的两个 Agent 实例。
2. 为它们设置不同的模型配置和工具权限。
3. 分别写入不同记忆。
4. 重新运行，验证记忆自动加载及隔离。
5. Agent A 通过 `agent_dispatch` 调用 Agent B。
6. Agent B 使用授权的联网搜索工具。
7. Agent B 将结构化结果返回 Agent A。
8. Agent A 创建一条 Workflow 定义。
9. Agent A 通过通知工具向用户 A 发出通知请求。
10. 检查数据库，确认记忆、工作流和运行记录已经真实持久化。
11. 使用另一个测试用户验证越权访问被拒绝。

整个演示通过 Go 内部接口完成，不依赖 HTTP。

**最终目标：交付一个完全独立于具体学业业务、能够通过 Internal Bridge 集成到现有 Go 后端的通用 Agent Framework。**

框架必须能够让我们未来直接注册 Filter、Worker、Learner 等 Agent，而不必重新开发模型调用循环、工具执行、记忆隔离和智能体间调度机制。