import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { getCurrencyLabel } from '@/lib/currency'
import { getEditableQuotaStep, parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getInvitationPolicy, updateInvitationPolicy } from '../api'
import { invitationPolicySchema, type InvitationPolicyForm } from '../lib/forms'

export function InvitationPolicySettings() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const queryClient = useQueryClient()
  const submitting = useRef(false)
  const form = useForm<InvitationPolicyForm>({ resolver: zodResolver(invitationPolicySchema(t)), defaultValues: { enabled: false, amount: 0 } })
  const query = useQuery({ queryKey: ['agent-redemption', user?.id, user?.role, 'invitation-policy'], queryFn: ({ signal }) => getInvitationPolicy(signal), enabled: user?.role === ROLE.SUPER_ADMIN })
  useEffect(() => { if (query.data) form.reset({ enabled: query.data.enabled, amount: quotaUnitsToDollars(query.data.quota) }) }, [query.data, form])
  const mutation = useMutation({ mutationFn: updateInvitationPolicy, retry: false,
    onSuccess: () => { toast.success(t('Settings saved successfully')); void queryClient.invalidateQueries({ queryKey: ['agent-redemption'] }) },
    onSettled: () => { submitting.current = false },
  })
  if (user?.role !== ROLE.SUPER_ADMIN) return null
  if (query.isLoading) return <LoadingState />
  if (query.isError) return <ErrorState onRetry={() => { void query.refetch() }} />
  return <Form {...form}><form className='max-w-xl space-y-5' onSubmit={form.handleSubmit((data) => {
    if (submitting.current || !query.data) return
    submitting.current = true
    const quota = form.getFieldState('amount').isDirty ? parseQuotaFromDollars(data.amount) : query.data.quota
    mutation.mutate({ enabled: data.enabled, quota })
  })}>
    <p className='text-muted-foreground text-sm'>{t('Only the super administrator can configure registration test grants. Disabled by default; changes apply only to new invitations.')}</p>
    <FormField control={form.control} name='enabled' render={({ field }) => <FormItem className='flex items-center justify-between gap-4'><FormLabel>{t('Enable invitation test quota')}</FormLabel><FormControl><Switch checked={field.value} onCheckedChange={field.onChange} disabled={mutation.isPending} /></FormControl><FormMessage /></FormItem>} />
    <FormField control={form.control} name='amount' render={({ field }) => <FormItem><FormLabel>{t('Test quota ({{currency}})', { currency: getCurrencyLabel() })}</FormLabel><FormControl><Input {...field} type='number' min={0} step={getEditableQuotaStep()} disabled={mutation.isPending} onChange={(event) => field.onChange(event.target.value === '' ? NaN : Number(event.target.value))} /></FormControl><FormMessage /></FormItem>} />
    <Button type='submit' disabled={mutation.isPending || !query.data}>{mutation.isPending ? t('Saving...') : t('Save changes')}</Button>
  </form></Form>
}
