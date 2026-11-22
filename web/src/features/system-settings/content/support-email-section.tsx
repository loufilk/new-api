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
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  getSupportEmailSettings,
  saveSupportEmailSettings,
  type SupportEmailSettings,
  type SupportEmailSettingsInput,
} from '@/features/support/email/api'
import { SupportEmailOutbox } from '@/features/support/email/outbox'
import {
  supportEmailFormSchema,
  supportEmailRecipients,
  type SupportEmailFormValues,
} from '@/features/support/email/schema'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  SettingsForm,
  SettingsSwitchField,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'

export function SupportEmailSection() {
  const user = useAuthStore((state) => state.auth.user)
  if (user?.role !== ROLE.SUPER_ADMIN) return null
  return <RootSupportEmailSettings key={user.id} userId={user.id} />
}

function RootSupportEmailSettings(props: { userId: number }) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['support-email-settings', props.userId],
    queryFn: ({ signal }) => getSupportEmailSettings(signal),
    staleTime: 0,
    refetchOnMount: 'always',
    retry: false,
    meta: { errorToast: false },
  })
  if (query.isError) {
    return (
      <ErrorState
        title={t('Failed to load settings')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  if (!query.isFetchedAfterMount || !query.data) return <LoadingState />
  return (
    <SupportEmailSettingsForm userId={props.userId} settings={query.data} />
  )
}

function SupportEmailSettingsForm(props: {
  userId: number
  settings: SupportEmailSettings
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const initial = {
    enabled: props.settings.enabled,
    recipients: props.settings.admin_recipients.join('; '),
    siteOrigin: props.settings.site_origin,
  }
  const form = useForm<SupportEmailFormValues>({
    resolver: zodResolver(supportEmailFormSchema),
    defaultValues: initial,
  })
  const mutation = useMutation({
    mutationFn: async (input: SupportEmailSettingsInput) => {
      await saveSupportEmailSettings(input)
      return getSupportEmailSettings()
    },
    onError: (error) => handleServerError(error),
    onSuccess: async (saved) => {
      client.setQueryData(['support-email-settings', props.userId], saved)
      await Promise.all([
        client.invalidateQueries({ queryKey: ['support-email-preferences'] }),
        client.invalidateQueries({ queryKey: ['support-email-outbox'] }),
      ])
      toast.success(t('Setting updated successfully'))
    },
  })
  const pending = form.formState.isSubmitting || mutation.isPending
  useResetForm(form, pending ? undefined : initial)
  const submit = form.handleSubmit(async (values) => {
    try {
      const saved = await mutation.mutateAsync({
        enabled: values.enabled,
        admin_recipients: supportEmailRecipients(values.recipients),
        site_origin: values.siteOrigin,
      })
      form.reset({
        enabled: saved.enabled,
        recipients: saved.admin_recipients.join('; '),
        siteOrigin: saved.site_origin,
      })
    } catch {
      // The mutation owns error presentation; keep the unsaved draft.
    }
  })

  return (
    <SettingsSection title={t('Support email notifications')}>
      <Alert>
        <AlertDescription>
          {t(
            'Emails contain only the ticket number, event and a sign-in link. No ticket subject, message, key or attachment is included. Email replies are not imported.'
          )}
        </AlertDescription>
      </Alert>
      <Form {...form}>
        <SettingsForm onSubmit={submit}>
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <SettingsSwitchField
                controlId='support-email-enabled'
                label={t('Enable support email notifications')}
                description={t(
                  'Disabled by default. New tickets and customer replies notify the configured administrators; public administrator replies notify the verified ticket owner.'
                )}
                checked={field.value}
                onCheckedChange={field.onChange}
                disabled={pending}
              />
            )}
          />
          <FormField
            control={form.control}
            name='recipients'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('Administrator notification addresses')}
                </FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    placeholder='support@example.com; admin@example.com'
                    disabled={pending}
                    autoComplete='off'
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='siteOrigin'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Trusted site origin')}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    placeholder='https://api.example.com'
                    disabled={pending}
                    autoComplete='off'
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <p className='text-muted-foreground text-sm'>
            {t(
              'Up to five addresses, separated by commas or semicolons. Each recipient receives a separate email.'
            )}
          </p>
          <p role='status' className='text-sm'>
            {props.settings.smtp_configured
              ? t('SMTP is configured')
              : t(
                  'Configure SMTP in email settings before enabling notifications.'
                )}
          </p>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Disabling notifications cancels queued mail. Changing recipients or the site origin cancels old queued mail instead of redirecting it. Already dispatched mail cannot be recalled.'
            )}
          </p>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Delivery is rate limited and retried with a limit. Pending notifications expire after seven days. Security emails are unaffected.'
            )}
          </p>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Mail already approved for dispatch may still be delivered and cannot be recalled.'
            )}
          </p>
          <SettingsPageFormActions
            onSave={() => void submit()}
            isSaving={pending}
            isSaveDisabled={!form.formState.isDirty}
            onReset={() => form.reset(initial)}
            isResetDisabled={!form.formState.isDirty}
          />
        </SettingsForm>
      </Form>
      <SupportEmailOutbox userId={props.userId} />
    </SettingsSection>
  )
}
