import { useQuery } from '@tanstack/react-query'
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
import { ApplicationHistory } from './application-history'
import { PartnerApplicationForm } from './application-form'

const partnershipSteps = [
  {
    title: 'Submit a quote',
    detail: 'Provide your site information and group rates.',
  },
  {
    title: 'Compare offers',
    detail: 'We compare supplier quotes across sites.',
  },
  {
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
      <SectionPageLayout.Title>{t('Partner application')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-4'>
          <h2 className='text-base font-semibold'>
            {t('Share your upstream and start a partnership')}
          </h2>
          <p className='text-muted-foreground text-sm'>
            {t('Tell us about your site, available groups and rates. API keys are not needed.')}
          </p>
          <div className='grid gap-3 sm:grid-cols-3'>
            {partnershipSteps.map((step, index) => (
              <Card key={step.title} className='min-w-0 rounded-md'>
                <CardContent className='flex gap-3 p-3'>
                  <span className='text-muted-foreground text-sm tabular-nums'>
                    0{index + 1}
                  </span>
                  <div className='min-w-0'>
                    <p className='text-sm font-medium'>{t(step.title)}</p>
                    <p className='text-muted-foreground mt-1 text-sm'>
                      {t(step.detail)}
                    </p>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
          <Alert>
            <AlertDescription>
              {t('Do not include API keys. Submitted applications cannot be edited. Review the details before submitting.')}
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
                {t('Partner applications are currently closed. You can still view your submissions.')}
              </AlertDescription>
            </Alert>
          )}
          {hasActiveApplication && (
            <Alert>
              <AlertDescription>
                {t('An application is already awaiting review. You can follow its status below.')}
              </AlertDescription>
            </Alert>
          )}
          {history.isError && (
            <Alert>
              <AlertDescription className='flex flex-wrap items-center gap-2'>
                {t('Unable to verify whether an application is already pending. Retry before submitting.')}
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
