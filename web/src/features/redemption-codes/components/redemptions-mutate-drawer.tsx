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
import { type FormEvent, useEffect, useState } from 'react'
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
import { ErrorState } from '@/components/error-state'
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
import { OperationDialog } from '@/features/agent-platform/components/operation-dialog'
import type {
  OperationDraft,
  OperationPayload,
} from '@/features/agent-platform/types'
import { useAgentAccount } from '@/features/agent-redemption/hooks/use-agent-account'
import {
  canReserveQuota,
  getBatchQuota,
} from '@/features/agent-redemption/lib/quota'
import { isAgentWorkspaceUser } from '@/lib/agent-permissions'
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
import { useAuthStore } from '@/stores/auth-store'

import { getRedemption } from '../api'
import { REDEMPTION_STATUS, SUCCESS_MESSAGES } from '../constants'
import {
  getRedemptionFormSchema,
  type RedemptionFormValues,
  REDEMPTION_FORM_DEFAULT_VALUES,
  transformFormDataToPayload,
  transformRedemptionToFormDefaults,
} from '../lib'
import { isProtectedRedemption, redemptionOperation } from '../lib/transaction'
import type { Redemption } from '../types'
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
  const isAgent = useAuthStore((state) => isAgentWorkspaceUser(state.auth.user))
  const accountQuery = useAgentAccount(open)
  const role = useAuthStore((state) => state.auth.user?.role)
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const isUpdate = !!currentRow
  const redemptionId = currentRow?.id
  const { triggerRefresh } = useRedemptions()
  const isSubmitting = draft !== null
  const [createdCodes, setCreatedCodes] = useState<RedemptionExportData | null>(
    null
  )
  const [redemptionLoadState, setRedemptionLoadState] = useState<
    'idle' | 'loading' | 'ready' | 'error'
  >('idle')
  const [loadedRedemption, setLoadedRedemption] = useState<Redemption | null>(
    null
  )

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
      Number.isSafeInteger(loadedRedemption?.version) &&
      !isProtectedRedemption(loadedRedemption) &&
      loadedRedemption?.status !== REDEMPTION_STATUS.USED)
  const isLoadingRedemption = redemptionLoadState === 'loading'

  const onSubmit = (data: RedemptionFormValues) => {
    if (draft) return
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
      getBatchQuota(basePayload.quota, basePayload.count ?? 1) === null
    ) {
      form.setError('quota_dollars', { message: t('Invalid quota amount') })
      return
    }
    if (isAgent) {
      const existingReservation =
        isUpdate && loadedRedemption?.status === 1 ? loadedRedemption.quota : 0
      const available =
        (accountQuery.account?.available_quota ?? -1) + existingReservation
      if (
        !accountQuery.account ||
        !canReserveQuota(
          basePayload.quota,
          isUpdate ? 1 : (basePayload.count ?? 1),
          available
        )
      ) {
        form.setError('quota_dollars', {
          message: t('Insufficient available agent quota'),
        })
        return
      }
    }
    // Freeze the exact reviewed values; edits after cancellation require a new review.
    const payload: OperationPayload =
      isUpdate && loadedRedemption
        ? {
            id: loadedRedemption.id,
            version: Number(loadedRedemption.version),
            name: basePayload.name,
            quota: basePayload.quota,
            expired_time: basePayload.expired_time,
            status: 0,
            status_only: false,
          }
        : {
            name: basePayload.name,
            quota: basePayload.quota,
            expired_time: basePayload.expired_time,
            count: basePayload.count ?? 1,
          }
    setDraft(
      redemptionOperation(
        isUpdate ? 'update' : 'create',
        role,
        payload,
        '/api/redemption/',
        [
          {
            label: 'Name',
            before: loadedRedemption?.name,
            after: basePayload.name,
          },
          {
            label: 'Quota',
            before: loadedRedemption
              ? formatQuotaWithCurrency(loadedRedemption.quota, {
                  abbreviate: false,
                })
              : undefined,
            after: formatQuotaWithCurrency(basePayload.quota, {
              abbreviate: false,
            }),
          },
          {
            label: 'Quantity',
            after: String(isUpdate ? 1 : (basePayload.count ?? 1)),
          },
          {
            label: 'Expiration time',
            after: data.expired_time
              ? data.expired_time.toISOString()
              : t('Never expires'),
          },
        ]
      )
    )
  }

  const completeOperation = (result?: unknown) => {
    if (!draft) return
    if (
      draft.action.endsWith('.create') &&
      Array.isArray(result) &&
      result.every((code) => typeof code === 'string')
    ) {
      toast.success(
        result.length > 1
          ? t('Successfully created {{count}} redemption codes', {
              count: result.length,
            })
          : t(SUCCESS_MESSAGES.REDEMPTION_CREATED)
      )
      if (result.length) {
        setCreatedCodes({
          keys: result,
          name: String(draft.payload.name),
          quota: formatQuotaWithCurrency(Number(draft.payload.quota), {
            abbreviate: false,
          }),
        })
      }
    } else if (draft.action.endsWith('.update')) {
      toast.success(t(SUCCESS_MESSAGES.REDEMPTION_UPDATED))
    }
    setDraft(null)
    onOpenChange(false)
    triggerRefresh()
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
  const batchQuota = getBatchQuota(
    parseQuotaFromDollars(form.watch('quota_dollars')),
    isUpdate ? 1 : (form.watch('count') ?? 1)
  )
  const budgetLoading =
    isAgent &&
    (!accountQuery.account || accountQuery.isFetching || accountQuery.isError)
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
          if (!draft) onOpenChange(nextOpen)
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
          {loadedRedemption && isProtectedRedemption(loadedRedemption) && (
            <p role='status' className='text-muted-foreground px-6 text-sm'>
              {t('Customer rights protected')}
            </p>
          )}
          {loadedRedemption?.status === REDEMPTION_STATUS.USED && (
            <p role='status' className='text-muted-foreground px-6 text-sm'>
              {t('Used redemption codes cannot be changed or re-enabled')}
            </p>
          )}
          {isAgent && accountQuery.isError && (
            <ErrorState
              onRetry={() => {
                void accountQuery.refetch()
              }}
            />
          )}
          <Form {...form}>
            <form
              id='redemption-form'
              onSubmit={handleSubmit}
              className={sideDrawerFormClassName()}
              aria-busy={isLoadingRedemption}
            >
              <fieldset
                disabled={!isUpdateReady || isSubmitting || budgetLoading}
                className='contents'
              >
                <SideDrawerSection>
                  {isAgent && (
                    <div
                      className='bg-muted space-y-1 rounded-md p-3 text-sm'
                      role='status'
                    >
                      <p>
                        {t('Available quota')}:{' '}
                        {formatQuotaWithCurrency(
                          accountQuery.account?.available_quota ?? 0,
                          { abbreviate: false }
                        )}
                      </p>
                      <p>
                        {t('Batch reservation')}:{' '}
                        {batchQuota === null
                          ? '—'
                          : formatQuotaWithCurrency(batchQuota, {
                              abbreviate: false,
                            })}
                      </p>
                      <p className='text-muted-foreground'>
                        {t(
                          'The server rechecks the budget before creating codes.'
                        )}
                      </p>
                    </div>
                  )}
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
              disabled={isSubmitting || !isUpdateReady || budgetLoading}
            >
              {submitButtonLabel}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
      {draft && (
        <OperationDialog
          key={draft.request_id}
          draft={draft}
          onClose={() => setDraft(null)}
          onComplete={completeOperation}
        />
      )}
      {createdCodes && (
        <RedemptionsExportDialog
          data={createdCodes}
          onClose={() => setCreatedCodes(null)}
        />
      )}
    </>
  )
}
