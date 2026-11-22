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
import { skipToken, useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'

import { loadImageTokenModels, loadImagePageBootstrap } from '../api'
import { getAvailableImageModels } from '../lib/model-filter'

export function useImageData() {
  const [apiKeyId, setApiKeyId] = useState<number | null>(null)
  const [model, setModel] = useState('')

  const bootstrap = useQuery({
    queryKey: ['image-generation-bootstrap'],
    queryFn: loadImagePageBootstrap,
  })

  const keys = useMemo(() => bootstrap.data?.keys ?? [], [bootstrap.data?.keys])
  const pricingModels = useMemo(
    () => bootstrap.data?.pricingModels ?? [],
    [bootstrap.data?.pricingModels]
  )

  const selectedKey = useMemo(
    () => keys.find((key) => key.id === apiKeyId) ?? null,
    [keys, apiKeyId]
  )
  const group = selectedKey?.group ?? ''

  const tokenModelsQuery = useQuery({
    queryKey: [
      'image-generation-token-models',
      selectedKey?.id ?? null,
      group,
      selectedKey?.auto_groups ?? null,
      selectedKey?.model_limits_enabled ?? false,
      selectedKey?.model_limits ?? '',
    ],
    queryFn: selectedKey
      ? () => loadImageTokenModels(selectedKey.id)
      : skipToken,
    staleTime: 0,
  })

  const tokenModels = useMemo(
    () => (tokenModelsQuery.isError ? [] : (tokenModelsQuery.data ?? [])),
    [tokenModelsQuery.data, tokenModelsQuery.isError]
  )

  const availableModels = useMemo(
    () =>
      getAvailableImageModels({
        pricingModels,
        tokenModels,
        token: selectedKey,
      }),
    [pricingModels, tokenModels, selectedKey]
  )

  const effectiveApiKeyId = apiKeyId ?? selectedKey?.id ?? null

  useEffect(() => {
    if (keys.length === 0) return
    if (apiKeyId === null || !keys.some((key) => key.id === apiKeyId)) {
      setApiKeyId(keys[0].id)
    }
  }, [keys, apiKeyId])

  useEffect(() => {
    if (!availableModels.includes(model)) {
      setModel(availableModels.length > 0 ? availableModels[0] : '')
    }
  }, [availableModels, model])

  return {
    keys,
    pricingModels,
    tokenModels,
    availableModels,
    selectedKey,
    apiKeyId: effectiveApiKeyId,
    setApiKeyId,
    model,
    setModel,
    group,
    isLoadingBootstrap: bootstrap.isLoading,
    isBootstrapError: bootstrap.isError,
    bootstrapError: bootstrap.error,
    refetchBootstrap: bootstrap.refetch,
    isLoadingTokenModels: tokenModelsQuery.isLoading,
    isTokenModelsError: tokenModelsQuery.isError,
    refetchTokenModels: tokenModelsQuery.refetch,
  }
}
