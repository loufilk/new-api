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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { postPlatform } from '../api'
import { useRequestId } from '../hooks/use-request-id'
import {
  forgetOperation,
  pendingOperations,
  rememberOperation,
} from '../lib/recovery'
import { BusinessDate, Quota } from './values'
import type { WorkflowRow } from './workflow-table'

export function CampaignClaimDialog(props: {
  campaign: WorkflowRow
  onClose: () => void
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const client = useQueryClient()
  const requestId = useRequestId()
  const locked = useRef(false)
  const [uncertain, setUncertain] = useState(false)
  const mutation = useMutation({
    mutationFn: (payload: unknown) => postPlatform('/campaigns/claim', payload),
    retry: false,
    meta: { errorToast: false },
  })
  const existing =
    !!userId &&
    pendingOperations(userId).some((entry) => entry.action === 'campaign.claim')
  return (
    <ConfirmDialog
      open
      title={t('Review campaign claim')}
      desc={t(
        'This is a noncash benefit, not a payment or withdrawable balance. Approval and budget checks may still be required.'
      )}
      confirmText={t('Confirm noncash claim')}
      disabled={uncertain || existing || props.campaign.can_claim !== true}
      isLoading={mutation.isPending}
      onOpenChange={(open) => {
        if (!open && !mutation.isPending) props.onClose()
      }}
      handleConfirm={() => {
        if (
          locked.current ||
          uncertain ||
          existing ||
          !userId ||
          props.campaign.can_claim !== true
        ) {
          return
        }
        locked.current = true
        const payload = {
          campaign_id: props.campaign.id,
          terms_hash: props.campaign.terms_hash,
          confirm_noncash: true,
        }
        const id = requestId(payload)
        rememberOperation(userId, {
          action: 'campaign.claim',
          request_id: id,
          resource: '/campaign-claims',
        })
        mutation.mutate(
          { ...payload, request_id: id },
          {
            onSuccess: () => {
              forgetOperation(userId, id)
              void client.invalidateQueries({ queryKey: ['agent-platform'] })
              toast.success(t('Operation recorded'))
              props.onClose()
            },
            onError: (error) => {
              setUncertain(true)
              handleServerError(error)
            },
            onSettled: () => {
              locked.current = false
            },
          }
        )
      }}
    >
      <dl className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
        <div>
          <dt>{t('Campaign name')}</dt>
          <dd>{String(props.campaign.name)}</dd>
        </div>
        <div>
          <dt>{t('Grant quota')}</dt>
          <dd>
            <Quota value={Number(props.campaign.grant_quota)} />
          </dd>
        </div>
        <div>
          <dt>{t('Benefit expires')}</dt>
          <dd>
            <BusinessDate value={Number(props.campaign.expires_at)} />
          </dd>
        </div>
        <div>
          <dt>{t('Manual review required')}</dt>
          <dd>{props.campaign.manual_review === true ? t('Yes') : t('No')}</dd>
        </div>
      </dl>
      {(uncertain || existing) && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              'The response may have been recorded. Close this dialog and check the unresolved request above before creating another claim.'
            )}
          </AlertDescription>
        </Alert>
      )}
    </ConfirmDialog>
  )
}
