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
import type { TFunction } from 'i18next'

export function formatModelTestReason(reason: string, t: TFunction): string {
  switch (reason) {
    case 'accepted':
      return t('monitoring.reason.accepted')
    case 'insufficient_numbers':
      return t('monitoring.reason.insufficient_numbers')
    case 'request_failed':
      return t('monitoring.reason.request_failed')
    case 'response_too_large':
      return t('monitoring.reason.response_too_large')
    case 'non_model_response':
      return t('monitoring.reason.non_model_response')
    case 'strict3_requires_three_complete_samples':
      return t('monitoring.reason.strict3_requires_three_complete_samples')
    case 'no_valid_samples':
      return t('monitoring.reason.no_valid_samples')
    case 'Consecutive numbers change differently from the language-model replies in the reference bank. Program-generated, sorted or shuffled numbers often do this. No ranking was produced.':
      return t('The response pattern differs from the model reference samples.')
    case 'Confidence is a closed-set probability within the reference bank.':
      return t('Probabilities compare models within the reference library.')
    case 'The confidence calibration does not match this detector. Confidence is unavailable.':
      return t(
        'Probability calibration is unavailable for this reference data.'
      )
  }
  const ranked =
    /^Ranked ([12]) valid samples?\. Confidence requires three valid samples\.$/.exec(
      reason
    )
  if (ranked) {
    return t(
      'Valid samples used: {{count}}. Probabilities require three valid samples.',
      { count: Number(ranked[1]) }
    )
  }
  const received =
    /^Received ([0-3])\/3 valid samples\. Three valid samples are required\.$/.exec(
      reason
    )
  if (received) {
    return t(
      'Valid samples received: {{count}} / 3. Three valid samples are required.',
      { count: Number(received[1]) }
    )
  }
  if (/^[a-z0-9]+(?:_[a-z0-9]+)+$/.test(reason)) {
    return t('No assessment is available for this response.')
  }
  return t(reason)
}
