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
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { useForm, type UseFormReturn } from 'react-hook-form'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import { useResetForm } from '../hooks/use-reset-form'
import { useSettingsForm } from '../hooks/use-settings-form'

type Values = { first: string; second: string }
type FixtureProps = {
  defaults: Values
  onSubmit: (data: Values, changed?: Record<string, unknown>) => Promise<void>
}

function Fields(props: {
  form: UseFormReturn<Values>
  onSubmit: (event: React.FormEvent) => Promise<void>
  onReset: () => void
}) {
  const [error, setError] = useState('')
  const state = props.form.formState
  return (
    <form
      onSubmit={(event) => {
        setError('')
        void props.onSubmit(event).catch(() => setError('Save rejected'))
      }}
    >
      <Input aria-label='First' {...props.form.register('first')} />
      <Input aria-label='Second' {...props.form.register('second')} />
      <output aria-label='Dirty'>{state.isDirty ? 'dirty' : 'saved'}</output>
      <output aria-label='Saving'>
        {state.isSubmitting ? 'saving' : 'idle'}
      </output>
      <output aria-label='Error'>{error}</output>
      <Button type='submit' disabled={state.isSubmitting}>
        Save
      </Button>
      <Button type='button' onClick={props.onReset}>
        Reset
      </Button>
    </form>
  )
}

function ResetFixture(props: FixtureProps) {
  const form = useForm<Values>({ defaultValues: props.defaults })
  useResetForm(form, props.defaults)
  return (
    <Fields
      form={form}
      onSubmit={form.handleSubmit((data) => props.onSubmit(data))}
      onReset={() => form.reset()}
    />
  )
}

function SettingsFixture(props: FixtureProps) {
  const settings = useSettingsForm<Values>({
    defaultValues: props.defaults,
    onSubmit: props.onSubmit,
  })
  return (
    <Fields
      form={settings.form}
      onSubmit={settings.handleSubmit}
      onReset={settings.handleReset}
    />
  )
}

function AsyncSettingsFixture(props: { loadDefaults: () => Promise<Values> }) {
  const settings = useSettingsForm<Values>({
    defaultValues: props.loadDefaults,
    onSubmit: async () => undefined,
  })
  return (
    <>
      <output aria-label='Loading defaults'>
        {settings.form.formState.isLoading ? 'loading' : 'ready'}
      </output>
      <Fields
        form={settings.form}
        onSubmit={settings.handleSubmit}
        onReset={settings.handleReset}
      />
    </>
  )
}

afterEach(cleanup)

const defaults = { first: 'old-first', second: 'old-second' }

it('leaves asynchronous default initialization with react-hook-form', async () => {
  let resolveDefaults!: (values: Values) => void
  const pending = new Promise<Values>((resolve) => {
    resolveDefaults = resolve
  })
  const loadDefaults = vi.fn(() => pending)
  render(<AsyncSettingsFixture loadDefaults={loadDefaults} />)
  expect(screen.getByLabelText('Loading defaults')).toHaveTextContent('loading')
  await act(async () => resolveDefaults(defaults))
  await waitFor(() =>
    expect(screen.getByLabelText('Loading defaults')).toHaveTextContent('ready')
  )
  expect(screen.getByRole('textbox', { name: 'First' })).toHaveValue('old-first')
  expect(screen.getByRole('textbox', { name: 'Second' })).toHaveValue('old-second')
  expect(loadDefaults).toHaveBeenCalledTimes(1)
})

describe.each([
  ['legacy settings', ResetFixture],
  ['unified settings', SettingsFixture],
] as const)('%s refetch safety', (_, Fixture) => {
  it('keeps the failed field editable after one saved option refetches during submission', async () => {
    const user = userEvent.setup()
    let rejectSave!: (error: Error) => void
    const pending = new Promise<void>((_, reject) => {
      rejectSave = reject
    })
    const onSubmit = vi
      .fn()
      .mockReturnValueOnce(pending)
      .mockResolvedValue(undefined)
    const view = render(<Fixture defaults={defaults} onSubmit={onSubmit} />)
    await user.clear(screen.getByRole('textbox', { name: 'First' }))
    await user.type(
      screen.getByRole('textbox', { name: 'First' }),
      'saved-first'
    )
    await user.clear(screen.getByRole('textbox', { name: 'Second' }))
    await user.type(
      screen.getByRole('textbox', { name: 'Second' }),
      'retry-second'
    )
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    view.rerender(
      <Fixture
        defaults={{ first: 'saved-first', second: 'old-second' }}
        onSubmit={onSubmit}
      />
    )
    expect(screen.getByRole('textbox', { name: 'Second' })).toHaveValue(
      'retry-second'
    )
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    await act(async () => rejectSave(new Error('second option denied')))
    await waitFor(() =>
      expect(screen.getByLabelText('Error')).toHaveTextContent('Save rejected')
    )
    expect(screen.getByRole('textbox', { name: 'Second' })).toHaveValue(
      'retry-second'
    )
    expect(screen.getByLabelText('Dirty')).toHaveTextContent('dirty')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2))
    expect(onSubmit.mock.calls[1][0]).toEqual({
      first: 'saved-first',
      second: 'retry-second',
    })
    if (Fixture === SettingsFixture) {
      expect(onSubmit.mock.calls[1][1]).toEqual({ second: 'retry-second' })
    }
  })

  it('refreshes untouched fields while retaining edits, then resets to the newest saved values', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    const view = render(<Fixture defaults={defaults} onSubmit={onSubmit} />)
    await user.clear(screen.getByRole('textbox', { name: 'Second' }))
    await user.type(screen.getByRole('textbox', { name: 'Second' }), 'draft')
    view.rerender(
      <Fixture
        defaults={{ first: 'external-first', second: 'external-second' }}
        onSubmit={onSubmit}
      />
    )
    expect(screen.getByRole('textbox', { name: 'First' })).toHaveValue(
      'external-first'
    )
    expect(screen.getByRole('textbox', { name: 'Second' })).toHaveValue('draft')
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    expect(screen.getByRole('textbox', { name: 'Second' })).toHaveValue(
      'external-second'
    )
    expect(screen.getByLabelText('Dirty')).toHaveTextContent('saved')
  })

  it('marks acknowledged edits saved without losing later input', async () => {
    const user = userEvent.setup()
    let finishSave!: () => void
    const onSubmit = vi.fn().mockReturnValue(
      new Promise<void>((resolve) => {
        finishSave = resolve
      })
    )
    const view = render(<Fixture defaults={defaults} onSubmit={onSubmit} />)
    await user.clear(screen.getByRole('textbox', { name: 'Second' }))
    await user.type(
      screen.getByRole('textbox', { name: 'Second' }),
      'submitted'
    )
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    await user.type(screen.getByRole('textbox', { name: 'Second' }), '-later')
    view.rerender(
      <Fixture
        defaults={{ first: 'old-first', second: 'submitted' }}
        onSubmit={onSubmit}
      />
    )
    await act(async () => finishSave())
    await waitFor(() =>
      expect(screen.getByLabelText('Saving')).toHaveTextContent('idle')
    )
    expect(screen.getByRole('textbox', { name: 'Second' })).toHaveValue(
      'submitted-later'
    )
    expect(screen.getByLabelText('Dirty')).toHaveTextContent('dirty')
    view.rerender(
      <Fixture
        defaults={{ first: 'old-first', second: 'submitted-later' }}
        onSubmit={onSubmit}
      />
    )
    expect(screen.getByLabelText('Dirty')).toHaveTextContent('saved')
  })
})

it('does not mark an untouched server update dirty when another field finishes saving', async () => {
  const user = userEvent.setup()
  let finishSave!: () => void
  const onSubmit = vi.fn().mockReturnValue(
    new Promise<void>((resolve) => {
      finishSave = resolve
    })
  )
  const view = render(
    <SettingsFixture defaults={defaults} onSubmit={onSubmit} />
  )
  await user.clear(screen.getByRole('textbox', { name: 'Second' }))
  await user.type(screen.getByRole('textbox', { name: 'Second' }), 'submitted')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
  view.rerender(
    <SettingsFixture
      defaults={{ first: 'external-first', second: 'submitted' }}
      onSubmit={onSubmit}
    />
  )
  await act(async () => finishSave())
  await waitFor(() =>
    expect(screen.getByLabelText('Saving')).toHaveTextContent('idle')
  )
  expect(screen.getByRole('textbox', { name: 'First' })).toHaveValue(
    'external-first'
  )
  expect(screen.getByLabelText('Dirty')).toHaveTextContent('saved')
})
