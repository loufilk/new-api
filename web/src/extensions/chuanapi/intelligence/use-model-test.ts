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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { handleServerError } from '@/lib/handle-server-error'

import { cancelTest, estimateTest, startTest } from './api'
import { modelTestErrorKey, rejectedBeforeModelTestStart } from './errors'
import type { TestEstimate, TestRequest } from './types'

export function useModelTest() {
  const { t } = useTranslation()
  const report = (error: unknown) => {
    const key = modelTestErrorKey(error)
    handleServerError(error, undefined, key ? { title: t(key) } : undefined)
  }
  const client = useQueryClient()
  const [quote, setQuote] = useState<TestEstimate | null>(null)
  const [pending, setPending] = useState<TestRequest | null>(null)
  const [uncertain, setUncertain] = useState(false)
  const startLock = useRef(false)
  const hadUncertainAttempt = useRef(false)
  const estimate = useMutation({
    mutationFn: estimateTest,
    onSuccess: setQuote,
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
    if (!quote || startLock.current || quote.expires_at <= Date.now() / 1000) {
      return
    }
    startLock.current = true
    const request: TestRequest = {
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
    if (!pending || startLock.current) return
    startLock.current = true
    setUncertain(false)
    start.mutate(pending)
  }
  return {
    quote,
    setQuote,
    pending,
    uncertain,
    estimate,
    start,
    cancel,
    confirm,
    retry,
  }
}
