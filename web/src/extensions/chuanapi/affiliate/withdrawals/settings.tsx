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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Controller, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { hasPermission } from '@/lib/admin-permissions'
import { formatQuota } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { getWithdrawalConfig, saveWithdrawalConfig } from './api'
import { withdrawalConfigSchema } from './schema'
import type { WithdrawalConfig } from './types'

export function WithdrawalSettings() {
  const user = useAuthStore((state) => state.auth.user)
  const query = useQuery({
    queryKey: ['referral-withdrawals', 'config', user?.id],
    queryFn: getWithdrawalConfig,
  })
  return (
    <>
      {query.isLoading && <LoadingState />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {query.data && (
        <WithdrawalSettingsForm
          initial={query.data}
          editable={hasPermission(
            user,
            'referral_withdrawal_settings',
            'update'
          )}
        />
      )}
    </>
  )
}

function WithdrawalSettingsForm({
  initial,
  editable,
}: {
  initial: WithdrawalConfig
  editable: boolean
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm<WithdrawalConfig>({
    resolver: zodResolver(withdrawalConfigSchema),
    defaultValues: initial,
  })
  const save = useMutation({
    mutationFn: saveWithdrawalConfig,
    onSuccess: async (value) => {
      form.reset(value)
      toast.success(t('Settings saved'))
      await client.invalidateQueries({ queryKey: ['referral-withdrawals'] })
    },
  })
  const disabled = save.isPending || !editable
  return (
    <form
      className='mx-auto grid max-w-5xl gap-5'
      onSubmit={form.handleSubmit((value) => save.mutate(value))}
    >
      <Alert>
        <AlertDescription>
          {t(
            'The rate and fees are saved with each request. Changing these settings affects new requests only.'
          )}
        </AlertDescription>
      </Alert>
      <Card>
        <CardHeader>
          <CardTitle>{t('Referral withdrawal settings')}</CardTitle>
          <CardDescription>
            {t(
              'Configure eligibility, conversion and network fees for ordinary invitation rewards.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-5'>
          <Controller
            name='enabled'
            control={form.control}
            render={({ field }) => (
              <Field orientation='horizontal'>
                <FieldLabel htmlFor='withdrawal-enabled'>
                  {t('Accept withdrawal requests')}
                </FieldLabel>
                <Switch
                  id='withdrawal-enabled'
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  disabled={disabled}
                />
              </Field>
            )}
          />
          <div className='grid gap-4 sm:grid-cols-3'>
            {(
              [
                {
                  name: 'min_consumed_quota',
                  label: 'Minimum account usage (quota)',
                  min: 0,
                  max: Number.MAX_SAFE_INTEGER,
                },
                {
                  name: 'min_withdrawal_quota',
                  label: 'Minimum withdrawal (quota)',
                  min: 1,
                  max: Number.MAX_SAFE_INTEGER,
                },
                {
                  name: 'quota_per_usdt',
                  label: 'Quota units per 1 USDT',
                  min: 1,
                  max: 1_000_000_000_000,
                },
              ] as const
            ).map((field) => (
              <Field key={field.name}>
                <FieldLabel htmlFor={field.name}>{t(field.label)}</FieldLabel>
                <Input
                  id={field.name}
                  type='number'
                  min={field.min}
                  max={field.max}
                  step={1}
                  disabled={disabled}
                  {...form.register(field.name, { valueAsNumber: true })}
                  aria-invalid={!!form.formState.errors[field.name]}
                />
                <p className='text-muted-foreground text-xs'>
                  {formatQuota(form.watch(field.name) || 0)}
                </p>
                <FieldError>
                  {form.formState.errors[field.name]
                    ? t('Enter a whole number within the allowed range')
                    : null}
                </FieldError>
              </Field>
            ))}
          </div>
          <div className='grid gap-4 sm:grid-cols-3'>
            {(
              [
                { name: 'bsc_fee_bps', network: 'BSC' },
                { name: 'polygon_fee_bps', network: 'POLYGON' },
                { name: 'trc20_fee_bps', network: 'TRC20' },
              ] as const
            ).map((field) => (
              <Field key={field.name}>
                <FieldLabel htmlFor={field.name}>
                  {t('{{network}} fee (basis points)', {
                    network: field.network,
                  })}
                </FieldLabel>
                <Input
                  id={field.name}
                  type='number'
                  min={0}
                  max={9999}
                  step={1}
                  disabled={disabled}
                  {...form.register(field.name, { valueAsNumber: true })}
                  aria-invalid={!!form.formState.errors[field.name]}
                />
                <p className='text-muted-foreground text-xs'>
                  {(form.watch(field.name) || 0) / 100}%
                </p>
                <FieldError>
                  {form.formState.errors[field.name]
                    ? t('Enter a whole number within the allowed range')
                    : null}
                </FieldError>
              </Field>
            ))}
          </div>
          <p className='text-muted-foreground text-xs'>
            {t(
              '100 basis points = 1%. USDT fees round up to six decimal places.'
            )}
          </p>
          <Field>
            <FieldLabel htmlFor='withdrawal-instructions'>
              {t('Withdrawal instructions')}
            </FieldLabel>
            <Textarea
              id='withdrawal-instructions'
              rows={4}
              maxLength={3000}
              disabled={disabled}
              {...form.register('instructions')}
            />
          </Field>
        </CardContent>
      </Card>
      <div className='flex justify-end gap-3'>
        <Button
          type='button'
          variant='outline'
          disabled={save.isPending}
          onClick={() => form.reset(initial)}
        >
          {t('Reset changes')}
        </Button>
        <Button type='submit' disabled={disabled || !form.formState.isDirty}>
          {save.isPending ? t('Saving...') : t('Save settings')}
        </Button>
      </div>
    </form>
  )
}
