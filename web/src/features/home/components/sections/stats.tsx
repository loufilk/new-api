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
import { Braces, KeyRound, ReceiptText, ScrollText } from 'lucide-react'
import { useTranslation } from 'react-i18next'

interface StatsProps {
  className?: string
}

export function Stats(_props: StatsProps) {
  const { t } = useTranslation()
  const capabilities = [
    {
      title: t('OpenAI Compatible'),
      description: t(
        'Compatible API routes for common AI application workflows'
      ),
      icon: Braces,
    },
    {
      title: t('API Keys'),
      description: t(
        'Add your API keys, set up channels and configure access permissions'
      ),
      icon: KeyRound,
    },
    {
      title: t('Billing'),
      description: t('Pay-as-you-go with real-time usage monitoring'),
      icon: ReceiptText,
    },
    {
      title: t('Usage Logs'),
      description: t(
        'Track usage, costs and performance with real-time analytics'
      ),
      icon: ScrollText,
    },
  ]

  return (
    <section className='border-border/40 bg-muted/10 relative z-10 border-y'>
      <h2 className='sr-only'>{t('Core Features')}</h2>
      <div className='bg-border/40 mx-auto grid max-w-6xl gap-px px-6 md:grid-cols-4'>
        {capabilities.map((capability) => {
          const Icon = capability.icon
          return (
            <div
              key={capability.title}
              className='bg-background flex items-start gap-3 px-4 py-6 md:px-5'
            >
              <span className='border-border/50 bg-muted/30 text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg border'>
                <Icon className='size-4' aria-hidden='true' />
              </span>
              <div>
                <h3 className='text-sm font-semibold'>{capability.title}</h3>
                <p className='text-muted-foreground mt-1 text-xs leading-relaxed'>
                  {capability.description}
                </p>
              </div>
            </div>
          )
        })}
      </div>
    </section>
  )
}
