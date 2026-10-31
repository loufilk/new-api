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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeAll, beforeEach, describe, expect, test, vi } from 'vitest'

import type {
  OperationDraft,
  PreparedOperation,
} from '@/features/agent-platform/types'
import en from '@/i18n/locales/en.json'
import { api, getSelf } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  calculateAmount,
  getAffiliateCode,
  getReferralRewards,
  getTopupInfo,
  getWalletSummary,
  redeemTopupCode,
  requestPayment,
} from '../api'
import { Wallet } from '../index'
import type {
  ReferralCommissionPage,
  TopupInfo,
  UserWalletData,
  WalletSummary,
} from '../types'

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({
    status: { price: 7.3 },
    loading: false,
    error: null,
  }),
}))

vi.mock('@/hooks/use-system-config', () => ({
  useSystemConfig: () => ({
    currency: { quotaDisplayType: 'USD', usdExchangeRate: 1 },
    loading: false,
    logoLoaded: true,
  }),
}))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  getSelf: vi.fn(),
}))

vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  getTopupInfo: vi.fn(),
  calculateAmount: vi.fn(),
  requestPayment: vi.fn(),
  redeemTopupCode: vi.fn(),
  getWalletSummary: vi.fn(),
  getAffiliateCode: vi.fn(),
  getReferralRewards: vi.fn(),
}))

vi.mock('../components/ledger-table', () => ({
  LedgerTable: () => null,
}))

const walletUser: UserWalletData = {
  id: 7,
  username: 'wallet-user',
  quota: 1_000_000,
  used_quota: 250_000,
  request_count: 42,
  aff_count: 2,
  aff_quota: 1_000_000,
  aff_history_quota: 3_000_000,
  group: 'default',
}

const walletSummary: WalletSummary = {
  total: 1_200_000,
  paid: 800_000,
  bonus: 200_000,
  reward: 200_000,
  rules: {
    signup_bonus_enabled: false,
    signup_bonus_quota: 0,
    referral_bonus_enabled: true,
    referral_bonus_rate: 0.05,
  },
}

const referralRewards: ReferralCommissionPage = {
  items: [
    {
      id: 1,
      source_user_id: 9,
      source_name: 'a***',
      paid_quota: 1_000_000,
      reward_quota: 50_000,
      rate: 0.05,
      created_at: 1735689600,
    },
  ],
  total: 1,
}

const onlineTopupInfo: TopupInfo = {
  enable_online_topup: true,
  enable_stripe_topup: false,
  enable_redemption: true,
  pay_methods: [
    { name: 'Epay', type: 'epay', min_topup: 10 },
    { name: 'Alipay', type: 'alipay', min_topup: 10 },
  ],
  min_topup: 10,
  stripe_min_topup: 5,
  amount_options: [10, 50, 100],
  discount: {},
}

const testI18n = createInstance()

beforeAll(async () => {
  await testI18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: en.translation } },
    interpolation: { escapeValue: false },
  })
})

function renderWallet() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <I18nextProvider i18n={testI18n}>
      <QueryClientProvider client={queryClient}>
        <Wallet />
      </QueryClientProvider>
    </I18nextProvider>
  )
}

describe('wallet funding flow', () => {
  beforeEach(() => {
    sessionStorage.clear()
    useAuthStore.getState().auth.setUser({ ...walletUser, role: 1 })
    vi.spyOn(api, 'get').mockImplementation(async (url) => ({
      data: {
        success: true,
        data:
          url === '/api/verify/methods'
            ? {
                scope: 'agent.transaction',
                methods: [{ method: '2fa', available: true }],
                oauth_providers: [],
                password_encryption_enabled: false,
              }
            : {
                status: 'not_observed',
                request_id: String(url).split('/').at(-1),
              },
      },
    }))
    vi.spyOn(api, 'post').mockImplementation(async (url) => ({
      data: {
        success: true,
        data:
          url === '/api/verify'
            ? {
                proof_token: 'legacy-one-use',
                scope: 'agent.transaction',
                method: '2fa',
                expires_at: Math.floor(Date.now() / 1000) + 60,
              }
            : { quota: 500000 },
      },
    }))
    vi.spyOn(api, 'request').mockImplementation(async (config) => {
      const data = config.data as {
        action: string
        request_id: string
        payload: OperationDraft['payload']
      }
      const context = {
        action: data.action,
        request_id: data.request_id,
        agent_user_id: 7,
        profile_version: 0,
        policy_version: 0,
        currency: '',
        amount_minor: '0',
        quota: Number(data.payload.quota),
        target_type: 'legacy_affiliate',
        target_id: '7',
        target_version: 0,
        recipient_version: 0,
        recipient_digest: '',
        parameters_hash: 'legacy-params-hash',
      }
      const result: PreparedOperation = {
        verification_context: context,
        summary: {
          action: data.action,
          agent_user_id: 7,
          currency: '',
          currency_exponent: 0,
          amount_minor: '0',
          fee_minor: '0',
          net_minor: '0',
          quota: context.quota,
          target_label: 'Legacy reward wallet',
          recipient_label: '',
          policy_version: 0,
        },
        status_url: `/api/agent-platform/operations/${data.request_id}`,
      }
      return { data: { success: true, data: result } }
    })
    vi.mocked(getSelf).mockResolvedValue({ success: true, data: walletUser })
    vi.mocked(getWalletSummary).mockResolvedValue({
      success: true,
      data: walletSummary,
    })
    vi.mocked(getAffiliateCode).mockResolvedValue({
      success: true,
      data: 'wallet-aff',
    })
    vi.mocked(getReferralRewards).mockResolvedValue({
      success: true,
      data: referralRewards,
    })
    vi.mocked(calculateAmount).mockResolvedValue({ success: true, data: '73' })
  })

  test('hides payment methods and the redemption input when topup info fails to load', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: false,
      message: 'topup unavailable',
    })

    renderWallet()

    expect(
      await screen.findByText(
        'Online topup is not enabled. Please use redemption code or contact administrator.'
      )
    ).toBeInTheDocument()

    expect(
      screen.queryByRole('button', { name: 'Epay' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Alipay' })
    ).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Custom Amount')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Have a Code?')).not.toBeInTheDocument()
    expect(
      screen.getByText(
        'Redemption codes are disabled until the administrator confirms compliance terms.'
      )
    ).toBeInTheDocument()
  })

  test('shows a financial error state instead of zero balances when wallet summary fails', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    vi.mocked(getWalletSummary).mockResolvedValue({
      success: false,
      message: 'wallet unavailable',
    })

    const { container } = renderWallet()

    expect(
      await screen.findByText('Oops! Something went wrong')
    ).toBeInTheDocument()
    expect(container.querySelector('[data-wallet-stat="paid"]')).toBeNull()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  test('shows amount, payment methods, and redemption input when online topup and redemption are enabled', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })

    renderWallet()

    const amountInput = await screen.findByLabelText('Custom Amount')
    await waitFor(() => expect(amountInput).toHaveDisplayValue('10'))

    expect(screen.getByRole('region', { name: 'Add Funds' })).toContainElement(
      amountInput
    )
    expect(
      screen.getByRole('region', { name: 'Invitation Rewards' })
    ).toContainElement(screen.getByLabelText('Invitation Link'))
    expect(screen.getByLabelText('Have a Code?')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Redeem' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Epay' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Alipay' })).toBeEnabled()
    expect(screen.getByRole('button', { name: /^50 Pay/ })).toBeInTheDocument()

    await waitFor(() =>
      expect(calculateAmount).toHaveBeenCalledWith({ amount: 10 })
    )
  })

  test('keeps recharge before rewards in a responsive two-column workspace', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    renderWallet()
    await screen.findByLabelText('Custom Amount')
    const recharge = screen.getByRole('region', { name: 'Add Funds' })
    const rewards = screen.getByRole('region', { name: 'Invitation Rewards' })
    expect(recharge.parentElement).toHaveClass('grid', 'lg:grid-cols-2')
    expect(recharge.parentElement).toBe(rewards.parentElement)
    expect(recharge.nextElementSibling).toBe(rewards)
    expect(recharge).toHaveClass('min-w-0')
    expect(rewards).toHaveClass('min-w-0')
  })

  test('opens the payment confirm dialog and submits the selected method with the entered amount', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    vi.mocked(requestPayment).mockResolvedValue({
      success: true,
      data: {},
      url: 'https://pay.example.com/submit',
    })
    // jsdom cannot submit real payment forms; the redirect target itself is
    // covered by the requestPayment assertion below.
    vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(() => {})

    renderWallet()

    const epayButton = await screen.findByRole('button', { name: 'Epay' })
    await waitFor(() => expect(epayButton).toBeEnabled())
    await userEvent.setup().click(epayButton)

    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('Epay')).toBeInTheDocument()

    const summaryCallsBeforeConfirm =
      vi.mocked(getWalletSummary).mock.calls.length
    await userEvent
      .setup()
      .click(within(dialog).getByRole('button', { name: 'Confirm Payment' }))

    await waitFor(() => {
      expect(requestPayment).toHaveBeenCalledWith({
        amount: 10,
        payment_method: 'epay',
      })
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
      expect(vi.mocked(getWalletSummary).mock.calls.length).toBe(
        summaryCallsBeforeConfirm + 1
      )
    })
  })

  test('clears the redemption code and refreshes user and wallet summary after a successful redemption', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    vi.mocked(redeemTopupCode).mockResolvedValue({
      success: true,
      data: 500_000,
    })

    renderWallet()

    const codeInput = await screen.findByLabelText('Have a Code?')
    await userEvent.setup().type(codeInput, 'WELCOME-10')

    const selfCallsBeforeRedeem = vi.mocked(getSelf).mock.calls.length
    const summaryCallsBeforeRedeem =
      vi.mocked(getWalletSummary).mock.calls.length

    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Redeem' }))

    await waitFor(() => {
      expect(redeemTopupCode).toHaveBeenCalledWith({ key: 'WELCOME-10' })
      expect(codeInput).toHaveDisplayValue('')
      expect(vi.mocked(getSelf).mock.calls.length).toBeGreaterThan(
        selfCallsBeforeRedeem
      )
      expect(vi.mocked(getWalletSummary).mock.calls.length).toBe(
        summaryCallsBeforeRedeem + 1
      )
    })
  })

  test('refreshes balances when the wallet becomes visible again and removes listeners on unmount', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    const visibilityState = vi.spyOn(document, 'visibilityState', 'get')
    visibilityState.mockReturnValue('visible')

    const view = renderWallet()
    await screen.findByLabelText('Custom Amount')

    const initialSelfCalls = vi.mocked(getSelf).mock.calls.length
    const initialSummaryCalls = vi.mocked(getWalletSummary).mock.calls.length
    window.dispatchEvent(new Event('focus'))

    await waitFor(() => {
      expect(vi.mocked(getSelf).mock.calls.length).toBe(initialSelfCalls + 1)
      expect(vi.mocked(getWalletSummary).mock.calls.length).toBe(
        initialSummaryCalls + 1
      )
    })

    visibilityState.mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
    expect(vi.mocked(getSelf).mock.calls.length).toBe(initialSelfCalls + 1)

    visibilityState.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    await waitFor(() => {
      expect(vi.mocked(getSelf).mock.calls.length).toBe(initialSelfCalls + 2)
      expect(vi.mocked(getWalletSummary).mock.calls.length).toBe(
        initialSummaryCalls + 2
      )
    })

    view.unmount()
    window.dispatchEvent(new Event('focus'))
    expect(vi.mocked(getSelf).mock.calls.length).toBe(initialSelfCalls + 2)
    visibilityState.mockRestore()
  })

  test('keeps the redemption input hidden when the backend does not enable redemption', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: { ...onlineTopupInfo, enable_redemption: false },
    })

    renderWallet()

    const amountInput = await screen.findByLabelText('Custom Amount')
    await waitFor(() => expect(amountInput).toHaveDisplayValue('10'))
    expect(screen.getByRole('button', { name: 'Epay' })).toBeEnabled()
    expect(screen.queryByLabelText('Have a Code?')).not.toBeInTheDocument()
    expect(
      screen.getByText(
        'Redemption codes are disabled until the administrator confirms compliance terms.'
      )
    ).toBeInTheDocument()
  })

  async function openLegacyTransfer() {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    renderWallet()
    await userEvent.click(
      await screen.findByRole('button', { name: 'Transfer to Balance' })
    )
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Transfer' })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Authorize transaction' })
      ).toBeEnabled()
    )
  }
  async function authorizeLegacyTransfer() {
    await userEvent.click(
      screen.getByRole('button', { name: 'Authorize transaction' })
    )
    await userEvent.type(
      await screen.findByRole('textbox', { name: 'Authenticator code' }),
      '123456'
    )
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }))
  }
  const transferCalls = () =>
    vi
      .mocked(api.post)
      .mock.calls.filter(([url]) => url === '/api/user/aff_transfer')
  test('transfers pending rewards only after transaction MFA with a stable idempotency key and refreshes balances', async () => {
    await openLegacyTransfer()
    expect(transferCalls()).toHaveLength(0)
    await authorizeLegacyTransfer()
    await waitFor(() => expect(transferCalls()).toHaveLength(1))
    expect(transferCalls()[0]).toEqual([
      '/api/user/aff_transfer',
      { quota: 500000 },
      expect.objectContaining({
        headers: {
          'Idempotency-Key': expect.any(String),
          'X-Security-Proof': 'legacy-one-use',
        },
        singleUseAuthorization: true,
        skipAuthRefresh: true,
      }),
    ])
    await waitFor(() =>
      expect(vi.mocked(getWalletSummary).mock.calls.length).toBeGreaterThan(1)
    )
  })
  test('interrupted transfer remains uncertain and does not automatically obtain another proof', async () => {
    vi.mocked(api.post).mockImplementation(async (url) => {
      if (url === '/api/user/aff_transfer') throw new Error('connection lost')
      return {
        data: {
          success: true,
          data: {
            proof_token: 'legacy-one-use',
            scope: 'agent.transaction',
            method: '2fa',
            expires_at: Math.floor(Date.now() / 1000) + 60,
          },
        },
      }
    })
    await openLegacyTransfer()
    await authorizeLegacyTransfer()
    await screen.findByText('Do not submit a new request')
    expect(
      screen.getByRole('button', { name: 'Authorize transaction' })
    ).toBeDisabled()
    expect(transferCalls()).toHaveLength(1)
    expect(
      vi.mocked(api.post).mock.calls.filter(([url]) => url === '/api/verify')
    ).toHaveLength(1)
  })
  test('reopening the same transfer after a lost response recovers the same business ID instead of paying twice', async () => {
    vi.mocked(api.post).mockImplementation(async (url) => {
      if (url === '/api/user/aff_transfer') throw new Error('connection lost')
      return {
        data: {
          success: true,
          data: {
            proof_token: 'legacy-one-use',
            scope: 'agent.transaction',
            method: '2fa',
            expires_at: Math.floor(Date.now() / 1000) + 60,
          },
        },
      }
    })
    await openLegacyTransfer()
    await authorizeLegacyTransfer()
    await screen.findByText('Do not submit a new request')
    const first = transferCalls()[0][2]?.headers?.['Idempotency-Key']
    await userEvent.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Cancel',
      })
    )
    await userEvent.click(
      within(await screen.findByRole('dialog')).getByRole('button', {
        name: 'Transfer',
      })
    )
    expect(
      await screen.findByText('Do not submit a new request')
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Authorize transaction' })
    ).toBeDisabled()
    await userEvent.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Check request status',
      })
    )
    await waitFor(() =>
      expect(api.get).toHaveBeenCalledWith(
        `/api/agent-platform/operations/${String(first)}`,
        expect.anything()
      )
    )
    expect(transferCalls()).toHaveLength(1)
  })
  test('changing the amount cannot bypass an unresolved transfer', async () => {
    vi.mocked(api.post).mockImplementation(async (url) => {
      if (url === '/api/user/aff_transfer') throw new Error('connection lost')
      return {
        data: {
          success: true,
          data: {
            proof_token: 'legacy-one-use',
            scope: 'agent.transaction',
            method: '2fa',
            expires_at: Math.floor(Date.now() / 1000) + 60,
          },
        },
      }
    })
    await openLegacyTransfer()
    await authorizeLegacyTransfer()
    await screen.findByText('Do not submit a new request')
    await userEvent.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Cancel',
      })
    )
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('Transfer Amount'), {
      target: { value: '2' },
    })
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Transfer' })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Authorize transaction' })
      ).toBeEnabled()
    )
    await userEvent.click(
      screen.getByRole('button', { name: 'Authorize transaction' })
    )
    expect(transferCalls()).toHaveLength(1)
    expect(
      vi.mocked(api.post).mock.calls.filter(([url]) => url === '/api/verify')
    ).toHaveLength(1)
  })
  test('duplicate clicks cannot submit a pending authorized transfer twice', async () => {
    let finish!: (value: { data: { success: boolean } }) => void
    vi.mocked(api.post).mockImplementation(async (url) => {
      if (url === '/api/user/aff_transfer') {
        return new Promise((resolve) => {
          finish = resolve
        })
      }
      return {
        data: {
          success: true,
          data: {
            proof_token: 'legacy-one-use',
            scope: 'agent.transaction',
            method: '2fa',
            expires_at: Math.floor(Date.now() / 1000) + 60,
          },
        },
      }
    })
    await openLegacyTransfer()
    await authorizeLegacyTransfer()
    await waitFor(() => expect(transferCalls()).toHaveLength(1))
    const button = screen.getByRole('button', { name: 'Authorize transaction' })
    expect(button).toBeDisabled()
    fireEvent.click(button)
    expect(transferCalls()).toHaveLength(1)
    await act(async () => finish({ data: { success: true } }))
  })
  test('bounds the transfer before requesting an exact quota transaction review', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    renderWallet()
    await userEvent.click(
      await screen.findByRole('button', { name: 'Transfer to Balance' })
    )
    const dialog = await screen.findByRole('dialog'),
      input = within(dialog).getByLabelText('Transfer Amount'),
      button = within(dialog).getByRole('button', { name: 'Transfer' })
    expect(input).toHaveAttribute('min', '1')
    expect(input).toHaveAttribute('max', '2')
    fireEvent.change(input, { target: { value: '0.5' } })
    expect(button).toBeDisabled()
    fireEvent.change(input, { target: { value: '3' } })
    expect(button).toBeDisabled()
    fireEvent.change(input, { target: { value: '2' } })
    expect(button).toBeEnabled()
    await userEvent.click(button)
    await waitFor(() =>
      expect(api.request).toHaveBeenCalledWith(
        expect.objectContaining({
          url: '/api/agent-platform/operations/prepare',
          data: expect.objectContaining({ payload: { quota: 1000000 } }),
        })
      )
    )
    expect(transferCalls()).toHaveLength(0)
  })

  test('copies the real sign-up link generated from the affiliate code', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })

    renderWallet()

    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: 'Copy referral link' })
    )

    expect(await navigator.clipboard.readText()).toBe(
      `${window.location.origin}/sign-up?aff=wallet-aff`
    )
  })

  test('shows the commission records error state and recovers on retry', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    vi.mocked(getReferralRewards)
      .mockResolvedValueOnce({ success: false, message: 'records unavailable' })
      .mockResolvedValue({ success: true, data: referralRewards })

    renderWallet()

    expect(
      await screen.findByText('Oops! Something went wrong')
    ).toBeInTheDocument()
    expect(
      screen.queryByText(
        'No commission records yet. Invite friends to recharge and they will appear here.'
      )
    ).not.toBeInTheDocument()

    await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByText('a***')).toBeInTheDocument()
    expect(vi.mocked(getReferralRewards).mock.calls.length).toBeGreaterThan(1)
  })

  test('embeds the native invitation rewards card without legacy standalone referral surfaces', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })

    renderWallet()

    expect(await screen.findByText('Invitation Rewards')).toBeInTheDocument()
    expect(
      screen.getByText(
        /For the first 3 recharges of an invited friend, you will receive 5\.0% of the recharge amount as a reward\./
      )
    ).toBeInTheDocument()
    expect(
      screen.getByDisplayValue(/\/sign-up\?aff=wallet-aff$/)
    ).toBeInTheDocument()
    expect(await screen.findByText('a***')).toBeInTheDocument()
    expect(screen.queryByText('Referral Program')).not.toBeInTheDocument()
    expect(screen.queryByText('Referral History')).not.toBeInTheDocument()
  })

  test.each(['custom amount', 'preset'])(
    'changing %s during a method quote releases loading and cannot open a stale confirmation',
    async (selection) => {
      vi.mocked(getTopupInfo).mockResolvedValue({
        success: true,
        data: onlineTopupInfo,
      })
      renderWallet()
      const amountInput = await screen.findByLabelText('Custom Amount')
      await waitFor(() => expect(amountInput).toHaveDisplayValue('10'))
      await waitFor(() =>
        expect(calculateAmount).toHaveBeenCalledWith({ amount: 10 })
      )
      let resolveMethod!: (
        value: Awaited<ReturnType<typeof calculateAmount>>
      ) => void
      let resolveAmount!: (
        value: Awaited<ReturnType<typeof calculateAmount>>
      ) => void
      vi.mocked(calculateAmount)
        .mockImplementationOnce(
          () =>
            new Promise((resolve) => {
              resolveMethod = resolve
            })
        )
        .mockImplementationOnce(
          () =>
            new Promise((resolve) => {
              resolveAmount = resolve
            })
        )
      const epay = screen.getByRole('button', { name: 'Epay' })
      await userEvent.setup().click(epay)
      expect(epay).toBeDisabled()
      if (selection === 'preset') {
        await userEvent
          .setup()
          .click(screen.getByRole('button', { name: /^50 Pay/ }))
      } else {
        fireEvent.change(amountInput, { target: { value: '20' } })
      }
      const selectedAmount = selection === 'preset' ? 50 : 20
      await waitFor(() =>
        expect(calculateAmount).toHaveBeenLastCalledWith({
          amount: selectedAmount,
        })
      )
      await act(async () => {
        resolveAmount({ success: true, data: '146.5' })
      })
      await act(async () => {
        resolveMethod({ success: true, data: '73' })
      })
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
      expect(epay).toBeEnabled()
      expect(screen.getByRole('button', { name: 'Alipay' })).toBeEnabled()
      await userEvent.setup().click(epay)
      const dialog = await screen.findByRole('alertdialog')
      expect(
        within(dialog).getByRole('button', { name: 'Confirm Payment' })
      ).toBeEnabled()
      expect(calculateAmount).toHaveBeenLastCalledWith({
        amount: selectedAmount,
      })
    }
  )
})
