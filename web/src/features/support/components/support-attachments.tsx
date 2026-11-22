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
import { Loader2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth-store'

import { getSupportTicketAttachment } from '../api'
import type { SupportTicketAttachment } from '../types'

function PrivateSupportImage(props: {
  admin: boolean
  ticketId: number
  attachment: SupportTicketAttachment
  number: number
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const identity = `${userId}:${sessionId}:${props.admin}:${props.ticketId}:${props.attachment.id}`
  const [result, setResult] = useState<{
    identity: string
    url?: string
    error?: boolean
  } | null>(null)
  const [attempt, setAttempt] = useState(0)
  const [open, setOpen] = useState(false)

  useEffect(() => {
    if (!userId) return
    const controller = new AbortController()
    let url: string | undefined
    setResult(null)
    setOpen(false)
    void getSupportTicketAttachment(
      props.admin,
      props.ticketId,
      props.attachment.id,
      controller.signal
    )
      .then((blob) => {
        if (controller.signal.aborted) return
        url = URL.createObjectURL(blob)
        setResult({ identity, url })
      })
      .catch(() => {
        if (!controller.signal.aborted) setResult({ identity, error: true })
      })
    return () => {
      controller.abort()
      if (url) URL.revokeObjectURL(url)
    }
  }, [
    identity,
    props.admin,
    props.ticketId,
    props.attachment.id,
    userId,
    attempt,
  ])

  const current = result?.identity === identity ? result : null
  const label = t('Support attachment {{number}}', { number: props.number })
  if (!userId) return null
  if (current?.error) {
    return (
      <div className='rounded-md border p-2 text-xs' role='status'>
        <p>{t('Unable to load private image.')}</p>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          onClick={() => setAttempt((value) => value + 1)}
        >
          {t('Retry')}
        </Button>
      </div>
    )
  }
  if (!current?.url) {
    return (
      <div
        className='flex h-24 w-28 items-center justify-center rounded-md border'
        role='status'
        aria-label={t('Loading private image...')}
      >
        <Loader2 className='size-4 animate-spin' aria-hidden='true' />
      </div>
    )
  }
  return (
    <>
      <button
        type='button'
        className='focus-visible:ring-ring overflow-hidden rounded-md border focus-visible:ring-2'
        aria-label={t('View support attachment {{number}}', {
          number: props.number,
        })}
        onClick={() => setOpen(true)}
      >
        <img src={current.url} alt={label} className='h-24 w-28 object-cover' />
      </button>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={label}
        description={t(
          'Private image visible only to the ticket owner and authorized support staff.'
        )}
        contentClassName='sm:max-w-4xl'
      >
        <img
          src={current.url}
          alt={label}
          className='max-h-[75vh] w-full object-contain'
        />
      </Dialog>
    </>
  )
}

export function SupportAttachments(props: {
  admin: boolean
  ticketId: number
  attachments?: SupportTicketAttachment[]
}) {
  if (!props.attachments?.length) return null
  return (
    <div className='mt-2 flex flex-wrap gap-2'>
      {props.attachments.map((attachment, index) => (
        <PrivateSupportImage
          key={attachment.id}
          admin={props.admin}
          ticketId={props.ticketId}
          attachment={attachment}
          number={index + 1}
        />
      ))}
    </div>
  )
}
