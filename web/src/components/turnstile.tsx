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
import { useEffect, useRef } from 'react'

declare global {
  interface Window {
    turnstile?: {
      render: (
        element: HTMLElement,
        options: Record<string, unknown>
      ) => string | void
      remove?: (widgetId: string) => void
    }
  }
}

interface TurnstileProps {
  siteKey: string
  action: string
  onVerify: (token: string) => void
  onExpire?: () => void
  className?: string
}

export function Turnstile({
  siteKey,
  action,
  onVerify,
  onExpire,
  className,
}: TurnstileProps) {
  const ref = useRef<HTMLDivElement | null>(null)
  const callbacks = useRef({ onVerify, onExpire })

  useEffect(() => {
    callbacks.current = { onVerify, onExpire }
  }, [onVerify, onExpire])

  useEffect(() => {
    let active = true
    let rendered = false
    let widgetId: string | void
    let widgetAPI: Window['turnstile']
    let script: HTMLScriptElement | undefined
    const expire = () => {
      if (active) callbacks.current.onExpire?.()
    }
    const render = () => {
      if (!active || rendered || !ref.current || !window.turnstile) return
      rendered = true
      widgetAPI = window.turnstile
      try {
        widgetId = widgetAPI.render(ref.current, {
          sitekey: siteKey,
          action,
          callback: (token: string) => {
            if (active) callbacks.current.onVerify(token)
          },
          'error-callback': expire,
          'expired-callback': expire,
        })
      } catch {
        expire()
      }
    }
    const scriptFailed = () => {
      expire()
      script?.remove()
    }

    if (window.turnstile) {
      render()
    } else {
      const existing = document.querySelector('#cf-turnstile')
      if (existing instanceof HTMLScriptElement) {
        script = existing
      } else if (!existing) {
        script = document.createElement('script')
        script.id = 'cf-turnstile'
        script.src =
          'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'
        script.async = true
        script.defer = true
      }
      // A dialog can reopen while the shared script is still loading. Every
      // mounted widget must subscribe; the first dialog may already be gone.
      script?.addEventListener('load', render)
      script?.addEventListener('error', scriptFailed)
      if (script && !script.isConnected) document.head.appendChild(script)
    }

    return () => {
      active = false
      script?.removeEventListener('load', render)
      script?.removeEventListener('error', scriptFailed)
      if (typeof widgetId === 'string') widgetAPI?.remove?.(widgetId)
    }
  }, [siteKey, action])

  return <div ref={ref} className={className} />
}
