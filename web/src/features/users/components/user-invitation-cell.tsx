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
import { useTranslation } from 'react-i18next'

import { LongText } from '@/components/long-text'
import { extensionFlags } from '@/extensions'
import {
  invitationBonusLabel,
  invitationSourceLabel,
} from '@/features/agent-redemption/lib/invitation-labels'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatQuota } from '@/lib/format'
import { getRoleLabelKey } from '@/lib/roles'

import type { User } from '../types'

export function UserInvitationCell(props: { user: User }) {
  const { t } = useTranslation()
  const invitation = props.user.invitation
  const affCount = props.user.aff_count ?? 0
  const historyQuota = props.user.aff_history_quota ?? 0
  const legacyEarnings =
    extensionFlags.showLegacyAffiliateQuota && historyQuota !== 0
  const inviterId = invitation?.inviter_id ?? props.user.inviter_id ?? 0
  if (!invitation && affCount === 0 && !legacyEarnings && inviterId === 0) {
    return <span className='text-muted-foreground text-sm'>—</span>
  }
  return (
    <div
      data-table-text='secondary'
      className='min-w-0 space-y-1 text-xs font-normal break-words'
    >
      {affCount > 0 && (
        <LongText>{t('Invited {{count}} users', { count: affCount })}</LongText>
      )}
      {legacyEarnings && (
        <LongText>
          {t('Earnings')}:{' '}
          <span className='tabular-nums'>{formatQuota(historyQuota)}</span>
        </LongText>
      )}
      {inviterId > 0 && (
        <p>
          {invitation
            ? `${t('Inviter')}: ${invitation.inviter_username || '—'} #${inviterId}`
            : t('Inviter ID: {{id}}', { id: inviterId })}
        </p>
      )}
      {invitation && (
        <>
          <p>
            {invitationSourceLabel(invitation.source, t)}
            {!invitation.legacy &&
              ` · ${t(getRoleLabelKey(invitation.inviter_role))}`}
          </p>
          {!invitation.legacy && (
            <p>
              {t('Test quota granted')}:{' '}
              {formatQuotaWithCurrency(invitation.bonus_quota, {
                abbreviate: false,
              })}
            </p>
          )}
          {!invitation.legacy && (
            <p className='text-muted-foreground'>
              {invitationBonusLabel(invitation.bonus_reason, t)}
            </p>
          )}
        </>
      )}
    </div>
  )
}
