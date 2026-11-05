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
import {
  getServerErrorSources,
  safeServerErrorMessage,
} from '@/lib/server-error-message'

export const modelTestErrorKeys: Record<string, string> = {
  automatic_static_pricing_required:
    'Automatic site tests support standard OpenAI channels with static pricing only. Dynamic pricing expressions require a manually confirmed personal test.',
  confirmation_token_changed:
    'The selected API key changed. Select a valid key and confirm a new estimate.',
  token_unavailable:
    'This API key is unavailable for the selected group and model. Select another valid key or create one.',
  fingerprint_unavailable:
    'Fingerprint reference data is unavailable. New tests are blocked.',
  invalid_selection:
    'Select a valid group, model and API key before requesting a test.',

  model_tests_disabled:
    'New model tests are disabled. You can still view your history and cancel active tests.',
  model_test_settings_unavailable:
    'Model test availability could not be verified. New tests are blocked; history and cancellation remain available.',
  unsupported_test_channel:
    'This channel does not support controlled model tests.',
  output_budget_changed:
    'The channel changed the confirmed output limit. The test was blocked.',
  confirmation_price_changed:
    'Test pricing changed after confirmation. Request and confirm a new estimate.',
  channel_overrides_not_supported:
    'This channel overrides request parameters and cannot run a controlled comparison.',
  confirmation_required: 'Review and confirm the test cost before starting.',
  confirmation_expired_or_invalid:
    'The test estimate expired or changed. Request a new estimate.',
  invalid_request_id:
    'The test request identifier is invalid. Reload the page.',
  model_or_group_unavailable:
    'This model or group is not available for this test.',
  rate_or_concurrency_limit:
    'The test limit has been reached. Finish an active test or try again later.',
  request_id_conflict:
    'This request identifier belongs to a different test. Check your history.',
  history_unavailable: 'Test history is temporarily unavailable.',
  catalogue_unavailable: 'The test model catalogue is temporarily unavailable.',
  run_not_found: 'This test was not found in your account.',
  result_persistence_failed:
    'The test result could not be saved. Check history before retrying.',
}
export const modelTestFailureKeys: Record<string, string> = {
  fingerprint_unavailable: modelTestErrorKeys.fingerprint_unavailable,
  usage_unknown:
    'The upstream outcome or usage is unknown. This is not a model mismatch.',
  upstream_outcome_unknown:
    'The upstream outcome or usage is unknown. This is not a model mismatch.',
  run_budget_insufficient:
    'The site test exceeded or could not reserve its configured quota budget.',
  budget_exceeded:
    'The site test exceeded or could not reserve its configured quota budget.',
  fingerprint_analysis_failed:
    'Fingerprint analysis failed. This is not a model mismatch.',
  group_configuration_changed:
    'The route or configuration changed. The site test was stopped safely.',
  channel_changed:
    'The route or configuration changed. The site test was stopped safely.',
  channel_unavailable:
    'The route or configuration changed. The site test was stopped safely.',
  pricing_changed:
    'The route or configuration changed. The site test was stopped safely.',
  model_mapping_changed:
    'The route or configuration changed. The site test was stopped safely.',
  model_mapping_failed:
    'The route or configuration changed. The site test was stopped safely.',
  dispatch_not_authorized:
    'The route or configuration changed. The site test was stopped safely.',
  history_unavailable: modelTestErrorKeys.history_unavailable,
  invalid_response: 'The model response was incomplete.',
  invalid_request: 'The model request failed.',
  request_interrupted:
    'The test worker was interrupted. This is not a valid model result.',

  model_tests_disabled: modelTestErrorKeys.model_tests_disabled,
  model_test_settings_unavailable:
    modelTestErrorKeys.model_test_settings_unavailable,
  confirmation_price_changed: modelTestErrorKeys.confirmation_price_changed,
  channel_overrides_not_supported:
    modelTestErrorKeys.channel_overrides_not_supported,
  unsupported_test_channel: modelTestErrorKeys.unsupported_test_channel,
  output_budget_changed: modelTestErrorKeys.output_budget_changed,
  worker_interrupted:
    'The test worker was interrupted. This is not a valid model result.',
  cancel_requested:
    'Cancellation was requested. Usage already incurred may still be charged.',
  user_call_intercepted:
    'Your call interception rule handled this request; it is not a model result.',
  request_timeout: 'The test exceeded its time limit.',
  request_cancelled: 'The test was cancelled.',
  response_too_large: 'The response exceeded the safe preview size limit.',
  relay_failed: 'The model request failed.',
  incomplete_response: 'The model response was incomplete.',
  unsafe_or_invalid_artwork: 'The artwork was unsafe or structurally invalid.',
}
export function modelTestErrorCode(error: unknown): string | undefined {
  const sources = getServerErrorSources(error)
  if (sources.some((source) => source[safeServerErrorMessage])) return undefined
  for (const source of sources) {
    if (typeof source.code === 'string' && modelTestErrorKeys[source.code]) {
      return source.code
    }
  }
  return undefined
}

export function modelTestErrorKey(error: unknown): string | undefined {
  const code = modelTestErrorCode(error)
  return code ? modelTestErrorKeys[code] : undefined
}

// These responses are emitted before reserving/starting a new run. A rejected
// replay does not resolve an earlier transport failure; callers retain that ID.
export function rejectedBeforeModelTestStart(error: unknown): boolean {
  const code = modelTestErrorCode(error)
  return (
    !!code &&
    [
      'confirmation_token_changed',
      'token_unavailable',
      'fingerprint_unavailable',
      'invalid_selection',
      'model_tests_disabled',
      'model_test_settings_unavailable',
      'confirmation_required',
      'invalid_request_id',
      'confirmation_expired_or_invalid',
      'model_or_group_unavailable',
      'confirmation_price_changed',
      'rate_or_concurrency_limit',
      'request_id_conflict',
    ].includes(code)
  )
}
