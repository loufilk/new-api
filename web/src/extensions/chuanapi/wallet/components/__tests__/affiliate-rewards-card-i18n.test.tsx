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
import { render, screen } from '@testing-library/react'
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
  return render(
    <I18nextProvider i18n={testI18n}>
      <AffiliateRewardsCard
        user={user}
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
}

describe('affiliate rewards card Chinese copy', () => {
  test('renders the approved Chinese titles, stats, and four rule sentences', () => {
    renderChineseCard()

    expect(screen.getByText('邀请奖励')).toBeInTheDocument()
    expect(screen.getByText('邀请好友获得额外奖励')).toBeInTheDocument()
    expect(screen.getByText('待使用收益')).toBeInTheDocument()
    expect(screen.getByText('总收益')).toBeInTheDocument()
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

    expect(
      screen.getByText('邀请好友注册，好友充值后您可获得相应奖励')
    ).toBeInTheDocument()
    expect(
      screen.getByText('好友前3次充值，您可获得充值额度的 5.0% 作为奖励')
    ).toBeInTheDocument()
    expect(
      screen.getByText('通过划转功能将奖励额度转入您的账户余额中')
    ).toBeInTheDocument()
    expect(
      screen.getByText('邀请的好友越多，获得的奖励就越多')
    ).toBeInTheDocument()
    expect(
      screen.getByText(new Date(record.created_at * 1000).toLocaleString('zh'))
    ).toBeInTheDocument()
  })

  test('renders the configured rate instead of a hardcoded percentage', () => {
    renderChineseCard({ referralRate: 0.0333 })

    expect(
      screen.getByText('好友前3次充值，您可获得充值额度的 3.33% 作为奖励')
    ).toBeInTheDocument()
  })

  test('renders the approved Chinese empty state', () => {
    renderChineseCard({ records: [], recordsTotal: 0 })

    expect(
      screen.getByText('暂无返佣记录，邀请好友充值后将在此显示')
    ).toBeInTheDocument()
  })
})
