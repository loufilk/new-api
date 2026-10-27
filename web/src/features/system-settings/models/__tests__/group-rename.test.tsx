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
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { assert, expect, test, vi } from 'vitest'

import { SettingsPageProvider } from '../../components/settings-page-context'
import type { GroupRenameMapping } from '../../types'
import { GroupRatioForm, type GroupFormValues } from '../group-ratio-form'
import {
  GroupRatioVisualEditor,
  type GroupSettingsSection,
} from '../group-ratio-visual-editor'
import {
  applyGroupRenamesToValues,
  collectGroupRenamePlan,
  collectGroupRenames,
  type GroupRenameEditableValues,
  type GroupRatioSaveOutcome,
} from '../group-rename'

test('collectGroupRenames reports only explicit non-reserved original-key changes', () => {
  expect(
    collectGroupRenames([
      { originalName: 'default', name: 'standard' },
      { originalName: 'vip', name: 'vip' },
      { originalName: null, name: 'fresh' },
      { originalName: 'svip', name: ' svip2 ' },
      { originalName: 'vip2', name: 'auto' },
    ])
  ).toEqual([{ from: 'svip', to: 'svip2' }])
})

test('applyGroupRenamesToValues rewrites every group-keyed JSON field', () => {
  const renamed = applyGroupRenamesToValues(
    {
      GroupRatio: '{"old":0.8,"keep":1}',
      TopupGroupRatio: '{"old":1.2}',
      UserUsableGroups: '{"old":"Old group","keep":"Keep"}',
      GroupGroupRatio: '{"old":{"keep":0.5,"old":0.4},"keep":{"old":0.9}}',
      AutoGroups: '["old","keep"]',
      GroupSpecialUsableGroup:
        '{"old":{ "+:keep":"k","-:old":"h","plain":"p"},"keep":{ "+:old":"o"}}',
    },
    [{ from: 'old', to: 'new' }]
  )

  expect(JSON.parse(renamed.GroupRatio)).toEqual({ new: 0.8, keep: 1 })
  expect(JSON.parse(renamed.TopupGroupRatio)).toEqual({ new: 1.2 })
  expect(JSON.parse(renamed.UserUsableGroups)).toEqual({
    new: 'Old group',
    keep: 'Keep',
  })
  expect(JSON.parse(renamed.GroupGroupRatio)).toEqual({
    keep: { new: 0.9 },
    new: { keep: 0.5, new: 0.4 },
  })
  expect(JSON.parse(renamed.AutoGroups)).toEqual(['new', 'keep'])
  expect(JSON.parse(renamed.GroupSpecialUsableGroup)).toEqual({
    keep: { '+:new': 'o' },
    new: { '+:keep': 'k', '-:new': 'h', plain: 'p' },
  })
})

const BASELINE_VALUES: GroupRenameEditableValues = {
  GroupRatio: '{"vip":1}',
  TopupGroupRatio: '{}',
  UserUsableGroups: '{"vip":"VIP"}',
  GroupGroupRatio: '{}',
  AutoGroups: '[]',
  GroupSpecialUsableGroup: '{}',
}

test('collectGroupRenamePlan requires an explicit mapping for raw replacements', () => {
  const plan = collectGroupRenamePlan(
    BASELINE_VALUES,
    { ...BASELINE_VALUES, GroupRatio: '{"gold":1}' },
    [],
    {}
  )

  expect(plan.reservedViolations).toEqual([])
  expect(plan.unresolved).toEqual([
    { field: 'GroupRatio', key: 'vip', targets: ['gold'] },
  ])
})

test('collectGroupRenamePlan accepts explicit mappings and plain removals', () => {
  const submitted = { ...BASELINE_VALUES, GroupRatio: '{"gold":1}' }

  expect(
    collectGroupRenamePlan(BASELINE_VALUES, submitted, [], {
      vip: 'gold',
    }).unresolved
  ).toEqual([])
  expect(
    collectGroupRenamePlan(
      BASELINE_VALUES,
      submitted,
      [{ from: 'vip', to: 'gold' }],
      {}
    ).unresolved
  ).toEqual([])

  // A removal without a same-option addition is a plain deletion and is left
  // to the server's reference check instead of being guessed as a rename.
  expect(
    collectGroupRenamePlan(
      BASELINE_VALUES,
      { ...BASELINE_VALUES, GroupRatio: '{}' },
      [],
      {}
    ).unresolved
  ).toEqual([])
})

test('collectGroupRenamePlan reports reserved group removals separately', () => {
  const baseline = { ...BASELINE_VALUES, GroupRatio: '{"default":1,"vip":1}' }
  const plan = collectGroupRenamePlan(
    baseline,
    { ...baseline, GroupRatio: '{"standard":1,"vip":1}' },
    [],
    {}
  )

  expect(plan.reservedViolations).toEqual(['default'])
  expect(plan.unresolved).toEqual([])
})

test.each([
  [
    'UserUsableGroups',
    '{"default":"Default","vip":"VIP"}',
    '{"enterprise":"Enterprise"}',
  ],
  ['AutoGroups', '["default","vip"]', '["enterprise"]'],
  [
    'GroupSpecialUsableGroup',
    '{"default":{"+:vip":"VIP"},"vip":{}}',
    '{"enterprise":{"+:default":"Authorized access"}}',
  ],
  ['TopupGroupRatio', '{"default":1,"vip":1.2}', '{"enterprise":1.1}'],
  [
    'GroupGroupRatio',
    '{"default":{"vip":0.8}}',
    '{"enterprise":{"default":0.9}}',
  ],
] as const)(
  'changing %s references does not request a definition rename or forbid removing default',
  (field, before, after) => {
    const baseline = {
      ...BASELINE_VALUES,
      GroupRatio: '{"default":1,"vip":1,"enterprise":1}',
      [field]: before,
    }

    expect(
      collectGroupRenamePlan(baseline, { ...baseline, [field]: after }, [], {})
    ).toEqual({
      unresolved: [],
      reservedViolations: [],
    })
  }
)

function PricingFixture(props: {
  onRenamesChange: (renames: GroupRenameMapping[]) => void
}) {
  const [section, setSection] = useState<GroupSettingsSection>('pricing')
  const [settings, setSettings] = useState<Record<string, string>>({
    GroupRatio: '{"default":1,"vip":1}',
    TopupGroupRatio: '{}',
    UserUsableGroups: '{"default":"Default","vip":"VIP"}',
  })
  return (
    <GroupRatioVisualEditor
      section={section}
      onSectionChange={setSection}
      defaultUseAutoGroupField={null}
      groupRatio={settings.GroupRatio}
      topupGroupRatio={settings.TopupGroupRatio}
      userUsableGroups={settings.UserUsableGroups}
      groupGroupRatio='{}'
      autoGroups='[]'
      maxTokenAutoGroupsField={null}
      groupSpecialUsableGroup='{}'
      onChange={(field, value) =>
        setSettings((current) => ({ ...current, [field]: value }))
      }
      onRenamesChange={props.onRenamesChange}
    />
  )
}

test('renaming a priced group exposes an explicit rename from its original key', async () => {
  const onRenamesChange = vi.fn()
  render(<PricingFixture onRenamesChange={onRenamesChange} />)

  fireEvent.change(screen.getByDisplayValue('vip'), {
    target: { value: 'vip2' },
  })

  await waitFor(() =>
    expect(onRenamesChange).toHaveBeenLastCalledWith([
      { from: 'vip', to: 'vip2' },
    ])
  )
})

test('renaming a reserved group is not inferred as a rename', async () => {
  const onRenamesChange = vi.fn()
  render(<PricingFixture onRenamesChange={onRenamesChange} />)

  fireEvent.change(screen.getByDisplayValue('default'), {
    target: { value: 'standard' },
  })

  await waitFor(() =>
    expect(screen.getByDisplayValue('standard')).toBeInTheDocument()
  )
  expect(onRenamesChange.mock.calls.at(-1)?.[0]).toEqual([])
})

test('removing a priced group is not inferred as a rename', async () => {
  const onRenamesChange = vi.fn()
  render(<PricingFixture onRenamesChange={onRenamesChange} />)

  const defaultRow = screen.getByDisplayValue('default').closest('tr')
  assert(defaultRow)
  fireEvent.click(within(defaultRow).getByRole('button', { name: 'Delete' }))

  await waitFor(() => expect(onRenamesChange).toHaveBeenLastCalledWith([]))
})

test('a newly added group is not reported as a rename', async () => {
  const onRenamesChange = vi.fn()
  render(<PricingFixture onRenamesChange={onRenamesChange} />)

  fireEvent.click(screen.getAllByRole('button', { name: 'Add group' })[0])
  const newRow = screen.getByDisplayValue('group_1').closest('tr')
  assert(newRow)
  fireEvent.change(within(newRow).getByDisplayValue('group_1'), {
    target: { value: 'fresh' },
  })

  await waitFor(() => expect(onRenamesChange).toHaveBeenLastCalledWith([]))
})

const GROUP_FORM_VALUES: GroupFormValues = {
  GroupRatio: '{\n  "default": 1,\n  "vip": 1\n}',
  TopupGroupRatio: '{}',
  UserUsableGroups: '{\n  "default": "Default",\n  "vip": "VIP"\n}',
  GroupGroupRatio: '{}',
  AutoGroups: '["default"]',
  MaxTokenAutoGroups: 5,
  DefaultUseAutoGroup: false,
  GroupSpecialUsableGroup: '{}',
}

function baselineFrom(values: GroupFormValues): GroupRenameEditableValues {
  return {
    GroupRatio: values.GroupRatio,
    TopupGroupRatio: values.TopupGroupRatio,
    UserUsableGroups: values.UserUsableGroups,
    GroupGroupRatio: values.GroupGroupRatio,
    AutoGroups: values.AutoGroups,
    GroupSpecialUsableGroup: values.GroupSpecialUsableGroup,
  }
}

function GroupFormFixture(props: {
  onSave: (
    values: GroupFormValues,
    renames: GroupRenameMapping[]
  ) => Promise<GroupRatioSaveOutcome>
  values?: GroupFormValues
  baseline?: GroupRenameEditableValues
}) {
  const values = props.values ?? GROUP_FORM_VALUES
  const form = useForm<GroupFormValues>({ defaultValues: values })
  const [actionsContainer] = useState(() => {
    const element = document.createElement('div')
    document.body.appendChild(element)
    return element
  })
  useEffect(() => () => actionsContainer.remove(), [actionsContainer])

  return (
    <SettingsPageProvider actionsContainer={actionsContainer}>
      <GroupRatioForm
        form={form}
        baseline={props.baseline ?? baselineFrom(values)}
        onSave={props.onSave}
        isSaving={false}
      />
    </SettingsPageProvider>
  )
}

function createSaveSpy() {
  const calls: Array<{
    values: GroupFormValues
    renames: GroupRenameMapping[]
  }> = []
  const onSave = async (
    values: GroupFormValues,
    renames: GroupRenameMapping[]
  ) => {
    calls.push({ values, renames })
    return 'saved' as const
  }
  return { calls, onSave }
}

test('saving group changes disables repeated submissions until the result arrives', async () => {
  let finishSave: (outcome: GroupRatioSaveOutcome) => void = () => {}
  const pendingSave = new Promise<GroupRatioSaveOutcome>((resolve) => {
    finishSave = resolve
  })
  const onSave = vi.fn(() => pendingSave)
  render(<GroupFormFixture onSave={onSave} baseline={GROUP_FORM_VALUES} />)

  fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  const saving = screen.getByRole('button', { name: 'Saving...' })
  expect(saving).toBeDisabled()
  fireEvent.click(saving)
  expect(onSave).toHaveBeenCalledTimes(1)

  await act(async () => finishSave('saved'))
  expect(
    screen.getByRole('button', { name: 'Save group settings' })
  ).toBeEnabled()
})

test('saving in visual mode forwards explicit renames', async () => {
  const { calls, onSave } = createSaveSpy()
  render(<GroupFormFixture onSave={onSave} />)

  fireEvent.change(screen.getByDisplayValue('vip'), {
    target: { value: 'vip2' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))

  await waitFor(() => expect(calls).toHaveLength(1))
  expect(calls[0].renames).toEqual([{ from: 'vip', to: 'vip2' }])
  expect(JSON.parse(calls[0].values.GroupRatio)).toEqual({
    default: 1,
    vip2: 1,
  })
  expect(JSON.parse(calls[0].values.UserUsableGroups)).toEqual({
    default: 'Default',
    vip2: 'VIP',
  })
})

test('a pending rename survives a mode round-trip and is still saved', async () => {
  const { calls, onSave } = createSaveSpy()
  render(<GroupFormFixture onSave={onSave} />)

  fireEvent.change(screen.getByDisplayValue('vip'), {
    target: { value: 'vip2' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Switch to JSON' }))
  fireEvent.click(screen.getByRole('button', { name: 'Switch to Visual' }))

  expect(screen.getByDisplayValue('vip2')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))

  await waitFor(() => expect(calls).toHaveLength(1))
  expect(calls[0].renames).toEqual([{ from: 'vip', to: 'vip2' }])
  expect(JSON.parse(calls[0].values.GroupRatio)).toEqual({
    default: 1,
    vip2: 1,
  })
})

test('JSON mode raw replacement blocks save until the mapping is resolved', async () => {
  const values: GroupFormValues = {
    ...GROUP_FORM_VALUES,
    GroupRatio: '{"vip":1}',
    UserUsableGroups: '{"vip":"VIP"}',
    AutoGroups: '[]',
  }
  const { calls, onSave } = createSaveSpy()
  render(<GroupFormFixture onSave={onSave} values={values} />)

  fireEvent.click(screen.getByRole('button', { name: 'Switch to JSON' }))
  fireEvent.input(screen.getByLabelText('Group ratios'), {
    target: { value: '{"gold":1}' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))

  await waitFor(() =>
    expect(screen.getByText('vip was removed')).toBeInTheDocument()
  )
  expect(
    screen.getByRole('alert', { name: 'Resolve group changes' })
  ).toBeInTheDocument()
  expect(calls).toHaveLength(0)
  expect(
    screen.getByText('Resolve the group changes before saving.')
  ).toBeInTheDocument()
})

test('JSON mode reserved group rename blocks save', async () => {
  const { calls, onSave } = createSaveSpy()
  render(<GroupFormFixture onSave={onSave} />)

  fireEvent.click(screen.getByRole('button', { name: 'Switch to JSON' }))
  fireEvent.input(screen.getByLabelText('Group ratios'), {
    target: { value: '{"standard":1,"vip":1}' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))

  await waitFor(() =>
    expect(
      screen.getByText(
        'The reserved groups cannot be renamed or removed: default'
      )
    ).toBeInTheDocument()
  )
  expect(
    screen.getByText('Resolve the group changes before saving.')
  ).toBeInTheDocument()
  expect(calls).toHaveLength(0)
})

test('revoking user selectable default saves without renaming or removing its definition', async () => {
  const { calls, onSave } = createSaveSpy()
  render(<GroupFormFixture onSave={onSave} />)
  const row = screen.getByDisplayValue('default').closest('tr')
  assert(row)
  fireEvent.click(within(row).getByRole('checkbox'))
  fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))

  await waitFor(() => expect(calls).toHaveLength(1))
  expect(calls[0].renames).toEqual([])
  expect(JSON.parse(calls[0].values.GroupRatio)).toEqual({ default: 1, vip: 1 })
  expect(JSON.parse(calls[0].values.UserUsableGroups)).toEqual({ vip: 'VIP' })
})

test('replacing permission references in JSON saves without fabricated enterprise rename mappings', async () => {
  const { calls, onSave } = createSaveSpy()
  const values = {
    ...GROUP_FORM_VALUES,
    GroupRatio: '{"default":1,"vip":1,"enterprise":1}',
    GroupSpecialUsableGroup: '{"vip":{"+:default":"Default"}}',
  }
  render(<GroupFormFixture onSave={onSave} values={values} />)
  fireEvent.click(screen.getByRole('button', { name: 'Switch to JSON' }))
  fireEvent.input(screen.getByLabelText('Selectable groups'), {
    target: { value: '{"enterprise":"Enterprise"}' },
  })
  fireEvent.input(screen.getByLabelText('Auto group order'), {
    target: { value: '["enterprise"]' },
  })
  fireEvent.input(screen.getByLabelText('Special usable group rules'), {
    target: { value: '{"enterprise":{"+:default":"Authorized access"}}' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))

  await waitFor(() => expect(calls).toHaveLength(1))
  expect(calls[0].renames).toEqual([])
  expect(JSON.parse(calls[0].values.GroupRatio)).toEqual({
    default: 1,
    vip: 1,
    enterprise: 1,
  })
  expect(JSON.parse(calls[0].values.GroupSpecialUsableGroup)).toEqual({
    enterprise: { '+:default': 'Authorized access' },
  })
})

test('pending renames do not crash the conflict preview while unrelated JSON is temporarily invalid', async () => {
  const { calls, onSave } = createSaveSpy()
  render(<GroupFormFixture onSave={onSave} />)
  fireEvent.change(screen.getByDisplayValue('vip'), {
    target: { value: 'enterprise' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Switch to JSON' }))
  fireEvent.input(screen.getByLabelText('Auto group order'), {
    target: { value: '{}' },
  })
  expect(screen.getByLabelText('Auto group order')).toHaveValue('{}')
  fireEvent.input(screen.getByLabelText('Auto group order'), {
    target: { value: '["default"]' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(calls).toHaveLength(1))
  expect(calls[0].renames).toEqual([{ from: 'vip', to: 'enterprise' }])
})

test.each([
  [
    'target groups',
    '{"vip":{"+:vip":"VIP","-:enterprise":"remove"}}',
    'enterprise',
  ],
  [
    'user groups',
    '{"vip":{"+:default":"Standard"},"enterprise":{"-:default":"remove"}}',
    'default',
  ],
])(
  'pending renames reject visibility conflicts between renamed %s',
  async (_label, rules, target) => {
    const { calls, onSave } = createSaveSpy()
    render(
      <GroupFormFixture
        onSave={onSave}
        values={{
          ...GROUP_FORM_VALUES,
          GroupSpecialUsableGroup: rules,
        }}
      />
    )
    fireEvent.change(
      within(
        screen.getByRole('table', { name: 'Pricing groups' })
      ).getByDisplayValue('vip'),
      {
        target: { value: 'enterprise' },
      }
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      `Group ${target} cannot be both extra visible and hidden for enterprise. Remove one rule before saving.`
    )
    fireEvent.click(screen.getByRole('button', { name: 'Save group settings' }))
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save group settings' })
      ).toBeEnabled()
    )
    expect(calls).toHaveLength(0)
  }
)
