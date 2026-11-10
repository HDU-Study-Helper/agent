> Memory V0.2 增量验收已完成，当前实际工具目录为20个。本文原有18工具与真实模型验证记录保留为历史结果，不代表新增关联工具已用外部模型验收。新版测试、迁移与限制见 [MEMORY-V0.2.md](MEMORY-V0.2.md)。

# 自动化验证记录

验证日期：2026-11-03（Asia/Shanghai）。

## 运行环境及结果

- 框架：Go 1.24 module，`go test -race ./... -timeout 60s` 通过；`go vet ./...` 通过。
- 后端全包：`go test -race ./... -timeout 60s` 通过；`go vet ./...` 通过。
- 后端测试设置 TEST_DATABASE_URL，使用本次任务新建的临时 PostgreSQL 17.11（Debian）实例。数据库测试没有 skip，且每项测试创建、清理独立随机 schema。
- PostgreSQL 通过普通用户在 `/tmp` 解包并运行，使用独立 55437 端口；未使用已有应用数据库、未修改生产配置。测试完成后停止此临时实例。
- JSON Schema 目录由 `go run ./cmd/tool-catalog` 实际生成，包含 18 个工具的完整输入输出契约。

实际验收命令（数据库地址是仅用于本次任务的临时测试实例）：

```bash
cd agent/framework
go test -race ./... -timeout 60s
go vet ./...

cd ../../backend
TEST_DATABASE_URL='postgres://academic_test@127.0.0.1:55437/postgres?sslmode=disable' \
  go test -race ./... -timeout 60s
go vet ./...
```

## 关键验证

| 范围 | 结果及证据 |
|---|---|
| Runtime | 多轮 Tool Calling、单轮多个调用与 tool_call_id 对应；未知工具回传；模型/工具超时、截断输出、模型/工具次数、上下文和并发限制、Provider panic 安全失败；Run 状态与统计 |
| 权限与 Schema | 默认拒绝，伪造 owner/额外参数被拒绝，服务端二次权限校验；输入输出 Schema 校验；外部 Schema 引用无法联网加载；失败调用消耗共享预算 |
| Memory | PostgreSQL CRUD、搜索与确定性优先级排序；每 Run 自动注入；同用户不同 Agent 和跨用户读/改/删隔离；超出注入预算不删除原始记录 |
| Notification | Mock 经实际 Bridge 调用；可信接收用户、权限拒绝、接收者伪造拒绝、服务错误保真；仅验证通知请求收据 |
| Data | 实际数据库 owner 范围和 active 连接约束；数据集/字段白名单、过滤范围、分页行数、超时；无法读取私有 Memory、会话或任意 SQL |
| OpenAI Adapter | httptest 验证 /chat/completions 消息、function tools、多个 call、null content、Usage；Provider 失败和 HTTP 超时；跳转不会泄露 bearer key |
| DeepSeek Adapter | httptest 验证 /v1/tools/search 和 /v1/tools/fetch 的官方请求响应；429 后重试、来源 URL、数量与中文正文长度限制；错误脱敏；内部地址和重定向目标拒绝 |
| Dispatch | 实际 Tool Registry → Bridge → Runtime 链；同 owner 正常；跨 owner、禁用目标、无权限被拒绝；B 使用自己的模型配置与授权工具；环和共享预算耗尽终止 |
| Workflow | 实际数据库 CRUD；同事务版本快照、乐观版本冲突、创建者保留、父级同 owner、父级不可更换、含子级删除拒绝；执行接口明确 NOT_IMPLEMENTED |
| 兼容性 | 后端所有原有模块测试含 PostgreSQL 集成、race 和 vet 均通过；原迁移幂等测试已随 v5 更新 |

## 最小验收演示

`backend/internal/dbbridge/agent_test.go` 的 `TestFrameworkAcceptancePersistedBridgeDispatch` 在真实临时 PostgreSQL 上执行两次完整流程：

1. 创建用户 A 和另一个用户 B。
2. 给 A 创建两个 Agent，分别使用不同的模型配置、工具和权限。
3. 为两个 Agent 写入不同私有记忆。
4. Runtime 每次运行自动加载对应记忆，Mock Model 断言未看到对方记忆。
5. Agent A 的真实模型工具循环调用 agent_dispatch，经实际内部 Bridge 运行 Agent B。
6. Agent B 在自己权限下使用 Mock Search，输出保留 URL 的结构化结果。
7. A 读取真实 ToolResult 后，同轮创建 Workflow 定义并调用 Mock Notification。
8. 检查 PostgreSQL 实际行数：2 条 Memory、2 条 Workflow、2 条 Workflow version、4 条 Run；验证父 Run、子 Run、Dispatcher、状态与模型/工具调用统计。
9. 用户 B 读取 A 的 Run 或运行 A 的 Agent 被拒绝；独立 CRUD 测试进一步检查 Memory 与 Workflow 的跨 owner 和跨 Agent 访问。
10. 测试 schema 自动清理，演示不通过 HTTP 调度 Agent。

## 验证限制

没有使用真实模型或 DeepSeek 账号，没有验证真实通知渠道、设备投递或业务 Agent。Provider 的 HTTP 协议测试不等于上游真实账号验证。DeepSeek 远程 Fetch 默认因无法独立证明远端 DNS/重定向策略而关闭；只有可信服务端确认该保证时才允许启用，详见 README。没有 Workflow 执行引擎或异步调度实现。

## 后续验收：Agent 临时管理页

2026-11-03 完成管理后台接入：后端全包 `go test -race ./... -timeout 90s`、框架全包 `go test -race ./... -timeout 60s` 及两者 `go vet ./...` 通过，后端数据库测试使用独立临时 PostgreSQL、未跳过。

新增 Go 测试验证配置加密/重启读取/Key 不回显/留空保留/显式清除/版本冲突、无加密密钥时拒绝保存、实际框架 ProviderResolver 使用保存的 endpoint/API Key、管理实例与记忆 CRUD、owner 隔离、管理员访问限制、本地 Demo 真实工具循环、运行历史和 Token 统计。

实际 Chromium 浏览器验证通过：

- 现有管理员会话登录；主后台与 Agent 管理页共用认证。
- 创建/修改自测 Agent，实际执行 memory_list，记录 2 次模型调用、1 次工具调用。
- 浏览 JSON 记忆树、修改内容后重新运行，得到修改后的持久化记忆。
- 保存测试 Key 后响应和输入框不回显，清除后 has_api_key 为 false；本地验证 Key 已清除，未调用外部模型。
- 无 CSRF 的配置写入返回 403，普通用户访问管理 API 返回 403。
- 创建临时第二用户，切换 owner 只见该用户实例和记忆；错误 owner 查询返回 404。验证用第二用户已清理。
- 390px 移动端无页面水平溢出；浏览器无 JS 异常。
- 重启本地后端后，配置版本、实例、记忆和历史保留，自测再次成功。

截图：[概览](../../.build/agent-console-overview.png)、[框架配置](../../.build/agent-console-settings.png)、[记忆结构](../../.build/agent-console-memory.png)、[移动端](../../.build/agent-console-mobile.png)。

本地 Demo：`http://127.0.0.1:2610/admin/agents`，复用原管理员账号，保留“本地演示 · 研究助手”及结构化私有记忆。Demo 使用原本地后端数据库，测试用临时 PostgreSQL 与其独立。真实模型/搜索账号尚未验证；配置有效 Key 后由用户发起实际测试。


## 2026-11-03 已配置密钥的真实批量回归

`backend/scripts/agent_regression.py --live` 最终 56 项通过、0 失败，覆盖全部 Agent 管理接口方法和 18 个工具的实际调用记录；通知与 Fetch 是预期的 NOT_IMPLEMENTED，不能解释为功能已上线。其余 16 个工具成功，含真实模型、多轮记忆与工作流 CRUD、授权数据查询、DeepSeek Search、内部 Bridge 父子调度。详见 `.build/agent-regression.json` 和 `.build/agent-regression-report.md`。

修复旧演示 30 秒总运行时限导致的第二轮模型超时；管理页默认 180 秒、上限 300 秒。调度使用剩余 Run 预算；取消与超时分开返回，失败保留 Run ID。增加不记录参数/结果的工具执行诊断；兼容模型网关遗漏 role，并接受搜索来源 URL 锚点、过滤非法来源。框架和后端全包 race、vet 通过，独立 PostgreSQL 的数据库测试未跳过；浏览器验证超时字段和页面脚本通过。
