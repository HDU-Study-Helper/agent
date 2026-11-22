# Worker · 具体事项执行与 Learner 修订交接

[system-prompt.md](system-prompt.md) 为重写后的完整提示词，[definition.template.json](definition.template.json) 内嵌相同文本。[prompt.source.md](prompt.source.md) 保留历史材料。文件修改不自动更新数据库实例。

## 职责与效果

Worker依据当前Workflow定义、有关记忆和本次任务调查、执行授权操作并交付有来源结果。它可以直接记录有来源的新事实，更新当前理解/规则下的状态和执行结果；课程身份、长期理解、知识组织、追踪范围或处理规则需要调整，以及影响多条知识的问题，交给Learner。Worker不直接修改Workflow定义，不采用完全自进化或Host多Agent讨论机制。

按当前任务读取定义与相关记忆，不要求profile_index、固定六主题或完整遍历画像。必要调查由影响本次目标的缺口驱动，已有依据足够即停止，无全量覆盖不声称全部完成。

## 记忆与操作

保留memory.write与原有记忆工具。课程事实新增显式使用visibility=PRI和owner_workflow_id=可信绑定ID，避免落入通用Worker Agent PRI；更新前核对对象和来源，保留时效、旧依据与仍有效事实，写后回读。没有后续使用价值的查询结果只作为本次产物，不强制写记忆。

普通截止时间或提交状态更新可直接记录，不必每次调用Learner。重新分类、跨源归属更正、关联知识失效及规则/偏好影响需集中交Learner；存下用户要求不代表对应规则已经修改。永久删除、扩大范围等仍需明确授权，来源消息不能扩权。

业务操作先查对象、重复、授权及依赖；未知写入先核查，不盲重试。草案、持久化、通知送达及学生完成作业分别表述。当前日程和普通记忆操作仍为现有正式写入，提示词不将它们假装成Learner暂存事务。

## Learner 固定接线与回执

提示词使用 `{{LEARNER_AGENT_ID}}`。安装器[install.py](install.py)在登录用户的完整Agent列表中解析同owner唯一enabled Learner，缺失、不唯一或缺ID时拒绝安装。只替换提示词目标占位符，不改变Learner实例、模型服务、权限或运行限制。

Worker在当前可信Workflow内调用Learner，省略顶层workflow即可继承范围，context说明target_workflow_id、执行版本、具体问题、新证据、影响、已做与未做操作。普通有据纠错无需用户额外批准“调用Learner”本身；授权扩张和破坏性管理仍单独判断。

Learner的READY_TO_COMMIT只是请求提交。只有宿主校验并提交后的APPLIED/NO_CHANGE兼容回执才能作为管理完成依据，目标、版本、核验和错误仍须核对。evolution_feedback保存实际交接及提交状态，不把Run成功当作正式修订成功。

现有framework没有刷新当前执行scope接口。Learner只改记忆且Workflow定义版本未变时，可以定向回读受影响知识后重判；定义改版时，workflow_get不能替换当前可信启动快照，应停止依赖新版本的写入/派发，必要时PARTIAL返回需宿主以新版本发起新Run。顶层workflow_version保留本Run启动版本，修订后版本放反馈中，不伪装自动切换。

## 工具与结果格式

保留原模板21个工具：Workflow只读2、Memory读写8、Data只读4、日程5、公共搜索1、agent_dispatch1。未新增Workflow写工具或权限。正式工具JSON Schema通过tools提供；提示词中的记忆写入、Learner调用、最终结果、交互和修订反馈均采用多行缩进JSON示例。

最终顶层字段仍为run_id、workflow_id、workflow_version、status、summary、artifacts、actions、evidence_refs、interaction、collaboration、evolution_feedback。status保留COMPLETED、NEEDS_USER_INPUT、PARTIAL、FAILED；artifact保留type/title/description/entity_ref/data及现有六类枚举。actions只记实际有意义操作，不重复整份工具日志；collaboration固定null，不扩展Host讨论。修订状态记录在evolution_feedback，不改现有消费者顶层结构。

## 安装与验证状态

项目根目录安装入口为 `python3 agent/worker-agent/install.py`；更新既有实例需 `--update-agent-id <existing-worker-id>`。认证使用既有环境，不把口令或API Key写入提示词。安装前先配置该用户唯一启用的Learner。

初稿在Worker目录完成提示词、模板、说明和安装接线，离线检查解析JSON/代码块、Python语法及安装器辅助函数，不直接导入具有顶层登录行为的脚本。后续已实现宿主暂存并在隔离冻结副本联调：真实Filter→Learner提交→Worker完成，后续只读任务107.623秒、4模型/7工具、COMPLETED且带5条证据。没有修改public实例或启动平台采集；这不等同通知/日程等外部业务动作验收。

真实关联调查暴露账号级通知可能无课程字段，Worker一度将其按course_id/class_id过滤成空；已补输入具体ID优先、标题/正文关联核查及memory_search非空格OR说明。修订链路仍受供应商请求超时影响，完整课程要求的语义质量验收需查看闭环报告。

## 本轮变更目的与检查记录

| 原有问题 | 修改 | 当前收益及证据边界 |
|---|---|---|
| 强求profile_index及固定画像主题 | 改为按本次目标读取定义与相关记忆 | 删除无必要的固定读取前置要求；尚无真实模型耗时对照 |
| Worker记忆写与Learner职责混杂 | 普通事实、状态、执行结果直接记录，长期理解/组织/规则修订交Learner | 责任边界及实例归属写法明确；未实测语义判断准确率 |
| Learner仅在额外用户授权时才可调用，且未接目标 | 普通有据纠错可派发，安装器解析同owner唯一enabled Learner | 离线覆盖合法目标及缺失、多项、跨owner、disabled、空ID拒绝 |
| READY与正式生效、旧快照与新版本混淆 | 只接受实际提交结果，定义改版后必要时PARTIAL要求新Run | 不宣称Runtime有自动scope刷新；后端联调仍待配套 |
| 调用与输出都挤在单行 | 提供5个多行JSON示例，保留顶层字段、状态和产物枚举 | 5个代码块均可被JSON解析，模板正文逐字相等 |

安装器Python语法、回读归属/配置差异拒绝、列表分页及游标不推进拒绝均通过离线辅助函数检查；工具权限及运行限制未变，仍有memory.write且无Workflow定义写工具。`git diff --check -- worker-agent`通过。辅助检查首轮因检查脚本未注入urllib依赖而失败，补齐检查环境后通过，未据此改动安装器行为。没有模型、平台、数据库请求或安装操作，也没有给出未经测量的性能收益。
