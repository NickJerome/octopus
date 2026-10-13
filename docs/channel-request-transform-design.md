# 渠道请求体改写方案

状态：设计参考，核心规则、预设和预览已实现。当前使用方式与实际边界见 [使用说明](channel-request-transform.md)。日期：2026-10-13。

建议把渠道「高级 → 参数覆盖」扩展为「请求体改写」：使用有版本的声明式规则，按顺序修改最终发给上游的 JSON；常见需求提供预设，并与 JSON 编辑器共用同一配置。先实现结构化修改和文本处理，未来再扩展数组选择和动态取值。

**1. 现状与接入位置**

- `internal/relay/channel.go` 的 `applyChannelConfig` 把 `param_override` 解析为顶层键值表，覆盖整个字段。键里的点号被转义，所以 `reasoning.effort` 现在是字面键名，不会修改嵌套属性。`null` 是赋值，不是删除。
- `model`、`stream` 由转发流程控制，现有覆盖会忽略这两个键。
- 同协议透传在 `internal/relay/protocol.go` 的 `buildPassthroughRequest` 调用覆盖；跨协议在 `internal/relay/upstream.go` 的 `OnOutboundRawRequest` 调用覆盖。两个入口适合共用新引擎。
- `internal/op/channel.go` 的 `normalizeChannelConfig` 目前仅清理配置两端空白，覆盖 JSON 的错误到转发时才暴露。
- `internal/relay/handler.go` 每轮会改写 `raw.Body` 的模型和用量选项；新引擎需要明确每轮请求的所有权，不能依赖浅复制保证隔离。
- `internal/relay/state.go` 的 `ReasoningEffort` 和请求体展示记录的是客户端原始请求，不能把它们当成改写后的实际上游参数。

建议的执行顺序：

```text
保留不可变的客户端原始请求
  → 为当前重试轮次创建独立请求
  → 路由确定模型、目标协议、流式模式
  → 同协议构造请求 / 跨协议转换
  → 现有顶层参数覆盖
  → 新规则按顺序执行
  → 校验受保护字段、请求结构并同步请求体
  → 发送上游
```

首版只有「最终上游请求」这一个执行阶段。条件使用明确的 `source_protocol` 和 `target_protocol`，字段路径总是针对目标协议。这样同协议与跨协议行为一致，转换器也不会把改写结果覆盖回去。

这一阶段不能修复在入站解析或跨协议转换期间就被拒绝的请求。未来确有这种需求时，新增显式的转换前阶段；该阶段仍在选定渠道后、当前轮次的独立副本上执行，不能改共享入站数据。

**2. 配置与兼容策略**

保留现有 `param_override` 的顶层替换语义，新增结构化字段 `request_transform`，以 JSON 序列化到渠道表。空值表示不开启。使用独立字段避免旧覆盖里的 `version`、`rules` 等普通参数被误识别为控制配置。

- 旧覆盖先执行，新规则后执行；同一字段后写覆盖先写，预览显示冲突和最终值。
- 规则及动作均按数组顺序执行，不依赖 map 遍历顺序。
- `version: 1` 定义一份确定语义；不认识的版本、操作和配置字段在保存时拒绝。
- 规则有唯一 `id`、可选 `name`、`enabled`、`when` 和 `actions`。`enabled` 缺省为 true。
- 初期界面保留「基础覆盖」与「改写规则」，统一归入「请求体改写」；可提供把旧覆盖转换成 `set` 动作的按钮，但不自动迁移。
- 新前端保存时提交完整 `request_transform`；后端对旧客户端未携带该字段的更新保留原配置，显式 `null` 才清空。这需要在更新请求绑定时区分缺省和 null，不能只凭指针是否为空判断。

以下配置可用于新增的 `request_transform` 字段：

```json
{
  "version": 1,
  "rules": [
    {
      "id": "responses-effort",
      "name": "Responses 使用 high 思考强度",
      "when": { "target_protocol": ["openai_responses"] },
      "actions": [
        {
          "op": "set",
          "target": { "path": "/reasoning/effort" },
          "value": "high",
          "create_missing": true
        }
      ]
    },
    {
      "id": "chat-effort",
      "name": "Chat 使用 high 思考强度",
      "when": { "target_protocol": ["openai_chat"] },
      "actions": [
        {
          "op": "set",
          "target": { "path": "/reasoning_effort" },
          "value": "high"
        }
      ]
    },
    {
      "id": "clean-total-tokens",
      "name": "移除系统和开发者提示词中从 total_tokens 开始的尾部",
      "actions": [
        {
          "op": "regex_replace",
          "target": {
            "scope": "prompt_text",
            "roles": ["system", "developer"]
          },
          "pattern": "<total_tokens>[\\s\\S]*",
          "replacement": "",
          "replacement_mode": "literal"
        }
      ]
    }
  ]
}
```

该示例在对应协议下强制写入 high；模型是否支持 high 仍取决于实际服务商。混合模型渠道应增加 `target_model` 条件，限定到已确认支持的模型。

**3. 核心能力与明确语义**

| 能力 | 用途 | 首版范围 |
| --- | --- | --- |
| `set` | 设置、增加任意字段，或只在缺失时提供默认值 | 支持 |
| `remove` | 删除字段或一个数组元素 | 支持 |
| `merge` | 深层合并对象，保留未指定字段 | 支持 |
| `replace` | 字符串字面替换 | 支持 |
| `regex_replace` | 正则清理标签、脱敏或规范化文本 | 支持 |
| `append` / `prepend` | 追加数组元素或文本 | 后续扩展 |
| `copy` / `move` | 从其他字段复制或移动数据 | 后续扩展 |
| 多节点选择、数组过滤 | 根据角色、类型等批量操作任意数组 | 后续扩展 |
| 动态取值 | 从白名单上下文或请求字段生成值 | 后续扩展 |

精确字段使用 JSON Pointer，例如 `/reasoning/effort`、`/messages/0/content`，遵守 `~0` 和 `~1` 转义。根路径不允许整体覆盖或删除。它不会把含点号的真实字段名错误解析为嵌套路径。

首版目标是二选一：`{ "path": "..." }` 选择一个精确节点；`{ "scope": "prompt_text", "roles": [...] }` 选择由协议适配器识别的提示词文本叶子。语义目标仅用于文本动作。后续增加 `{ "select": "..." }` 多节点目标时，明确采用受限 JSONPath 子集，不混用 GJSON、SJSON 和 JSONPath 的语法。

动作语义：

- `set` 默认覆盖现有值，也允许在已有父对象下增加属性；`only_if_missing: true` 时只处理不存在的属性，已有 null 仍算存在。`create_missing: true` 只允许补齐缺失的父对象，不把现有标量/null 强行改为对象，不推断数组、不填充稀疏数组。
- 数组索引必须已存在；数组追加由未来的专门动作承担。
- `remove` 在节点缺失时跳过；删除数组元素会压缩数组。后续多节点删除应先锁定节点集合，再按索引降序删除。
- `merge` 要求源和目标都是对象，递归合并对象；数组整体替换；null 是字面值，需要删除时显式用 `remove`。
- 文本动作只修改字符串。精确路径不存在时默认跳过，存在但类型不对时报错；`require_match: true` 可要求至少选中一个目标节点。文本中正则没有命中属于正常无变化，预览报告替换次数。
- 字面替换及正则替换默认处理全部匹配，支持 `limit` 限制次数。正则使用 Go 标准库语法；默认替换串按字面值处理，只有显式 `replacement_mode: "expand"` 才展开捕获组。
- 每条规则在执行前判断 `when`，后续规则看到前序规则改过的请求体；协议、模型等路由上下文始终不可变。
- 首版条件支持 `source_protocol`、`target_protocol`、`source_model`、`target_model`、`stream`，以及基于 JSON Pointer 的 `exists` / `equals`。不同条件之间为 AND，协议/模型列表内部为 OR；不做隐式字符串与数字转换，缺省条件表示不限制。
- `source_model` 是客户端提交的分组名称，`target_model` 是选路后的真实模型名。协议标识固定为 `openai_chat`、`openai_responses`、`anthropic_messages`，在 relay 边界与现有协议位值转换。

**4. 思考强度预设**

| 目标协议 / 模式 | 改写位置 | 处理原则 |
| --- | --- | --- |
| OpenAI Chat 兼容接口 | `/reasoning_effort` | 按模型支持范围设置 |
| OpenAI Responses 兼容接口 | `/reasoning/effort` | 只改 effort，保留 reasoning 下其他属性 |
| Anthropic 显式 thinking 预算 | `/thinking/type`、`/thinking/budget_tokens` | 预算与 max_tokens 等约束需要一起校验 |
| 支持 adaptive thinking / effort 的 Anthropic 模型 | `/thinking/type`、`/output_config/effort` | 使用服务商和模型对应的预设，检查冲突字段 |

预设是普通规则的配置生成器，底层不为每一种需求加专门操作。特别是不能把 high 自动换算成固定的 Anthropic token 预算：两者含义和支持范围不同。

界面提供「强制覆盖」和「客户端未设置时使用」两种模式。Anthropic 的预算模式与 adaptive 模式分别建预设，切换时显式删除不兼容字段。预设的模型范围、支持值及约束在实现时按实际接入服务商核对；通用 JSON 模式仍可配置服务商自定义扩展。

**5. 系统提示词文本适配**

| 目标协议 | `prompt_text` 应遍历的位置 |
| --- | --- |
| Chat | `messages` 中选定 role 的 `content` 字符串，或其中已知文本块的 `text` |
| Responses | 顶层 `instructions`，以及 `input` 中选定 role 的消息文本 |
| Anthropic | 顶层 `system` 字符串，或 system 文本块的 `text` |

`instructions` 按系统指令处理；Responses 的 input 可以是字符串或混合类型数组，工具调用、工具输出等非消息项不参与扫描。Chat 的 system 与 developer 可分别选中。跨协议可能合并角色，规则按转换后的结构识别；无法还原已经合并的原始角色。

只遍历协议定义的文本叶子，不递归处理整个 JSON 中所有字符串。保留内容块的 `type`、`cache_control` 等元数据，不修改图片、音频、工具 schema、历史 thinking/signature 或用户消息。

采用用户指定的 `<total_tokens>[\s\S]*`，从第一个开始标签删除到当前文本块末尾，包含换行和闭合标签后面的内容，也不要求存在闭合标签。例如：

```text
修改前：你是助手。<total_tokens>120000</total_tokens>请简洁回答。
修改后：你是助手。
```

它按字面开始标签匹配，不解析 XML，不跨文本块拼接。后续独立文本块保持原样；开始标签带属性时不匹配。没有标签属于正常无变化，不额外 trim 正文空白。

文本被清空后的处理也必须由适配器完成：删除不允许为空的文本块；空的 system/instructions 字段移除；system/developer 消息无剩余内容时移除该消息。若因此失去带 `cache_control` 的块，在预览中提示缓存行为变化，不自动把缓存标记移动到其他位置。完成后再检查协议所需的基本消息结构。

**6. 执行、失败与性能边界**

- 原子执行：在当前轮次私有副本上完成整组规则和验证，全部成功才替换 `request.Body`；若 `JSONBody` 有值，同步更新。每次重试都从原始客户端请求重新构造，追加或删除不能累积。
- 禁止新规则修改 `/model`、`/stream` 及其子路径；操作祖先节点、合并对象也不能绕过检查。最终再比较实际模型和流式状态，防止执行器漏洞造成路由与请求不一致。
- Chat 流式的 `stream_options.include_usage` 是网关统计所需字段，新规则应保护它，且最终构造明确保证 true。旧覆盖若曾将它关闭，属于需要单独提示的兼容行为变化，不应声称完全无影响。
- 保存时编译规则、路径和正则；语法、类型、受保护目标错误返回带规则及动作位置的 400 响应。不要等线上请求才发现无效正则。
- 首版运行时执行错误终止本次请求，按客户端协议返回网关错误，并记录规则 ID、动作序号和路径。不要跳过清理后继续发送，也不要把确定性的配置错误投入无限重试。handler 必须在通用上游重试逻辑之前识别改写错误。
- 条件不满足、可选目标缺失、字符串没有匹配不属于失败；预览区分「跳过」「无变化」「已修改」「失败」。首版不加入通用 `on_error: ignore`，避免未完成规则被当作成功。
- Go 标准库正则满足标签替换需求，避免首版引入支持任意脚本或无界回溯的执行器。JS/Lua/jq/CEL 等各有能力，但当前两类需求用规则更容易提供编辑器、校验和执行轨迹；确有复杂计算需求时再评估受限表达式。
- 给配置大小、动作数量、JSON 深度、处理字节数、匹配次数、输出大小设上限，并检查请求取消。处理字节预算需要结合已有请求体限制，不能随意设小而破坏多模态请求。
- 未启用规则时走现有路径；无实际修改时返回原字节。不要把 JSON 解码到模型 SDK 的固定请求结构后重编码，那样会丢失未知扩展字段。
- 执行器使用保留原始 JSON 值/数字的表示，或 `json.Decoder.UseNumber`，避免大整数被 float64 改写。重编码允许改变空白和键顺序，但不能改变未修改字段的 JSON 值。
- 编译结果不可变，使用有容量限制的配置指纹缓存；配置更新或进程重启后重新编译。首版可以先做简单的有界缓存，不引入分布式缓存和额外任务系统。
- 请求体长度变更后确保 HTTP 层重新计算 Content-Length，不保留失效的长度或签名。如果未来支持依赖 body 的认证签名，应在改写完成后签名；当前渠道使用静态密钥认证。

**7. 编辑器、预览与诊断**

渠道高级设置提供三部分：现有基础覆盖；可启停、排序的规则卡片；与卡片共用配置的 JSON 编辑模式。首版预设包含设置思考强度、清理 total_tokens 标签、删除字段、文本替换。编辑器展示字段错误及规则冲突，切换模式不丢弃合法配置。

预览是首版交付的一部分：

1. 输入或选用示例请求体，选择入站协议和目标授权（目标模型、协议）。也支持直接输入最终上游请求体做改写调试，清楚标明两种输入阶段。
2. 使用未保存的规则和基础覆盖运行后端引擎；客户端请求模式复用正式协议转换及模型改写流程，展示转换后的「改写前」请求。
3. 返回改写后的请求、JSON 差异，以及每条规则的条件结果、匹配节点数、替换次数、修改路径和错误。不能只比较客户端请求与最终请求，否则协议转换也会被误认为规则修改。
4. 预览只做本地转换与规则执行，不发送上游、不产生模型费用。它验证转换和改写行为，不代表上游已接受全部参数。

建议新增管理接口 `POST /api/v1/channel/preview-transform`，沿用渠道管理认证。请求携带编辑中的配置、输入阶段、样例 body 和协议/模型上下文；返回结果与 trace，不返回任何渠道密钥或认证头。预览和正式发送调用同一个 Compile/Apply 实现。

运行时默认只记录配置指纹、规则 ID、修改路径、计数和耗时，避免把提示词值扩散进日志。保留原来的客户端 `reasoning_effort` 语义，新增每轮实际发送的思考设置摘要（协议、effort、thinking 模式或预算）。详细最终 body 可作为后续按需诊断能力，并与现有请求详情共用权限和有限保留策略。

**8. 代码落点与交付顺序**

| 位置 | 建议改动 |
| --- | --- |
| 新增 `internal/requesttransform/` | 配置、Compile/Apply、路径处理、文本适配、预览轨迹；不依赖 op/relay，避免循环依赖 |
| `internal/model/channel.go` | 新增结构化配置字段；数据库序列化，编译对象不落库 |
| `internal/op/channel.go` | 保存前校验、更新字段白名单及缓存；处理旧客户端缺省字段 |
| `internal/relay/channel.go` | 在现有覆盖后接入新执行器，拆清正文和 Header 配置职责 |
| `internal/relay/protocol.go`、`upstream.go` | 传入真实目标协议和当前轮次上下文，共用改写调用 |
| `internal/relay/handler.go` | 保留原始请求并按轮次克隆；单独处理改写错误，不进入普通重试 |
| `internal/relay/state.go`、前端请求日志 | 原始与实际上游思考设置分开显示，记录本轮规则摘要 |
| `internal/server/handlers/channel.go` | 预览入口、结构化校验错误和旧客户端更新绑定语义 |
| `web/src/api/channel.ts`、渠道 `state.ts` / `Form.tsx` | 新配置类型、规则编辑、预设、预览；同步语言文案 |
| 数据库迁移与备份路径 | 验证新字段在 SQLite/MySQL/PostgreSQL 的序列化、回读和导入导出行为 |

引擎接口建议保持简单：`Compile(config) -> Program`，`Program.Apply(ctx, body, immutableContext) -> body, trace, error`。预设生成配置，适配器定位文本，执行器执行操作，relay 负责当前请求与重试生命周期。

交付分两步：

- 首版完整交付：版本化规则、set/remove/merge/replace/regex_replace、精确路径、提示词语义目标、协议/模型条件、保存校验、逐轮隔离、基础预设与后端预览。这已经覆盖两个当前需求和常见字段改写需求。
- 后续按实际案例扩展：受限数组选择、append/prepend/copy/move、动态取值、复杂条件；确有转换前修复需求才增加第二阶段。所有扩展沿用同一个规则模型。

实现验证至少覆盖：三种协议的思考字段；字符串和文本块提示词；多行、多段、无标签、清空文本、cache_control 保留；用户消息和工具数据不被改动；缺失/null/类型错误及大整数；受保护字段；同协议与跨协议；失败重试切换渠道后的正文隔离；预览与发送体一致；旧配置继续工作、旧客户端缺省字段不会清空新规则。仓库贡献规范不提交测试文件，可用临时验证程序、现有构建检查和本地模拟上游完成验收。

本方案推荐先把规则语义、预览和两条真实请求链路打通，再增加操作种类。这样新增需求主要是添加规则或预设，不需要反复改转发主流程。
