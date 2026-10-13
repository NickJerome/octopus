# 渠道请求体改写

在「渠道 → 编辑 → 高级 → 请求体改写」添加规则。规则在选路和协议转换完成后执行，修改的是发给上游的 JSON。原有「参数覆盖」仍是顶层字段替换，先于新规则生效。

界面提供 total_tokens 尾部清理、OpenAI 思考强度、字段删除、文本替换预设。规则卡片可重命名、启停、上移、下移、删除；通过「编辑 JSON」配置动作、路径和条件。

**清理 total_tokens**

「添加 total_tokens 尾部清理」采用 `<total_tokens>[\s\S]*`：从开始标记删除到当前文本末尾，包含换行、结束标签及其后面的内容。无需闭合标签。它只作用于选中的系统/开发者提示词文本块，其他块与用户消息保持原样。

例如 `保留内容<total_tokens>120000</total_tokens>后续元信息` 变成 `保留内容`。

```json
{
  "version": 1,
  "rules": [
    {
      "id": "clean-total-tokens",
      "actions": [
        {
          "op": "regex_replace",
          "target": {
            "scope": "prompt_text",
            "roles": ["system", "developer"]
          },
          "pattern": "<total_tokens>[\\s\\S]*",
          "replacement": ""
        }
      ]
    }
  ]
}
```

提示词位置随**目标协议**变化：Chat 处理对应 role 的 `messages[].content`；Responses 处理 `instructions` 与 `input` 中对应 role 的消息；Anthropic 处理 `system`。支持字符串与协议文本块，保留未被删除块上的 `cache_control` 等元数据。清空的文本块或系统消息会移除；被清空块含缓存标记时，预览提供提示。

**检查实际发送内容**

在「日志 → 打开请求详情 → 上游请求」查看发送时的最终请求体、地址和请求头。这里展示的是完成协议转换、基础参数覆盖和请求体改写后，交给 HTTP 客户端发送的记录；「原始请求」保留客户端传入内容，便于对照。可以复制最终请求体，并通过下拉框查看不同轮次或跟随最新发送。

同协议、跨协议、流式和非流式请求都会记录；HTTP 重定向会产生同一轮内的多条发送记录。连接失败时仍可能存在发送尝试记录，这不表示上游已收到或接受请求；发送前校验失败不会生成记录。

日志沿用进程内存储：最多保留 50 个已完成请求，清空日志或重启后失效。每个请求最多保留最近 5 条发送记录，正文合计不超过 8 MiB；超大的单条正文只记录开头 8 MiB，界面会标明截断，实际转发不受影响。认证头、Cookie 及 URL 中常见凭据参数会脱敏；请求体保留实际内容。HTTP 传输层自动添加的头可能不在此快照中。

**思考强度**

选择强度后点击「添加 OpenAI 思考强度」，同时生成 Chat 的 `/reasoning_effort` 与 Responses 的 `/reasoning/effort` 两条规则。它们分别匹配目标协议，可填写精确模型名，或勾选「仅在未设置时填写」。使用前选择服务商及模型实际支持的强度。

Anthropic 的预算与 adaptive 配置可用普通字段动作编写；当前不内置通用换算，也不自动调整 max_tokens。以下动作适用于支持显式 thinking 预算的模型，需自行保证 max_tokens 大于预算，且符合上游约束：

```json
{
  "id": "thinking-budget",
  "when": { "target_protocol": ["anthropic_messages"] },
  "actions": [
    {
      "op": "set",
      "target": { "path": "/thinking" },
      "value": { "type": "enabled", "budget_tokens": 8192 }
    }
  ]
}
```

**配置规则**

顶层为 `{ "version": 1, "rules": [...] }`，规则与动作均按数组顺序执行。每个规则需要唯一 `id` 和非空 `actions`，可选 `name`、`enabled`（默认 true）与 `when`。

`when` 支持：

- `source_protocol`、`target_protocol`：协议数组，取值 `openai_chat`、`openai_responses`、`anthropic_messages`。
- `source_model`、`target_model`：精确名称数组，前者为客户端模型/分组名，后者为实际上游模型名。
- `stream`：true 或 false。
- `exists`：必须存在的 JSON Pointer 路径数组。
- `equals`：路径到 JSON 值的对象，例如 `{ "/reasoning/effort": "low" }`。

不同条件为 AND，单个名称数组内部为 OR。前序动作会影响后续规则的 exists/equals 判断，路由上下文不会变化。已有 null 算存在，不进行字符串/数字隐式转换。

| 动作 | 语义 |
| --- | --- |
| `set` | 设置 `value`，支持任何 JSON 类型。`create_missing: true` 可补齐缺失父对象；`only_if_missing: true` 仅设置不存在的字段 |
| `remove` | 删除精确路径，缺失时跳过；删除数组元素会压缩数组 |
| `merge` | 目标与 value 必须是对象，递归合并对象，数组整体替换，null 是值而非删除标记 |
| `replace` | 对字符串执行字面替换，需要 pattern 和 replacement |
| `regex_replace` | Go 标准库正则替换，需要 pattern 和 replacement |

精确路径使用 JSON Pointer，例如 `/reasoning/effort`、`/messages/0/content`。路径中的 `~` 转义为 `~0`，`/` 转义为 `~1`。数组索引必须是非负整数，set 不扩展数组；不支持根节点替换。create_missing 不把已有 null、标量或数组强行改成对象。

文本动作可以使用精确路径，或 `{ "scope": "prompt_text", "roles": ["system", "developer"] }`。缺失目标默认跳过，已有节点类型错误则失败。`require_match: true` 要求至少选中一个目标，但不要求正则实际替换字符。

文本动作默认替换所有匹配。`limit` 可设置 1–10000 次；0 表示默认全部（仍受匹配数限制）。replacement 默认按字面值处理，`replacement_mode: "expand"` 才展开 Go 风格的捕获组，如 `${1}`。不支持 JavaScript 正则的前后查找和反向引用。

**预览与错误处理**

展开「预览改写结果」，选择输入阶段、入站协议、目标协议及模型，载入样例或粘贴请求，再运行预览。

- 客户端请求体模式：先应用模型映射并转换协议，随后执行基础覆盖与规则。
- 最终上游请求体模式：直接执行基础覆盖与规则；目标模型从样例 body 的 model 读取，另填的客户端模型名供条件匹配使用。
- 返回修改前后完整 JSON、每个动作的匹配数、替换次数、变化路径及警告。预览与正式发送共用执行器，不请求上游，不保证上游接受参数。
- 修改配置或样例后，旧预览自动隐藏，需重新运行。

保存时校验未知配置字段、版本、操作、路径及正则。`model`、`stream`、`stream_options.include_usage` 受保护，也不允许替换整个 stream_options。其他 stream_options 子字段可用精确路径修改。启用规则时 Chat 流式用量上报保持开启。

运行时错误会原子撤销本轮改写，返回客户端协议的 502 `request_transform_error`，不发送半改请求、不进入上游无限重试。每轮从客户端原始请求重新构造，渠道切换不会继承上一轮的修改。

**存储及边界**

- 渠道 API 新增 `request_transform` JSON 字段。显式 null 清空，旧客户端更新时未传该字段则保留已有规则。导入导出随渠道配置保存。
- 初期上限：配置 64 KiB、64 条规则、128 个动作、JSON 深度 128、启用规则的请求体/结果 64 MiB、每个文本节点最多 10000 次匹配，并有累计处理和增长预算。大请求和大量规则的组合可能触及预算。
- 执行器保留请求中的未知字段和大整数数值。修改后 JSON 空白与键顺序可能变化，无修改时保留原字节。
- 不合并跨块文本，不恢复跨协议转换时已合并的原始角色，不执行任意脚本。
- 当前日志仍显示客户端原始请求和客户端思考强度；实际上游改写可以通过预览检查。每轮改写轨迹的日志展示属于后续扩展。
