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
import { useEffect, useRef, useState } from 'react'
import { FormProvider, useForm } from 'react-hook-form'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { markServerErrorHandled } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  getServerErrorSources,
} from '@/lib/server-error-message'

import { invoiceKey } from '../customer/api'
import { useFinanceLabels } from './i18n'
import {
  getSettings,
  putSettings,
  type AdminInvoiceSettings,
} from './settings-api'
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
  const [loaded, setLoaded] = useState<AdminInvoiceSettings | null>(null)
  const client = useQueryClient()
  const locked = useRef(false)
  const [reloading, setReloading] = useState(false)
  const [stale, setStale] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const form = useForm<SettingsFormValues>({
    mode: 'onChange',
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
  useEffect(() => {
    if (!loaded && query.data) {
      reset(editSettings(query.data))
      setLoaded(query.data)
    }
  }, [loaded, query.data, reset])
  const { isDirty, isValid } = form.formState
  const save = useMutation({
    mutationFn: putSettings,
    retry: false,
    gcTime: 0,
    networkMode: 'always',
    onSuccess: (settings) => {
      client.setQueryData(settingsKey(props.reviewer), settings)
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
    onSettled: () => {
      locked.current = false
    },
  })
  const busy = save.isPending || reloading
  const submit = form.handleSubmit((draft) => {
    const body = settingsPayload(draft)
    if (locked.current || stale || !body) return
    locked.current = true
    setError(null)
    save.mutate(body)
  })
  async function reload() {
    if (locked.current) return
    locked.current = true
    setReloading(true)
    try {
      const { data: latest, error: readError } = await query.refetch({
        throwOnError: true,
      })
      if (!latest) throw readError
      // Advance the expected version only together with the newly loaded fields.
      reset(editSettings(latest))
      setLoaded(latest)
      setError(null)
      setStale(false)
    } catch (err) {
      markServerErrorHandled(err)
      setError(err)
    } finally {
      setReloading(false)
      locked.current = false
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !locked.current) props.onClose()
      }}
      title={label('Invoice settings', '开票设置')}
      description={label(
        'Changes apply only to new applications. The configured tax rate is for reference and is not a fee.',
        '修改仅影响新申请。配置税率仅供参考，不作为手续费收取。'
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
      {loaded && (
        <FormProvider {...form}>
          <form
            id='invoice-settings-form'
            className='space-y-4'
            onSubmit={submit}
            noValidate
          >
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
