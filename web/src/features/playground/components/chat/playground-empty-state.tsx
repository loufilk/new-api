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
  BarChartIcon,
  CodeSquareIcon,
  GraduationCapIcon,
  NotepadTextIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

type PlaygroundEmptyStateProps = {
  onSelectPrompt: (prompt: string) => void
}

const starterCards = [
  {
    icon: BarChartIcon,
    title: 'Analyze data',
    description: 'Find patterns in your data',
  },
  {
    icon: NotepadTextIcon,
    title: 'Summarize text',
    description: 'Condense long content into key points',
  },
  {
    icon: CodeSquareIcon,
    title: 'Code',
    description: 'Write, debug, and explain code',
  },
  {
    icon: GraduationCapIcon,
    title: 'Get advice',
    description: 'Get thoughtful answers and ideas',
  },
]

export function PlaygroundEmptyState({
  onSelectPrompt,
}: PlaygroundEmptyStateProps) {
  const { t } = useTranslation()

  return (
    <div className='flex min-h-[min(520px,calc(100svh-18rem))] items-center justify-center px-1 py-8 md:py-12'>
      <div className='grid w-full max-w-2xl justify-items-center gap-6 text-center'>
        <div className='grid gap-2'>
          <h2 className='text-2xl font-semibold tracking-tight text-balance md:text-3xl'>
            {t('Start a playground chat')}
          </h2>
          <p className='text-muted-foreground mx-auto max-w-lg text-sm leading-6 text-balance'>
            {t(
              'Test a model with a starter prompt, or write your own request below.'
            )}
          </p>
        </div>

        <div className='grid w-full gap-3 sm:grid-cols-2'>
          {starterCards.map(({ icon: Icon, title, description }) => {
            const prompt = t(title)

            return (
              <Button
                className='group h-auto min-h-24 justify-start gap-3 p-4 text-left whitespace-normal'
                key={title}
                onClick={() => onSelectPrompt(prompt)}
                variant='outline'
              >
                <span className='bg-muted text-foreground group-hover:bg-primary/10 group-hover:text-primary inline-flex size-9 shrink-0 items-center justify-center rounded-lg transition-colors'>
                  <Icon className='size-4' aria-hidden='true' />
                </span>
                <span className='grid min-w-0 gap-0.5'>
                  <span className='text-sm font-semibold'>{prompt}</span>
                  <span className='text-muted-foreground text-xs leading-5'>
                    {t(description)}
                  </span>
                </span>
              </Button>
            )
          })}
        </div>
      </div>
    </div>
  )
}
