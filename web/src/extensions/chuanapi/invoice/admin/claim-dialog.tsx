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
import { useState } from 'react'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldLabel } from '@/components/ui/field'

import { useFinanceLabels } from './i18n'
import { useSaveReview } from './use-save-review'

export function ClaimDialog(props: {
  id: number
  kind: 'claim' | 'release'
  onCancel: () => void
  onSaved: () => void
}) {
  const { label } = useFinanceLabels()
  const save = useSaveReview(props.id, props.onSaved, props.onCancel)
  const [notIssued, setNotIssued] = useState(false)
  const release = props.kind === 'release'
  const title = release
    ? label('Release claim', '释放开票占用')
    : label('Claim for issuance', '开始开票')
  return (
    <ConfirmDialog
      open
      title={`${title} · #${props.id}`}
      desc={
        release
          ? label(
              'Release returns the application to pending review and lets the customer cancel again. It does not void an invoice or contact the issuer.',
              '释放后申请回到待审核，客户可再次撤销。此操作不联系开票方，也不能作废已开具的发票。'
            )
          : label(
              'Claim this application before starting any external issuance. Once it shows as claimed by you, customer cancellation is blocked and you can proceed outside this console. This action does not issue an invoice or send anything.',
              '请先占用申请，待页面显示由您处理后再前往站外开具发票。占用期间客户不可撤销。此操作不代开发票，也不对外发送任何信息。'
            )
      }
      onOpenChange={(open) => {
        if (!open && !save.locked.current) props.onCancel()
      }}
      isLoading={save.isPending}
      disabled={release && !notIssued}
      confirmText={title}
      handleConfirm={() => {
        if (release) {
          if (notIssued) {
            save.submit({ kind: 'release', confirmedNotIssued: true })
          }
        } else {
          save.submit({ kind: 'claim' })
        }
      }}
    >
      {release && (
        <Field orientation='horizontal'>
          <Checkbox
            id='finance-not-issued'
            checked={notIssued}
            onCheckedChange={setNotIssued}
            disabled={save.isPending}
          />
          <FieldLabel htmlFor='finance-not-issued'>
            {label(
              'I verified that no invoice has been issued and no external issuance is in progress for this application.',
              '我已核实此申请尚未开具发票，且站外没有正在进行的开票操作。'
            )}
          </FieldLabel>
        </Field>
      )}
    </ConfirmDialog>
  )
}
