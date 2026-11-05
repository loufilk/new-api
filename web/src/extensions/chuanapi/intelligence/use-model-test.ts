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
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { handleServerError } from '@/lib/handle-server-error'

import { cancelTest, estimateTest, getTestHistory, startTest } from './api'
import {
  modelTestErrorCode,
  modelTestErrorKey,
  rejectedBeforeModelTestStart,
} from './errors'
import type {
  TestEstimate,
  TestHistory,
  TestRequest,
  TestSelection,
} from './types'

export function useModelTest() {
  const { t } = useTranslation()
  const report = (error: unknown) => {
    const code = modelTestErrorCode(error)
    if (
      code === 'model_tests_disabled' ||
      code === 'model_test_settings_unavailable'
    ) {
      // A rejected stale quote is not permission to keep showing paid actions.
      void client.invalidateQueries({ queryKey: ['model-tests', 'history'] })
      client.setQueryData(
        ['model-tests', 'history'],
        (previous: TestHistory | undefined) =>
          previous && {
            ...previous,
            enabled: false,
            availability_error:
              code === 'model_test_settings_unavailable' ? code : '',
          }
      )
    }
    const key = modelTestErrorKey(error)
    handleServerError(error, undefined, key ? { title: t(key) } : undefined)
  }
  const client = useQueryClient()
  const [quote, setQuote] = useState<TestEstimate | null>(null)
  const [pending, setPending] = useState<TestRequest | null>(null)
  const [uncertain, setUncertain] = useState(false)
  const startLock = useRef(false)
  const hadUncertainAttempt = useRef(false)
  const history = useQuery({
    queryKey: ['model-tests', 'history'],
    queryFn: ({ signal }) => getTestHistory(signal),
    retry: false,
    staleTime: 0,
    refetchOnMount: 'always',
    refetchInterval: (query) => {
      const saved = query.state.data?.items.find(
        (run) => run.id === pending?.request_id
      )
      const unresolved = !!pending && (!saved || saved.status === 'running')
      return unresolved ||
        query.state.data?.items.some((run) => run.status === 'running')
        ? 3_000
        : 30_000
    },
    meta: { errorToast: false },
  })
  // Navigation/status caches never authorize a new paid action. Even a cached
  // history response must first be refreshed on this mount.
  const enabled =
    history.isFetchedAfterMount &&
    !history.isError &&
    history.data?.enabled === true
  const availability = useRef({ enabled, generation: 0 })
  useEffect(() => {
    if (availability.current.enabled !== enabled) {
      availability.current = {
        enabled,
        generation: availability.current.generation + 1,
      }
    }
    if (!enabled) setQuote(null)
  }, [enabled])
  const estimate = useMutation({
    mutationFn: (selection: TestSelection) => {
      if (!availability.current.enabled) {
        return Promise.reject({ code: 'model_tests_disabled' })
      }
      return estimateTest(selection)
    },
    onMutate: () => ({ generation: availability.current.generation }),
    onSuccess: (value, _selection, context) => {
      if (
        availability.current.enabled &&
        context?.generation === availability.current.generation
      ) {
        setQuote(value)
      }
    },
    onError: report,
  })
  const start = useMutation({
    mutationFn: startTest,
    retry: false,
    onSuccess() {
      setPending(null)
      setUncertain(false)
    },
    onError(error) {
      if (!hadUncertainAttempt.current && rejectedBeforeModelTestStart(error)) {
        setPending(null)
        setUncertain(false)
      } else {
        hadUncertainAttempt.current = true
        setUncertain(true)
      }
      report(error)
    },
    onSettled() {
      startLock.current = false
      void client.invalidateQueries({ queryKey: ['model-tests', 'history'] })
    },
  })
  const cancel = useMutation({
    mutationFn: cancelTest,
    onError: report,
    onSettled() {
      void client.invalidateQueries({ queryKey: ['model-tests', 'history'] })
    },
  })
  const confirm = () => {
    if (
      !availability.current.enabled ||
      !quote ||
      startLock.current ||
      quote.expires_at <= Date.now() / 1000
    ) {
      return
    }
    startLock.current = true
    const request: TestRequest = {
      token_id: quote.token_id,
      model: quote.model,
      group: quote.group,
      max_output_tokens: quote.max_output_tokens,
      confirmation_token: quote.confirmation_token,
      confirmed: true,
      request_id: crypto.randomUUID(),
    }
    setQuote(null)
    hadUncertainAttempt.current = false
    setPending(request)
    setUncertain(false)
    start.mutate(request)
  }
  const retry = () => {
    if (!availability.current.enabled || !pending || startLock.current) return
    startLock.current = true
    setUncertain(false)
    start.mutate(pending)
  }
  return {
    history,
    enabled,
    quote: enabled ? quote : null,
    setQuote,
    dismissEstimate: () => {
      availability.current.generation += 1
      setQuote(null)
    },
    pending,
    uncertain,
    estimate,
    start,
    cancel,
    confirm,
    retry,
  }
}
