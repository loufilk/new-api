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
import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { OperationDialog } from '@/features/agent-platform/components/operation-dialog'
import type { OperationDraft } from '@/features/agent-platform/types'
import { formatQuotaWithCurrency, getCurrencyLabel } from '@/lib/currency'
import { getEditableQuotaStep, parseQuotaFromDollars } from '@/lib/format'

import { allocationSchema, type AllocationForm } from '../lib/forms'
import { canAllocateQuota } from '../lib/quota'
import type { AgentAccount } from '../types'

export function AllocationDialog(props: {
  account: AgentAccount
  onClose: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [pending, setPending] = useState<OperationDraft | null>(null)
  const form = useForm<AllocationForm>({
    resolver: zodResolver(allocationSchema(t)),
    defaultValues: { amount: 0, reason: '' },
  })
  const onSubmit = (data: AllocationForm) => {
    if (pending) return
    const delta = parseQuotaFromDollars(data.amount)
    if (delta > 0 && props.account.active_agent !== true) {
      form.setError('amount', {
        message: t(
          'Agent account must be active before issuing redemption codes'
        ),
      })
      return
    }
    if (!canAllocateQuota(delta, props.account.available_quota)) {
      form.setError('amount', {
        message: t(
          'Adjustment exceeds the available quota or safe integer range'
        ),
      })
      return
    }
    setPending({
      action: 'redemption.allocate',
      request_id: crypto.randomUUID(),
      payload: {
        agent_user_id: props.account.agent_user_id,
        delta,
        reason: data.reason,
      },
      endpoint: '/api/agent-redemption/allocations',
      resource: '/agent-redemption',
      review: [
        {
          label: 'Quota adjustment',
          after: formatQuotaWithCurrency(delta, { abbreviate: false }),
        },
        { label: 'Reason', after: data.reason },
      ],
    })
  }

  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open && !pending) props.onClose()
        }}
        title={t('Adjust agent quota')}
        description={t(
          'Only available quota can be reclaimed. Reserved and redeemed quota cannot be withdrawn.'
        )}
        showCloseButton={!pending}
        footer={
          <>
            <Button
              variant='outline'
              disabled={pending !== null}
              onClick={props.onClose}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='submit'
              form='agent-allocation'
              disabled={pending !== null}
            >
              {t('Continue')}
            </Button>
          </>
        }
      >
        <p className='mb-4 text-sm'>
          {t('Agent')} #{props.account.agent_user_id} · {t('Available quota')}:{' '}
          {formatQuotaWithCurrency(props.account.available_quota, {
            abbreviate: false,
          })}
        </p>
        <Form {...form}>
          <form
            id='agent-allocation'
            onSubmit={form.handleSubmit(onSubmit)}
            className='space-y-4'
          >
            <FormField
              control={form.control}
              name='amount'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {t('Adjustment ({{currency}})', {
                      currency: getCurrencyLabel(),
                    })}
                  </FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      type='number'
                      step={getEditableQuotaStep()}
                      disabled={pending !== null}
                      onChange={(event) =>
                        field.onChange(
                          event.target.value === ''
                            ? Number.NaN
                            : Number(event.target.value)
                        )
                      }
                    />
                  </FormControl>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Positive adds quota; negative reclaims available quota.'
                    )}
                  </p>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='reason'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Reason')}</FormLabel>
                  <FormControl>
                    <Textarea
                      {...field}
                      maxLength={200}
                      disabled={pending !== null}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </form>
        </Form>
      </Dialog>
      {pending && (
        <OperationDialog
          key={pending.request_id}
          draft={pending}
          onClose={() => setPending(null)}
          onComplete={() => {
            toast.success(t('Quota adjusted successfully'))
            void queryClient.invalidateQueries({
              queryKey: ['agent-redemption'],
            })
            setPending(null)
            props.onClose()
          }}
        />
      )}
    </>
  )
}
