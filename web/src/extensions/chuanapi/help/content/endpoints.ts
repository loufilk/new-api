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
import type {
  EndpointCategory,
  EndpointDoc,
  EndpointResponseExample,
} from './types'

function json(value: unknown): string {
  return JSON.stringify(value, null, 2)
}

const badRequest: EndpointResponseExample = {
  status: 400,
  label: '请求参数错误',
  description: '请求体、路径参数或模型参数不符合当前端点要求。',
  body: json({
    error: {
      message: 'Invalid request body',
      type: 'invalid_request_error',
      code: 'invalid_request',
    },
  }),
}

const rateLimited: EndpointResponseExample = {
  status: 429,
  label: '请求过于频繁',
  description:
    '触发令牌、模型或系统限速；若有 Retry-After 头则遵从，否则使用带抖动的指数退避，不要立即反复提交。',
  body: json({
    error: {
      message: 'Rate limit reached. Retry later.',
      type: 'rate_limit_error',
      code: 'rate_limit_exceeded',
    },
  }),
}

function responses(
  label: string,
  description: string,
  body: unknown,
  status = 200
): readonly EndpointResponseExample[] {
  return [
    {
      status,
      label,
      description,
      body: typeof body === 'string' ? body : json(body),
    },
    badRequest,
    rateLimited,
  ]
}

function imageResponses(
  label: string,
  description: string,
  body: unknown
): readonly EndpointResponseExample[] {
  return [
    ...responses(label, description, body),
    {
      status: 504,
      label: '等待已持久化图片任务超时',
      description:
        '这不是成功响应，也不是重新提交的指令。仅当图片任务已持久化并在同步等待期间超时，才返回以下任务查询信息；未知提交结果不保证包含这些字段。响应头包含 Cache-Control: private, no-store 与 X-Should-Retry: false。',
      body: json({
        error: {
          message:
            'Image generation is still processing. Do not submit it again; query the existing task for its status.',
          type: 'server_error',
          code: 'task_timeout',
        },
        task_id: 'TASK_ID',
        status_url: '/v1/tasks/TASK_ID',
        retryable: false,
      }),
    },
  ]
}

const commonAvailabilityNote =
  '本页是网关路由与协议的静态说明，不代表端点已启用或模型实际可用。实际可用性取决于模型目录、令牌分组、渠道和插件配置及上游能力；请先在模型广场确认 model id。'

export const endpointCategories: readonly EndpointCategory[] = [
  {
    id: 'models',
    title: '模型列表',
    endpoints: [
      {
        id: 'models-list',
        category: 'models',
        name: '列出模型',
        method: 'GET',
        path: '/v1/models',
        description: '列出当前令牌分组可访问的模型目录。',
        relayFormat: 'OpenAI / Anthropic / Gemini 模型列表兼容格式',
        responseType: 'application/json',
        notes: [
          '响应会按令牌分组过滤，不保证与未登录定价目录完全相同。',
          commonAvailabilityNote,
        ],
        responses: responses('模型列表', '返回可用模型对象数组。', {
          object: 'list',
          data: [
            {
              id: 'MODEL_ID',
              object: 'model',
              owned_by: 'provider',
            },
          ],
        }),
      },
      {
        id: 'models-retrieve',
        category: 'models',
        name: '获取模型',
        method: 'GET',
        path: '/v1/models/{model}',
        description: '按真实 model id 获取单个模型的公开信息。',
        relayFormat: 'OpenAI / Anthropic 模型详情兼容格式',
        responseType: 'application/json',
        notes: [
          '把路径中的 {model} 替换为定价页显示的完整 model id。',
          commonAvailabilityNote,
        ],
        responses: responses('模型详情', '返回单个模型对象。', {
          id: 'MODEL_ID',
          object: 'model',
          owned_by: 'provider',
        }),
      },
    ],
  },
  {
    id: 'chat',
    title: '聊天与生成',
    endpoints: [
      {
        id: 'chat-completions',
        category: 'chat',
        name: '聊天完成',
        method: 'POST',
        path: '/v1/chat/completions',
        description:
          '根据对话历史生成回复，兼容 OpenAI Chat Completions，并在模型支持时提供流式输出。',
        relayFormat: '原生 OpenAI Chat Completions 格式',
        responseType: 'application/json 或 text/event-stream',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          messages: [
            { role: 'system', content: 'You are a concise assistant.' },
            { role: 'user', content: 'Say hello in one sentence.' },
          ],
          temperature: 0.7,
          stream: false,
        }),
        notes: [
          '这是 OpenAI 兼容聊天客户端的主要端点。',
          'stream=true 时使用 Server-Sent Events，客户端应逐块读取并处理结束标记。',
          commonAvailabilityNote,
        ],
        responses: responses('聊天完成', '返回一个或多个选择和令牌用量。', {
          id: 'chatcmpl-abc123',
          object: 'chat.completion',
          created: 1710000000,
          model: 'MODEL_ID',
          choices: [
            {
              index: 0,
              message: { role: 'assistant', content: 'Hello! How can I help?' },
              finish_reason: 'stop',
            },
          ],
          usage: {
            prompt_tokens: 18,
            completion_tokens: 7,
            total_tokens: 25,
          },
        }),
      },
      {
        id: 'responses',
        category: 'chat',
        name: 'Responses',
        method: 'POST',
        path: '/v1/responses',
        description:
          '使用 OpenAI Responses 格式生成文本、工具调用或多模态响应。',
        relayFormat: '原生 OpenAI Responses 格式',
        responseType: 'application/json 或 text/event-stream',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          input: 'Summarize why idempotency matters in one paragraph.',
          stream: false,
        }),
        notes: [
          '适合 Codex CLI 与使用 Responses API 的新客户端。',
          '工具与多模态字段是否生效由所选模型能力决定。',
          commonAvailabilityNote,
        ],
        responses: responses('Responses 完成', '返回统一 response 对象。', {
          id: 'resp_abc123',
          object: 'response',
          status: 'completed',
          model: 'MODEL_ID',
          output: [
            {
              type: 'message',
              role: 'assistant',
              content: [
                {
                  type: 'output_text',
                  text: 'Idempotency prevents duplicate side effects.',
                },
              ],
            },
          ],
        }),
      },
      {
        id: 'responses-compact',
        category: 'chat',
        name: '压缩 Responses 上下文',
        method: 'POST',
        path: '/v1/responses/compact',
        description: '压缩较长的 Responses 会话上下文，供后续请求继续使用。',
        relayFormat: 'OpenAI Responses Compaction 格式',
        responseType: 'application/json',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          input: [
            {
              role: 'user',
              content: 'Keep the important implementation decisions.',
            },
          ],
        }),
        notes: [
          '仅在对应上游与模型支持 Responses Compaction 时可用。',
          commonAvailabilityNote,
        ],
        responses: responses('上下文压缩完成', '返回可复用的压缩上下文。', {
          id: 'cmpct_abc123',
          object: 'response.compaction',
          created_at: 1710000000,
          output: [
            {
              type: 'compaction',
              id: 'cmp_abc123',
              encrypted_content: 'OPAQUE_COMPACTED_CONTEXT',
            },
          ],
        }),
      },
      {
        id: 'claude-messages',
        category: 'chat',
        name: 'Claude Messages',
        method: 'POST',
        path: '/v1/messages',
        description: '使用 Anthropic Messages 原生协议调用 Claude 兼容模型。',
        relayFormat: '原生 Anthropic Messages 格式',
        responseType: 'application/json 或 text/event-stream',
        requestKind: 'json',
        authHeader: 'x-api-key',
        extraHeaders: { 'anthropic-version': '2023-06-01' },
        requestBody: json({
          model: 'MODEL_ID',
          max_tokens: 256,
          messages: [
            { role: 'user', content: 'Explain retry backoff briefly.' },
          ],
        }),
        notes: [
          'Anthropic SDK 的 base URL 填站点根地址，不追加 /v1。',
          '鉴权使用 x-api-key，同时发送 anthropic-version。',
          commonAvailabilityNote,
        ],
        responses: responses(
          'Claude 消息完成',
          '返回 Anthropic message 对象。',
          {
            id: 'msg_abc123',
            type: 'message',
            role: 'assistant',
            model: 'MODEL_ID',
            content: [
              { type: 'text', text: 'Use exponential backoff with jitter.' },
            ],
            stop_reason: 'end_turn',
          }
        ),
      },
      {
        id: 'completions',
        category: 'chat',
        name: '文本补全',
        method: 'POST',
        path: '/v1/completions',
        description: '兼容旧版 OpenAI Completions 的纯文本补全端点。',
        relayFormat: 'OpenAI Completions 格式',
        responseType: 'application/json 或 text/event-stream',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          prompt: 'A reliable API client should',
          max_tokens: 64,
        }),
        notes: [
          '新项目优先使用 /v1/chat/completions 或 /v1/responses。',
          commonAvailabilityNote,
        ],
        responses: responses('补全完成', '返回文本 choices。', {
          id: 'cmpl-abc123',
          object: 'text_completion',
          model: 'MODEL_ID',
          choices: [
            {
              index: 0,
              text: ' retry transient errors with backoff.',
              finish_reason: 'stop',
            },
          ],
        }),
      },
    ],
  },
  {
    id: 'embeddings',
    title: '嵌入',
    endpoints: [
      {
        id: 'embeddings',
        category: 'embeddings',
        name: '创建嵌入',
        method: 'POST',
        path: '/v1/embeddings',
        description: '把文本转换为向量，用于检索、聚类与相似度计算。',
        relayFormat: 'OpenAI Embeddings 格式',
        responseType: 'application/json',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          input: ['first document', 'second document'],
        }),
        notes: [commonAvailabilityNote],
        responses: responses('嵌入完成', '返回每条输入对应的向量。', {
          object: 'list',
          data: [
            {
              object: 'embedding',
              index: 0,
              embedding: [0.012, -0.031, 0.008],
            },
          ],
          model: 'MODEL_ID',
          usage: { prompt_tokens: 4, total_tokens: 4 },
        }),
      },
    ],
  },
  {
    id: 'rerank',
    title: '重排序',
    endpoints: [
      {
        id: 'rerank',
        category: 'rerank',
        name: '重排序文档',
        method: 'POST',
        path: '/v1/rerank',
        description: '按查询与候选文档的相关度重新排序。',
        relayFormat: 'Rerank 兼容格式',
        responseType: 'application/json',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          query: 'What is idempotency?',
          documents: ['A duplicate-safe operation.', 'A rendering technique.'],
          top_n: 2,
        }),
        notes: [commonAvailabilityNote],
        responses: responses('重排序完成', '返回按相关度排列的索引与分数。', {
          id: 'rerank-abc123',
          results: [
            { index: 0, relevance_score: 0.98 },
            { index: 1, relevance_score: 0.04 },
          ],
        }),
      },
    ],
  },
  {
    id: 'moderations',
    title: '内容审查',
    endpoints: [
      {
        id: 'moderations',
        category: 'moderations',
        name: '内容审查',
        method: 'POST',
        path: '/v1/moderations',
        description: '使用所选审查模型评估输入内容。',
        relayFormat: 'OpenAI Moderations 格式',
        responseType: 'application/json',
        requestKind: 'json',
        requestBody: json({ model: 'MODEL_ID', input: 'Text to review.' }),
        notes: [commonAvailabilityNote],
        responses: responses('审查完成', '返回分类标记与分数。', {
          id: 'modr-abc123',
          model: 'MODEL_ID',
          results: [{ flagged: false, categories: {}, category_scores: {} }],
        }),
      },
    ],
  },
  {
    id: 'audio',
    title: '音频',
    endpoints: [
      {
        id: 'audio-transcriptions',
        category: 'audio',
        name: '音频转写',
        method: 'POST',
        path: '/v1/audio/transcriptions',
        description: '把音频文件转写为原始语言文本。',
        relayFormat: 'OpenAI Audio Transcriptions 格式',
        responseType: 'application/json、text 或 vtt/srt',
        requestKind: 'multipart',
        multipartFields: [
          { name: 'file', value: 'audio.mp3', file: true },
          { name: 'model', value: 'MODEL_ID' },
          { name: 'response_format', value: 'json' },
        ],
        notes: ['上传大小还受网关请求体限制约束。', commonAvailabilityNote],
        responses: responses('转写完成', '返回识别文本。', {
          text: 'Transcribed audio text.',
        }),
      },
      {
        id: 'audio-translations',
        category: 'audio',
        name: '音频翻译',
        method: 'POST',
        path: '/v1/audio/translations',
        description: '把支持的音频翻译并转写为英文文本。',
        relayFormat: 'OpenAI Audio Translations 格式',
        responseType: 'application/json、text 或 vtt/srt',
        requestKind: 'multipart',
        multipartFields: [
          { name: 'file', value: 'audio.mp3', file: true },
          { name: 'model', value: 'MODEL_ID' },
          { name: 'response_format', value: 'json' },
        ],
        notes: ['输入语言与格式支持取决于所选模型。', commonAvailabilityNote],
        responses: responses('翻译完成', '返回英文转写文本。', {
          text: 'Translated audio text.',
        }),
      },
      {
        id: 'audio-speech',
        category: 'audio',
        name: '文本转语音',
        method: 'POST',
        path: '/v1/audio/speech',
        description: '把文本合成为音频流。',
        relayFormat: 'OpenAI Audio Speech 格式',
        responseType: 'audio/mpeg 或其他请求格式',
        responseKind: 'binary',
        outputFilename: 'output.mp3',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          voice: 'VOICE_ID',
          input: 'Hello from the API.',
          response_format: 'mp3',
        }),
        notes: [
          '客户端应把响应按二进制音频保存，不要按 JSON 解析。',
          commonAvailabilityNote,
        ],
        responses: responses(
          '语音合成完成',
          '响应体是二进制音频流。',
          '(binary audio stream)'
        ),
      },
    ],
  },
  {
    id: 'realtime',
    title: '实时',
    endpoints: [
      {
        id: 'realtime',
        category: 'realtime',
        name: 'Realtime WebSocket',
        method: 'GET',
        path: '/v1/realtime',
        description: '建立 OpenAI Realtime 兼容 WebSocket 会话。',
        relayFormat: 'OpenAI Realtime WebSocket 格式',
        responseType: 'WebSocket 事件流',
        requestKind: 'websocket',
        notes: [
          '使用 wss:// 协议并在连接请求中携带 API 令牌。',
          '会话事件与音频能力取决于所选实时模型。',
          commonAvailabilityNote,
        ],
        responses: responses(
          '连接升级成功',
          'HTTP 连接升级为 WebSocket，随后接收 session 事件。',
          json({
            type: 'session.created',
            session: { id: 'sess_abc123', model: 'MODEL_ID' },
          }),
          101
        ),
      },
    ],
  },
  {
    id: 'images',
    title: '图片',
    endpoints: [
      {
        id: 'image-generations',
        category: 'images',
        name: '生成图片',
        method: 'POST',
        path: '/v1/images/generations',
        description: '根据提示词生成图片。',
        relayFormat: 'OpenAI Images Generations 格式',
        responseType:
          'application/json；原生中继支持时也可为 text/event-stream',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          prompt: 'A paper-cut illustration of a mountain API gateway.',
          n: 1,
          size: '1024x1024',
        }),
        notes: [
          '生成请求使用 JSON。图片返回 URL 或 base64 取决于模型与请求参数。',
          '原生图片中继仅在对应模型与上游支持时接受 stream=true，并返回 SSE；本页示例使用非流式响应。',
          '若收到 504 task_timeout 且含 task_id/status_url/retryable:false，请用 API 令牌查询 status_url 指向的已有任务，不要重复提交；X-Should-Retry:false 同样表示不应自动重试创建。错误消息可本地化或附带本地请求 id。',
          '任务插件的 openai_image 是同步等待接口：网关会等待或轮询到终态再返回图片 JSON，不是视频式创建后轮询，也不承诺 SSE。超时或客户端断开不代表任务已取消，避免盲目重提交。',
          commonAvailabilityNote,
        ],
        responses: imageResponses(
          '图片生成完成',
          '返回图片 URL 或 base64 数据。',
          {
            created: 1710000000,
            data: [{ url: 'https://example.invalid/generated-image.png' }],
          }
        ),
      },
      {
        id: 'image-edits',
        category: 'images',
        name: '编辑图片',
        method: 'POST',
        path: '/v1/images/edits',
        description: '根据原图与提示词执行图片编辑。',
        relayFormat: 'OpenAI Images Edits 格式',
        responseType: 'application/json',
        requestKind: 'multipart',
        multipartFields: [
          { name: 'image', value: 'image.png', file: true },
          { name: 'model', value: 'MODEL_ID' },
          { name: 'prompt', value: 'Add a blue night sky.' },
        ],
        notes: [
          '编辑支持 multipart/form-data，或所选上游/插件支持的 JSON 图片输入；JSON 字段并非对所有模型通用。multipart 的 Content-Type 边界应由客户端自动生成。',
          '已持久化任务的等待超时保持 HTTP 504，并携带 task_id/status_url/retryable:false；查询已有任务，不要再次提交编辑。未知提交结果不保证含任务查询字段。',
          '可接受的图片格式、尺寸、数量及 stream=true 由所选模型与中继实现决定；任务插件 openai_image 同步等待到终态，不能当成 SSE 或异步视频接口。',
          commonAvailabilityNote,
        ],
        responses: imageResponses('图片编辑完成', '返回编辑后的图片数据。', {
          created: 1710000000,
          data: [{ url: 'https://example.invalid/edited-image.png' }],
        }),
      },
    ],
  },
  {
    id: 'video',
    title: '视频与异步任务',
    endpoints: [
      {
        id: 'videos-create',
        category: 'video',
        name: '创建视频',
        method: 'POST',
        path: '/v1/videos',
        description:
          '提交视频生成任务，保存响应中的公开 id，再查询状态和下载产物。',
        relayFormat: 'OpenAI Video 兼容格式',
        responseType: 'application/json',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          prompt: 'A slow aerial view of a mountain lake.',
        }),
        notes: [
          '支持 JSON 或模型/插件要求的 multipart。参考图、时长、尺寸等参数须按所选模型填写，不存在通用于所有视频模型的固定参数组合。',
          '创建成功不代表生成完成。请轮询 GET /v1/videos/{task_id}，遇到 failed 等终态应停止轮询并检查 error；不要重新创建同一个任务来查询进度。',
          commonAvailabilityNote,
        ],
        responses: responses(
          '任务已接受',
          '示例为待处理视频对象，不是视频文件。',
          {
            id: 'TASK_ID',
            object: 'video',
            model: 'MODEL_ID',
            status: 'queued',
            progress: 0,
            created_at: 1710000000,
          }
        ),
      },
      {
        id: 'videos-retrieve',
        category: 'video',
        name: '查询视频任务',
        method: 'GET',
        path: '/v1/videos/{task_id}',
        description: '用创建返回的公开 id 查询当前用户的视频任务。',
        relayFormat: 'OpenAI Video 兼容格式',
        responseType: 'application/json',
        notes: [
          '将 {task_id} 替换为创建结果中的 id。插件任务返回最近持久化的状态快照；不是每次读取都实时请求上游。',
          'completed 后再请求 content；failed 时检查错误并停止轮询。查询需要有权访问该任务的 API 令牌。',
          commonAvailabilityNote,
        ],
        responses: responses('视频任务状态', '示例为生成完成的任务。', {
          id: 'TASK_ID',
          object: 'video',
          model: 'MODEL_ID',
          status: 'completed',
          progress: 100,
          created_at: 1710000000,
          completed_at: 1710000060,
        }),
      },
      {
        id: 'videos-content',
        category: 'video',
        name: '下载视频',
        method: 'GET',
        path: '/v1/videos/{task_id}/content',
        description: '下载已完成视频任务的二进制产物。',
        relayFormat: '网关视频内容代理',
        responseType: 'video/* 或上游媒体类型',
        responseKind: 'binary',
        outputFilename: 'output.mp4',
        notes: [
          '使用创建返回的公开 id，并携带 API 令牌。不要按 JSON 解析成功响应；未就绪、失败或不存在时可能返回 JSON 错误。',
          '产物是否存在、保留时间及格式取决于渠道、插件和上游。',
          commonAvailabilityNote,
        ],
        responses: responses(
          '视频内容',
          '成功响应为二进制视频。',
          '(binary video content)'
        ),
      },
      {
        id: 'videos-content-head',
        category: 'video',
        name: '检查视频内容',
        method: 'HEAD',
        path: '/v1/videos/{task_id}/content',
        description: '检查视频内容响应头，不下载响应体。',
        relayFormat: '网关视频内容代理',
        responseType: '响应头，无响应体',
        responseKind: 'none',
        notes: [
          'HEAD 使用与 GET content 相同的任务归属及鉴权检查；不要调用 JSON 解析。',
          commonAvailabilityNote,
        ],
        responses: responses(
          '内容可访问',
          '示例响应头；具体媒体类型由产物决定。',
          'Content-Type: video/mp4'
        ),
      },
      {
        id: 'tasks-retrieve',
        category: 'video',
        name: '查询通用任务',
        method: 'GET',
        path: '/v1/tasks/{task_id}',
        description: '读取当前用户已持久化任务的状态与进度，不创建新任务。',
        relayFormat: '网关通用任务格式',
        responseType: 'application/json',
        notes: [
          '通用任务 status 使用网关状态（如 SUCCESS、FAILURE、IN_PROGRESS），与视频协议的小写状态不同。',
          '进度是字符串；失败原因在 fail_reason。此接口不返回完整供应商结果。',
          commonAvailabilityNote,
        ],
        responses: responses('任务状态', '示例为已完成的通用任务。', {
          task_id: 'TASK_ID',
          platform: 'PLUGIN_KEY',
          status: 'SUCCESS',
          progress: '100%',
          fail_reason: '',
          created_at: 1710000000,
          finished_at: 1710000060,
        }),
      },
      {
        id: 'tasks-artifacts',
        category: 'video',
        name: '列出任务产物',
        method: 'GET',
        path: '/v1/tasks/{task_id}/artifacts',
        description: '列出任务可访问的产物及网关 content_url。',
        relayFormat: '网关通用任务格式',
        responseType: 'application/json',
        notes: [
          '优先使用响应中的 content_url，不猜测供应商 URL 或私有任务 id。产物列表可能为空，部分不保留结果的即时任务无法再次获取。',
          commonAvailabilityNote,
        ],
        responses: responses('产物列表', '产物类型与 key 由任务插件决定。', {
          task_id: 'TASK_ID',
          artifacts: [
            {
              key: 'video',
              type: 'video',
              mime_type: 'video/mp4',
              content_url: '/v1/tasks/TASK_ID/artifacts/video/content',
            },
          ],
        }),
      },
      {
        id: 'tasks-artifact-content',
        category: 'video',
        name: '下载任务产物',
        method: 'GET',
        path: '/v1/tasks/{task_id}/artifacts/{artifact_key}/content',
        description: '下载 artifacts 列表中选定产物的内容。',
        relayFormat: '网关任务产物代理',
        responseType: '产物媒体类型',
        responseKind: 'binary',
        outputFilename: 'output.bin',
        notes: [
          '将 artifact_key 替换为列表中真实 key；使用 API 令牌。成功响应为二进制，错误响应可能为 JSON。',
          commonAvailabilityNote,
        ],
        responses: responses(
          '产物内容',
          '媒体类型取决于产物。',
          '(binary artifact content)'
        ),
      },
      {
        id: 'tasks-artifact-content-head',
        category: 'video',
        name: '检查任务产物',
        method: 'HEAD',
        path: '/v1/tasks/{task_id}/artifacts/{artifact_key}/content',
        description: '获取任务产物响应头，不读取响应体。',
        relayFormat: '网关任务产物代理',
        responseType: '响应头，无响应体',
        responseKind: 'none',
        notes: [
          '需要有权访问该任务与产物的令牌；HEAD 不提供 JSON 响应体。',
          commonAvailabilityNote,
        ],
        responses: responses(
          '产物可访问',
          '媒体类型仅为示例。',
          'Content-Type: application/octet-stream'
        ),
      },
      {
        id: 'responses-retrieve',
        category: 'video',
        name: '查询插件 Responses 任务',
        method: 'GET',
        path: '/v1/responses/{response_id}',
        description:
          '读取网关本地任务插件通过 Responses 协议创建并持久化的响应。',
        relayFormat: '任务插件 Responses 格式',
        responseType: 'application/json',
        notes: [
          '仅适用于本地任务插件记录；不承诺查询任意上游原生 Responses id。',
          'background=true 仅在所选插件声明支持时返回待处理响应。无 background/stream 的插件请求同步等待；stream=true 同样需要插件明确支持。',
          '使用创建返回的公开 response id 及有权访问任务的令牌。终态失败应检查 error，不要无限轮询。',
          commonAvailabilityNote,
        ],
        responses: responses(
          '插件响应状态',
          '输出形状由插件能力决定，此处为待处理示例。',
          {
            id: 'RESPONSE_ID',
            object: 'response',
            model: 'MODEL_ID',
            status: 'in_progress',
            output: [],
          }
        ),
      },
    ],
  },
  {
    id: 'gemini',
    title: 'Gemini 原生',
    endpoints: [
      {
        id: 'gemini-generate-content',
        category: 'gemini',
        name: 'GenerateContent',
        method: 'POST',
        path: '/v1beta/models/{model}:generateContent',
        description: '使用 Gemini 原生 generateContent 协议生成内容。',
        relayFormat: '原生 Gemini generateContent 格式',
        responseType: 'application/json',
        requestKind: 'json',
        authHeader: 'x-goog-api-key',
        requestBody: json({
          contents: [
            {
              role: 'user',
              parts: [{ text: 'Explain circuit breakers in one sentence.' }],
            },
          ],
        }),
        notes: [
          '把 {model} 替换为真实 Gemini model id。',
          '鉴权使用 x-goog-api-key。',
          commonAvailabilityNote,
        ],
        responses: responses('内容生成完成', '返回 Gemini candidates。', {
          candidates: [
            {
              content: {
                role: 'model',
                parts: [
                  {
                    text: 'A circuit breaker stops repeated calls to a failing dependency.',
                  },
                ],
              },
              finishReason: 'STOP',
            },
          ],
        }),
      },
    ],
  },
]

export const allEndpoints: readonly EndpointDoc[] = endpointCategories.flatMap(
  (category) => category.endpoints
)

export const defaultEndpointId = 'chat-completions'

const endpointMap = new Map<string, EndpointDoc>(
  allEndpoints.map((endpoint) => [endpoint.id, endpoint])
)

export function getEndpoint(id?: string): EndpointDoc {
  const selected = endpointMap.get(id ?? '')
  if (selected) return selected

  const fallback = endpointMap.get(defaultEndpointId)
  if (fallback) return fallback

  throw new Error(`Default endpoint ${defaultEndpointId} is not registered`)
}
