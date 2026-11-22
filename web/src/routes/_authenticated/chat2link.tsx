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
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { Loader2 } from 'lucide-react'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { useChatPresets } from '@/features/chat/hooks/use-chat-presets'

export const Route = createFileRoute('/_authenticated/chat2link')({
  component: Chat2LinkPage,
})

function Chat2LinkPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { chatPresets, isLoading } = useChatPresets()
  const chatId = chatPresets.find((preset) => preset.type === 'web')?.id
  useEffect(() => {
    if (chatId !== undefined) {
      void navigate({ to: '/chat/$chatId', params: { chatId }, replace: true })
    }
  }, [chatId, navigate])

  if (chatId === undefined && !isLoading) {
    return <ErrorState title={t('No available Web chat links')} />
  }

  return (
    <div className='flex h-full flex-col items-center justify-center gap-3'>
      <Loader2 className='text-muted-foreground h-8 w-8 animate-spin' />
      <p className='text-muted-foreground text-sm'>
        {t('Redirecting to chat page...')}
      </p>
    </div>
  )
}
