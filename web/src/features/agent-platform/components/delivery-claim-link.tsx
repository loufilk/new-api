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

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'

import { Quota } from './values'

export interface IssuedDeliveryLink {
  id: number
  quota: number
  claim_code: string
}
export function DeliveryClaimLink(props: {
  delivery: IssuedDeliveryLink
  onClose: () => void
}) {
  const { t } = useTranslation()
  const issued = props.delivery
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Customer claim link')}
      description={t(
        'Copy this bearer claim link now. Share it only with the intended customer. The secret is carried in the URL fragment, not in server query logs.'
      )}
    >
      <div className='flex flex-col gap-3'>
        <p>
          {t('Delivery ID')} #{issued.id} · <Quota value={issued.quota} />
        </p>
        <CopyButton
          value={`${window.location.origin}/agent-platform/claim#code=${encodeURIComponent(issued.claim_code)}`}
          variant='outline'
          size='default'
          aria-label={t('Copy customer claim link')}
        >
          {t('Copy customer claim link')}
        </CopyButton>
      </div>
    </Dialog>
  )
}
