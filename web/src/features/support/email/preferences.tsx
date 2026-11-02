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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { SettingsSwitchField } from '@/features/system-settings/components/settings-form-layout'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { getSupportEmailPreferences, saveSupportEmailPreferences } from './api'

export function SupportEmailPreferences() {
  const userId = useAuthStore((state) => state.auth.user?.id)
  if (!userId) return null
  return <UserSupportEmailPreferences key={userId} userId={userId} />
}

function UserSupportEmailPreferences(props: { userId: number }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const queryKey = ['support-email-preferences', props.userId] as const
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => getSupportEmailPreferences(signal),
    staleTime: 0,
    refetchOnMount: 'always',
    retry: false,
    meta: { errorToast: false, redirectOnInternalServerError: false },
  })
  const mutation = useMutation({
    mutationFn: async (enabled: boolean) => {
      await saveSupportEmailPreferences(enabled)
      return getSupportEmailPreferences()
    },
    onSuccess: (saved) => {
      client.setQueryData(queryKey, saved)
      toast.success(t('Setting updated successfully'))
    },
    onError: (error) => handleServerError(error),
  })
  let content
  if (query.isError) {
    content = (
      <ErrorState
        title={t('Failed to load notification preferences')}
        onRetry={() => void query.refetch()}
      />
    )
  } else if (!query.isFetchedAfterMount || !query.data) {
    content = <LoadingState />
  } else {
    const data = query.data
    content = (
      <>
        <SettingsSwitchField
          controlId='support-email-preference'
          checked={data.enabled}
          onCheckedChange={(enabled) => mutation.mutate(enabled)}
          disabled={mutation.isPending || query.isFetching}
          label={t('Email me when support replies')}
          description={t(
            'This preference affects support notifications only, not verification or security emails.'
          )}
        />
        <p className='text-sm break-all'>{data.email || t('No email bound')}</p>
        <p className='text-muted-foreground text-sm'>
          {data.email_verified && data.email
            ? t('Verified notification email')
            : t(
                'Bind or verify your email in Account security before receiving support notifications. For an existing address, choose Change and verify the same address.'
              )}
        </p>
        {(!data.email_verified || !data.email) && (
          <Button
            variant='outline'
            render={<Link to='/security' />}
            role='link'
          >
            {t('Account security')}
          </Button>
        )}
        {!data.system_enabled && (
          <p role='status' className='text-muted-foreground text-sm'>
            {t(
              'Support email notifications are currently disabled by the site administrator.'
            )}
          </p>
        )}
      </>
    )
  }
  return (
    <Card size='sm'>
      <CardHeader>
        <CardTitle>{t('Support email notifications')}</CardTitle>
      </CardHeader>
      <CardContent className='flex min-w-0 flex-col items-start gap-2 [&>div]:w-full'>
        {content}
      </CardContent>
    </Card>
  )
}
