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
cd /home/skysight/aigc-project/agent/framework
go test -race ./... -timeout 60s
go vet ./...

cd /home/skysight/aigc-project/backend
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
