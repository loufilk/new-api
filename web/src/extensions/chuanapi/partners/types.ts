export type PartnerApplicationStatus =
  | 'submitted'
  | 'reviewing'
  | 'approved'
  | 'rejected'
  | 'cancelled'

export type PartnerContactType = 'wechat' | 'qq' | 'email' | 'phone' | 'other'

export type PartnerPlatform =
  | 'openai'
  | 'anthropic'
  | 'google'
  | 'deepseek'
  | 'alibaba'
  | 'bytedance'
  | 'zhipu'
  | 'moonshot'
  | 'other'

export interface PartnerQuote {
  platform: PartnerPlatform
  group: string
  rate: string
  models: string
  description: string
}

export interface PartnerApplicationInput {
  site_name: string
  site_url: string
  contact_type: PartnerContactType
  contact: string
  description: string
  quotes: PartnerQuote[]
}

export interface PartnerApplicationSettings {
  revision: number
  partner_enabled: boolean
  partner_instructions: string
  updated_at: number
  updated_by: number
}

export interface PartnerApplicationEvent {
  id: number
  actor_id: number
  action: string
  from_status: PartnerApplicationStatus
  to_status: PartnerApplicationStatus
  note?: string
  created_at: number
}

export interface PartnerApplication {
  id: number
  user_id: number
  site_name: string
  site_url: string
  contact_type: PartnerContactType
  contact: string
  description: string
  quotes: PartnerQuote[]
  status: PartnerApplicationStatus
  revision: number
  review_note?: string
  reviewed_by: number
  created_at: number
  updated_at: number
  events?: PartnerApplicationEvent[]
}

export interface PartnerApplicationPage {
  items: PartnerApplication[]
  total: number
}
