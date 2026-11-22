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
import { Link } from '@tanstack/react-router'
import { useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '@/features/system-settings/components/settings-form-layout'
import { SettingsPageFormActions } from '@/features/system-settings/components/settings-page-context'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { toIntlLocale } from '@/i18n/languages'
import { getAuthSessionEpoch } from '@/lib/auth-session'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { RechargeHistoryButton } from '../wallet/components/recharge-history-dialog'
import { getBenefitConfig, saveBenefitConfig } from './api'
import {
  BENEFIT_CONFIG_QUERY_KEY,
  BENEFIT_QUERY_KEY,
  benefitErrorMessage,
  formatBenefitCny,
  isBenefitConfigConflict,
  normalizeCnyAmount,
} from './lib'
import {
  createBenefitConfigSchema,
  type BenefitConfigValues,
} from './lib/config-schema'
import type { NewUserBenefitConfig } from './types'

function benefitConfigFormValues(
  config: NewUserBenefitConfig
): BenefitConfigValues {
  return {
    enabled: config.enabled,
    require_verified_email: config.require_verified_email ?? true,
    require_turnstile: config.require_turnstile ?? true,
    network_limit_enabled: config.network_limit_enabled ?? true,
    payment_identity_check_enabled:
      config.payment_identity_check_enabled ?? false,
    pay_amount_cny: config.pay_amount_cny,
    credit_amount_cny: config.credit_amount_cny,
    max_total_orders: String(config.max_total_orders),
    max_orders_per_24h: String(config.max_orders_per_24h),
    max_orders_per_network_24h: String(config.max_orders_per_network_24h),
    total_bonus_budget_cny: config.total_bonus_budget_cny,
  }
}

function BenefitSettingsForm(props: {
  config: NewUserBenefitConfig
  onReload: () => Promise<NewUserBenefitConfig | undefined>
}) {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const [snapshot, setSnapshot] = useState(props.config)
  const [conflict, setConflict] = useState(false)
  const [reloading, setReloading] = useState(false)
  const saving = useRef(false)
  const locale = i18n.resolvedLanguage || i18n.language
  const numberLocale = toIntlLocale(locale)
  const schema = createBenefitConfigSchema(
    t,
    numberLocale,
    props.config.turnstile_ready
  )
  const form = useForm<BenefitConfigValues>({
    resolver: zodResolver(schema),
    mode: 'onChange',
    defaultValues: benefitConfigFormValues(snapshot),
  })
  const save = useMutation({
    mutationFn: saveBenefitConfig,
    retry: false,
    meta: { errorToast: false },
  })
  const values = form.watch()
  const busy = save.isPending || form.formState.isSubmitting || reloading
  const enableUnavailable =
    values.enabled && values.require_turnstile && !props.config.turnstile_ready

  const submit = async (data: BenefitConfigValues) => {
    if (saving.current || conflict || reloading) return
    saving.current = true
    const epoch = getAuthSessionEpoch()
    try {
      const result = await save.mutateAsync({
        enabled: data.enabled,
        require_verified_email: data.require_verified_email,
        require_turnstile: data.require_turnstile,
        network_limit_enabled: data.network_limit_enabled,
        payment_identity_check_enabled: data.payment_identity_check_enabled,
        pay_amount_cny: normalizeCnyAmount(data.pay_amount_cny),
        credit_amount_cny: normalizeCnyAmount(data.credit_amount_cny),
        max_total_orders: Number(data.max_total_orders),
        max_orders_per_24h: Number(data.max_orders_per_24h),
        max_orders_per_network_24h: data.network_limit_enabled
          ? Number(data.max_orders_per_network_24h)
          : snapshot.max_orders_per_network_24h || 3,
        total_bonus_budget_cny: normalizeCnyAmount(data.total_bonus_budget_cny),
        version: snapshot.version,
      })
      if (epoch !== getAuthSessionEpoch()) return
      setSnapshot(result)
      form.reset(benefitConfigFormValues(result))
      client.setQueryData([...BENEFIT_CONFIG_QUERY_KEY, epoch], result)
      void client.invalidateQueries({ queryKey: BENEFIT_QUERY_KEY })
      toast.success(t('New user recharge benefit saved'))
    } catch (error) {
      if (epoch !== getAuthSessionEpoch()) return
      if (isBenefitConfigConflict(error)) {
        setConflict(true)
      } else {
        handleServerError(error, t('Failed to save recharge benefit'), {
          title: t(
            benefitErrorMessage(error, 'Failed to save recharge benefit')
          ),
        })
      }
    } finally {
      saving.current = false
    }
  }

  const reload = async () => {
    setReloading(true)
    try {
      const current = await props.onReload()
      if (current) {
        setSnapshot(current)
        form.reset(benefitConfigFormValues(current))
        setConflict(false)
      }
    } catch (error) {
      handleServerError(error)
    } finally {
      setReloading(false)
    }
  }

  return (
    <SettingsSection title={t('New user recharge benefit')}>
      <div>
        <RechargeHistoryButton admin />
      </div>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Offer one discounted recharge to accounts without a successful recharge. Set the amount paid and the total credit received in CNY.'
        )}
      </p>
      <Alert role='region' aria-label={t('Benefit reservations')}>
        <AlertTitle>{t('Benefit reservations')}</AlertTitle>
        <AlertDescription>
          <dl className='grid w-full gap-3 sm:grid-cols-3'>
            <div>
              <dt>{t('Reserved benefit orders')}</dt>
              <dd className='text-foreground font-semibold tabular-nums'>
                {formatNumber(props.config.reserved_orders, numberLocale)}
              </dd>
            </div>
            <div>
              <dt>{t('Orders reserved in the last 24 hours')}</dt>
              <dd className='text-foreground font-semibold tabular-nums'>
                {formatNumber(props.config.reserved_orders_24h, numberLocale)}
              </dd>
            </div>
            <div>
              <dt>{t('Reserved bonus credit (CNY)')}</dt>
              <dd className='text-foreground font-semibold tabular-nums'>
                {formatBenefitCny(props.config.reserved_bonus_cny, locale)}
              </dd>
            </div>
          </dl>
          <p>
            {t(
              'Reservations include pending payments. Turning the offer off and on does not reset these totals. Lower limits stop new orders while existing orders keep their original credit.'
            )}
          </p>
        </AlertDescription>
      </Alert>
      {values.require_turnstile && !props.config.turnstile_ready && (
        <Alert>
          <AlertTitle>{t('Security verification setup required')}</AlertTitle>
          <AlertDescription>
            <p>
              {t(
                'Configure and enable Turnstile before enabling the new user benefit'
              )}
            </p>
            <Button
              size='sm'
              variant='outline'
              role='link'
              render={
                <Link
                  to='/system-settings/auth/$section'
                  params={{ section: 'bot-protection' }}
                />
              }
            >
              {t('Configure bot protection')}
            </Button>
          </AlertDescription>
        </Alert>
      )}
      <Form {...form}>
        <SettingsForm
          onSubmit={form.handleSubmit(submit)}
          autoComplete='off'
          data-no-autosubmit='true'
        >
          <SettingsPageFormActions
            onSave={() => void form.handleSubmit(submit)()}
            isSaving={busy}
            isSaveDisabled={
              !form.formState.isDirty || conflict || enableUnavailable
            }
            saveLabel='Save recharge benefit'
          />
          {conflict && (
            <Alert>
              <AlertTitle>{t('Recharge benefit settings changed')}</AlertTitle>
              <AlertDescription>
                <p>
                  {t(
                    'Another administrator updated these settings. Your edits are preserved. Reload the saved settings before editing again.'
                  )}
                </p>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  disabled={busy}
                  onClick={() => void reload()}
                >
                  {t('Reload saved settings')}
                </Button>
              </AlertDescription>
            </Alert>
          )}
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable new user recharge benefit')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Disabling stops new benefit orders. Existing orders keep their original price and credit.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={
                      busy ||
                      conflict ||
                      (values.require_turnstile &&
                        !props.config.turnstile_ready &&
                        !field.value)
                    }
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />
          <Alert>
            <AlertTitle>{t('Benefit risk controls')}</AlertTitle>
            <AlertDescription>
              {t(
                'Choose the checks required for new benefit orders. Each account can reserve the benefit once. Existing orders retain their saved policies and amounts.'
              )}
            </AlertDescription>
          </Alert>
          {(
            [
              {
                name: 'require_verified_email',
                label: t('Require a verified email'),
                description: t(
                  'Require a verified email before ordering and allow each verified email to reserve the benefit once. When off, email verification and email deduplication are skipped.'
                ),
              },
              {
                name: 'require_turnstile',
                label: t('Require human verification'),
                description: t(
                  'Require a Turnstile challenge for new orders. When off, this benefit does not require a human verification challenge.'
                ),
              },
              {
                name: 'network_limit_enabled',
                label: t('Limit orders per network'),
                description: t(
                  'Apply the 24-hour network limit to new orders. When off, the network limit is skipped; total orders, daily orders and the bonus budget still apply.'
                ),
              },
              {
                name: 'payment_identity_check_enabled',
                label: t('Check payment identity'),
                description: t(
                  'Only Alipay is available when enabled. ZPAY must return a complete Alipay user ID when the server queries the paid order. Each payment identity can receive the bonus once across accounts. Missing identity details leave the bonus pending; a previously used identity receives no bonus. The payment amount is still credited.'
                ),
              },
            ] as const
          ).map((policy) => (
            <FormField
              key={policy.name}
              control={form.control}
              name={policy.name}
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{policy.label}</FormLabel>
                    <FormDescription>{policy.description}</FormDescription>
                    <FormMessage />
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={(checked) => {
                        field.onChange(checked)
                        if (
                          policy.name === 'network_limit_enabled' &&
                          !checked
                        ) {
                          form.clearErrors('max_orders_per_network_24h')
                        }
                      }}
                      disabled={busy || conflict}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
          ))}
          <FormField
            control={form.control}
            name='pay_amount_cny'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Payment amount (CNY)')}</FormLabel>
                <FormControl>
                  <Input
                    type='text'
                    inputMode='decimal'
                    placeholder='0.10'
                    disabled={busy || conflict}
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t('The amount charged by Epay for this benefit.')}{' '}
                  {t('Allowed range: CNY 0.01 to CNY 1,000,000.00.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='credit_amount_cny'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Total credit (CNY)')}</FormLabel>
                <FormControl>
                  <Input
                    type='text'
                    inputMode='decimal'
                    placeholder='1.00'
                    disabled={busy || conflict}
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Includes the paid amount and bonus. The bonus follows the existing bonus balance usage rules.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          {(
            [
              {
                name: 'max_total_orders',
                label: t('Total benefit order limit'),
                placeholder: '1000',
              },
              {
                name: 'max_orders_per_24h',
                label: t('Benefit order limit per 24 hours'),
                placeholder: '100',
              },
              {
                name: 'max_orders_per_network_24h',
                label: t('Benefit order limit per network per 24 hours'),
                placeholder: '3',
              },
            ] as const
          ).map((limit) => (
            <FormField
              key={limit.name}
              control={form.control}
              name={limit.name}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{limit.label}</FormLabel>
                  <FormControl>
                    <Input
                      type='text'
                      inputMode='numeric'
                      placeholder={limit.placeholder}
                      disabled={
                        busy ||
                        conflict ||
                        (limit.name === 'max_orders_per_network_24h' &&
                          !values.network_limit_enabled)
                      }
                      {...field}
                    />
                  </FormControl>
                  {limit.name === 'max_orders_per_network_24h' && (
                    <FormDescription>
                      {t(
                        'A network is one IPv4 address or one IPv6 /64. Shared networks may include multiple customers.'
                      )}
                    </FormDescription>
                  )}
                  <FormMessage />
                </FormItem>
              )}
            />
          ))}
          <FormField
            control={form.control}
            name='total_bonus_budget_cny'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Total bonus credit limit (CNY)')}</FormLabel>
                <FormControl>
                  <Input
                    type='text'
                    inputMode='decimal'
                    placeholder='900.00'
                    disabled={busy || conflict}
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Caps the cumulative bonus face value: total credit minus the payment amount. Limits must be positive; use the switch to pause new orders.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <Alert>
            <AlertTitle>{t('Benefit preview')}</AlertTitle>
            <AlertDescription>
              {t('Pay {{pay}} and get {{credit}} in total credit', {
                pay: formatBenefitCny(values.pay_amount_cny, locale),
                credit: formatBenefitCny(values.credit_amount_cny, locale),
              })}
              <p className='mt-2'>
                {t(
                  'Each account can reserve the benefit once. The selected risk controls, total order limit, daily order limit and bonus budget apply to new orders.'
                )}
              </p>
              <p className='mt-2'>
                {t(
                  'Uses the configured Epay payment methods. Confirm that your provider accepts the payment amount before enabling.'
                )}
              </p>
            </AlertDescription>
          </Alert>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}

export function NewUserBenefitSettings() {
  const user = useAuthStore((state) => state.auth.user)
  const role = user?.role
  const epoch = getAuthSessionEpoch()
  const query = useQuery({
    queryKey: [...BENEFIT_CONFIG_QUERY_KEY, epoch],
    queryFn: ({ signal }) => getBenefitConfig(signal),
    enabled: role === 100,
    refetchOnWindowFocus: false,
    retry: false,
    meta: { errorToast: false, redirectOnInternalServerError: false },
  })
  if (role !== 100) return null
  if (!query.data) {
    if (query.isError) {
      return <ErrorState onRetry={() => void query.refetch()} />
    }
    return <LoadingState />
  }
  return (
    <BenefitSettingsForm
      key={epoch}
      config={query.data}
      onReload={async () => (await query.refetch({ throwOnError: true })).data}
    />
  )
}
