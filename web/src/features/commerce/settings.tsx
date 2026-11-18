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
import { useForm, Controller } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
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
import { useAuthStore } from '@/stores/auth-store'

import { getCommerceConfig, saveCommerceConfig } from './api'
import { commerceSettingsSchema } from './lib/schema'
import type { CommerceConfig } from './types'

export function CommerceSettingsPage() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const config = useQuery({
    queryKey: ['commerce', 'admin-config', user?.id],
    queryFn: () => getCommerceConfig(true),
  })
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Commerce settings')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t(
          'Configure invoice eligibility, fees and partnership intake before accepting applications.'
        )}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        <div className='mx-auto w-full max-w-5xl'>
          {config.isLoading && <LoadingState />}
          {config.isError && (
            <ErrorState onRetry={() => void config.refetch()} />
          )}
          {config.data && (
            <CommerceSettingsForm
              initial={config.data}
              editable={hasPermission(user, 'commerce_settings', 'update')}
            />
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

function CommerceSettingsForm(props: {
  initial: CommerceConfig
  editable: boolean
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm<CommerceConfig>({
    resolver: zodResolver(commerceSettingsSchema),
    defaultValues: props.initial,
  })
  const save = useMutation({
    mutationFn: saveCommerceConfig,
    onSuccess: async (value) => {
      form.reset(value)
      toast.success(t('Settings saved'))
      await client.invalidateQueries({ queryKey: ['commerce'] })
    },
  })
  const busy = save.isPending || !props.editable
  return (
    <form
      onSubmit={form.handleSubmit((values) => save.mutate(values))}
      className='grid gap-5'
    >
      <Alert>
        <AlertDescription>
          {t(
            'Customers can pay invoice fees from their paid balance or through enabled online payment methods. Cancelled balance payments are returned automatically; online refunds are recorded after payment.'
          )}
        </AlertDescription>
      </Alert>
      <Card>
        <CardHeader>
          <CardTitle>{t('Invoice rules')}</CardTitle>
          <CardDescription>
            {t(
              'Paid recharge orders and redeemed payment codes with verified settlement currency and amount can be selected.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-5'>
          <Controller
            name='invoice_enabled'
            control={form.control}
            render={({ field }) => (
              <Field orientation='horizontal'>
                <FieldLabel htmlFor='invoice-enabled'>
                  {t('Accept invoice applications')}
                </FieldLabel>
                <Switch
                  id='invoice-enabled'
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  disabled={busy}
                />
              </Field>
            )}
          />
          <div className='grid gap-4 sm:grid-cols-2'>
            {(
              [
                {
                  name: 'invoice_window_days',
                  label: 'Eligible order age in days',
                  min: 1,
                  max: 3650,
                },
                {
                  name: 'invoice_max_orders',
                  label: 'Maximum orders per application',
                  min: 1,
                  max: 100,
                },
                {
                  name: 'general_fee_bps',
                  label: 'General invoice fee in basis points',
                  min: 0,
                  max: 10000,
                },
                {
                  name: 'special_fee_bps',
                  label: 'Special invoice fee in basis points',
                  min: 0,
                  max: 10000,
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
                  {...form.register(field.name, { valueAsNumber: true })}
                  disabled={busy}
                  aria-invalid={!!form.formState.errors[field.name]}
                />
                <FieldError>
                  {form.formState.errors[field.name] &&
                    t('Enter a whole number within the allowed range')}
                </FieldError>
              </Field>
            ))}
          </div>
          <p className='text-muted-foreground text-xs'>
            {t('100 basis points = 1%. Set 0 for no service fee.')}
          </p>
          <Field>
            <FieldLabel htmlFor='invoice-currency'>
              {t('Invoice settlement currency')}
            </FieldLabel>
            <Input
              id='invoice-currency'
              maxLength={3}
              {...form.register('invoice_currency')}
              disabled={busy}
              aria-invalid={!!form.formState.errors.invoice_currency}
            />
            <FieldError>
              {form.formState.errors.invoice_currency?.message &&
                t(form.formState.errors.invoice_currency.message)}
            </FieldError>
          </Field>
          <Controller
            name='special_invoice_enabled'
            control={form.control}
            render={({ field }) => (
              <Field orientation='horizontal'>
                <FieldLabel htmlFor='invoice-special-enabled'>
                  {t('Accept special invoices')}
                </FieldLabel>
                <Switch
                  id='invoice-special-enabled'
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  disabled={busy}
                />
              </Field>
            )}
          />
          <Field>
            <FieldLabel htmlFor='invoice-instructions'>
              {t('Invoice application instructions')}
            </FieldLabel>
            <Textarea
              id='invoice-instructions'
              rows={4}
              maxLength={3000}
              {...form.register('invoice_instructions')}
              disabled={busy}
            />
          </Field>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>{t('Partner intake')}</CardTitle>
          <CardDescription>
            {t(
              'Set the instructions shown to suppliers when they submit an application.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-5'>
          <Controller
            name='partner_enabled'
            control={form.control}
            render={({ field }) => (
              <Field orientation='horizontal'>
                <FieldLabel htmlFor='partner-enabled'>
                  {t('Accept partner applications')}
                </FieldLabel>
                <Switch
                  id='partner-enabled'
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  disabled={busy}
                />
              </Field>
            )}
          />
          <Field>
            <FieldLabel htmlFor='partner-instructions'>
              {t('Partner application instructions')}
            </FieldLabel>
            <Textarea
              id='partner-instructions'
              rows={4}
              maxLength={3000}
              {...form.register('partner_instructions')}
              disabled={busy}
            />
          </Field>
        </CardContent>
      </Card>
      <div className='flex flex-wrap justify-end gap-3'>
        <Button
          type='button'
          variant='outline'
          disabled={save.isPending}
          onClick={() => form.reset(props.initial)}
        >
          {t('Reset changes')}
        </Button>
        <Button type='submit' disabled={busy || !form.formState.isDirty}>
          {save.isPending ? t('Saving...') : t('Save settings')}
        </Button>
      </div>
    </form>
  )
}
