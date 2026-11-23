import {
  CHANNEL_TYPE_OPTIONS,
  type CHANNEL_TYPES,
} from '@/features/channels/constants'

// Channel labels and ordering are shared; the partner API persists stable string
// IDs, not channel numbers. Only explicitly supported options may be submitted.
const platformByChannel = {
  1: 'openai',
  2: 'mjproxy',
  3: 'azure',
  4: 'ollama',
  5: 'mjproxy-plus',
  7: 'ohmygpt',
  8: 'custom',
  14: 'anthropic',
  15: 'baidu',
  16: 'zhipu',
  17: 'alibaba',
  18: 'xunfei',
  19: '360',
  20: 'openrouter',
  22: 'fastgpt',
  23: 'tencent',
  24: 'google',
  25: 'moonshot',
  26: 'zhipu-glm',
  27: 'perplexity',
  31: 'lingyiwanwu',
  33: 'aws',
  34: 'cohere',
  35: 'minimax',
  36: 'sunoapi',
  37: 'dify',
  38: 'jina',
  39: 'cloudflare',
  40: 'siliconflow',
  41: 'vertex',
  42: 'mistral',
  43: 'deepseek',
  44: 'mokaai',
  45: 'bytedance',
  46: 'baidu-v2',
  47: 'xinference',
  48: 'xai',
  49: 'coze',
  50: 'kling',
  51: 'jimeng',
  52: 'vidu',
  53: 'submodel',
  54: 'doubao',
  55: 'sora',
  56: 'replicate',
  57: 'codex',
  58: 'advanced-custom',
  59: 'sub2api',
  60: 'newapi',
  62: 'vllm',
  63: 'sglang',
} as const satisfies Partial<Record<keyof typeof CHANNEL_TYPES, string>>

export const partnerPlatformOptions = CHANNEL_TYPE_OPTIONS.flatMap((option) => {
  const value =
    platformByChannel[option.value as keyof typeof platformByChannel]
  return value ? [{ value, label: option.label }] : []
})

export const partnerPlatforms = partnerPlatformOptions.map(
  (option) => option.value
)
export const partnerContacts = [
  'wechat',
  'telegram',
  'qq',
  'email',
  'other',
] as const

export type PartnerPlatform = (typeof partnerPlatforms)[number] | 'other'
export type PartnerContactType = (typeof partnerContacts)[number] | 'phone'

const platformLabels = new Map<string, string>(
  partnerPlatformOptions.map((option) => [option.value, option.label])
)

const contactLabels: Record<PartnerContactType, string> = {
  wechat: 'WeChat',
  telegram: 'Telegram',
  qq: 'QQ',
  email: 'Email',
  other: 'Other',
  phone: 'Phone',
}

// Old 'other' / 'phone' records remain readable in both history and review.
export function platformTitle(
  platform: PartnerPlatform,
  t: (key: string) => string
) {
  return t(platformLabels.get(platform) ?? 'Other')
}

export function contactTitle(
  type: PartnerContactType,
  t: (key: string) => string
) {
  return t(contactLabels[type] ?? 'Other')
}

export const maxPartnerGroups = 20
