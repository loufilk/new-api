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
import React, {
  useState,
  useCallback,
  useRef,
  useEffect,
  useSyncExternalStore,
} from 'react'
import { useTranslation } from 'react-i18next'

import useDialogState from '@/hooks/use-dialog'
import { getAuthSessionEpoch } from '@/lib/auth-session'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { fetchTokenKey, fetchTokenKeysBatch } from '../api'
import { ERROR_MESSAGES } from '../constants'
import type { MarketKeyDraft } from '../lib/market-key-draft'
import type { ApiKey, ApiKeysDialogType } from '../types'

type ApiKeysContextType = {
  open: ApiKeysDialogType | null
  setOpen: (str: ApiKeysDialogType | null) => void
  createDraft: MarketKeyDraft | undefined
  diagnostic?: {
    tokenId?: number
    trigger: React.RefObject<HTMLButtonElement | null>
  }
  openDiagnostic: (
    trigger: React.RefObject<HTMLButtonElement | null>,
    tokenId?: number
  ) => void
  openMarketCreate: (draft?: MarketKeyDraft) => void
  currentRow: ApiKey | null
  setCurrentRow: React.Dispatch<React.SetStateAction<ApiKey | null>>
  refreshTrigger: number
  triggerRefresh: () => void
  resolvedKey: string
  setResolvedKey: React.Dispatch<React.SetStateAction<string>>
  resolveRealKey: (id: number) => Promise<string | null>
  resolveTransientKey: (
    id: number,
    signal: AbortSignal
  ) => Promise<string | null>
  resolveRealKeysBatch: (ids: number[]) => Promise<Record<number, string>>
  resolvedKeys: Record<number, string>
  loadingKeys: Record<number, boolean>
  copiedKeyId: number | null
  markKeyCopied: (id: number) => void
}

const ApiKeysContext = React.createContext<ApiKeysContextType | null>(null)

export function ApiKeysProvider({ children }: { children: React.ReactNode }) {
  const ownerEpoch = useSyncExternalStore(
    useAuthStore.subscribe,
    getAuthSessionEpoch
  )
  return (
    <SessionApiKeysProvider key={ownerEpoch} ownerEpoch={ownerEpoch}>
      {children}
    </SessionApiKeysProvider>
  )
}

function SessionApiKeysProvider({
  children,
  ownerEpoch,
}: {
  children: React.ReactNode
  ownerEpoch: number
}) {
  const { t } = useTranslation()
  const [dialog, setDialog] = useDialogState<{
    type: ApiKeysDialogType
    draft?: MarketKeyDraft
    diagnostic?: ApiKeysContextType['diagnostic']
  }>(null)
  const open = dialog?.type ?? null
  const createDraft = open === 'create' ? dialog?.draft : undefined
  const setOpen = useCallback(
    (type: ApiKeysDialogType | null) => setDialog(type ? { type } : null),
    [setDialog]
  )
  const openDiagnostic: ApiKeysContextType['openDiagnostic'] = useCallback(
    (trigger, tokenId) =>
      setDialog({ type: 'diagnostic', diagnostic: { trigger, tokenId } }),
    [setDialog]
  )
  const openMarketCreate = useCallback(
    (draft?: MarketKeyDraft) => {
      setDialog((previous) => {
        // A repeated route effect must not discard an already consumed private draft.
        if (!draft && previous?.type === 'create') return previous
        return { type: 'create', draft }
      })
    },
    [setDialog]
  )
  const [currentRow, setCurrentRow] = useState<ApiKey | null>(null)
  const [refreshTrigger, setRefreshTrigger] = useState(0)
  const [resolvedKey, setResolvedKey] = useState('')

  const [resolvedKeys, setResolvedKeys] = useState<Record<number, string>>({})
  const [loadingKeys, setLoadingKeys] = useState<Record<number, boolean>>({})
  const pendingRequests = useRef<Record<number, Promise<string | null>>>({})

  const [copiedKeyId, setCopiedKeyId] = useState<number | null>(null)
  const copiedTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined)
  const mountedRef = useRef(true)
  const requestControllers = useRef(new Set<AbortController>())

  const isCurrentOwner = useCallback(
    () => mountedRef.current && getAuthSessionEpoch() === ownerEpoch,
    [ownerEpoch]
  )

  useEffect(() => {
    mountedRef.current = true
    const controllers = requestControllers.current
    return () => {
      mountedRef.current = false
      clearTimeout(copiedTimerRef.current)
      for (const controller of controllers) controller.abort()
      controllers.clear()
    }
  }, [])

  const markKeyCopied = useCallback((id: number) => {
    setCopiedKeyId(id)
    clearTimeout(copiedTimerRef.current)
    copiedTimerRef.current = setTimeout(() => setCopiedKeyId(null), 2000)
  }, [])

  const triggerRefresh = useCallback(() => {
    setRefreshTrigger((prev) => prev + 1)
  }, [])

  const resolveRealKey = useCallback(
    async (id: number): Promise<string | null> => {
      if (!isCurrentOwner()) return null
      if (resolvedKeys[id]) return resolvedKeys[id]
      if (id in pendingRequests.current) return pendingRequests.current[id]

      const controller = new AbortController()
      requestControllers.current.add(controller)
      const request = (async () => {
        setLoadingKeys((prev) => ({ ...prev, [id]: true }))
        try {
          const res = await fetchTokenKey(id, { signal: controller.signal })
          if (!isCurrentOwner() || controller.signal.aborted) return null
          if (res.success && res.data?.key) {
            const fullKey = `sk-${res.data.key}`
            setResolvedKeys((prev) => ({ ...prev, [id]: fullKey }))
            return fullKey
          }
          handleServerError(res, t(ERROR_MESSAGES.UNEXPECTED))
          return null
        } catch (error) {
          if (isCurrentOwner() && !controller.signal.aborted) {
            handleServerError(error, t(ERROR_MESSAGES.UNEXPECTED))
          }
          return null
        } finally {
          requestControllers.current.delete(controller)
          delete pendingRequests.current[id]
          if (isCurrentOwner()) {
            setLoadingKeys((prev) => {
              const next = { ...prev }
              delete next[id]
              return next
            })
          }
        }
      })()

      pendingRequests.current[id] = request
      return request
    },
    [resolvedKeys, t, isCurrentOwner]
  )

  // Use the existing owner-scoped endpoint and epoch checks, but never fill
  // the ordinary session cache or report a response body for local import.
  const resolveTransientKey = useCallback(
    async (id: number, signal: AbortSignal): Promise<string | null> => {
      if (!isCurrentOwner() || signal.aborted) return null
      try {
        const res = await fetchTokenKey(id, { signal })
        if (!isCurrentOwner() || signal.aborted) return null
        const raw = res.success ? res.data?.key : null
        return typeof raw === 'string' && raw.length > 0 ? `sk-${raw}` : null
      } catch {
        return null
      }
    },
    [isCurrentOwner]
  )

  const resolveRealKeysBatch = useCallback(
    async (ids: number[]): Promise<Record<number, string>> => {
      if (!isCurrentOwner()) return {}
      const uncachedIds = ids.filter((id) => !resolvedKeys[id])
      if (uncachedIds.length === 0) {
        const result: Record<number, string> = {}
        for (const id of ids) result[id] = resolvedKeys[id]
        return result
      }

      for (const id of uncachedIds) {
        setLoadingKeys((prev) => ({ ...prev, [id]: true }))
      }

      const controller = new AbortController()
      requestControllers.current.add(controller)
      try {
        const res = await fetchTokenKeysBatch(uncachedIds, {
          signal: controller.signal,
        })
        if (!isCurrentOwner() || controller.signal.aborted) return {}
        if (res.success && res.data?.keys) {
          const newKeys: Record<number, string> = {}
          for (const [idStr, key] of Object.entries(res.data.keys)) {
            newKeys[Number(idStr)] = `sk-${key}`
          }
          setResolvedKeys((prev) => ({ ...prev, ...newKeys }))

          const result: Record<number, string> = { ...newKeys }
          for (const id of ids) {
            if (resolvedKeys[id]) result[id] = resolvedKeys[id]
          }
          return result
        }
        handleServerError(res, t(ERROR_MESSAGES.UNEXPECTED))
        return {}
      } catch (error) {
        if (isCurrentOwner() && !controller.signal.aborted) {
          handleServerError(error, t(ERROR_MESSAGES.UNEXPECTED))
        }
        return {}
      } finally {
        requestControllers.current.delete(controller)
        if (isCurrentOwner()) {
          setLoadingKeys((prev) => {
            const next = { ...prev }
            for (const id of uncachedIds) delete next[id]
            return next
          })
        }
      }
    },
    [resolvedKeys, t, isCurrentOwner]
  )

  return (
    <ApiKeysContext
      value={{
        open,
        setOpen,
        createDraft,
        openMarketCreate,
        diagnostic: dialog?.diagnostic,
        openDiagnostic,
        currentRow,
        setCurrentRow,
        refreshTrigger,
        triggerRefresh,
        resolvedKey,
        setResolvedKey,
        resolveRealKey,
        resolveTransientKey,
        resolveRealKeysBatch,
        resolvedKeys,
        loadingKeys,
        copiedKeyId,
        markKeyCopied,
      }}
    >
      {children}
    </ApiKeysContext>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export const useApiKeys = () => {
  const apiKeysContext = React.useContext(ApiKeysContext)

  if (!apiKeysContext) {
    throw new Error('useApiKeys has to be used within <ApiKeysContext>')
  }

  return apiKeysContext
}
