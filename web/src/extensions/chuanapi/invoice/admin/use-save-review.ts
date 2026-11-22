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
import { useRef } from 'react'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'

import { reviewKey, saveDecision, type ReviewDecision } from './api'
import { useFinanceLabels } from './i18n'

export function useSaveReview(
  id: number,
  onSaved: () => void,
  onFailed?: () => void
) {
  const client = useQueryClient()
  const { label } = useFinanceLabels()
  const locked = useRef(false)
  const mutation = useMutation({
    mutationFn: (decision: ReviewDecision) => saveDecision(id, decision),
    gcTime: 0,
    retry: false,
    networkMode: 'always',
    onSuccess: async (_data, decision) => {
      await client.invalidateQueries({ queryKey: reviewKey })
      const messages = {
        claim: label(
          'Application claimed. Check the claim in the application before issuing externally.',
          '申请已占用。请核对申请显示由您处理后，再在站外开票。'
        ),
        release: label(
          'Claim released. No invoice was issued or voided here.',
          '占用已释放。本站未开具或作废发票。'
        ),
        record: label('Issued invoice recorded', '已开具的发票已登记'),
        reject: label('Application rejected', '申请已驳回'),
        reconciliation: label('Added to refund review', '已加入退款待核对'),
      }
      toast.success(messages[decision.kind])
      onSaved()
    },
    onError: async (error) => {
      handleServerError(error)
      onFailed?.()
      // Another reviewer or customer may have won the server-side transition.
      await client.invalidateQueries({ queryKey: reviewKey })
    },
    onSettled: () => {
      locked.current = false
    },
  })
  return {
    locked,
    isPending: mutation.isPending,
    submit(decision: ReviewDecision) {
      if (locked.current) return
      locked.current = true
      mutation.mutate(decision)
    },
  }
}
