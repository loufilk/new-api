/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { GuideDoc } from '../types'

export const functionCalling: GuideDoc = {
  slug: 'function-calling',
  title: '工具调用',
  description: 'tools / tool_choice 多轮流程与可用模型范围、常见失败模式。',
  updated: '2026-08-30',
  body: `
> 工具调用是两轮以上的对话。第一轮模型只返回「想调用什么、参数是什么」，真正的执行由你完成，再把结果作为新消息发回去换取最终回答。模型本身不会执行任何代码。

## 哪些模型支持

能力随模型而异，以 [定价页](/pricing) 该模型的标注为准；对未声明该能力的模型传 \`tools\` 可能被忽略或直接报错。

## 定义工具

用 JSON Schema 描述参数。**description 是提示词的一部分**——模型靠它判断何时该调用，写清触发条件比堆字段更有效。

\`\`\`python
tools = [{
    "type": "function",
    "function": {
        "name": "get_order_status",
        "description": "按订单号查询订单当前状态。仅在用户明确提供订单号时调用。",
        "parameters": {
            "type": "object",
            "properties": {
                "order_id": {
                    "type": "string",
                    "description": "订单号，形如 WC-20260818-0001",
                }
            },
            "required": ["order_id"],
            "additionalProperties": False,
        },
    },
}]
\`\`\`

把必填项放进 \`required\`，并设置 \`additionalProperties: false\`，可以显著减少模型编造多余参数的情况。

## 完整多轮流程

\`\`\`python
from openai import OpenAI
import json

client = OpenAI(api_key="...", base_url="{{BASE_URL}}/v1")

messages = [{"role": "user", "content": "帮我查下 WC-20260818-0001 到哪了"}]

# 第 1 轮：模型决定要不要调用工具
first = client.chat.completions.create(
    model="MODEL_ID", messages=messages, tools=tools,
)
reply = first.choices[0].message

if reply.tool_calls:
    # 必须把带 tool_calls 的这条 assistant 消息原样加回历史
    messages.append(reply)

    for call in reply.tool_calls:
        args = json.loads(call.function.arguments)
        result = get_order_status(**args)          # 你自己的实现
        messages.append({
            "role": "tool",
            "tool_call_id": call.id,               # 必须回填，用于配对
            "content": json.dumps(result, ensure_ascii=False),
        })

    # 第 2 轮：把工具结果交回模型，产出面向用户的回答
    second = client.chat.completions.create(
        model="MODEL_ID", messages=messages, tools=tools,
    )
    print(second.choices[0].message.content)
else:
    print(reply.content)
\`\`\`

两个细节最容易出错：带 \`tool_calls\` 的 assistant 消息必须原样加回消息历史；每条 \`role: "tool"\` 的结果必须回填对应的 \`tool_call_id\`。少了任一条，模型就无法把结果和请求配对。

## 控制是否调用工具

\`\`\`python
# 默认：由模型决定
tool_choice="auto"

# 禁止调用工具，强制直接回答
tool_choice="none"

# 强制调用指定函数（适合表单抽取这类确定场景）
tool_choice={"type": "function", "function": {"name": "get_order_status"}}
\`\`\`

## 并行工具调用

一次响应里 \`tool_calls\` 可能包含多项，代表模型希望同时调用多个函数。要遍历全部并为每一项都追加一条 \`tool\` 消息——只处理第一项是常见 bug，表现为模型反复索要同一个工具结果。

## 常见失败模式

- **参数幻觉**：模型编造了不存在的订单号。工具内部要做校验并把错误作为结果返回，让模型有机会追问，而不是直接抛异常中断对话。
- **死循环**：模型持续请求同一个工具。给多轮流程设一个上限（例如 5 轮），超出后改为直接回答或返回失败。
- **arguments 不是合法 JSON**：它是字符串，需要自己解析。解析失败时应把错误回传给模型重试，而不是让整个请求崩掉。
- **历史无限增长**：工具结果会累积进上下文并计入输入费用。长会话要裁剪旧的工具结果，见 [计费与赠金说明](/help/guides/billing)。
`.trim(),
}
