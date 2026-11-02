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
import { useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { ROLE } from '@/lib/roles'
import { statusQueryOptions } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsSwitchField } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

export function ModelTestSection() {
  const { t } = useTranslation()
  const isRoot = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  // The periodic options map can lag on another node. Read the authoritative
  // bool from status before presenting an editable value, with no cache fallback.
  const query = useQuery({
    ...statusQueryOptions,
    enabled: isRoot,
    staleTime: 0,
    placeholderData: undefined,
    refetchOnMount: 'always',
    retry: false,
    meta: { errorToast: false },
  })
  const settingsAvailable =
    query.data?.model_test_settings_available === true &&
    typeof query.data?.model_test_enabled === 'boolean'
  const savedEnabled = query.data?.model_test_enabled === true
  const [enabled, setEnabled] = useState(false)
  const updateOption = useUpdateOption()
  useEffect(() => {
    if (query.isFetchedAfterMount && settingsAvailable) setEnabled(savedEnabled)
  }, [query.isFetchedAfterMount, savedEnabled, settingsAvailable])
  if (!isRoot) return null
  if (query.isError || (query.isFetchedAfterMount && !settingsAvailable)) {
    return (
      <ErrorState
        title={t('Failed to load settings')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  if (!query.isFetchedAfterMount) return <LoadingState />

  return (
    <SettingsSection title={t('intelligence.title')}>
      <SettingsSwitchField
        controlId='model-test-enabled'
        label={t('Enable model performance tests')}
        description={t(
          'Disabled by default. Turning this off hides the test entry and blocks new tests, including confirmed requests not yet admitted for dispatch. Normal model calls, account pool and referral rewards are unaffected.'
        )}
        checked={enabled}
        onCheckedChange={setEnabled}
        disabled={updateOption.isPending}
      />
      <p className='text-muted-foreground text-sm'>
        {t(
          'Saved changes take effect without a restart. Your test history and cancellation remain available. Requests already admitted for dispatch may still be sent, complete and incur charges.'
        )}
      </p>
      <SettingsPageFormActions
        onSave={() =>
          updateOption.mutate({ key: 'ModelTestEnabled', value: enabled })
        }
        isSaving={updateOption.isPending}
        isSaveDisabled={enabled === savedEnabled}
      />
    </SettingsSection>
  )
}
