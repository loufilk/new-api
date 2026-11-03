import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform, postPlatform } from '../api'
import { formatAgentCash } from '../lib/money'

interface Reminder {
  id: number
  agent_user_id: number
  day: string
  quota: number
}

export function SimpleReminderNotice() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const eligible =
    !!user && (user.role === 100 || (!!user.agent_profile && user.role < 10))
  const client = useQueryClient()
  const key = ['agent-platform', user?.id, 'unread-reminders']
  const reminders = useQuery({
    queryKey: key,
    queryFn: () => getPlatform<Reminder[]>('/simple/reminders/unread'),
    enabled: eligible,
    refetchInterval: 60_000,
  })
  const acknowledge = useMutation({
    mutationFn: (ids: number[]) =>
      postPlatform('/simple/reminders/acknowledge', { ids }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: key })
    },
    onError: (error) => handleServerError(error),
  })
  const rows = reminders.data ?? []
  if (!rows.length || !eligible) return null
  return (
    <Dialog
      open
      title={t('Daily overdue reminder')}
      description={t(
        'Please settle the overdue amount or contact support. Only root can pause an agent.'
      )}
      onOpenChange={() => {}}
      showCloseButton={false}
      footer={
        <Button
          disabled={acknowledge.isPending}
          onClick={() => acknowledge.mutate(rows.map((row) => row.id))}
        >
          {t('Acknowledge today')}
        </Button>
      }
    >
      <ul className='max-h-72 space-y-3 overflow-y-auto text-sm'>
        {rows.map((row) => (
          <li key={row.id}>
            {t('Agent {{id}}: overdue {{amount}} ({{day}})', {
              id: row.agent_user_id,
              amount: formatAgentCash(row.quota),
              day: row.day,
            })}
          </li>
        ))}
      </ul>
    </Dialog>
  )
}
