import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { buttonVariants } from '@/components/ui/button'

import { formatAgentQuota } from '../lib/money'

export function WithdrawalContactDialog(props: {
  userId: number
  referralQuota: number
  paymentQuota: number
  amountDue?: number
  recoveryDebt?: number
  onClose: () => void
}) {
  const { t } = useTranslation()
  const paymentBeforeCredit = Math.max(
    0,
    props.paymentQuota - (props.recoveryDebt ?? 0)
  )
  const payment = Math.max(0, paymentBeforeCredit - (props.amountDue ?? 0))
  const referral = Math.max(
    0,
    props.referralQuota -
      Math.max(0, (props.amountDue ?? 0) - paymentBeforeCredit)
  )
  const total = referral + payment
  const summary = [
    t('Withdrawal contact summary'),
    `${t('User ID')}: ${props.userId}`,
    `${t('Referral Rewards Available')}: ${formatAgentQuota(referral)}`,
    `${t('Payment income available')}: ${formatAgentQuota(payment)}`,
    `${t('Combined amount available')}: ${formatAgentQuota(total)}`,
    t(
      'Handled by support. Minimum $20 combined, once per calendar month, with a 2% fee.'
    ),
  ].join('\n')
  return (
    <Dialog
      open
      title={t('Contact support to withdraw')}
      description={t(
        'Referral rewards and payment income remain separate. Tell support which source and amount to use; root records the completed result.'
      )}
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      footerClassName='grid grid-cols-1 gap-2 sm:flex'
      footer={
        <>
          <CopyButton
            value={summary}
            size='default'
            variant='outline'
            className='w-full sm:w-auto'
            aria-label={t('Copy withdrawal summary')}
          >
            {t('Copy withdrawal summary')}
          </CopyButton>
          <a
            className={buttonVariants({ className: 'w-full sm:w-auto' })}
            href='/support'
          >
            {t('Contact customer support')}
          </a>
        </>
      }
    >
      <dl className='space-y-3 text-sm'>
        <div className='flex justify-between gap-4'>
          <dt>{t('Referral Rewards Available')}</dt>
          <dd>{formatAgentQuota(referral)}</dd>
        </div>
        <div className='flex justify-between gap-4'>
          <dt>{t('Payment income available')}</dt>
          <dd>{formatAgentQuota(payment)}</dd>
        </div>
        <div className='flex justify-between gap-4 border-t pt-3 font-semibold'>
          <dt>{t('Combined amount available')}</dt>
          <dd>{formatAgentQuota(total)}</dd>
        </div>
      </dl>
      <p className='mt-4 text-sm'>
        {t(
          'Handled by support. Minimum $20 combined, once per calendar month, with a 2% fee.'
        )}
      </p>
      {total < 10_000_000 && (
        <p role='status' className='text-muted-foreground mt-2 text-xs'>
          {t('The available total has not reached the $20 withdrawal minimum.')}
        </p>
      )}
      {((props.amountDue ?? 0) > 0 || (props.recoveryDebt ?? 0) > 0) && (
        <p className='text-muted-foreground mt-2 text-xs'>
          {t(
            'Outstanding debt is offset before withdrawal. The final available amount is confirmed by support.'
          )}
        </p>
      )}
    </Dialog>
  )
}
