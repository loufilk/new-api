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
import { getRouteApi } from '@tanstack/react-router'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import { SectionPageLayout } from '@/components/layout'

import { ApiKeysDialogs } from './components/api-keys-dialogs'
import {
  ApiKeysAddressBar,
  ApiKeysPrimaryButtons,
} from './components/api-keys-primary-buttons'
import { ApiKeysProvider, useApiKeys } from './components/api-keys-provider'
import { ApiKeysTable } from './components/api-keys-table'
import { useApiKeyAddresses } from './hooks/use-api-key-addresses'
import { consumePrivateMarketKeyDraft } from './lib/market-key-draft'

const route = getRouteApi('/_authenticated/keys/')

export function ApiKeys() {
  return (
    <ApiKeysProvider>
      <ApiKeysPage />
    </ApiKeysProvider>
  )
}

function ApiKeysPage() {
  const { t } = useTranslation()
  const apiAddresses = useApiKeyAddresses()
  const navigate = route.useNavigate()
  const { create, group, model, publicLine } = route.useSearch()
  const { createDraft, openMarketCreate } = useApiKeys()

  useEffect(() => {
    if (!create) return
    const controller = new AbortController()
    const clearIntent = () =>
      void navigate({
        replace: true,
        search: (previous) => ({
          ...previous,
          create: undefined,
          group: undefined,
          model: undefined,
          publicLine: undefined,
        }),
      })
    if (publicLine) {
      // Resolve the opaque line after login with current permissions. No Key
      // is issued by this lookup or when the dialog opens.
      void api.get('/api/user/public-model-line', {
        params: { id: publicLine, model },
        signal: controller.signal,
        disableDuplicate: true,
      })
        .then((result) => {
          const draft = requireServerSuccess(result.data).data
          if (!controller.signal.aborted && draft?.group && draft.model === model)
            openMarketCreate({ group: draft.group, model: draft.model })
        })
        .catch(() => {
          if (!controller.signal.aborted) toast.error(t('Public model line not available'))
        })
        .finally(() => {
          if (!controller.signal.aborted) clearIntent()
        })
      return () => controller.abort()
    }
    const privateDraft = consumePrivateMarketKeyDraft()
    openMarketCreate(group || model ? { group, model } : privateDraft)
    // One-time intent: never re-open if the user cancels or clicks Back.
    clearIntent()
  }, [create, group, model, publicLine, navigate, openMarketCreate, t])

  return (
    <>
      <SectionPageLayout fixedContent stackActionsOnMobile>
        <SectionPageLayout.Title>{t('API Keys')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <ApiKeysPrimaryButtons {...apiAddresses} />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='flex h-full min-h-0 flex-col gap-2'>
            <ApiKeysAddressBar {...apiAddresses} />
            <div className='min-h-0 flex-1'>
              <ApiKeysTable />
            </div>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      <ApiKeysDialogs draft={createDraft} />
    </>
  )
}
