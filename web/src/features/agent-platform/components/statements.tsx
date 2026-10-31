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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import { currencyOptions } from '../lib/currencies'
import {
  parseStatementCSV,
  STATEMENT_CSV_HEADER,
} from '../lib/financial-inputs'
import type { AgentPolicy } from '../types'
import {
  statementAction,
  statementLineColumns,
  statementReason,
} from './statement-definitions'
import { StatementEvidence } from './statement-evidence'
import { StatementLineReview } from './statement-line-review'
import type { WorkflowAction } from './workflow-operation'
import { WorkflowTable, type WorkflowRow } from './workflow-table'

export function StatementPanel() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [section, setSection] = useState('periods')
  const [periodId, setPeriodId] = useState<number | undefined>()
  const [importId, setImportId] = useState<number | undefined>()
  const [line, setLine] = useState<number | null>(null)
  const policy = useQuery({
    queryKey: ['agent-platform', user?.id, 'policy'],
    queryFn: ({ signal }) =>
      getPlatform<AgentPolicy>('/policy/current', undefined, signal),
  })
  const createPeriod = statementAction(
    'period.create',
    'Create reconciliation period',
    [
      {
        name: 'account_dossier_reference',
        label: 'Controlled bank account dossier ID',
        max: 128,
      },
      {
        name: 'currency',
        label: 'Currency',
        kind: 'select',
        options: currencyOptions(),
      },
      { name: 'starts_at', label: 'Period starts', kind: 'datetime' },
      { name: 'ends_at', label: 'Period ends', kind: 'datetime' },
      statementReason,
    ],
    {}
  )
  createPeriod.transform = (_values, payload) => {
    if (
      Number(payload.ends_at) <= Number(payload.starts_at) ||
      Number(payload.ends_at) - Number(payload.starts_at) > 366 * 86400
    ) {
      throw new Error(
        t('Reconciliation periods must be ordered and no longer than 366 days.')
      )
    }
    return payload
  }
  const periodActions = (row: WorkflowRow): WorkflowAction[] => {
    if (!policy.data?.manual_reconciliation_enabled) return []
    const payload = { period_id: row.id, expected_version: row.version }
    if (row.state === 'closed') {
      return row.closed_by !== user?.id
        ? [
            statementAction(
              'period.reopen',
              'Reopen reconciliation period',
              [statementReason],
              payload
            ),
          ]
        : []
    }
    const importStatement = statementAction(
      'import',
      'Import bank statement',
      [
        {
          name: 'source_reference',
          label: 'Controlled statement dossier ID',
          max: 128,
        },
        {
          name: 'csv',
          label: 'Statement CSV',
          kind: 'csv',
          max: 49152,
          description:
            'Choose a CSV file or paste the exact bank statement format below. Only controlled references are accepted; never paste raw bank account details.',
          validate: (value) => {
            try {
              parseStatementCSV(String(value))
              return undefined
            } catch (error) {
              return error instanceof Error
                ? error.message
                : t('Invalid statement CSV.')
            }
          },
        },
        statementReason,
      ],
      payload
    )
    importStatement.initial = { csv: `${STATEMENT_CSV_HEADER}\n` }
    importStatement.transform = (values, body) => {
      const rows = parseStatementCSV(String(values.csv))
      if (
        rows.some(
          (entry) =>
            entry.currency !== row.currency ||
            entry.occurred_at < Number(row.starts_at) ||
            entry.occurred_at >= Number(row.ends_at)
        )
      ) {
        throw new Error(
          t(
            'Every statement row must match the selected period currency and time window.'
          )
        )
      }
      if (
        new TextEncoder().encode(
          JSON.stringify({ action: 'statement.import', payload: body })
        ).length > 60000
      ) {
        throw new Error(
          t(
            'The encoded statement is too large. Split the file without duplicating transactions.'
          )
        )
      }
      return body
    }
    importStatement.description =
      'Import and reconciliation only. No bank API is called, no payment is sent and no wallet or liability is changed.'
    const result = policy.data?.manual_reconciliation_enabled
      ? [importStatement]
      : []
    if (row.created_by !== user?.id) {
      result.push(
        statementAction(
          'period.close',
          'Close reviewed period',
          [statementReason],
          payload
        )
      )
    }
    return result
  }
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Import and reconciliation only. No bank API is called, no payment is sent and no wallet or liability is changed.'
          )}
        </AlertDescription>
      </Alert>
      {(periodId || importId) && (
        <div className='flex flex-wrap items-center gap-3'>
          <span>
            {t('Period ID')}: {periodId ?? '—'} · {t('Import ID')}:{' '}
            {importId ?? '—'}
          </span>
          <Button
            variant='outline'
            onClick={() => {
              setPeriodId(undefined)
              setImportId(undefined)
            }}
          >
            {t('Clear filters')}
          </Button>
        </div>
      )}
      <Tabs value={section} onValueChange={setSection}>
        <TabsList
          className='h-auto max-w-full flex-wrap'
          aria-label={t('Bank reconciliation records')}
        >
          <TabsTrigger value='periods'>
            {t('Reconciliation periods')}
          </TabsTrigger>
          <TabsTrigger value='imports'>{t('Statement imports')}</TabsTrigger>
          <TabsTrigger value='lines'>{t('Statement lines')}</TabsTrigger>
          <TabsTrigger value='exceptions'>
            {t('Reconciliation exceptions')}
          </TabsTrigger>
          <TabsTrigger value='audit'>{t('Review audit')}</TabsTrigger>
          <TabsTrigger value='evidence'>
            {t('Supplementary receipt evidence')}
          </TabsTrigger>
        </TabsList>
        <TabsContent value='periods'>
          <WorkflowTable
            resource='/statements/periods'
            create={[createPeriod]}
            disabled={!policy.data?.manual_reconciliation_enabled}
            columns={[
              { key: 'id', label: 'Period ID' },
              { key: 'state', label: 'Status', kind: 'state' },
              {
                key: 'account_dossier_reference',
                label: 'Controlled bank account dossier ID',
              },
              { key: 'currency', label: 'Currency' },
              { key: 'starts_at', label: 'Period starts', kind: 'date' },
              { key: 'ends_at', label: 'Period ends', kind: 'date' },
              { key: 'version', label: 'Version' },
              { key: 'created_by', label: 'Created by' },
              { key: 'closed_by', label: 'Closed by' },
            ]}
            actions={periodActions}
            onInspect={(row) => {
              setPeriodId(row.id)
              setImportId(undefined)
              setSection('imports')
            }}
          />
        </TabsContent>
        <TabsContent value='imports'>
          <WorkflowTable
            key={periodId}
            resource='/statements/imports'
            filters={{ period_id: periodId }}
            columns={[
              { key: 'id', label: 'Import ID' },
              { key: 'period_id', label: 'Period ID' },
              {
                key: 'source_reference',
                label: 'Controlled statement dossier ID',
              },
              { key: 'source_digest', label: 'Original source digest' },
              { key: 'row_count', label: 'Rows', kind: 'integer' },
              { key: 'imported_by', label: 'Imported by' },
              { key: 'created_at', label: 'Created', kind: 'date' },
            ]}
            onInspect={(row) => {
              setImportId(row.id)
              setPeriodId(Number(row.period_id))
              setSection('lines')
            }}
          />
        </TabsContent>
        <TabsContent value='lines'>
          <WorkflowTable
            key={`${periodId}:${importId}`}
            resource='/statements/lines'
            filters={{ period_id: periodId, import_id: importId }}
            columns={statementLineColumns}
            onInspect={(row) => setLine(row.id)}
          />
        </TabsContent>
        <TabsContent value='exceptions'>
          <WorkflowTable
            key={periodId}
            resource='/statements/exceptions'
            filters={{ period_id: periodId }}
            columns={[
              { key: 'id', label: 'Exception ID' },
              { key: 'line_id', label: 'Statement line ID' },
              { key: 'state', label: 'Status', kind: 'state' },
              { key: 'code', label: 'Exception code', kind: 'state' },
              { key: 'resolution', label: 'Resolution', kind: 'state' },
              { key: 'raised_by', label: 'Raised by' },
              { key: 'resolved_by', label: 'Resolved by' },
            ]}
            onInspect={(row) => setLine(Number(row.line_id))}
          />
        </TabsContent>
        <TabsContent value='evidence'>
          <StatementEvidence
            enabled={policy.data?.risk_policy_approved === true}
          />
        </TabsContent>
        <TabsContent value='audit'>
          <WorkflowTable
            key={periodId}
            resource='/statements/audit'
            filters={{ period_id: periodId }}
            columns={[
              { key: 'id', label: 'ID' },
              { key: 'action', label: 'Operation' },
              { key: 'period_id', label: 'Period ID' },
              { key: 'line_id', label: 'Statement line ID' },
              { key: 'actor_user_id', label: 'Operator ID' },
              { key: 'reason', label: 'Reason' },
              { key: 'created_at', label: 'Created', kind: 'date' },
            ]}
          />
        </TabsContent>
      </Tabs>
      {line !== null && (
        <StatementLineReview
          lineId={line}
          actionsEnabled={policy.data?.manual_reconciliation_enabled === true}
          onClose={() => setLine(null)}
        />
      )}
    </div>
  )
}
