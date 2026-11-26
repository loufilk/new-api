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
import type { QueryClient } from '@tanstack/react-query'
import i18next from 'i18next'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'

import { updateModelStatus } from '../api'
import { invalidateVendorData } from '../vendor-api'

// ============================================================================
// Model Status Actions
// ============================================================================

/**
 * Enable a model
 */
export async function handleEnableModel(
  id: number,
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  try {
    const response = await updateModelStatus(id, 1)
    if (response.success) {
      toast.success(i18next.t('Model shown in model square'))
      if (queryClient) await invalidateVendorData(queryClient)
      onSuccess?.()
    } else {
      handleServerError(
        response,
        i18next.t('Failed to show model in model square')
      )
    }
  } catch (error: unknown) {
    handleServerError(error, i18next.t('Failed to show model in model square'))
  }
}

/**
 * Disable a model
 */
export async function handleDisableModel(
  id: number,
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  try {
    const response = await updateModelStatus(id, 0)
    if (response.success) {
      toast.success(i18next.t('Model hidden from model square'))
      if (queryClient) await invalidateVendorData(queryClient)
      onSuccess?.()
    } else {
      handleServerError(
        response,
        i18next.t('Failed to hide model from model square')
      )
    }
  } catch (error: unknown) {
    handleServerError(
      error,
      i18next.t('Failed to hide model from model square')
    )
  }
}

/**
 * Toggle model status
 */
export async function handleToggleModelStatus(
  id: number,
  currentStatus: number,
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  if (currentStatus === 1) {
    await handleDisableModel(id, queryClient, onSuccess)
  } else {
    await handleEnableModel(id, queryClient, onSuccess)
  }
}

// ============================================================================
// Batch Status Actions
// ============================================================================

async function updateBatchVisibility(
  ids: number[],
  status: 0 | 1,
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  if (ids.length === 0) {
    toast.error(i18next.t('Please select at least one model'))
    return
  }

  try {
    // Each write commits independently. A lost HTTP response does not mean
    // that the server rolled back that write or canceled its siblings.
    const results = await Promise.allSettled(
      ids.map((id) => updateModelStatus(id, status))
    )
    const confirmed = results.filter(
      (result) => result.status === 'fulfilled' && result.value.success
    ).length
    const rejected = results.filter(
      (result) => result.status === 'rejected'
    ).length
    const failed = results.length - confirmed - rejected
    if (confirmed > 0) {
      toast.success(
        status === 1
          ? i18next.t('Shown {{count}} models in model square', {
              count: confirmed,
            })
          : i18next.t('Hidden {{count}} models from model square', {
              count: confirmed,
            })
      )
    }
    if (failed > 0) {
      toast.error(
        status === 1
          ? i18next.t('Failed to enable {{count}} model(s)', { count: failed })
          : i18next.t('Failed to disable {{count}} model(s)', { count: failed })
      )
    }
    if (rejected > 0) {
      toast.error(i18next.t('Refresh the list and try again.'))
    }
    if (queryClient && (confirmed > 0 || rejected > 0)) {
      await invalidateVendorData(queryClient)
    }
    if (confirmed === results.length) onSuccess?.()
  } catch (error: unknown) {
    handleServerError(
      error,
      status === 1
        ? i18next.t('Batch enable failed')
        : i18next.t('Batch disable failed')
    )
  }
}

export function handleBatchEnableModels(
  ids: number[],
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  return updateBatchVisibility(ids, 1, queryClient, onSuccess)
}

export function handleBatchDisableModels(
  ids: number[],
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  return updateBatchVisibility(ids, 0, queryClient, onSuccess)
}
