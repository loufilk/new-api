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
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { expect, test } from 'vitest'

import { ModelGroupSelector } from '@/components/model-group-selector'

const longGroupName = '官方高质量·长分组名称·完整显示'
const groups = Array.from({ length: 32 }, (_, index) => ({
  value: `group-${index + 1}`,
  label: index === 1 ? longGroupName : `Group ${index + 1}`,
}))

function SelectorFixture() {
  const [group, setGroup] = useState('group-1')
  return (
    <ModelGroupSelector
      selectedModel='model-one'
      models={[{ value: 'model-one', label: 'Model one' }]}
      onModelChange={() => undefined}
      selectedGroup={group}
      groups={groups}
      onGroupChange={setGroup}
    />
  )
}

test('desktop groups retain full labels and every option remains selectable independently of model search', async () => {
  render(<SelectorFixture />)
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox'))
  const longOption = screen.getByRole('button', { name: longGroupName })
  expect(longOption).toHaveClass('h-auto', 'whitespace-normal')
  expect(within(longOption).getByText(longGroupName)).not.toHaveClass(
    'truncate'
  )
  expect(longOption.parentElement).toHaveClass('overflow-y-auto', 'min-h-0')
  expect(
    within(longOption.parentElement as HTMLElement).getAllByRole('button')
  ).toHaveLength(32)
  await user.type(
    screen.getByPlaceholderText('Search models...'),
    'not-a-model'
  )
  expect(screen.getByText('No model found.')).toBeInTheDocument()
  const lastGroup = screen.getByRole('button', { name: 'Group 32' })
  await user.click(lastGroup)
  expect(lastGroup).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: 'Group 1' })).toHaveAttribute(
    'aria-pressed',
    'false'
  )
  await user.click(longOption)
  expect(longOption).toHaveAttribute('aria-pressed', 'true')
  expect(
    within(screen.getAllByRole('combobox')[0]).getByText(longGroupName)
  ).toHaveAttribute('title', longGroupName)
})
