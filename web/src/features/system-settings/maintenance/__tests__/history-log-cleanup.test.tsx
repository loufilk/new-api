import { act, fireEvent, render, screen } from '@testing-library/react'
import { toast } from 'sonner'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import * as api from '../../api'
import type { LogCleanupTask } from '../../types'
import { HistoryLogCleanup } from '../history-log-cleanup'

vi.mock('../../api', () => ({
  getCurrentLogCleanupTask: vi.fn(),
  getSystemTask: vi.fn(),
  startLogCleanupTask: vi.fn(),
}))
vi.mock('@/components/datetime-picker', () => ({ DateTimePicker: () => null }))

function task(
  taskId: string,
  status: LogCleanupTask['status'],
  progress: number,
  cutoff = 13,
  updatedAt = progress
): LogCleanupTask {
  return {
    id: 1,
    task_id: taskId,
    type: 'log_cleanup',
    status,
    created_at: 12,
    updated_at: updatedAt,
    payload: { target_timestamp: cutoff, batch_size: 100 },
    state: {
      progress,
      processed: progress,
      total: 100,
      remaining: 100 - progress,
    },
  }
}

const flush = () =>
  act(async () => {
    await Promise.resolve()
  })
const advance = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms))
const success = <T,>(data: T) => ({ success: true, message: '', data })

async function startCleanup() {
  fireEvent.click(screen.getByRole('button', { name: 'Clean logs' }))
  await flush()
  fireEvent.click(screen.getByRole('button', { name: 'Delete logs' }))
  await flush()
}

beforeEach(() => {
  sessionStorage.clear()
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

test('reload reconciles lost acknowledgement through original task ID without a second delete', async () => {
  vi.spyOn(api, 'getCurrentLogCleanupTask').mockResolvedValue(success(null))
  const start = vi
    .spyOn(api, 'startLogCleanupTask')
    .mockRejectedValue(new Error('lost acknowledgement'))
  const check = vi
    .spyOn(api, 'getSystemTask')
    .mockRejectedValue(new Error('read temporarily unavailable'))
  const view = render(<HistoryLogCleanup />)
  await flush()
  await startCleanup()
  await advance(0)
  const [cutoff, taskId] = start.mock.calls[0]
  expect(taskId).toMatch(/^systask_[0-9a-f-]{36}$/)
  expect(
    screen.queryByRole('button', { name: 'Delete logs' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Clean logs' })).toBeDisabled()
  expect(screen.getByText(/Could not confirm the log cleanup/)).toBeVisible()

  view.unmount()
  check.mockResolvedValue(success(task(taskId, 'succeeded', 100, cutoff)))
  render(<HistoryLogCleanup />)
  await advance(0)
  expect(screen.getByText('100%')).toBeVisible()
  expect(check).toHaveBeenLastCalledWith(taskId, expect.any(AbortSignal))
  expect(start).toHaveBeenCalledTimes(1)
})

test('known pre-admission refusal exits confirmation instead of keeping unknown task', async () => {
  vi.spyOn(api, 'getCurrentLogCleanupTask').mockResolvedValue(success(null))
  vi.spyOn(api, 'startLogCleanupTask').mockRejectedValue({
    response: { status: 409, data: { code: 'log_cleanup_task_id_conflict' } },
  })
  const check = vi.spyOn(api, 'getSystemTask')
  render(<HistoryLogCleanup />)
  await flush()
  await startCleanup()
  await advance(0)
  expect(
    screen.queryByText(/Could not confirm the log cleanup/)
  ).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Clean logs' })).toBeEnabled()
  expect(check).not.toHaveBeenCalled()
})

test.each([true, false])(
  'another cutoff is not a success; typed refusal is %s',
  async (definite) => {
    vi.spyOn(api, 'getCurrentLogCleanupTask').mockResolvedValue(success(null))
    vi.spyOn(api, 'startLogCleanupTask').mockResolvedValue({
      ...success(task('other-id', 'running', 34)),
      success: !definite,
      code: definite ? 'log_cleanup_already_running' : undefined,
    })
    vi.spyOn(api, 'getSystemTask').mockRejectedValue(new Error('not found'))
    const started = vi.spyOn(toast, 'success')
    render(<HistoryLogCleanup />)
    await flush()
    await startCleanup()
    await advance(0)
    expect(started).not.toHaveBeenCalled()
    if (definite) {
      expect(screen.getByText('34%')).toBeVisible()
      expect(
        screen.queryByText(/Could not confirm the log cleanup/)
      ).not.toBeInTheDocument()
    } else {
      expect(screen.queryByText('34%')).not.toBeInTheDocument()
      expect(
        screen.getByText(/Could not confirm the log cleanup/)
      ).toBeVisible()
    }
  }
)

test('polling is serial, does not apply stale progress, and recovers after a read failure', async () => {
  vi.spyOn(api, 'getCurrentLogCleanupTask').mockResolvedValue(
    success(task('known-id', 'running', 20))
  )
  let reply: (
    task: Awaited<ReturnType<typeof api.getSystemTask>>
  ) => void = () => {}
  const check = vi
    .spyOn(api, 'getSystemTask')
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          reply = resolve
        })
    )
    .mockResolvedValueOnce(success(task('known-id', 'running', 30)))
    .mockRejectedValueOnce(new Error('read temporarily unavailable'))
    .mockResolvedValueOnce(success(task('known-id', 'succeeded', 100)))
  render(<HistoryLogCleanup />)
  await flush()
  await advance(3_000)
  expect(check).toHaveBeenCalledTimes(1)
  await act(async () => reply(success(task('known-id', 'running', 80))))
  await advance(1_000)
  expect(screen.getByText('80%')).toBeVisible()
  expect(screen.queryByText('30%')).not.toBeInTheDocument()
  await advance(1_000)
  expect(screen.getByText('Could not check log cleanup status.')).toBeVisible()
  await advance(2_000)
  expect(screen.getByText('100%')).toBeVisible()
  expect(
    screen.queryByText('Could not check log cleanup status.')
  ).not.toBeInTheDocument()
})
