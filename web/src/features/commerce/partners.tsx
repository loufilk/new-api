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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ClipboardCheck,
  Handshake,
  Plus,
  RefreshCw,
  ShieldCheck,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'
import { hasPermission } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import {
  createPartner,
  getCommerceConfig,
  getPartner,
  listPartners,
  reviewPartner,
} from './api'
import { ApplicationStatus } from './components/application-status'
import { PartnerCreateDialog } from './components/partner-create-dialog'
import { PartnerDetailDialog } from './components/partner-detail-dialog'
import type { ApplicationReview, PartnerInput } from './types'

export function PartnersPage(props: { admin?: boolean }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const admin = props.admin === true
  const user = useAuthStore((state) => state.auth.user)
  const canReview = admin && hasPermission(user, 'commerce_partners', 'review')
  const client = useQueryClient()
  const [status, setStatus] = useState('')
  const [page, setPage] = useState(1)
  const [createOpen, setCreateOpen] = useState(false)
  const [selected, setSelected] = useState<number | null>(null)
  const config = useQuery({
    queryKey: ['commerce', 'config', user?.id],
    queryFn: () => getCommerceConfig(),
    enabled: !admin,
  })
  const list = useQuery({
    queryKey: ['commerce', 'partners', user?.id, admin, status, page],
    queryFn: () => listPartners(admin, status, page),
  })
  const detail = useQuery({
    queryKey: ['commerce', 'partner', user?.id, admin, selected],
    queryFn: () => getPartner(admin, selected as number),
    enabled: selected !== null,
  })
  const create = useMutation({
    mutationFn: (input: { data: PartnerInput; key: string }) =>
      createPartner(input.data, input.key),
    onSuccess: async (row) => {
      setCreateOpen(false)
      setSelected(row.id)
      toast.success(t('Application submitted'))
      await client.invalidateQueries({ queryKey: ['commerce'] })
    },
  })
  const review = useMutation({
    mutationFn: (input: ApplicationReview) =>
      reviewPartner(admin, selected as number, input),
    onSuccess: async () => {
      toast.success(t('Application updated'))
      await client.invalidateQueries({ queryKey: ['commerce'] })
    },
  })
  const pages = Math.max(1, Math.ceil((list.data?.total ?? 0) / 20))
  const hasPending =
    !admin &&
    list.data?.items.some(
      (item) => item.status === 'submitted' || item.status === 'reviewing'
    )

  return (
    <>
      <SectionPageLayout stackActionsOnMobile>
        <SectionPageLayout.Title>
          {admin
            ? t('Partner application management')
            : t('Supply partnership')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Description>
          {admin
            ? t(
                'Review supplier introductions and group quotes, with a traceable decision history.'
              )
            : t(
                'Share your API supply, protocol coverage and group pricing with our team.'
              )}
        </SectionPageLayout.Description>
        <SectionPageLayout.Actions>
          <Button
            variant='outline'
            aria-label={t('Refresh')}
            onClick={() => void list.refetch()}
          >
            <RefreshCw aria-hidden />
          </Button>
          {!admin && (
            <Button
              onClick={() => setCreateOpen(true)}
              disabled={!config.data?.partner_enabled || hasPending}
            >
              <Plus aria-hidden />
              {t('New application')}
            </Button>
          )}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='mx-auto grid w-full max-w-7xl gap-5'>
            {!admin && (
              <Card>
                <CardContent className='grid gap-5 sm:grid-cols-3'>
                  {[
                    {
                      icon: Handshake,
                      title: 'Introduce your service',
                      text: 'Tell us about your site, supply and support.',
                    },
                    {
                      icon: ShieldCheck,
                      title: 'Submit transparent quotes',
                      text: 'List supported models, absolute rates and service notes.',
                    },
                    {
                      icon: ClipboardCheck,
                      title: 'Track the review',
                      text: 'Submitted details remain unchanged. Decisions and notes appear in your application.',
                    },
                  ].map((step, index) => (
                    <div key={step.title} className='flex gap-3'>
                      <div className='bg-primary/10 text-primary h-fit rounded-xl p-2.5'>
                        <step.icon className='size-5' aria-hidden />
                      </div>
                      <div>
                        <p className='text-muted-foreground text-xs'>
                          0{index + 1}
                        </p>
                        <h2 className='mt-1 font-semibold'>{t(step.title)}</h2>
                        <p className='text-muted-foreground mt-2 text-sm'>
                          {t(step.text)}
                        </p>
                      </div>
                    </div>
                  ))}
                </CardContent>
              </Card>
            )}
            {!admin && config.isError && (
              <ErrorState onRetry={() => void config.refetch()} />
            )}
            {!admin && config.data && !config.data.partner_enabled && (
              <Alert>
                <AlertDescription>
                  {t('Partner applications are currently closed.')}
                </AlertDescription>
              </Alert>
            )}
            {!admin && config.data?.partner_instructions && (
              <Alert>
                <AlertDescription className='whitespace-pre-wrap'>
                  {config.data.partner_instructions}
                </AlertDescription>
              </Alert>
            )}
            {hasPending && (
              <Alert>
                <AlertDescription>
                  {t(
                    'Your application is being reviewed. Open it below to view updates or cancel it before submitting another.'
                  )}
                </AlertDescription>
              </Alert>
            )}
            <div className='flex flex-wrap items-center justify-between gap-3'>
              <h2 className='text-lg font-semibold'>
                {admin ? t('Application queue') : t('Application history')}
              </h2>
              <NativeSelect
                aria-label={t('Application status')}
                value={status}
                onChange={(event) => {
                  setStatus(event.target.value)
                  setPage(1)
                }}
              >
                <NativeSelectOption value=''>
                  {t('All statuses')}
                </NativeSelectOption>
                <NativeSelectOption value='submitted'>
                  {t('Pending review')}
                </NativeSelectOption>
                <NativeSelectOption value='reviewing'>
                  {t('Under review')}
                </NativeSelectOption>
                <NativeSelectOption value='approved'>
                  {t('Approved')}
                </NativeSelectOption>
                <NativeSelectOption value='rejected'>
                  {t('Rejected')}
                </NativeSelectOption>
                <NativeSelectOption value='cancelled'>
                  {t('Cancelled')}
                </NativeSelectOption>
              </NativeSelect>
            </div>
            {list.isLoading && <LoadingState />}
            {list.isError && <ErrorState onRetry={() => void list.refetch()} />}
            {list.data?.items.length === 0 && (
              <EmptyState
                bordered
                icon={Handshake}
                title={t('No partner applications')}
                description={t(
                  'Submit your service and quotes to start a partnership review.'
                )}
              />
            )}
            <div className='grid gap-3 md:grid-cols-2'>
              {list.data?.items.map((row) => (
                <Card key={row.id}>
                  <CardContent className='space-y-4'>
                    <div className='flex items-start justify-between gap-3'>
                      <div className='min-w-0'>
                        <h3 className='truncate font-medium'>
                          {row.site_name}
                        </h3>
                        <p className='text-muted-foreground mt-1 text-xs'>
                          #{row.id}
                          {admin &&
                            ` · ${t('User #{{id}}', { id: row.user_id })}`}
                        </p>
                      </div>
                      <ApplicationStatus status={row.status} />
                    </div>
                    <p className='text-muted-foreground line-clamp-2 text-sm'>
                      {row.description}
                    </p>
                    <div className='flex flex-wrap items-end justify-between gap-3'>
                      <div className='text-muted-foreground text-xs'>
                        <p>
                          {t('{{count}} group quotes', {
                            count: row.quotes.length,
                          })}
                        </p>
                        <p className='mt-1'>
                          {new Date(row.created_at * 1000).toLocaleString(
                            locale
                          )}
                        </p>
                      </div>
                      <Button
                        variant='outline'
                        onClick={() => setSelected(row.id)}
                      >
                        {t('View details')}
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
            {pages > 1 && (
              <div className='flex items-center justify-end gap-3'>
                <Button
                  variant='outline'
                  disabled={page === 1}
                  onClick={() => setPage(page - 1)}
                >
                  {t('Previous')}
                </Button>
                <span className='text-muted-foreground text-sm'>
                  {t('Page {{page}} of {{total}}', { page, total: pages })}
                </span>
                <Button
                  variant='outline'
                  disabled={page >= pages}
                  onClick={() => setPage(page + 1)}
                >
                  {t('Next')}
                </Button>
              </div>
            )}
            {selected !== null && detail.isLoading && (
              <LoadingState message={t('Loading application...')} />
            )}
            {selected !== null && detail.isError && (
              <ErrorState onRetry={() => void detail.refetch()} />
            )}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {createOpen && (
        <PartnerCreateDialog
          key={user?.id}
          pending={create.isPending}
          onClose={() => setCreateOpen(false)}
          onSubmit={async (data, key) => {
            await create.mutateAsync({ data, key })
          }}
        />
      )}
      {selected !== null && detail.data && (
        <PartnerDetailDialog
          key={`${user?.id}:${detail.data.id}`}
          application={detail.data}
          admin={admin}
          canReview={canReview}
          pending={review.isPending}
          onClose={() => setSelected(null)}
          onReview={async (input) => {
            await review.mutateAsync(input)
          }}
        />
      )}
    </>
  )
}
