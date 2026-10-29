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

export function invitationBonusLabel(
  reason: string | undefined,
  t: TFunction
): string {
  switch (reason) {
    case 'granted':
      return t('Test quota granted')
    case 'policy_disabled':
      return t('Test grants disabled')
    case 'zero_quota':
      return t('No test quota configured')
    case 'payment_compliance_required':
      return t('Payment compliance confirmation required')
    case 'inviter_not_agent':
      return t('Inviter was not an agent')
    default:
      return '—'
  }
}

export function invitationSourceLabel(source: string, t: TFunction): string {
  switch (source) {
    case 'password':
      return t('Password registration')
    case 'oauth':
      return t('OAuth')
    case 'linuxdo':
      return 'LinuxDO'
    case 'legacy':
      return t('Legacy attribution')
    default:
      return source
  }
}
