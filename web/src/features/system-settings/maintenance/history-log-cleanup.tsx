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
import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DateTimePicker } from '@/components/datetime-picker'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorSources } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  getCurrentLogCleanupTask,
  getSystemTask,
  startLogCleanupTask,
} from '../api'
import type { LogCleanupTask } from '../types'

const HOURS_IN_DAY = 24

const getDateHoursAgo = (hours: number) => {
  const date = new Date()
  date.setHours(date.getHours() - hours)
  return date
}

const getDateDaysAgo = (days: number) => getDateHoursAgo(days * HOURS_IN_DAY)

const quickSelectOptions = [
  {
    label: '24 hours ago',
    getValue: () => getDateHoursAgo(24),
  },
  {
    label: '7 days ago',
    getValue: () => getDateDaysAgo(7),
  },
  {
    label: '30 days ago',
    getValue: () => getDateDaysAgo(30),
  },
]

function isActiveLogCleanupTask(task: LogCleanupTask | null) {
  return task?.status === 'pending' || task?.status === 'running'
}

type CleanupAttempt = { taskId: string; cutoff: number }

const cleanupStoragePrefix = 'log-cleanup:awaiting:'

function readCleanupAttempt(scope: string): CleanupAttempt | null {
  try {
    const raw = sessionStorage.getItem(scope)
    if (!raw) return null
    const value = JSON.parse(raw) as Partial<CleanupAttempt>
    if (
      typeof value.taskId === 'string' &&
      /^systask_[0-9a-f-]{36}$/.test(value.taskId) &&
      typeof value.cutoff === 'number' &&
      Number.isSafeInteger(value.cutoff) &&
      value.cutoff > 0
    ) {
      return { taskId: value.taskId, cutoff: value.cutoff }
    }
  } catch {
    // Session storage can be disabled.
  }
  return null
}

function saveCleanupAttempt(scope: string, attempt: CleanupAttempt | null) {
  try {
    if (attempt) sessionStorage.setItem(scope, JSON.stringify(attempt))
    else sessionStorage.removeItem(scope)
    return true
  } catch {
    return false
  }
}

function matchesCleanupAttempt(task: LogCleanupTask, attempt: CleanupAttempt) {
  return (
    task.type === 'log_cleanup' &&
    task.task_id === attempt.taskId &&
    task.payload?.target_timestamp === attempt.cutoff
  )
}

function definiteCleanupRejection(value: unknown) {
  return getServerErrorSources(value).some(
    ({ code }) =>
      code === 'invalid_log_cleanup_task_id' ||
      code === 'invalid_log_cleanup_cutoff' ||
      code === 'log_cleanup_task_id_conflict'
  )
}

/**
 * The history log module of the log maintenance page. It deletes the logs
 * created before a chosen time as a background task and shows its progress.
 */
export function HistoryLogCleanup() {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const scope = cleanupStoragePrefix + (userId ?? '')
  return <HistoryLogCleanupForm key={scope} scope={scope} />
}

function HistoryLogCleanupForm({ scope }: { scope: string }) {
  const { t } = useTranslation()
  const titleId = useId()
  const [purgeDate, setPurgeDate] = useState<Date | undefined>(() =>
    getDateDaysAgo(30)
  )
  const [isStartingLogCleanup, setIsStartingLogCleanup] = useState(false)
  const [logCleanupTask, setLogCleanupTask] = useState<LogCleanupTask | null>(
    null
  )
  const [showConfirmDialog, setShowConfirmDialog] = useState(false)

  const [attempt, setAttempt] = useState(() => readCleanupAttempt(scope))
  const [lookupProblem, setLookupProblem] = useState(false)
  const [manualCheck, setManualCheck] = useState(0)
  const [initialTaskChecked, setInitialTaskChecked] = useState(false)
  const submitting = useRef(false)
  const lastSeen = useRef<LogCleanupTask | null>(null)

  const logCleanupActive = isActiveLogCleanupTask(logCleanupTask)
  const targetId =
    attempt?.taskId ?? (logCleanupActive ? logCleanupTask?.task_id : null)
  const unconfirmed = Boolean(
    attempt && logCleanupTask?.task_id !== attempt.taskId
  )
  const needsCurrentRead = !targetId && (!initialTaskChecked || lookupProblem)

  useEffect(() => {
    if (targetId || initialTaskChecked || isStartingLogCleanup) return
    const controller = new AbortController()
    void getCurrentLogCleanupTask(controller.signal)
      .then((response) => {
        if (controller.signal.aborted) return
        setLookupProblem(response.success !== true)
        setInitialTaskChecked(true)
        if (response.success && response.data) {
          lastSeen.current = response.data
          setLogCleanupTask(response.data)
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setLookupProblem(true)
          setInitialTaskChecked(true)
        }
      })
    return () => controller.abort()
  }, [initialTaskChecked, isStartingLogCleanup, targetId])

  useEffect(() => {
    if (!targetId || isStartingLogCleanup) return
    const taskId = targetId
    const controller = new AbortController()
    let timer: number
    let failures = 0
    function schedule(delay: number) {
      timer = window.setTimeout(() => void read(), delay)
    }
    async function read() {
      try {
        const response = await getSystemTask(taskId, controller.signal)
        if (controller.signal.aborted) return
        const task = response.data
        if (
          !response.success ||
          !task ||
          task.type !== 'log_cleanup' ||
          task.task_id !== taskId ||
          (attempt && !matchesCleanupAttempt(task, attempt))
        ) {
          throw new Error('No matching log cleanup receipt')
        }
        failures = 0
        setLookupProblem(false)
        // Single outstanding read per ID. Do not move backwards if a node
        // temporarily returns an older snapshot.
        const previous = lastSeen.current
        if (
          previous?.task_id === task.task_id &&
          ((previous.updated_at ?? 0) > (task.updated_at ?? 0) ||
            (isActiveLogCleanupTask(task) &&
              (!isActiveLogCleanupTask(previous) ||
                (previous.state?.processed ?? 0) >
                  (task.state?.processed ?? 0))))
        ) {
          schedule(1_000)
          return
        }
        lastSeen.current = task
        setLogCleanupTask(task)
        if (isActiveLogCleanupTask(task)) {
          schedule(1_000)
          return
        }
        if (attempt) {
          saveCleanupAttempt(scope, null)
          setAttempt(null)
        }
        if (task.status === 'succeeded') {
          const count = task.result?.deleted_count ?? task.state?.processed ?? 0
          toast.success(
            count > 0
              ? t('{{count}} log entries removed.', { count })
              : t('No log entries matched the selected time.')
          )
        } else if (task.status === 'failed') {
          handleServerError(task, t('Failed to clean logs'))
        }
      } catch {
        if (controller.signal.aborted) return
        setLookupProblem(true)
        failures += 1
        if (attempt && failures === 1) {
          // Observe separately running work, never as this request's receipt.
          void getCurrentLogCleanupTask(controller.signal)
            .then((observed) => {
              if (
                !controller.signal.aborted &&
                observed.success &&
                observed.data &&
                !lastSeen.current
              ) {
                lastSeen.current = observed.data
                setLogCleanupTask(observed.data)
              }
            })
            .catch(() => undefined)
        }
        if (failures < 5) schedule(Math.min(1_000 * 2 ** failures, 15_000))
      }
    }
    schedule(manualCheck > 0 || attempt ? 0 : 1_000)
    return () => {
      controller.abort()
      window.clearTimeout(timer)
    }
  }, [attempt, isStartingLogCleanup, manualCheck, scope, t, targetId])

  const purgeTimestamp = useMemo(() => {
    if (!purgeDate) return null
    const timestamp = Math.floor(purgeDate.getTime() / 1000)
    return Number.isSafeInteger(timestamp) && timestamp > 0 ? timestamp : null
  }, [purgeDate])

  const formattedPurgeDate = useMemo(() => {
    if (!purgeDate) return ''
    return formatTimestampToDate(purgeDate.getTime(), 'milliseconds')
  }, [purgeDate])

  const logCleanupState = logCleanupTask?.state
  const logCleanupProgress = Math.min(
    100,
    Math.max(0, logCleanupState?.progress ?? 0)
  )
  const logCleanupProcessed = logCleanupState?.processed ?? 0
  const logCleanupTotal = logCleanupState?.total ?? 0

  const handleRequestCleanLogs = () => {
    if (!purgeTimestamp) {
      toast.error(t('Select a timestamp before clearing logs.'))
      return
    }
    if (submitting.current || attempt || logCleanupActive || needsCurrentRead) {
      return
    }

    setShowConfirmDialog(true)
  }

  const handleCleanLogs = async () => {
    if (
      !purgeTimestamp ||
      submitting.current ||
      attempt ||
      logCleanupActive ||
      needsCurrentRead
    ) {
      return
    }
    const uuid = globalThis.crypto?.randomUUID?.()
    if (!uuid) {
      toast.error(t('Could not start log cleanup.'))
      return
    }
    const next = { taskId: `systask_${uuid}`, cutoff: purgeTimestamp }
    if (!saveCleanupAttempt(scope, next)) {
      toast.error(t('Could not start log cleanup.'))
      return
    }
    submitting.current = true
    setIsStartingLogCleanup(true)
    setLookupProblem(false)
    setAttempt(next)
    try {
      const response = await startLogCleanupTask(next.cutoff, next.taskId)
      const task = response.data
      if (!task || task.type !== 'log_cleanup') {
        throw response
      }
      if (response.success && matchesCleanupAttempt(task, next)) {
        lastSeen.current = task
        setLogCleanupTask(task)
        toast.success(t('Log cleanup task started.'))
      } else if (
        !response.success &&
        response.code === 'log_cleanup_already_running' &&
        task.task_id !== next.taskId &&
        isActiveLogCleanupTask(task)
      ) {
        // The versioned route declined this cutoff and reported other work.
        lastSeen.current = task
        setLogCleanupTask(task)
        setAttempt(null)
        saveCleanupAttempt(scope, null)
        toast.info(
          t(
            'Another log cleanup is already running. Your request was not started.'
          )
        )
      } else {
        throw response
      }
    } catch (error) {
      if (definiteCleanupRejection(error)) {
        setAttempt(null)
        saveCleanupAttempt(scope, null)
        handleServerError(error, t('Could not start log cleanup.'))
      } else {
        setLookupProblem(true)
      }
    } finally {
      setShowConfirmDialog(false)
      submitting.current = false
      setIsStartingLogCleanup(false)
    }
  }

  return (
    <div
      role='group'
      aria-labelledby={titleId}
      className='flex min-w-0 flex-col gap-4'
    >
      <div>
        <h4 id={titleId} className='font-medium'>
          {t('Clean history logs')}
        </h4>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t('Remove all log entries created before the selected timestamp.')}
        </p>
      </div>
      <DateTimePicker value={purgeDate} onChange={setPurgeDate} />
      <div className='flex flex-wrap gap-3'>
        {quickSelectOptions.map((option) => (
          <Button
            key={option.label}
            type='button'
            variant='outline'
            onClick={() => setPurgeDate(option.getValue())}
          >
            {t(option.label)}
          </Button>
        ))}
        <Button
          type='button'
          variant='destructive'
          onClick={handleRequestCleanLogs}
          disabled={
            isStartingLogCleanup ||
            logCleanupActive ||
            Boolean(attempt) ||
            needsCurrentRead
          }
        >
          {isStartingLogCleanup || logCleanupActive
            ? t('Cleaning...')
            : t('Clean logs')}
        </Button>
      </div>
      {!isStartingLogCleanup && (unconfirmed || lookupProblem) && (
        <div
          role='status'
          className='flex flex-wrap items-center gap-2 text-sm'
        >
          <span>
            {unconfirmed
              ? t(
                  'Could not confirm the log cleanup request. Check its status before starting another.'
                )
              : t('Could not check log cleanup status.')}
          </span>
          <Button
            type='button'
            variant='outline'
            onClick={() => {
              if (targetId) setManualCheck((value) => value + 1)
              else setInitialTaskChecked(false)
            }}
          >
            {t('Check status')}
          </Button>
        </div>
      )}
      {logCleanupTask && (
        <div className='rounded-md border p-3'>
          <div className='mb-2 flex items-center justify-between gap-3 text-sm'>
            <span className='font-medium'>{t('Log cleanup progress')}</span>
            <span className='text-muted-foreground tabular-nums'>
              {logCleanupProgress}%
            </span>
          </div>
          {logCleanupTask.payload?.target_timestamp != null && (
            <p className='text-muted-foreground mb-2 text-xs'>
              {t('Cleanup cutoff: {{date}}', {
                date: formatTimestampToDate(
                  logCleanupTask.payload.target_timestamp
                ),
              })}
            </p>
          )}
          <Progress value={logCleanupProgress} />
          <div className='text-muted-foreground mt-2 text-xs'>
            {t('{{processed}} of {{total}} log entries processed.', {
              processed: logCleanupProcessed,
              total: logCleanupTotal,
            })}
          </div>
          {logCleanupTask.status === 'failed' && logCleanupTask.error && (
            <div className='text-destructive mt-2 text-xs'>
              {logCleanupTask.error}
            </div>
          )}
        </div>
      )}

      <ConfirmDialog
        open={showConfirmDialog}
        onOpenChange={setShowConfirmDialog}
        title={t('Confirm log cleanup')}
        desc={
          <>
            {formattedPurgeDate
              ? t(
                  'This will permanently remove all log entries created before {{date}}.',
                  { date: formattedPurgeDate }
                )
              : t(
                  'This will permanently remove log entries before the selected timestamp.'
                )}{' '}
            {t('This action cannot be undone.')}
          </>
        }
        destructive
        isLoading={isStartingLogCleanup}
        confirmText={isStartingLogCleanup ? t('Cleaning...') : t('Delete logs')}
        handleConfirm={handleCleanLogs}
      />
    </div>
  )
}
