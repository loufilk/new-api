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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
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
import { formatQuotaWithCurrency, getCurrencyLabel } from '@/lib/currency'
import { getEditableQuotaStep, parseQuotaFromDollars } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'

import { allocateAgentQuota } from '../api'
import { allocationSchema, type AllocationForm } from '../lib/forms'
import { canAllocateQuota } from '../lib/quota'
import type { AgentAccount, AgentAllocation } from '../types'

export function AllocationDialog(props: {
  account: AgentAccount
  onClose: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const submitting = useRef(false)
  const request = useRef<{ signature: string; id: string } | null>(null)
  const [pending, setPending] = useState<AgentAllocation | null>(null)
  const form = useForm<AllocationForm>({
    resolver: zodResolver(allocationSchema(t)),
    defaultValues: { amount: 0, reason: '' },
  })
  const mutation = useMutation({
    mutationFn: allocateAgentQuota,
    retry: false,
    onSuccess: () => {
      toast.success(t('Quota adjusted successfully'))
      void queryClient.invalidateQueries({ queryKey: ['agent-redemption'] })
      props.onClose()
    },
    onError: (error) => {
      handleServerError(error)
      setPending(null)
    },
    onSettled: () => {
      submitting.current = false
    },
  })

  const onSubmit = (data: AllocationForm) => {
    if (submitting.current) return
    const delta = parseQuotaFromDollars(data.amount)
    if (
      delta > 0 &&
      (props.account.user_deleted ||
        (props.account.user_role !== undefined &&
          props.account.user_role !== ROLE.AGENT) ||
        (props.account.user_status !== undefined &&
          props.account.user_status !== 1))
    ) {
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
    const signature = JSON.stringify([
      props.account.agent_user_id,
      delta,
      data.reason,
    ])
    if (request.current?.signature !== signature) {
      request.current = { signature, id: crypto.randomUUID() }
    }
    setPending({
      agent_user_id: props.account.agent_user_id,
      delta,
      reason: data.reason,
      request_id: request.current.id,
    })
  }

  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open && !mutation.isPending && !pending) props.onClose()
        }}
        title={t('Adjust agent quota')}
        description={t(
          'Only available quota can be reclaimed. Reserved and redeemed quota cannot be withdrawn.'
        )}
        showCloseButton={!mutation.isPending}
        footer={
          <>
            <Button
              variant='outline'
              disabled={mutation.isPending}
              onClick={props.onClose}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='submit'
              form='agent-allocation'
              disabled={mutation.isPending}
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
                      disabled={mutation.isPending}
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
                      disabled={mutation.isPending}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </form>
        </Form>
      </Dialog>
      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) setPending(null)
        }}
        title={t('Confirm quota adjustment')}
        desc={t(
          'Adjust agent #{{id}} by {{amount}}. This action is recorded in the ledger.',
          {
            id: props.account.agent_user_id,
            amount: formatQuotaWithCurrency(pending?.delta ?? 0, {
              abbreviate: false,
            }),
          }
        )}
        isLoading={mutation.isPending}
        confirmText={t('Confirm')}
        handleConfirm={() => {
          if (!pending || submitting.current) return
          submitting.current = true
          mutation.mutate(pending)
        }}
      />
    </>
  )
}
