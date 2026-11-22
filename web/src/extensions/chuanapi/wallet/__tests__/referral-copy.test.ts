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
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, test } from 'vitest'

const localesDir = resolve(
  dirname(fileURLToPath(import.meta.url)),
  '../../../../i18n/locales'
)

function translation(locale: string): Record<string, string> {
  const file = JSON.parse(
    readFileSync(resolve(localesDir, `${locale}.json`), 'utf8')
  )
  return file.translation as Record<string, string>
}

describe('invitation rewards copy contract', () => {
  test('zh locale uses the approved visible copy', () => {
    const zh = translation('zh')

    expect(zh['Invitation Rewards']).toBe('邀请奖励')
    expect(zh['Invite friends to earn extra rewards']).toBe(
      '邀请好友获得额外奖励'
    )
    expect(zh['Pending Rewards']).toBe('待使用收益')
    expect(zh['Total Rewards']).toBe('总收益')
    expect(zh['Referral Invited Users']).toBe('邀请人数')
    expect(zh['Invitation Link']).toBe('邀请链接')
    expect(zh['Copy']).toBe('复制')
    expect(zh['Reward Rules']).toBe('奖励说明')
    expect(zh['Commission Records']).toBe('返佣记录')
    expect(zh['Referral Transfer to Balance']).toBe('划转到余额')
    expect(
      zh[
        'No commission records yet. Invite friends to recharge and they will appear here.'
      ]
    ).toBe('暂无返佣记录，邀请好友充值后将在此显示')
    expect(
      zh[
        'Invite friends to register. You will receive rewards after they recharge.'
      ]
    ).toBe('邀请好友注册，好友充值后您可获得相应奖励')
    expect(
      zh[
        'For the first {{limit}} recharges of an invited friend, you will receive {{rate}} of the recharge amount as a reward.'
      ]
    ).toBe('好友前{{limit}}次充值，您可获得充值额度的 {{rate}} 作为奖励')
    expect(
      zh[
        'Use the transfer feature to move reward credits into your account balance.'
      ]
    ).toBe('通过划转功能将奖励额度转入您的账户余额中')
    expect(zh['The more friends you invite, the more rewards you earn.']).toBe(
      '邀请的好友越多，获得的奖励就越多'
    )
  })

  test('generic invitation and transfer keys keep their baseline values', () => {
    const baseline = {
      en: { invited: 'Invited Users', transfer: 'Transfer to Balance' },
      zh: { invited: '受邀用户', transfer: '转移到余额' },
      'zh-TW': { invited: '受邀用戶', transfer: '轉移到餘額' },
      fr: {
        invited: 'Utilisateurs invités',
        transfer: 'Transférer vers le solde',
      },
      ja: { invited: '招待されたユーザー', transfer: '残高への振替' },
      ru: {
        invited: 'Приглашенные пользователи',
        transfer: 'Перевести на баланс',
      },
      vi: { invited: 'Người dùng được mời', transfer: 'Chuyển vào số dư' },
    }

    for (const [locale, expected] of Object.entries(baseline)) {
      const values = translation(locale)
      expect(values['Invited Users'], `${locale}:Invited Users`).toBe(
        expected.invited
      )
      expect(
        values['Transfer to Balance'],
        `${locale}:Transfer to Balance`
      ).toBe(expected.transfer)
    }
  })

  test('all supported locales carry the referral card keys', () => {
    for (const locale of ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']) {
      const values = translation(locale)
      for (const key of [
        'Invitation Rewards',
        'Pending Rewards',
        'Total Rewards',
        'Referral Invited Users',
        'Invitation Link',
        'Reward Rules',
        'Commission Records',
        'Referral Transfer to Balance',
        'No commission records yet. Invite friends to recharge and they will appear here.',
      ]) {
        expect(values[key], `${locale}:${key}`).toBeTruthy()
      }
      if (locale !== 'en') {
        expect(values['Referral Transfer to Balance']).not.toBe(
          'Transfer to Balance'
        )
        expect(values['Referral Invited Users']).not.toBe('Invited Users')
      }
    }
  })
})
