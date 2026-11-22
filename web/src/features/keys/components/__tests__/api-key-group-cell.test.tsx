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
import { cleanup, render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, test } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'

import { ApiKeyGroupCell } from '../api-key-group-cell'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})

afterEach(async () => {
  cleanup()
  await i18n.changeLanguage('en')
})

function show(props: React.ComponentProps<typeof ApiKeyGroupCell>) {
  return render(
    <I18nextProvider i18n={i18n}>
      <TooltipProvider>
        <ApiKeyGroupCell {...props} />
      </TooltipProvider>
    </I18nextProvider>
  )
}

test('shows the published label plus the effective numeric multiplier', () => {
  show({ group: 'canonical-id', label: 'Public label', ratio: 0.125 })
  expect(screen.getByText('Public label')).toBeVisible()
  expect(screen.getByText('0.125×')).toBeVisible()
  expect(screen.queryByText('canonical-id')).not.toBeInTheDocument()
})

test('a legacy smart key does not advertise a dormant concrete group as its route', () => {
  show({ group: 'dormant group', ratio: 1, smartRouting: true })
  expect(screen.getByText('Legacy smart routing')).toBeVisible()
  expect(screen.queryByText('dormant group')).not.toBeInTheDocument()
  expect(screen.queryByText('1×')).not.toBeInTheDocument()
})

test('legacy auto and inherited keys remain distinguishable without inventing prices', () => {
  show({ group: 'auto', autoGroups: ['a', 'b'], ratio: 'auto' })
  show({ group: '' })
  expect(screen.getByText('Legacy automatic routing')).toBeVisible()
  expect(screen.getByText('Follow user group')).toBeVisible()
  expect(screen.queryByText(/×/)).not.toBeInTheDocument()
})

test.each(['zhCN', 'zhTW'])(
  'renders the group price with the %s interface locale',
  async (language) => {
    i18n.addResourceBundle(language, 'translation', {
      'Follow user group': 'group',
    })
    await i18n.changeLanguage(language)
    expect(i18n.resolvedLanguage).toBe(language)
    show({ group: 'vip', label: 'Private', ratio: 0.125 })
    expect(screen.getByText('0.125×')).toBeVisible()
  }
)
