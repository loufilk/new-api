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
export type TestRun = {
  id: string
  model: string
  group: string
  benchmark_id: string
  status:
    | 'running'
    | 'completed'
    | 'failed'
    | 'timeout'
    | 'cancelled'
    | 'intercepted'
    | 'unknown'
  assessment: string
  source?: 'user' | 'site'
  visibility?: 'public'
  token_id?: number
  fingerprint_result?: FingerprintResult | null
  created_at: number
  completed_at: number
  max_output_tokens: number
  estimated_quota: number | null
  request_id: string
  error_code: string
  cancel_requested: boolean
}
export type TestHistory = {
  enabled: boolean
  availability_error?: string
  benchmark: {
    id: string
    name: string
    prompt: string
    license: string
    public_prompt: boolean
    assessment: string
  }
  limits: {
    max_output_tokens: number
    default_output_tokens: number
    timeout_seconds: number
    runs_per_hour: number
    concurrency_per_user: number
  }
  site_results: TestRun[]
  items: TestRun[]
  effective_points?: TestRun[]
  eligible_models_by_group?: Record<string, string[]>
  eligible_tokens_by_group?: Record<string, TestToken[]>
}
export type TestCatalogue = {
  groups: { name: string; desc: string; ratio: number }[]
  models_by_group: Record<string, string[]>
  eligible_tokens_by_group?: Record<string, TestToken[]>
}
export type TestToken = {
  id: number
  name: string
  group: string
  model_limits_enabled: boolean
  model_limits: string[]
  eligible_models: string[]
}
export type TestSelection = {
  token_id: number
  model: string
  group: string
  max_output_tokens: number
}
export type TestEstimate = TestSelection & {
  estimated_quota: number | null
  quota_per_unit: number
  estimate_is_cap: false
  output_unbounded?: boolean
  cost_unknown?: boolean
  timeout_seconds: number
  benchmark_id: string
  confirmation_token: string
  expires_at: number
}
export type TestRequest = TestSelection & {
  confirmation_token: string
  confirmed: true
  request_id: string
}
export type TestDetail = TestRun & { artwork_html: string; prompt: string }

export type FingerprintCandidate = {
  model: string
  display_name: string
  family: string
  family_name: string
  score: number
  probability: number | null
}
export type FingerprintResult = {
  status: 'scored' | 'partial' | 'unscorable' | 'not_model'
  reason: string
  top_candidates: FingerprintCandidate[]
  sample_quality: {
    challenge_id: string
    expected_count: number
    parsed_numbers: number
    minimum_numbers: number
    accepted: boolean
    truncated: boolean
    out_of_range_count: number
    reason: string
  }[]
  data_version: string
  data_sha256: string
  sdk_commit: string
  probability_note: string
  probability_status: string
  used_outputs: number
  strict: boolean
}
export type SiteRun = Pick<
  TestRun,
  | 'id'
  | 'model'
  | 'group'
  | 'benchmark_id'
  | 'assessment'
  | 'fingerprint_result'
  | 'created_at'
  | 'completed_at'
> & { status: TestRun['status'] | 'succeeded'; finished_at: number }
export type PublicTestRun = Pick<
  TestRun,
  | 'id'
  | 'model'
  | 'group'
  | 'benchmark_id'
  | 'status'
  | 'assessment'
  | 'fingerprint_result'
  | 'created_at'
  | 'completed_at'
>
export type SiteTests = {
  items: TestRun[]
  user_items?: TestRun[]
  targets: {
    id: string
    group: string
    model: string
    period_secs: number
    enabled: boolean
  }[]
}
