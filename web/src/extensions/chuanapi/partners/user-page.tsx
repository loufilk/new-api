import { useQuery } from '@tanstack/react-query'
import { ClipboardList, Handshake, Scale, TriangleAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { useAuthStore } from '@/stores/auth-store'

import {
  getPartnerSettings,
  listPartnerApplications,
  partnerKey,
  privateRead,
} from './api'
import { PartnerApplicationForm } from './application-form'
import { ApplicationHistory } from './application-history'

const partnershipSteps = [
  {
    icon: ClipboardList,
    title: 'Submit a quote',
    detail: 'Provide your site information and group rates.',
  },
  {
    icon: Scale,
    title: 'Compare offers',
    detail: 'We compare supplier quotes across sites.',
  },
  {
    icon: Handshake,
    title: 'Discuss cooperation',
    detail: 'If there is a fit, an administrator will contact you.',
  },
] as const

function PartnerWorkspace(props: { owner: number }) {
  const { t } = useTranslation()
  const settings = useQuery({
    ...privateRead,
    queryKey: [...partnerKey, 'settings'],
    queryFn: ({ signal }) => getPartnerSettings(signal),
  })
  const history = useQuery({
    ...privateRead,
    queryKey: [...partnerKey, props.owner, 'history', 1],
    queryFn: ({ signal }) =>
      listPartnerApplications({
        admin: false,
        page: 1,
        status: '',
        signal,
      }),
  })
  const hasActiveApplication = history.data?.items.some(
    (application) =>
      application.status === 'submitted' || application.status === 'reviewing'
  )
  const intakeClosed = settings.data?.partner_enabled === false
  const disabled =
    settings.isPending ||
    settings.isError ||
    history.isPending ||
    history.isError ||
    intakeClosed ||
    hasActiveApplication === true

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Partner application')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-4'>
          <Card className='from-primary/5 bg-gradient-to-br to-transparent'>
            <CardContent className='grid min-w-0 gap-5 py-2 md:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)] md:gap-6'>
              <div className='flex min-w-0 flex-col justify-center gap-3'>
                <span className='bg-primary/10 text-primary flex size-10 items-center justify-center rounded-xl'>
                  <Handshake size={21} aria-hidden='true' />
                </span>
                <div className='space-y-2'>
                  <h2 className='text-xl leading-tight font-semibold md:text-2xl'>
                    {t('Share your upstream and start a partnership')}
                  </h2>
                  <p className='text-muted-foreground text-sm'>
                    {t('Share your site, contact details, groups and rates.')}
                  </p>
                </div>
              </div>
              <ol className='grid min-w-0 gap-3 border-t pt-4 md:border-t-0 md:border-l md:pt-0 md:pl-5'>
                {partnershipSteps.map((step) => (
                  <li
                    key={step.title}
                    className='flex min-w-0 items-start gap-3'
                  >
                    <span className='bg-background text-primary flex size-8 shrink-0 items-center justify-center rounded-lg border'>
                      <step.icon size={16} aria-hidden='true' />
                    </span>
                    <div className='min-w-0'>
                      <p className='text-sm font-medium'>{t(step.title)}</p>
                      <p className='text-muted-foreground mt-0.5 text-sm'>
                        {t(step.detail)}
                      </p>
                    </div>
                  </li>
                ))}
              </ol>
            </CardContent>
          </Card>
          <Alert
            role='note'
            className='border-amber-300 bg-amber-50 px-3 py-2.5 text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100'
          >
            <TriangleAlert aria-hidden='true' />
            <AlertDescription className='text-current'>
              {t(
                'Do not include API keys. Submitted applications cannot be edited. Review the details before submitting.'
              )}
            </AlertDescription>
          </Alert>
          {settings.isPending && <LoadingState className='min-h-20' />}
          {settings.isError && (
            <ErrorState
              className='min-h-28'
              title={t('Unable to load partner application settings')}
              onRetry={() => void settings.refetch()}
            />
          )}
          {intakeClosed && (
            <Alert>
              <AlertDescription>
                {t(
                  'Partner applications are currently closed. You can still view your submissions.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {hasActiveApplication && (
            <Alert>
              <AlertDescription>
                {t(
                  'An application is already awaiting review. You can follow its status below.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {history.isError && (
            <Alert>
              <AlertDescription className='flex flex-wrap items-center gap-2'>
                {t(
                  'Unable to verify whether an application is already pending. Retry before submitting.'
                )}
                <Button
                  type='button'
                  variant='link'
                  size='sm'
                  className='h-auto p-0'
                  onClick={() => void history.refetch()}
                >
                  {t('Retry')}
                </Button>
              </AlertDescription>
            </Alert>
          )}
          {settings.data?.partner_instructions && (
            <Alert>
              <AlertDescription className='whitespace-pre-wrap'>
                {settings.data.partner_instructions}
              </AlertDescription>
            </Alert>
          )}
          <PartnerApplicationForm owner={props.owner} disabled={disabled} />
          <ApplicationHistory owner={props.owner} />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

export default function PartnerApplicationPage() {
  const owner = useAuthStore((state) => state.auth.user?.id)
  if (!owner) return <LoadingState />
  return <PartnerWorkspace key={owner} owner={owner} />
}
