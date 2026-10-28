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
import { zodResolver } from '@hookform/resolvers/zod'
import {
  fireEvent,
  render,
  screen,
  within,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useCallback, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { describe, expect, it, vi } from 'vitest'
import { z } from 'zod'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { positiveIntegerSchema } from '../../utils/numeric-field'
import { GroupRatioForm } from '../group-ratio-form'

const defaults = {
  GroupRatio: '{"default":1,"vip":0.8}',
  TopupGroupRatio: '{"vip":1.2}',
  UserUsableGroups: '{"default":"Standard access","vip":"Premium access"}',
  GroupGroupRatio: '{}',
  AutoGroups: '["default","vip"]',
  MaxTokenAutoGroups: 5,
  DefaultUseAutoGroup: false,
  GroupSpecialUsableGroup: '{}',
}

const schema = z.object({
  GroupRatio: z.string(),
  TopupGroupRatio: z.string(),
  UserUsableGroups: z.string(),
  GroupGroupRatio: z.string(),
  AutoGroups: z.string(),
  MaxTokenAutoGroups: positiveIntegerSchema('Enter a positive integer'),
  DefaultUseAutoGroup: z.boolean(),
  GroupSpecialUsableGroup: z.string(),
})

function Fixture(props: {
  onSave?: (values: typeof defaults) => Promise<void>
  initial?: Partial<typeof defaults>
  isSaving?: boolean
}) {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  const baseline = useMemo(
    () => ({ ...defaults, ...props.initial }),
    [props.initial]
  )
  const form = useForm({
    defaultValues: baseline,
    resolver: zodResolver(schema),
  })
  const saveValues = props.onSave
  const onSave = useCallback(
    async (values: typeof defaults) => {
      await saveValues?.(values)
      return 'saved' as const
    },
    [saveValues]
  )
  return (
    <SettingsPageProvider actionsContainer={actions}>
      <div ref={setActions} />
      <GroupRatioForm
        form={form}
        baseline={baseline}
        onSave={onSave}
        isSaving={props.isSaving ?? false}
      />
    </SettingsPageProvider>
  )
}

describe('group settings workspace', () => {
  it('filters by description and clears search without discarding edits', async () => {
    const user = userEvent.setup()
    render(<Fixture />)
    const search = screen.getByRole('textbox', {
      name: 'Search groups by name or description',
    })
    await user.type(search, 'PREMIUM')
    expect(screen.getAllByRole('textbox', { name: 'Group name' })).toHaveLength(
      1
    )
    expect(screen.getByRole('textbox', { name: 'Group name' })).toHaveValue(
      'vip'
    )
    await user.clear(screen.getByRole('spinbutton', { name: 'Ratio' }))
    await user.type(screen.getByRole('spinbutton', { name: 'Ratio' }), '0.6')
    await user.click(screen.getByRole('button', { name: 'Clear search' }))
    expect(screen.getAllByRole('textbox', { name: 'Group name' })).toHaveLength(
      2
    )
    expect(screen.getAllByRole('spinbutton', { name: 'Ratio' })[1]).toHaveValue(
      0.6
    )
    await user.type(search, 'missing')
    expect(screen.getByText('No results found')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Add group' }))
    expect(search).toHaveValue('')
    expect(
      screen
        .getAllByRole<HTMLInputElement>('textbox', { name: 'Group name' })
        .map((input) => input.value)
    ).toEqual(['group_1', 'default', 'vip'])
    expect(screen.getAllByRole('spinbutton', { name: 'Ratio' })[2]).toHaveValue(
      0.6
    )
  })

  it('prepends consecutive new groups and saves them without changing existing settings', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn(async (_values: typeof defaults) => {})
    render(<Fixture onSave={onSave} />)

    await user.click(screen.getByRole('button', { name: 'Add group' }))
    await user.click(screen.getByRole('button', { name: 'Add group' }))
    expect(
      screen
        .getAllByRole<HTMLInputElement>('textbox', { name: 'Group name' })
        .map((input) => input.value)
    ).toEqual(['group_2', 'group_1', 'default', 'vip'])

    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    const saved = onSave.mock.calls[0][0]
    expect(JSON.parse(saved.GroupRatio)).toEqual({
      group_2: 1,
      group_1: 1,
      default: 1,
      vip: 0.8,
    })
    expect(JSON.parse(saved.UserUsableGroups)).toEqual({
      group_2: '',
      group_1: '',
      default: 'Standard access',
      vip: 'Premium access',
    })
    expect(JSON.parse(saved.TopupGroupRatio)).toEqual({ vip: 1.2 })
    expect(saved.AutoGroups).toBe(defaults.AutoGroups)
  })

  it('keeps unfinished visibility rules while switching sections and supports keyboard tabs', async () => {
    const user = userEvent.setup()
    render(
      <Fixture
        initial={{
          GroupSpecialUsableGroup: '{"vip":{ "+:default":"Standard"}}',
        }}
      />
    )
    const pricing = screen.getByRole('tab', { name: 'Pricing groups' })
    pricing.focus()
    await user.keyboard('{ArrowRight}{Enter}')
    expect(
      screen.getByRole('tab', { name: 'Special ratio rules' })
    ).toHaveAttribute('aria-selected', 'true')
    expect(
      screen.queryByRole('table', { name: 'Pricing groups' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: 'Group visibility' }))
    await user.click(screen.getByRole('button', { name: 'Add rule' }))
    expect(
      screen.getAllByRole('combobox', { name: 'Group name' })
    ).toHaveLength(2)
    await user.click(pricing)
    await user.click(screen.getByRole('tab', { name: 'Group visibility' }))
    expect(
      screen.getAllByRole('combobox', { name: 'Group name' })
    ).toHaveLength(2)
    const toggle = screen.getByRole('button', { name: 'Rules for vip' })
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await user.click(screen.getByRole('button', { name: 'Add rule' }))
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
  })

  it('reorders and removes auto groups with named controls and saves across sections', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn(async (_values: typeof defaults) => {})
    render(<Fixture onSave={onSave} />)
    await user.click(screen.getByRole('tab', { name: 'Auto group order' }))
    const list = screen.getByRole('list', { name: 'Auto group order' })
    expect(
      screen.getByRole('button', { name: 'Move default up' })
    ).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Move vip down' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Move vip up' }))
    expect(within(list).getAllByRole('listitem')[0]).toHaveTextContent('vip')
    await user.click(
      screen.getByRole('switch', { name: 'Default to auto groups' })
    )
    await user.click(screen.getByRole('button', { name: 'Remove vip' }))
    expect(
      screen.getByRole('button', { name: 'Move default up' })
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Move default down' })
    ).toBeDisabled()
    await user.click(screen.getByRole('tab', { name: 'Pricing groups' }))
    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(onSave.mock.calls[0]?.[0]).toEqual(
      expect.objectContaining({
        AutoGroups: '[\n  "default"\n]',
        DefaultUseAutoGroup: true,
      })
    )
  })

  it('reorders from the drag handle with arrow keys, preserves unknown groups and saves the new order', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn(async (_values: typeof defaults) => {})
    render(
      <Fixture
        onSave={onSave}
        initial={{
          AutoGroups: '["default","vip","retired"]',
          MaxTokenAutoGroups: 1,
        }}
      />
    )
    await user.click(screen.getByRole('tab', { name: 'Auto group order' }))
    const list = screen.getByRole('list', { name: 'Auto group order' })
    const handle = screen.getByRole('button', {
      name: 'Drag default to reorder',
    })
    handle.focus()
    await user.keyboard('{ArrowUp}')
    expect(within(list).getAllByRole('listitem')[0]).toHaveTextContent(
      'default'
    )
    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}')
    expect(within(list).getAllByRole('listitem')[2]).toHaveTextContent(
      'default'
    )
    expect(handle).toHaveFocus()
    await user.keyboard('{ArrowUp}')
    expect(within(list).getAllByRole('listitem')[1]).toHaveTextContent(
      'default'
    )
    expect(screen.getByText('Not in pricing table')).toBeVisible()
    await user.click(screen.getByRole('tab', { name: 'Pricing groups' }))
    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(JSON.parse(onSave.mock.calls[0][0].AutoGroups)).toEqual([
      'vip',
      'default',
      'retired',
    ])
  })

  it('keeps a single auto group unchanged when using its drag handle keyboard controls', async () => {
    const user = userEvent.setup()
    render(<Fixture initial={{ AutoGroups: '["default"]' }} />)
    await user.click(screen.getByRole('tab', { name: 'Auto group order' }))
    const handle = screen.getByRole('button', {
      name: 'Drag default to reorder',
    })
    handle.focus()
    await user.keyboard('{ArrowUp}{ArrowDown}')
    const list = screen.getByRole('list', { name: 'Auto group order' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(1)
    expect(within(list).getByRole('listitem')).toHaveTextContent('default')
    expect(handle).toHaveFocus()
    expect(
      screen.getByRole('button', { name: 'Move default up' })
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Move default down' })
    ).toBeDisabled()
  })

  it('opens the auto section and focuses its invalid limit when saving from another section', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn(async (_values: typeof defaults) => {})
    render(<Fixture onSave={onSave} />)
    await user.click(screen.getByRole('tab', { name: 'Auto group order' }))
    fireEvent.change(
      screen.getByRole('spinbutton', {
        name: 'Maximum custom groups per token',
      }),
      { target: { value: '0' } }
    )
    await user.click(screen.getByRole('tab', { name: 'Pricing groups' }))
    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('tab', { name: 'Auto group order' })
      ).toHaveAttribute('aria-selected', 'true')
    )
    const input = screen.getByRole('spinbutton', {
      name: 'Maximum custom groups per token',
    })
    expect(input).toHaveAttribute('aria-invalid', 'true')
    await waitFor(() => expect(input).toHaveFocus())
    expect(screen.getByText('Enter a positive integer')).toBeVisible()
    expect(onSave).not.toHaveBeenCalled()
  })

  it('shows empty state guidance and disables save during a pending request', async () => {
    const user = userEvent.setup()
    render(
      <Fixture
        isSaving
        initial={{
          GroupRatio: '{}',
          TopupGroupRatio: '{}',
          UserUsableGroups: '{}',
          AutoGroups: '[]',
        }}
      />
    )
    expect(
      screen.getByText('No groups yet. Add a group to get started.')
    ).toBeVisible()
    expect(screen.getByRole('button', { name: 'Saving...' })).toBeDisabled()
    await user.click(screen.getByRole('tab', { name: 'Special ratio rules' }))
    expect(
      screen.getByText('Base group ratios apply until you add an override.')
    ).toBeVisible()
    await user.click(screen.getByRole('tab', { name: 'Auto group order' }))
    expect(screen.getByText('No auto groups configured')).toBeVisible()
  })

  it('preserves pricing edits when switching between visual and JSON editors', async () => {
    const user = userEvent.setup()
    render(<Fixture />)
    await user.clear(
      screen.getAllByRole('textbox', { name: 'Group description' })[1]
    )
    await user.type(
      screen.getAllByRole('textbox', { name: 'Group description' })[1],
      'Updated description'
    )
    await user.click(screen.getByRole('button', { name: 'Switch to JSON' }))
    await user.click(screen.getByRole('button', { name: 'Switch to Visual' }))
    expect(
      screen.getAllByRole('textbox', { name: 'Group description' })[1]
    ).toHaveValue('Updated description')
  })
})

it('preserves six existing empty rule groups when granting enterprise extra visible default and round-tripping modes', async () => {
  const user = userEvent.setup()
  const onSave = vi.fn(async (_values: typeof defaults) => {})
  const emptyGroups = {
    default: {},
    vip: {},
    standard: {},
    premium: {},
    team: {},
    legacy: {},
  }
  render(
    <Fixture
      onSave={onSave}
      initial={{
        GroupRatio:
          '{"default":1,"vip":1,"standard":1,"premium":1,"team":1,"legacy":1,"enterprise":1}',
        GroupSpecialUsableGroup: JSON.stringify(emptyGroups),
      }}
    />
  )
  await user.click(screen.getByRole('tab', { name: 'Group visibility' }))
  await user.click(
    screen.getByRole('combobox', { name: 'Add rules for a user group' })
  )
  await user.click(await screen.findByRole('option', { name: 'enterprise' }))
  await user.click(screen.getByRole('combobox', { name: 'Group name' }))
  await user.click(await screen.findByRole('option', { name: 'default' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Description' }),
    'Authorized access'
  )
  await user.click(screen.getByRole('button', { name: 'Switch to JSON' }))
  expect(
    JSON.parse(
      (
        screen.getByLabelText(
          'Special usable group rules'
        ) as HTMLTextAreaElement
      ).value
    )
  ).toEqual({
    ...emptyGroups,
    enterprise: { '+:default': 'Authorized access' },
  })
  await user.click(screen.getByRole('button', { name: 'Switch to Visual' }))
  for (const group of Object.keys(emptyGroups)) {
    expect(
      screen.getByRole('button', { name: `Rules for ${group}` })
    ).toBeVisible()
  }
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  expect(JSON.parse(onSave.mock.calls[0][0].GroupSpecialUsableGroup)).toEqual({
    ...emptyGroups,
    enterprise: { '+:default': 'Authorized access' },
  })
})

it('deleting a last rule keeps an empty group until the group is explicitly removed', async () => {
  const user = userEvent.setup()
  const onSave = vi.fn(async (_values: typeof defaults) => {})
  render(
    <Fixture
      onSave={onSave}
      initial={{ GroupSpecialUsableGroup: '{"vip":{"+:default":"Standard"}}' }}
    />
  )
  await user.click(screen.getByRole('tab', { name: 'Group visibility' }))
  await user.click(screen.getByRole('button', { name: 'Delete rule' }))
  expect(screen.getByRole('button', { name: 'Rules for vip' })).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  expect(JSON.parse(onSave.mock.calls[0][0].GroupSpecialUsableGroup)).toEqual({
    vip: {},
  })
  await user.click(screen.getByRole('button', { name: 'Remove vip' }))
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(2))
  expect(JSON.parse(onSave.mock.calls[1][0].GroupSpecialUsableGroup)).toEqual(
    {}
  )
})

it.each(['+:default', 'default'])(
  'rejects conflicting %s and hidden rules in JSON without choosing precedence',
  async (visibleKey) => {
    const user = userEvent.setup()
    const onSave = vi.fn(async (_values: typeof defaults) => {})
    render(<Fixture onSave={onSave} />)
    await user.click(screen.getByRole('button', { name: 'Switch to JSON' }))
    fireEvent.input(screen.getByLabelText('Special usable group rules'), {
      target: {
        value: JSON.stringify({
          vip: { [visibleKey]: 'Standard', '-:default': 'remove' },
        }),
      },
    })
    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Group default cannot be both extra visible and hidden for vip. Remove one rule before saving.'
    )
    expect(onSave).not.toHaveBeenCalled()
    fireEvent.input(screen.getByLabelText('Special usable group rules'), {
      target: { value: '{"vip":{"+:default":"Standard"}}' },
    })
    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    expect(JSON.parse(onSave.mock.calls[0][0].GroupSpecialUsableGroup)).toEqual(
      { vip: { '+:default': 'Standard' } }
    )
  }
)

it('rejects a visual extra-visible and hidden conflict and saves after removing one rule', async () => {
  const user = userEvent.setup()
  const onSave = vi.fn(async (_values: typeof defaults) => {})
  render(
    <Fixture
      onSave={onSave}
      initial={{ GroupSpecialUsableGroup: '{"vip":{"+:default":"Standard"}}' }}
    />
  )
  await user.click(screen.getByRole('tab', { name: 'Group visibility' }))
  await user.click(screen.getByRole('button', { name: 'Add rule' }))
  await user.click(screen.getAllByRole('combobox', { name: 'Group name' })[1])
  await user.click(await screen.findByRole('option', { name: 'default' }))
  await user.click(
    screen.getAllByRole('combobox', { name: 'Group visibility' })[1]
  )
  await user.click(await screen.findByRole('option', { name: 'Hidden' }))
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Group default cannot be both extra visible and hidden for vip. Remove one rule before saving.'
  )
  expect(onSave).not.toHaveBeenCalled()
  await user.click(screen.getAllByRole('button', { name: 'Delete rule' })[1])
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  expect(JSON.parse(onSave.mock.calls[0][0].GroupSpecialUsableGroup)).toEqual({
    vip: { '+:default': 'Standard' },
  })
})

it('cancels and re-adds an unfinished group without inventing visibility grants or dropping existing empty entries', async () => {
  const user = userEvent.setup()
  const onSave = vi.fn(async (_values: typeof defaults) => {})
  render(
    <Fixture
      onSave={onSave}
      initial={{ GroupSpecialUsableGroup: '{"vip":{}}' }}
    />
  )
  await user.click(screen.getByRole('tab', { name: 'Group visibility' }))
  await user.click(
    screen.getByRole('combobox', { name: 'Add rules for a user group' })
  )
  await user.click(await screen.findByRole('option', { name: 'default' }))
  await user.click(screen.getByRole('button', { name: 'Remove default' }))
  expect(screen.getByRole('button', { name: 'Rules for vip' })).toBeVisible()
  await user.click(
    screen.getByRole('combobox', { name: 'Add rules for a user group' })
  )
  await user.click(await screen.findByRole('option', { name: 'default' }))
  await user.click(screen.getByRole('button', { name: 'Switch to JSON' }))
  await user.click(screen.getByRole('button', { name: 'Switch to Visual' }))
  expect(
    screen.getByRole('button', { name: 'Rules for default' })
  ).toBeVisible()
  expect(
    screen.queryByRole('combobox', { name: 'Group name' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  expect(JSON.parse(onSave.mock.calls[0][0].GroupSpecialUsableGroup)).toEqual({
    vip: {},
    default: {},
  })
})

it('a reserved definition rejection restores save with stable memo props and permits a corrected resubmission', async () => {
  const user = userEvent.setup()
  const onSave = vi.fn(async (_values: typeof defaults) => {})
  render(<Fixture onSave={onSave} />)
  await user.click(screen.getByRole('button', { name: 'Switch to JSON' }))
  fireEvent.input(screen.getByLabelText('Group ratios'), {
    target: { value: '{"vip":0.8}' },
  })
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))

  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Save group settings' })
    ).toBeEnabled()
  )
  expect(
    screen.getByText(
      'The reserved groups cannot be renamed or removed: default'
    )
  ).toBeVisible()
  expect(onSave).not.toHaveBeenCalled()

  fireEvent.input(screen.getByLabelText('Group ratios'), {
    target: { value: defaults.GroupRatio },
  })
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Save group settings' })
    ).toBeEnabled()
  )
})

it('a visibility conflict rejection keeps save usable with stable memo props and permits a corrected resubmission', async () => {
  const user = userEvent.setup()
  const onSave = vi.fn(async (_values: typeof defaults) => {})
  render(<Fixture onSave={onSave} />)
  await user.click(screen.getByRole('button', { name: 'Switch to JSON' }))
  fireEvent.input(screen.getByLabelText('Special usable group rules'), {
    target: { value: '{"vip":{"+:default":"Standard","-:default":"remove"}}' },
  })
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))

  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Save group settings' })
    ).toBeEnabled()
  )
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Group default cannot be both extra visible and hidden for vip. Remove one rule before saving.'
  )
  expect(onSave).not.toHaveBeenCalled()

  fireEvent.input(screen.getByLabelText('Special usable group rules'), {
    target: { value: '{"vip":{"+:default":"Standard"}}' },
  })
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Save group settings' })
    ).toBeEnabled()
  )
  expect(JSON.parse(onSave.mock.calls[0][0].GroupSpecialUsableGroup)).toEqual({
    vip: { '+:default': 'Standard' },
  })
})
