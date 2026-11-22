# 新增和修改文件

## 2026-11-14 框架职责收敛

- 移除 Runtime 按 learner/filter/worker 角色执行的业务门槛、强制分页与参数修复、输出格式纠正和路由要求；删除相应角色策略实现。
- 移除公共系统上下文中的检索和记忆写入策略，以及 Runtime 对查询结果和已交付历史的隐式业务清洗。显式清洗辅助 API 保留兼容，但不参与运行。
- agent_dispatch 使用通用 context 对象；Workflow 不强制附带事件，不再从定义中注入或比较 entity。宿主绑定原始事件时继续验证 owner 并完整转发、防止替换。
- 保留身份、工具权限及 Schema、Workflow 归属/版本/私有范围、取消、调用预算、并发及运行审计。具体角色协议由 Agent 配置与后端业务消费层负责。
- 更新受影响的契约目录、说明和回归源文件。用户要求不执行任何测试；收到该要求后未再运行测试、构建或其他验证命令。最终修改未做运行验证，亦未部署、重启服务或同步数据库中的 Agent 配置。


主体代码按任务要求放在 `agent/framework`。后端只增加必要的 Bridge/Repository 适配、v5 迁移和集成测试；无 HTTP 或业务采集改动。

## 新增：框架

| 文件 | 用途 |
|---|---|
| agent/framework/go.mod、go.sum | 独立可复用模块；JSON Schema 依赖与校验和 |
| agent/framework/types.go | 领域类型、Repository / Provider / Dispatcher / AsyncRunner / WorkflowExecutor 接口、结构化错误、共享预算 |
| agent/framework/registry.go | 实例创建配置、记忆自动加载选择、Workflow 未实现入口 |
| agent/framework/runtime.go | 多轮 Tool Calling、可信执行上下文、运行记录、取消与预算、调度隔离 |
| agent/framework/tools.go | 工具注册发现、权限、输入输出 Schema、超时和结果信封 |
| agent/framework/builtins.go | 六类 18 个内置工具及服务接口 |
| agent/framework/schemas.go | 内置工具输出契约 |
| agent/framework/http.go | OpenAI Chat Completions 适配与受控 HTTP、超时和错误脱敏 |
| agent/framework/search.go | DeepSeek Search / Fetch、重试限流、来源保留、长度限制与 Fetch Guard |
| agent/framework/runtime_test.go | Runtime、共享预算、记忆注入、权限、并发、错误测试 |
| agent/framework/builtins_test.go | 数据授权、通知、工具契约及外部 Schema 加载拒绝测试 |
| agent/framework/providers_test.go | 官方协议 HTTP fixture、失败/超时、Fetch 内网和重定向、内容限制测试 |
| agent/framework/cmd/tool-catalog/main.go | 离线导出工具契约 |
| agent/framework/tool-catalog.json | 完整工具输入输出 JSON Schema |
| agent/framework/architecture.mermaid | 内部架构图 |
| agent/framework/README.md | 执行链路、注册、Bridge、迁移、示例、扩展与限制 |
| agent/framework/VALIDATION.md | 验证命令和实际结果 |
| agent/framework/CHANGES.md | 本清单 |

## 新增：后端适配和集成测试

| 文件 | 用途 |
|---|---|
| backend/internal/agent/service.go | 现有 Bridge Port 适配、同步调度、后端通知/数据端口 |
| backend/internal/agent/service_test.go | 跨用户/禁用/无权限调度、环、共享预算、协议校验 |
| backend/internal/agent/example_test.go | 可编译的内部创建/运行/调度装配示例；默认不执行 |
| backend/internal/agent/README.md | 后端接入入口与边界 |
| backend/internal/dbbridge/agent.go | Agent、Memory、Workflow、版本、Run 的唯一 PostgreSQL Repository |
| backend/internal/dbbridge/agent_schema.go | 增量 v5 DDL |
| backend/internal/dbbridge/agent_data.go | owner + active 连接约束的安全只读字段投影 |
| backend/internal/dbbridge/agent_test.go | 真实 PostgreSQL CRUD、归属/版本、数据查询、双 Agent 最小验收 |

## 修改

| 文件 | 修改原因 |
|---|---|
| backend/go.mod、backend/go.sum | 引用本地 framework 模块和唯一新增直接依赖 jsonschema/v6；保留现有依赖 |
| backend/internal/dbbridge/migrate.go | 在原账本/事务锁内追加 v5，旧迁移不变 |
| backend/internal/dbbridge/db_test.go | 最新迁移测试期望从 4 改为 5 |
| backend/README.md | 更新已实现范围，指向框架交付文档 |
| backend/ARCHITECTURE.md | 区分通用 Agent 框架与未来业务 Agent |
| backend/DATA-CATALOG.md | 增加 Agent 表与数据查询投影说明 |
| backend/internal/README.md | 更新 Agent 目录职责和 Bridge 接入状态 |
| backend/internal/dbbridge/README.md | 说明 v5、Repository 和只读查询边界 |
| backend/internal/bridge/README.md | 说明复用 Port 的 Agent/通知/数据内部调用 |

任务原文 `agent/AGENT.md` 和原业务架构图 `agent/architecture.mermaid` 保留不变。现有 App 装配、HTTP 路由、认证、平台采集和客户端未修改。

## 后续：临时 Agent 管理后台

按用户新增要求扩展后台并启动本地 Demo。新增 `backend/internal/agentadmin/{http.go,settings.go,settings_test.go,agents.html,README.md}`、`backend/internal/model/agent_admin.go`、`backend/internal/dbbridge/{agent_admin.go,agent_console_test.go}`。修改 App 启动/路由/配置及配置测试、主后台导航、迁移账本 v6 和迁移测试；框架 types.go/runtime.go 增加可选 ModelProviderResolver，供后端安全切换命名 Provider。相关 README、架构与数据目录同步更新。

Memory V0.2：新增 memories/memory_relations、V9保守迁移、三种归属与有效期/层级、2个关联工具（总数20）、WorkflowContext 与 Runtime 自动加载。详见 [MEMORY-V0.2.md](MEMORY-V0.2.md)。
