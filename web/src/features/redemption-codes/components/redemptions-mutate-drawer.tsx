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
import { useMutation } from '@tanstack/react-query'
import { type FormEvent, useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DateTimePicker } from '@/components/datetime-picker'
import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
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
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import {
  formatQuotaWithCurrency,
  getCurrencyDisplay,
  getCurrencyLabel,
} from '@/lib/currency'
import {
  formatQuota,
  getEditableQuotaStep,
  parseQuotaFromDollars,
} from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { addTimeToDate } from '@/lib/time'

import { createRedemption, getRedemption, updateRedemption } from '../api'
import { REDEMPTION_STATUS, SUCCESS_MESSAGES } from '../constants'
import {
  getRedemptionFormSchema,
  type RedemptionFormValues,
  REDEMPTION_FORM_DEFAULT_VALUES,
  transformFormDataToPayload,
  transformRedemptionToFormDefaults,
} from '../lib'
import type { Redemption, RedemptionFormData } from '../types'
import {
  RedemptionsExportDialog,
  type RedemptionExportData,
} from './redemptions-export-dialog'
import { useRedemptions } from './redemptions-provider'

type RedemptionsMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: Redemption
}

export function RedemptionsMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: RedemptionsMutateDrawerProps) {
  const { t } = useTranslation()
  const submitting = useRef(false)
  const creationRequest = useRef<RedemptionFormData | null>(null)
  const isUpdate = !!currentRow
  const redemptionId = currentRow?.id
  const { triggerRefresh } = useRedemptions()
  const [createdCodes, setCreatedCodes] = useState<RedemptionExportData | null>(
    null
  )
  const [redemptionLoadState, setRedemptionLoadState] = useState<
    'idle' | 'loading' | 'ready' | 'error'
  >('idle')
  const [loadedRedemption, setLoadedRedemption] = useState<Redemption | null>(
    null
  )

  const saveMutation = useMutation({
    mutationFn: async (payload: RedemptionFormData) => {
      if (payload.id !== undefined) {
        await updateRedemption(payload)
        return null
      }
      return createRedemption(payload)
    },
    retry: false,
    onSuccess: (codes, payload) => {
      if (codes) {
        creationRequest.current = null
        toast.success(
          codes.length > 1
            ? t('Successfully created {{count}} redemption codes', {
                count: codes.length,
              })
            : t(SUCCESS_MESSAGES.REDEMPTION_CREATED)
        )
        if (codes.length) {
          setCreatedCodes({
            keys: codes,
            name: payload.name,
            quota: formatQuotaWithCurrency(payload.quota, {
              abbreviate: false,
            }),
          })
        }
      } else {
        toast.success(t(SUCCESS_MESSAGES.REDEMPTION_UPDATED))
      }
      onOpenChange(false)
      triggerRefresh()
    },
    onError: (error) => handleServerError(error),
    onSettled: () => {
      submitting.current = false
    },
  })
  const isSubmitting = saveMutation.isPending

  const form = useForm<RedemptionFormValues>({
    resolver: zodResolver(getRedemptionFormSchema(t)),
    defaultValues: REDEMPTION_FORM_DEFAULT_VALUES,
  })

  // Load existing data when updating
  useEffect(() => {
    if (!open) {
      form.reset(REDEMPTION_FORM_DEFAULT_VALUES)
      setRedemptionLoadState('idle')
      setLoadedRedemption(null)
      return
    }

    if (!isUpdate || redemptionId === undefined) {
      form.reset(REDEMPTION_FORM_DEFAULT_VALUES)
      setRedemptionLoadState('ready')
      setLoadedRedemption(null)
      return
    }

    let ignoreResult = false

    form.reset(REDEMPTION_FORM_DEFAULT_VALUES)
    setRedemptionLoadState('loading')
    setLoadedRedemption(null)

    void getRedemption(redemptionId)
      .then((result) => {
        if (ignoreResult) return

        if (
          !result.success ||
          !result.data ||
          result.data.id !== redemptionId
        ) {
          setRedemptionLoadState('error')
          handleServerError(result, t('Failed to load'))
          return
        }

        form.reset(transformRedemptionToFormDefaults(result.data))
        setLoadedRedemption(result.data)
        setRedemptionLoadState('ready')
      })
      .catch((error: unknown) => {
        if (ignoreResult) return

        setRedemptionLoadState('error')
        handleServerError(error)
      })

    return () => {
      ignoreResult = true
    }
  }, [open, isUpdate, redemptionId, form, t])

  const isUpdateReady =
    !isUpdate ||
    (redemptionLoadState === 'ready' &&
      loadedRedemption !== null &&
      loadedRedemption?.id === redemptionId &&
      loadedRedemption?.status !== REDEMPTION_STATUS.USED)
  const isLoadingRedemption = redemptionLoadState === 'loading'

  const onSubmit = (data: RedemptionFormValues) => {
    if (submitting.current) return
    if (isUpdate && (!currentRow || !loadedRedemption || !isUpdateReady)) {
      return
    }

    const basePayload = transformFormDataToPayload(data)
    if (
      isUpdate &&
      loadedRedemption &&
      !form.getFieldState('quota_dollars').isDirty
    ) {
      basePayload.quota = loadedRedemption.quota
    }
    if (!Number.isSafeInteger(basePayload.quota) || basePayload.quota <= 0) {
      form.setError('quota_dollars', { message: t('Invalid quota amount') })
      return
    }
    if (
      !isUpdate &&
      !Number.isSafeInteger(basePayload.quota * (basePayload.count ?? 1))
    ) {
      form.setError('quota_dollars', { message: t('Invalid quota amount') })
      return
    }
    let payload: RedemptionFormData
    if (isUpdate && loadedRedemption) {
      payload = {
        id: loadedRedemption.id,
        name: basePayload.name,
        quota: basePayload.quota,
        expired_time: basePayload.expired_time,
      }
    } else {
      const previous = creationRequest.current
      if (
        previous &&
        previous.name === basePayload.name &&
        previous.quota === basePayload.quota &&
        previous.count === basePayload.count &&
        previous.expired_time === basePayload.expired_time
      ) {
        payload = previous
      } else {
        payload = { ...basePayload, request_id: crypto.randomUUID() }
        creationRequest.current = payload
      }
    }
    submitting.current = true
    saveMutation.mutate(payload)
  }

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    if (!isUpdate) {
      const name = form.getValues('name')
      if (!name?.trim()) {
        const quota = parseQuotaFromDollars(form.getValues('quota_dollars'))
        form.setValue('name', formatQuota(quota), { shouldValidate: true })
      }
    }

    void form.handleSubmit(onSubmit)(event)
  }

  const handleSetExpiry = (months: number, days: number, hours: number) => {
    const newDate = addTimeToDate(months, days, hours)
    form.setValue('expired_time', newDate ?? null)
  }

  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'
  const quotaStep = getEditableQuotaStep()
  const quotaLabel = t('Quota ({{currency}})', { currency: currencyLabel })
  const quotaPlaceholder = tokensOnly
    ? t('Enter quota in tokens')
    : t('Enter quota in {{currency}}', { currency: currencyLabel })
  let submitButtonLabel = t('Save changes')
  if (isLoadingRedemption) {
    submitButtonLabel = t('Loading...')
  } else if (isSubmitting) {
    submitButtonLabel = t('Saving...')
  }

  return (
    <>
      <Sheet
        open={open}
        onOpenChange={(nextOpen) => {
          if (!isSubmitting) onOpenChange(nextOpen)
        }}
      >
        <SheetContent
          className={sideDrawerContentClassName('sm:max-w-[600px]')}
        >
          <SheetHeader className={sideDrawerHeaderClassName()}>
            <SheetTitle>
              {isUpdate
                ? t('Update Redemption Code')
                : t('Create Redemption Code')}
            </SheetTitle>
            <SheetDescription>
              {isUpdate
                ? t('Update the redemption code by providing necessary info.')
                : t(
                    'Add new redemption code(s) by providing necessary info.'
                  )}{' '}
              {t('Click save when you&apos;re done.')}
            </SheetDescription>
          </SheetHeader>
          {loadedRedemption?.status === REDEMPTION_STATUS.USED && (
            <p role='status' className='text-muted-foreground px-6 text-sm'>
              {t('Used redemption codes cannot be changed or re-enabled')}
            </p>
          )}
          <Form {...form}>
            <form
              id='redemption-form'
              onSubmit={handleSubmit}
              className={sideDrawerFormClassName()}
              aria-busy={isLoadingRedemption}
            >
              <fieldset
                disabled={!isUpdateReady || isSubmitting}
                className='contents'
              >
                <SideDrawerSection>
                  <FormField
                    control={form.control}
                    name='name'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Name')}</FormLabel>
                        <FormControl>
                          <Input {...field} placeholder={t('Enter a name')} />
                        </FormControl>
                        <FormDescription>
                          {t('Name for this redemption code (1-20 characters)')}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='quota_dollars'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{quotaLabel}</FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            type='number'
                            step={quotaStep}
                            placeholder={quotaPlaceholder}
                            onChange={(e) =>
                              field.onChange(
                                Number.parseFloat(e.target.value) || 0
                              )
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {tokensOnly
                            ? t('Enter the quota amount in tokens')
                            : t('Enter the quota amount in {{currency}}', {
                                currency: currencyLabel,
                              })}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='expired_time'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Expiration Time')}</FormLabel>
                        <div className='flex flex-col gap-2'>
                          <FormControl>
                            <DateTimePicker
                              value={field.value ?? undefined}
                              onChange={(date) => field.onChange(date ?? null)}
                              placeholder={t('Never expires')}
                            />
                          </FormControl>
                          <div className='grid grid-cols-4 gap-1.5 sm:flex sm:gap-2'>
                            <Button
                              type='button'
                              variant='outline'
                              size='sm'
                              onClick={() => handleSetExpiry(0, 0, 0)}
                            >
                              {t('Never')}
                            </Button>
                            <Button
                              type='button'
                              variant='outline'
                              size='sm'
                              onClick={() => handleSetExpiry(1, 0, 0)}
                            >
                              {t('1M')}
                            </Button>
                            <Button
                              type='button'
                              variant='outline'
                              size='sm'
                              onClick={() => handleSetExpiry(0, 7, 0)}
                            >
                              {t('1W')}
                            </Button>
                            <Button
                              type='button'
                              variant='outline'
                              size='sm'
                              onClick={() => handleSetExpiry(0, 1, 0)}
                            >
                              {t('1 Day')}
                            </Button>
                          </div>
                        </div>
                        <FormDescription>
                          {t('Leave empty for never expires')}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  {!isUpdate && (
                    <FormField
                      control={form.control}
                      name='count'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Quantity')}</FormLabel>
                          <FormControl>
                            <Input
                              {...field}
                              type='number'
                              min='1'
                              max='100'
                              placeholder={t('Number of codes to create')}
                              onChange={(e) =>
                                field.onChange(
                                  e.target.value === ''
                                    ? Number.NaN
                                    : Number(e.target.value)
                                )
                              }
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Create multiple redemption codes at once (1-100)'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  )}
                </SideDrawerSection>
              </fieldset>
            </form>
          </Form>
          <SheetFooter className={sideDrawerFooterClassName()}>
            <SheetClose
              render={<Button variant='outline' disabled={isSubmitting} />}
            >
              {t('Close')}
            </SheetClose>
            <Button
              form='redemption-form'
              type='submit'
              disabled={isSubmitting || !isUpdateReady}
            >
              {submitButtonLabel}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
      {createdCodes && (
        <RedemptionsExportDialog
          data={createdCodes}
          onClose={() => setCreatedCodes(null)}
        />
      )}
    </>
  )
}
