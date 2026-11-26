import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { PartnerApplicationForm } from '../application-form'

afterEach(() => vi.restoreAllMocks())

function renderForm() {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <PartnerApplicationForm owner={8} disabled={false} />
    </QueryClientProvider>
  )
}

test('a supplier retry after a transport failure reuses its idempotency key', async () => {
  const user = userEvent.setup()
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce(new Error('Connection interrupted'))
    .mockResolvedValueOnce({
      data: { success: true, data: { id: 14 } },
    })

  renderForm()

  await user.type(screen.getByLabelText(/Site name/), 'Example Provider')
  await user.type(screen.getByLabelText(/Contact details/), 'wx-example')
  await user.type(screen.getByLabelText(/Group name/), 'standard')
  await user.click(
    screen.getByRole('button', { name: 'Submit partner application' })
  )
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))

  await user.click(
    screen.getByRole('button', { name: 'Submit partner application' })
  )
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))

  const firstCall = post.mock.calls[0]
  const secondCall = post.mock.calls[1]
  expect(firstCall?.[0]).toBe('/api/partner-applications')
  expect(secondCall?.[0]).toBe(firstCall?.[0])
  expect(secondCall?.[1]).toEqual(firstCall?.[1])
  expect(secondCall?.[2]).toEqual(firstCall?.[2])
})

test('applicant can search the platform catalog and submit the chosen wire ID and contact', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { id: 15 } },
  })
  renderForm()

  const contact = screen.getByLabelText(/Contact method/)
  expect(contact).toHaveValue('wechat')
  expect(
    within(contact)
      .getAllByRole('option')
      .map((option) => option.textContent)
  ).toEqual(['WeChat', 'Telegram', 'QQ', 'Email', 'Other'])
  await user.selectOptions(contact, 'telegram')
  const platform = screen.getByRole('combobox', { name: /Platform/ })
  expect(platform).toHaveValue('OpenAI')
  await user.click(platform)
  const options = within(screen.getByRole('listbox')).getAllByRole('option')
  expect(options).toHaveLength(51)
  expect(options.slice(0, 5).map((option) => option.textContent)).toEqual([
    'OpenAI',
    'Anthropic',
    'Gemini',
    'AWS',
    'DeepSeek',
  ])
  await user.type(platform, 'zhipu')
  expect(
    screen.queryByRole('option', { name: 'OpenAI' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('option', { name: 'Zhipu GLM' }))
  await user.type(screen.getByLabelText(/Site name/), 'Example Provider')
  await user.type(screen.getByLabelText(/Contact details/), '@example')
  await user.type(screen.getByLabelText(/Group name/), 'fast')
  await user.type(
    screen.getByLabelText(/Group description/),
    'line one{Enter}line two'
  )
  await user.click(
    screen.getByRole('button', { name: 'Submit partner application' })
  )
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  expect(post.mock.calls[0]?.[1]).toMatchObject({
    contact_type: 'telegram',
    quotes: [
      {
        platform: 'zhipu-glm',
        group: 'fast',
        rate: '1',
        models: '',
        description: 'line one\nline two',
      },
    ],
  })
})

test('adding a group focuses it; removing it leaves the original intact and disables removal', async () => {
  const user = userEvent.setup()
  renderForm()
  expect(screen.getByRole('button', { name: 'Remove group' })).toBeDisabled()
  await user.type(screen.getByLabelText(/Group name/), 'original')
  await user.click(screen.getByRole('button', { name: 'Add group' }))
  expect(screen.getAllByLabelText(/Group name/)[1]).toHaveFocus()
  expect(screen.getAllByRole('spinbutton')[1]).toHaveValue(1)
  await user.click(screen.getAllByRole('button', { name: 'Remove group' })[1])
  expect(screen.getByLabelText(/Group name/)).toHaveValue('original')
  expect(screen.getByRole('button', { name: 'Remove group' })).toBeDisabled()
})

test('keeps removable tags and pending text in the legacy model string without submitting on Enter', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { id: 16 } },
  })
  renderForm()
  await user.type(screen.getByLabelText(/Site name/), 'Example Provider')
  await user.type(screen.getByLabelText(/Contact details/), 'wx-example')
  await user.type(screen.getByLabelText(/Group name/), 'intake')
  const models = screen.getByLabelText('Main models')
  await user.type(models, 'gpt-4o{Enter}gemini-2.5-flash{Enter}gpt-4o{Enter}')
  expect(post).not.toHaveBeenCalled()
  expect(models).toHaveValue('')
  expect(screen.getAllByRole('button', { name: 'Remove gpt-4o' })).toHaveLength(
    1
  )
  await user.click(
    screen.getByRole('button', { name: 'Remove gemini-2.5-flash' })
  )
  await user.type(models, 'claude-sonnet-4-5')
  fireEvent.keyDown(models, { key: 'Enter', isComposing: true })
  expect(models).toHaveValue('claude-sonnet-4-5')
  expect(post).not.toHaveBeenCalled()
  // Submit without blurring or pressing Enter. The pending text is still sent.
  const form = models.closest('form')
  expect(form).not.toBeNull()
  if (!form) return
  fireEvent.submit(form)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  expect(post.mock.calls[0]?.[1]).toMatchObject({
    quotes: [{ group: 'intake', models: 'gpt-4o, claude-sonnet-4-5' }],
  })
  await waitFor(() =>
    expect(screen.queryByRole('button', { name: 'Remove gpt-4o' })).toBeNull()
  )
  expect(screen.getByLabelText('Main models')).toHaveValue('')
})

test('validates combined committed and pending models instead of silently truncating them', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { id: 16 } },
  })
  renderForm()
  await user.type(screen.getByLabelText(/Site name/), 'Example Provider')
  await user.type(screen.getByLabelText(/Contact details/), 'example')
  await user.type(screen.getByLabelText(/Group name/), 'intake')
  const models = screen.getByLabelText('Main models')
  await user.type(models, 'gpt-4o{Enter}')
  const longModel = 'a'.repeat(501)
  fireEvent.change(models, { target: { value: longModel } })
  await user.click(
    screen.getByRole('button', { name: 'Submit partner application' })
  )
  await waitFor(() => expect(models).toHaveAttribute('aria-invalid', 'true'))
  expect(models).toHaveValue(longModel)
  expect(post).not.toHaveBeenCalled()
})

test('preserves the 500-character legacy boundary with compact separators', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { id: 17 } },
  })
  renderForm()
  expect(screen.getByLabelText('Notes')).toHaveAttribute(
    'placeholder',
    'Top-up rates, settlement terms, or anything else you would like to share.'
  )
  await user.type(screen.getByLabelText(/Site name/), 'Example Provider')
  await user.type(screen.getByLabelText(/Contact details/), 'wx-example')
  await user.type(screen.getByLabelText(/Group name/), 'intake')
  const models = screen.getByLabelText('Main models')
  const longModelName = 'a'.repeat(498)
  fireEvent.change(models, { target: { value: longModelName } })
  await user.type(models, '{Enter}b')
  const form = models.closest('form')
  expect(form).not.toBeNull()
  if (!form) return
  fireEvent.submit(form)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  expect(post.mock.calls[0]?.[1]).toMatchObject({
    quotes: [{ group: 'intake', models: `${longModelName},b` }],
  })
})
