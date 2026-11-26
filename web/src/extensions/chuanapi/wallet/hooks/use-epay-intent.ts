// Store the consent tuple, never credentials or a signed checkout form.
import i18next from 'i18next'
import { useCallback, useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'
import { getAuthSessionEpoch } from '@/lib/auth-session'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorSources,
  getServerErrorStatus,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'
import { getEpayIntent, isApiSuccess, requestPayment } from '../api'
import { submitPaymentForm } from '../lib'

type Draft = {
  id: string
  amount: number
  method: string
  minor: number
  handedOff?: boolean
}
type Recovery = {
  owner: number
  draft: Draft
  status: 'unknown' | 'missing' | 'pending' | 'success' | 'failed' | 'expired'
  reference?: string
  tradeNo?: string
  terms?: { method: string; minor: number }
}
const key = (owner: number) => 'chuanapi:epay-intent:v1:' + owner
function stored(owner: number): Draft | null {
  const raw = sessionStorage.getItem(key(owner))
  if (!raw) return null
  const d = JSON.parse(raw)
  if (
    !d ||
    typeof d.id !== 'string' ||
    !/^[0-9a-f-]{36}\.[0-9a-f]{32}$/.test(d.id) ||
    !Number.isSafeInteger(d.amount) ||
    d.amount <= 0 ||
    !Number.isSafeInteger(d.minor) ||
    d.minor <= 0 ||
    typeof d.method !== 'string' ||
    !d.method ||
    d.method.length > 50
  )
    throw Error('Invalid stored checkout')
  return d
}
const current = (id: number, epoch: number) =>
  useAuthStore.getState().auth.user?.id === id &&
  getAuthSessionEpoch() === epoch
export function useEpayIntent(onRequote?: () => void) {
  const owner = useAuthStore((s) => s.auth.user?.id)
  const [recovery, setRecovery] = useState<Recovery | null>(null)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const busy = useRef(false)
  const handoffGeneration = useRef(0)
  const lookupSeq = useRef(0)
  const check = useCallback(async (draft: Draft, id: number) => {
    const epoch = getAuthSessionEpoch(),
      seq = ++lookupSeq.current
    const result: Recovery = { owner: id, draft, status: 'unknown' }
    try {
      const receipt = await getEpayIntent(draft.id)
      if (
        receipt.success === true &&
        Number.isSafeInteger(receipt.payment_minor) &&
        receipt.payment_minor > 0 &&
        typeof receipt.payment_method === 'string' &&
        !!receipt.payment_method &&
        typeof receipt.trade_no === 'string' &&
        /^EI[A-Z2-7]{26}$/.test(receipt.trade_no) &&
        ['pending', 'success', 'failed', 'expired'].includes(
          receipt.order_status
        )
      ) {
        result.status = receipt.order_status
        result.tradeNo = receipt.trade_no
        result.terms = {
          method: receipt.payment_method,
          minor: receipt.payment_minor,
        }
      }
    } catch (e) {
      const absence = getServerErrorSources(e).find(
        (v) => v.code === 'epay_intent_not_found'
      )
      if (getServerErrorStatus(e) === 404 && absence) {
        result.status = 'missing'
        if (
          typeof absence.reference === 'string' &&
          /^EI[A-Z2-7]{26}$/.test(absence.reference)
        )
          result.reference = absence.reference
      }
    }
    if (
      seq === lookupSeq.current &&
      current(id, epoch) &&
      stored(id)?.id === draft.id
    )
      setRecovery(result)
    return result
  }, [])
  useEffect(() => {
    setRecovery(null)
    if (!owner) return
    try {
      const d = stored(owner)
      if (d) {
        setRecovery({ owner, draft: d, status: 'unknown' })
        void check(d, owner).catch(() => {})
      }
    } catch {
      /* cannot replace unreadable receipts */
    }
    return () => {
      lookupSeq.current++
    }
  }, [owner, check])

  const pay = useCallback(
    async (amount: number, method: string, minor: number, gateway: string) => {
      if (
        !owner ||
        !/^[0-9a-f]{32}$/.test(gateway) ||
        !Number.isSafeInteger(minor) ||
        minor <= 0 ||
        busy.current
      )
        return false
      const generation = handoffGeneration.current
      busy.current = true
      const epoch = getAuthSessionEpoch()
      let d: Draft | null = null
      let firstSubmission = false
      try {
        d = stored(owner)
        firstSubmission = d === null
        // The form may already have reached the merchant. Do not automatically
        // post another form even if the browser or the provider tab disappeared.
        if (d && (d.handedOff || !d.id.endsWith('.' + gateway))) {
          await check(d, owner)
          if (mounted.current && current(owner, epoch))
            toast.info(
              i18next.t(
                'Check the previous EPay order before starting another.'
              )
            )
          return false
        }
        // A changed choice can replace a missing intent, but never its ID: an
        // earlier insertion may still be racing the lookup. The DB arbitrates.
        if (
          d &&
          (d.amount !== amount || d.method !== method || d.minor !== minor)
        ) {
          const receipt = await check(d, owner)
          if (!mounted.current || !current(owner, epoch)) return false
          if (receipt.status !== 'missing') {
            toast.error(
              i18next.t(
                'Check the previous EPay order before starting another.'
              )
            )
            return false
          }
        }
        d = {
          id: d?.id ?? crypto.randomUUID() + '.' + gateway,
          amount,
          method,
          minor,
        }
        sessionStorage.setItem(key(owner), JSON.stringify(d))
        if (stored(owner)?.id !== d.id)
          throw Error('Checkout storage unavailable')
        setRecovery({ owner, draft: d, status: 'unknown' })
        const reply = await requestPayment(
          {
            amount,
            payment_method: method,
            expected_minor: minor,
            intent_id: d.id,
          },
          epoch
        )
        if (
          !mounted.current ||
          !current(owner, epoch) ||
          stored(owner)?.id !== d.id
        )
          return false
        if (generation !== handoffGeneration.current) {
          // The operator dismissed confirmation while signing was in flight.
          // Only reconcile; a database receipt is not authority to open payment.
          await check(d, owner)
          return false
        }
        if (!isApiSuccess(reply)) {
          handleServerError(reply, i18next.t('Payment request failed'))
          await check(d, owner)
          return false
        }
        if (
          reply.order_status === 'pending' &&
          reply.url &&
          reply.data &&
          /^EI[A-Z2-7]{26}$/.test(reply.trade_no ?? '') &&
          reply.data.out_trade_no === reply.trade_no
        ) {
          d = { ...d, handedOff: true }
          // Persist before submitting: a page crash on either side of form.submit()
          // is an unknown handoff, never permission to dispatch a second form.
          sessionStorage.setItem(key(owner), JSON.stringify(d))
          submitPaymentForm(reply.url, reply.data)
          lookupSeq.current++
          setRecovery({
            owner,
            draft: d,
            status: 'pending',
            tradeNo: reply.trade_no,
            terms: { method: d.method, minor: d.minor },
          })
          toast.success(i18next.t('Redirecting to payment page...'))
          return true
        }
        await check(d, owner)
        return false
      } catch (e) {
        if (mounted.current && current(owner, epoch)) {
          const sources = getServerErrorSources(e)
          const codes = sources.map((source) => source.code)
          if (
            codes.includes('quote_changed') ||
            codes.includes('gateway_changed')
          ) {
            onRequote?.()
            toast.error(
              i18next.t(
                'The payment quote changed. Close this dialog and select the payment method again.'
              )
            )
            // A 404 lookup cannot prove absence of an in-flight insert. Only
            // release a fresh (single dispatch, never handed off) intent when
            // that exact invocation explicitly confirms it did not try to insert.
            const active = stored(owner)
            if (
              firstSubmission &&
              d &&
              active?.id === d.id &&
              !active.handedOff &&
              getServerErrorStatus(e) === 409 &&
              sources.some(
                (s) =>
                  (s.code === 'quote_changed' ||
                    s.code === 'gateway_changed') &&
                  s.intent_id === d!.id &&
                  s.insert_attempted === false
              )
            ) {
              sessionStorage.removeItem(key(owner))
              lookupSeq.current++
              setRecovery(null)
              return false
            }
          } else if (codes.includes('intent_conflict'))
            toast.error(
              i18next.t(
                'Check the previous EPay order before starting another.'
              )
            )
          else handleServerError(e, i18next.t('Payment request failed'))
          if (d) await check(d, owner).catch(() => {})
        }
        return false
      } finally {
        busy.current = false
      }
    },
    [owner, check, onRequote]
  )
  const visible = recovery?.owner === owner ? recovery : null
  const forget = () => {
    if (
      !visible?.tradeNo ||
      !owner ||
      busy.current ||
      stored(owner)?.id !== visible.draft.id
    )
      return
    sessionStorage.removeItem(key(owner))
    lookupSeq.current++
    setRecovery(null)
  }
  return {
    pay,
    invalidateEpayHandoff: () => {
      handoffGeneration.current++
    },
    recovery: visible,
    checkEpay: () => {
      if (visible) void check(visible.draft, visible.owner).catch(() => {})
    },
    forgetEpay: forget,
  }
}
