import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RotateCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { SimpleRecordPager } from '@/features/agent-platform/components/simple-record-pager'
import { hasPermission } from '@/lib/admin-permissions'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import {
  getAdminPartnerSettings,
  listPartnerApplications,
  PARTNER_PAGE_SIZE,
  partnerKey,
  privateRead,
  saveAdminPartnerSettings,
} from './api'
import { PartnerApplicationDetailDialog } from './application-detail-dialog'
import type {
  PartnerApplication,
  PartnerApplicationStatus,
  PartnerApplicationSettings,
} from './types'

const statuses: Array<{ value: PartnerApplicationStatus | ''; label: string }> =
  [
    { value: 'submitted', label: 'Submitted' },
    { value: 'reviewing', label: 'Reviewing' },
    { value: 'approved', label: 'Approved' },
    { value: 'rejected', label: 'Rejected' },
    { value: 'cancelled', label: 'Cancelled' },
  ]

function PartnerSettings(props: { settings: PartnerApplicationSettings }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [enabled, setEnabled] = useState(props.settings.partner_enabled)
  const [instructions, setInstructions] = useState(
    props.settings.partner_instructions
  )
  const save = useMutation({
    gcTime: 0,
    mutationFn: () =>
      saveAdminPartnerSettings({
        revision: props.settings.revision,
        partner_enabled: enabled,
        partner_instructions: instructions,
      }),
    onSuccess: () => {
      void client.invalidateQueries({
        queryKey: [...partnerKey, 'admin', 'settings'],
      })
      toast.success(t('Partner settings saved.'))
    },
    onError: (error) => handleServerError(error),
    meta: { errorToast: false },
  })

  return (
    <Card className='min-w-0'>
      <CardHeader>
        <CardTitle>{t('Partner application settings')}</CardTitle>
      </CardHeader>
      <CardContent>
        <FieldGroup className='gap-4'>
          <Field
            orientation='horizontal'
            className='items-center justify-between'
          >
            <FieldLabel htmlFor='partner-intake-enabled'>
              {t('Accept partner applications')}
            </FieldLabel>
            <Switch
              id='partner-intake-enabled'
              checked={enabled}
              onCheckedChange={setEnabled}
              disabled={save.isPending}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor='partner-instructions'>
              {t('Application instructions')}
            </FieldLabel>
            <Textarea
              id='partner-instructions'
              value={instructions}
              onChange={(event) => setInstructions(event.target.value)}
              rows={4}
              maxLength={3000}
              disabled={save.isPending}
            />
          </Field>
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Changes apply to new submissions. Existing applications keep their status.'
              )}
            </p>
            <Button
              disabled={
                save.isPending ||
                (enabled === props.settings.partner_enabled &&
                  instructions === props.settings.partner_instructions)
              }
              onClick={() => save.mutate()}
            >
              {save.isPending ? t('Saving…') : t('Save settings')}
            </Button>
          </div>
        </FieldGroup>
      </CardContent>
    </Card>
  )
}

function ApplicationStatus(props: { status: PartnerApplicationStatus }) {
  const { t } = useTranslation()
  return (
    <Badge variant={props.status === 'approved' ? 'default' : 'warning'}>
      {statusTitle(props.status, t)}
    </Badge>
  )
}

function statusTitle(
  status: PartnerApplicationStatus,
  t: (key: string) => string
) {
  switch (status) {
    case 'submitted':
      return t('Submitted')
    case 'reviewing':
      return t('Reviewing')
    case 'approved':
      return t('Approved')
    case 'rejected':
      return t('Rejected')
    case 'cancelled':
      return t('Cancelled')
  }
}

function ReviewQueue(props: { reviewer: number; canReview: boolean }) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState<PartnerApplicationStatus | ''>(
    'submitted'
  )
  const [selected, setSelected] = useState<number | null>(null)
  const query = useQuery({
    ...privateRead,
    queryKey: [...partnerKey, 'admin', props.reviewer, 'list', status, page],
    queryFn: ({ signal }) =>
      listPartnerApplications({ admin: true, page, status, signal }),
  })

  return (
    <section className='flex flex-col gap-4'>
      <div className='flex flex-wrap items-center gap-3'>
        <Field orientation='horizontal' className='w-auto items-center gap-2'>
          <FieldLabel htmlFor='partner-status-filter'>
            {t('Application status')}
          </FieldLabel>
          <NativeSelect
            id='partner-status-filter'
            className='min-w-40'
            value={status}
            onChange={(event) => {
              setPage(1)
              setStatus(event.target.value as PartnerApplicationStatus | '')
            }}
          >
            <NativeSelectOption value=''>
              {t('All statuses')}
            </NativeSelectOption>
            {statuses.map((item) => (
              <NativeSelectOption key={item.value} value={item.value}>
                {t(item.label)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </Field>
        <Button
          className='ms-auto'
          size='sm'
          variant='outline'
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          <RotateCw
            aria-hidden='true'
            data-icon='inline-start'
            className={query.isFetching ? 'animate-spin' : undefined}
          />
          {t('Refresh')}
        </Button>
      </div>
      {query.isPending && <LoadingState />}
      {query.isError && (
        <ErrorState
          title={t('Unable to load partner applications')}
          onRetry={() => void query.refetch()}
        />
      )}
      {query.data && !query.isError && (
        <>
          {query.data.items.length ? (
            <StaticDataTable
              data={query.data.items}
              getRowKey={(row) => row.id}
              tableClassName='min-w-[700px]'
              columns={[
                {
                  id: 'application',
                  header: t('Application'),
                  cell: (row: PartnerApplication) => (
                    <Button
                      variant='link'
                      size='sm'
                      aria-label={`${t('View application')} #${row.id}`}
                      onClick={() => setSelected(row.id)}
                    >
                      #{row.id}
                    </Button>
                  ),
                },
                {
                  id: 'user',
                  header: t('User ID'),
                  cell: (row: PartnerApplication) => row.user_id,
                },
                {
                  id: 'site',
                  header: t('Site name'),
                  cell: (row: PartnerApplication) => (
                    <span className='break-all whitespace-normal'>
                      {row.site_name}
                    </span>
                  ),
                },
                {
                  id: 'status',
                  header: t('Application status'),
                  cell: (row: PartnerApplication) => (
                    <ApplicationStatus status={row.status} />
                  ),
                },
                {
                  id: 'created',
                  header: t('Submitted'),
                  cell: (row: PartnerApplication) =>
                    formatTimestamp(row.created_at),
                },
              ]}
            />
          ) : (
            <EmptyState
              className='min-h-40'
              title={t('No partner applications found')}
            />
          )}
          {query.data.total > PARTNER_PAGE_SIZE && (
            <SimpleRecordPager
              page={page}
              total={query.data.total}
              pageSize={PARTNER_PAGE_SIZE}
              onChange={setPage}
            />
          )}
        </>
      )}
      <PartnerApplicationDetailDialog
        key={selected ?? 'closed'}
        id={selected}
        reviewer={props.reviewer}
        canReview={props.canReview}
        onClose={() => setSelected(null)}
      />
    </section>
  )
}

export default function PartnerApplicationManagementPage() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const reviewer = user?.id ?? 0
  const canReview = hasPermission(user, 'partner_applications', 'review')
  const settings = useQuery({
    ...privateRead,
    queryKey: [...partnerKey, 'admin', 'settings'],
    queryFn: ({ signal }) => getAdminPartnerSettings(signal),
  })

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Partner applications')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-6'>
          {settings.isPending && <LoadingState className='min-h-24' />}
          {settings.isError && (
            <ErrorState
              className='min-h-28'
              title={t('Unable to load partner application settings')}
              onRetry={() => void settings.refetch()}
            />
          )}
          {settings.data && (
            <PartnerSettings
              key={settings.data.revision}
              settings={settings.data}
            />
          )}
          <Card className='min-w-0'>
            <CardHeader>
              <CardTitle>{t('Application review')}</CardTitle>
            </CardHeader>
            <CardContent>
              <ReviewQueue
                key={reviewer}
                reviewer={reviewer}
                canReview={canReview}
              />
            </CardContent>
          </Card>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
