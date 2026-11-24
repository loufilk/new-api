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
import { useState } from 'react'
import { Controller, FormProvider, useForm } from 'react-hook-form'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { SettingsSwitchField } from '@/features/system-settings/components/settings-form-layout'
import { markServerErrorHandled } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  getServerErrorSources,
} from '@/lib/server-error-message'
import { STATUS_QUERY_KEY } from '@/lib/status-query'

import { invoiceKey } from '../customer/api'
import { useFinanceLabels } from './i18n'
import { getSettings, putSettings } from './settings-api'
import { InvoiceCategoryFields } from './settings-category-fields'
import {
  editSettings,
  settingsFormSchema,
  settingsPayload,
  type SettingsFormValues,
} from './settings-form'
import { InvoiceTypeFields } from './settings-type-fields'

interface SettingsDialogProps {
  reviewer: number
  onClose: () => void
}
const settingsKey = (reviewer: number) =>
  ['invoice-finance-settings', reviewer] as const

export function SettingsDialog(props: SettingsDialogProps) {
  const { label } = useFinanceLabels()
  const query = useQuery({
    queryKey: settingsKey(props.reviewer),
    queryFn: ({ signal }) => getSettings(signal),
    gcTime: 0,
    staleTime: Infinity,
    retry: false,
    meta: { redirectOnInternalServerError: false, errorToast: false },
  })
  const loaded = query.data ?? null
  const intakeMessages = {
    center_off: label(
      'The saved invoice center switch is off.',
      '已保存的开票中心总开关处于关闭状态。'
    ),
    deployment_off: label(
      'Invoice intake is disabled by this node’s deployment settings.',
      '当前服务节点的部署配置禁用了开票申请。'
    ),
    merchant_not_configured: label(
      'This node has no valid invoice merchant scope configured.',
      '当前服务节点未配置有效的开票商户范围。'
    ),
    no_invoice_types: label(
      'No invoice type is currently enabled.',
      '当前没有启用任何发票类型。'
    ),
    no_categories: label(
      'No invoice category is currently enabled.',
      '当前没有启用任何开票类目。'
    ),
    configured: label(
      'This node’s saved settings allow new applications. Order and payment eligibility still apply.',
      '当前服务节点的已保存配置允许新申请，仍需满足订单与付款资格。'
    ),
  }
  const client = useQueryClient()
  const [reloading, setReloading] = useState(false)
  const [stale, setStale] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const form = useForm<SettingsFormValues>({
    mode: 'onChange',
    values: loaded ? editSettings(loaded) : undefined,
    resolver: zodResolver(
      settingsFormSchema(
        label(
          'Enter 0–100 with up to two decimal places.',
          '请输入 0 至 100 之间的数值，最多两位小数。'
        )
      )
    ),
  })
  const { reset } = form
  const formReady =
    form.getValues('ordinary') !== undefined &&
    form.getValues('special') !== undefined
  const { isDirty, isSubmitting, isValid } = form.formState
  const save = useMutation({
    mutationFn: putSettings,
    retry: false,
    gcTime: 0,
    networkMode: 'always',
    onSuccess: (settings) => {
      client.setQueryData(settingsKey(props.reviewer), settings)
      if (settings.center_enabled !== loaded?.center_enabled) {
        // Public status combines the saved switch with the environment override.
        void client.invalidateQueries({
          queryKey: STATUS_QUERY_KEY,
          exact: true,
        })
      }
      void client.invalidateQueries({
        queryKey: [...invoiceKey(props.reviewer), 'config'],
      })
      toast.success(label('Invoice settings saved', '开票设置已保存'))
      props.onClose()
    },
    onError: (err) => {
      markServerErrorHandled(err)
      const conflict = getServerErrorSources(err).some(
        (source) => source.code === 'INVOICE_SETTINGS_CONFLICT'
      )
      setStale(conflict)
      setError(conflict ? null : err)
    },
  })
  const busy = save.isPending || isSubmitting || reloading
  const submit = form.handleSubmit((draft) => {
    const body = settingsPayload(draft, loaded?.categories !== undefined)
    if (busy || stale || !body) return
    setError(null)
    save.mutate(body)
  })
  async function reload() {
    if (busy) return
    setReloading(true)
    try {
      const { data: latest, error: readError } = await query.refetch({
        throwOnError: true,
      })
      if (!latest) throw readError
      // Advance the expected version only together with the newly loaded fields.
      reset(editSettings(latest))
      setError(null)
      setStale(false)
    } catch (err) {
      markServerErrorHandled(err)
      setError(err)
    } finally {
      setReloading(false)
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) props.onClose()
      }}
      title={label('Invoice settings', '开票设置')}
      description={label(
        'The center switch controls customer navigation and new applications. Rate and category changes affect new applications only. The configured tax rate is not a fee.',
        '总开关控制客户入口和新申请；费率及类目修改仅影响新申请。配置税率不作为手续费收取。'
      )}
      showCloseButton={!busy}
      contentClassName='max-h-[90dvh]'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            disabled={busy}
            onClick={props.onClose}
          >
            {label('Cancel', '取消')}
          </Button>
          {loaded && (
            <Button
              type='submit'
              form='invoice-settings-form'
              disabled={busy || stale || !isDirty || !isValid}
            >
              {label('Save', '保存')}
            </Button>
          )}
        </>
      }
    >
      {!loaded && !query.isError && <LoadingState />}
      {!loaded && query.isError && (
        <ErrorState
          description={getServerErrorMessage(query.error)}
          onRetry={() => void query.refetch()}
        />
      )}
      {loaded && !formReady && <LoadingState />}
      {loaded && formReady && (
        <FormProvider {...form}>
          <form
            id='invoice-settings-form'
            className='space-y-4'
            onSubmit={submit}
            noValidate
          >
            {loaded.intake_state && (
              <Alert role='status'>
                <AlertDescription>
                  {intakeMessages[loaded.intake_state]}
                </AlertDescription>
              </Alert>
            )}
            {loaded.center_enabled !== undefined && (
              <Controller
                control={form.control}
                name='center_enabled'
                render={({ field }) => (
                  <SettingsSwitchField
                    controlId='invoice-center-enabled'
                    label={label('Enable invoice center', '启用开票中心')}
                    description={label(
                      'Controls customer navigation and new applications. Existing applications remain available to customers and finance.',
                      '控制客户入口和新申请；已有申请仍可由客户查看并由财务处理。'
                    )}
                    checked={field.value ?? loaded.center_enabled === true}
                    disabled={busy}
                    onCheckedChange={field.onChange}
                  />
                )}
              />
            )}
            <InvoiceTypeFields
              kind='ordinary'
              disabled={busy}
              paymentAvailable={loaded.fee_payment_available === true}
            />
            <InvoiceTypeFields
              kind='special'
              disabled={busy}
              paymentAvailable={loaded.fee_payment_available === true}
            />
            <InvoiceCategoryFields
              disabled={busy}
              savedCategories={loaded.categories}
            />
            {(stale || error != null) && (
              <Alert variant='destructive'>
                <AlertDescription>
                  {stale && (
                    <p>
                      {label(
                        'Another administrator changed the settings. Reload the latest settings to replace your edits before saving.',
                        '其他管理员已修改设置。请重新加载最新设置，替换本次编辑后再保存。'
                      )}
                    </p>
                  )}
                  {error != null && <p>{getServerErrorMessage(error)}</p>}
                  {stale && (
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      disabled={busy}
                      onClick={() => void reload()}
                    >
                      {label('Reload latest settings', '重新加载最新设置')}
                    </Button>
                  )}
                </AlertDescription>
              </Alert>
            )}
          </form>
        </FormProvider>
      )}
    </Dialog>
  )
}
