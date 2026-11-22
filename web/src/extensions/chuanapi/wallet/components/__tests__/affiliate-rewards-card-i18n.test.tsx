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
import { fireEvent, render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeAll, describe, expect, test, vi } from 'vitest'

import zh from '@/i18n/locales/zh.json'

import type { ReferralCommissionItem, UserWalletData } from '../../types'
import { AffiliateRewardsCard } from '../affiliate-rewards-card'

const affiliateLink = 'https://example.test/sign-up?aff=demo'

const user: UserWalletData = {
  id: 1,
  username: 'inviter',
  quota: 0,
  used_quota: 0,
  request_count: 0,
  aff_count: 2,
  aff_quota: 100000,
  aff_history_quota: 300000,
  group: 'default',
}

const record: ReferralCommissionItem = {
  id: 11,
  source_user_id: 2,
  source_name: 'a***',
  paid_quota: 1000000,
  reward_quota: 50000,
  reversed_quota: 0,
  status: 'released',
  matures_at: 0,
  rate: 0.05,
  created_at: 1735689600,
}

const testI18n = createInstance()

beforeAll(async () => {
  await testI18n.init({
    lng: 'zh',
    fallbackLng: 'en',
    resources: {
      en: { translation: {} },
      zh: { translation: zh.translation },
    },
    interpolation: { escapeValue: false },
  })
})

function renderChineseCard(overrides: Record<string, unknown> = {}) {
  const result = render(
    <I18nextProvider i18n={testI18n}>
      <AffiliateRewardsCard
        user={user}
        availableQuota={user.aff_quota ?? 0}
        affiliateLink={affiliateLink}
        onTransfer={vi.fn()}
        complianceConfirmed
        referralRate={0.05}
        records={[record]}
        recordsTotal={1}
        recordsPage={1}
        onRecordsPageChange={vi.fn()}
        {...overrides}
      />
    </I18nextProvider>
  )
  fireEvent.click(screen.getByRole('button', { name: '奖励说明' }))
  fireEvent.click(screen.getByRole('button', { name: '返佣记录' }))
  return result
}

describe('affiliate rewards card Chinese copy', () => {
  test('renders the approved Chinese titles, available rewards, and unlimited five-day reward rules', () => {
    renderChineseCard()

    expect(screen.getByText('邀请奖励')).toBeInTheDocument()
    expect(screen.getByText('邀请好友获得额外奖励')).toBeInTheDocument()
    expect(screen.getByText('邀请返利可提现')).toBeInTheDocument()
    expect(screen.getByText('累计邀请返利')).toBeInTheDocument()
    expect(screen.getByText('邀请人数')).toBeInTheDocument()
    expect(screen.getByLabelText('邀请链接')).toHaveDisplayValue(affiliateLink)
    expect(
      screen.getByRole('button', { name: '复制推荐链接' })
    ).toBeInTheDocument()
    expect(screen.getByText('复制')).toBeInTheDocument()
    expect(screen.getByText('奖励说明')).toBeInTheDocument()
    expect(screen.getByText('返佣记录')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: '划转到余额' })
    ).toBeInTheDocument()

    expect(screen.getByText('仅官网在线充值参与邀请返利。')).toBeInTheDocument()
    expect(
      screen.getByText('好友每次符合条件的充值均可获得 5.0% 返利，不封顶。')
    ).toBeInTheDocument()
    expect(
      screen.getByText('返利冻结 5 天后到账；退款时取消或扣回对应返利。')
    ).toBeInTheDocument()
    expect(screen.getByText('已到账')).toBeVisible()
    expect(
      screen.getByText(new Date(record.created_at * 1000).toLocaleString('zh'))
    ).toBeInTheDocument()
  })

  test('renders the configured rate instead of a hardcoded percentage', () => {
    renderChineseCard({ referralRate: 0.0333 })

    expect(
      screen.getByText('好友每次符合条件的充值均可获得 3.33% 返利，不封顶。')
    ).toBeInTheDocument()
  })

  test('renders the approved Chinese empty state', () => {
    renderChineseCard({ records: [], recordsTotal: 0 })

    expect(
      screen.getByText('暂无返佣记录，邀请好友充值后将在此显示')
    ).toBeInTheDocument()
  })
})
