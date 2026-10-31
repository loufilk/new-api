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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
import { Switch } from '@/components/ui/switch'
import { OperationDialog } from '@/features/agent-platform/components/operation-dialog'
import type { OperationDraft } from '@/features/agent-platform/types'
import { formatQuotaWithCurrency, getCurrencyLabel } from '@/lib/currency'
import {
  getEditableQuotaStep,
  parseQuotaFromDollars,
  quotaUnitsToDollars,
} from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getInvitationPolicy } from '../api'
import { invitationPolicySchema, type InvitationPolicyForm } from '../lib/forms'

export function InvitationPolicySettings() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const form = useForm<InvitationPolicyForm>({
    resolver: zodResolver(invitationPolicySchema(t)),
    defaultValues: { enabled: false, amount: 0 },
  })
  const query = useQuery({
    queryKey: ['agent-redemption', user?.id, user?.role, 'invitation-policy'],
    queryFn: ({ signal }) => getInvitationPolicy(signal),
    enabled: user?.role === ROLE.SUPER_ADMIN,
  })
  useEffect(() => {
    if (query.data) {
      form.reset({
        enabled: query.data.enabled,
        amount: quotaUnitsToDollars(query.data.quota),
      })
    }
  }, [query.data, form])
  if (user?.role !== ROLE.SUPER_ADMIN) return null
  if (query.isLoading) return <LoadingState />
  if (query.isError) {
    return (
      <ErrorState
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }
  return (
    <>
      <Form {...form}>
        <form
          className='max-w-xl space-y-5'
          onSubmit={form.handleSubmit((data) => {
            if (
              draft ||
              !query.data ||
              !Number.isSafeInteger(query.data.revision)
            ) {
              return
            }
            const quota = form.getFieldState('amount').isDirty
              ? parseQuotaFromDollars(data.amount)
              : query.data.quota
            setDraft({
              action: 'invitation.policy.update',
              request_id: crypto.randomUUID(),
              payload: {
                expected_revision: query.data.revision,
                enabled: data.enabled,
                quota,
              },
              endpoint: '/api/agent-redemption/invitation-policy',
              method: 'put',
              resource: '/agent-redemption',
              review: [
                {
                  label: 'Enable invitation test quota',
                  before: t(query.data.enabled ? 'Enabled' : 'Disabled'),
                  after: t(data.enabled ? 'Enabled' : 'Disabled'),
                },
                {
                  label: 'Quota',
                  before: formatQuotaWithCurrency(query.data.quota, {
                    abbreviate: false,
                  }),
                  after: formatQuotaWithCurrency(quota, { abbreviate: false }),
                },
              ],
            })
          })}
        >
          <p className='text-muted-foreground text-sm'>
            {t(
              'Only the super administrator can configure registration test grants. Disabled by default; changes apply only to new invitations.'
            )}
          </p>
          {query.data?.payment_compliance_confirmed === false && (
            <Alert>
              <AlertDescription>
                {t(
                  'Payment compliance is not confirmed. Test quota will not be granted even when this policy is enabled.'
                )}
              </AlertDescription>
            </Alert>
          )}
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <FormItem className='flex items-center justify-between gap-4'>
                <FormLabel>{t('Enable invitation test quota')}</FormLabel>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={draft !== null}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='amount'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('Test quota ({{currency}})', {
                    currency: getCurrencyLabel(),
                  })}
                </FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    type='number'
                    min={0}
                    step={getEditableQuotaStep()}
                    disabled={draft !== null}
                    onChange={(event) =>
                      field.onChange(
                        event.target.value === ''
                          ? Number.NaN
                          : Number(event.target.value)
                      )
                    }
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <Button
            type='submit'
            disabled={
              draft !== null ||
              !query.data ||
              !Number.isSafeInteger(query.data.revision)
            }
          >
            {draft !== null ? t('Saving...') : t('Save changes')}
          </Button>
        </form>
      </Form>
      {draft && (
        <OperationDialog
          key={draft.request_id}
          draft={draft}
          onClose={() => setDraft(null)}
          onComplete={() => {
            setDraft(null)
            toast.success(t('Settings saved successfully'))
            void queryClient.invalidateQueries({
              queryKey: ['agent-redemption'],
            })
          }}
        />
      )}
    </>
  )
}
