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
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { useRequestId } from '../hooks/use-request-id'
import type { CommissionRecipient, OperationDraft } from '../types'
import { ActionForm } from './action-form'
import { OperationDialog } from './operation-dialog'
import { ResourceTable } from './resource-table'
import { BusinessDate } from './values'

export function CommissionRecipients(props: { enabled: boolean }) {
  const { t } = useTranslation()
  const requestId = useRequestId()
  const [editing, setEditing] = useState<CommissionRecipient | 'new' | null>(
    null
  )
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const columns: ColumnDef<CommissionRecipient>[] = [
    {
      accessorKey: 'label',
      header: t('Recipient'),
      meta: { mobileTitle: true },
    },
    { accessorKey: 'reference_masked', header: t('Masked payout reference') },
    { accessorKey: 'version', header: t('Version') },
    {
      accessorKey: 'created_at',
      header: t('Created'),
      cell: ({ row }) => <BusinessDate value={row.original.created_at} />,
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <Button
          size='sm'
          variant='outline'
          disabled={!props.enabled}
          onClick={() => setEditing(row.original)}
        >
          {t('Create recipient revision')}
        </Button>
      ),
    },
  ]
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Use an opaque reference to the approved offline payout record, not banking passwords or credentials. A change creates a new version; existing withdrawals keep their original recipient.'
          )}
        </AlertDescription>
      </Alert>
      <Button
        className='self-start'
        disabled={!props.enabled}
        onClick={() => setEditing('new')}
      >
        {t('Add payout recipient')}
      </Button>
      <Tabs defaultValue='current'>
        <TabsList aria-label={t('Payout recipients')}>
          <TabsTrigger value='current'>{t('Current')}</TabsTrigger>
          <TabsTrigger value='history'>{t('History')}</TabsTrigger>
        </TabsList>
        <TabsContent value='current'>
          <ResourceTable
            resource='/commission-recipients'
            filters={{ current_only: 'true' }}
            columns={columns}
          />
        </TabsContent>
        <TabsContent value='history'>
          <ResourceTable
            resource='/commission-recipients'
            columns={columns.filter((column) => column.id !== 'actions')}
          />
        </TabsContent>
      </Tabs>
      {editing && (
        <ActionForm
          title={t('Review payout recipient')}
          fields={[
            { name: 'label', label: 'Recipient label', max: 80 },
            {
              name: 'recipient_reference',
              label: 'Approved payout record reference',
            },
          ]}
          initial={{
            label: editing === 'new' ? '' : editing.label,
            recipient_reference: '',
          }}
          onClose={() => setEditing(null)}
          onSubmit={(values) => {
            const payload = {
              recipient_id: editing === 'new' ? '' : editing.id,
              version: editing === 'new' ? 0 : editing.version,
              label: String(values.label).trim(),
              recipient_reference: String(values.recipient_reference).trim(),
            }
            setDraft({
              action: 'recipient.change',
              request_id: requestId(payload),
              payload,
              endpoint:
                editing === 'new'
                  ? '/commission-recipients'
                  : `/commission-recipients/${editing.id}`,
              method: editing === 'new' ? 'post' : 'put',
              resource: '/commission-recipients',
              review: [
                { label: 'Recipient label', after: payload.label },
                {
                  label: 'Approved payout record reference',
                  after: payload.recipient_reference,
                },
              ],
            })
            setEditing(null)
          }}
        />
      )}
      {draft && (
        <OperationDialog
          key={draft.request_id}
          draft={draft}
          onClose={() => setDraft(null)}
          onComplete={() => {
            requestId.reset()
            setDraft(null)
          }}
        />
      )}
    </div>
  )
}
