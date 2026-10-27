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
  Braces,
  Cable,
  Gauge,
  KeyRound,
  ReceiptText,
  ScrollText,
  Settings,
  Shapes,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { AnimateInView } from '@/components/animate-in-view'

interface FeaturesProps {
  className?: string
}

export function Features(_props: FeaturesProps) {
  const { t } = useTranslation()

  const features = [
    {
      id: 'routes',
      num: '01',
      title: t('OpenAI Compatible'),
      desc: t('Compatible API routes for common AI application workflows'),
      span: 'md:col-span-2',
      icon: <Braces className='size-4 text-blue-400' />,
      visual: (
        <div className='mt-4 grid gap-2 sm:grid-cols-3'>
          {['/v1/chat/completions', '/v1/responses', '/v1/models'].map(
            (route) => (
              <code
                key={route}
                className='border-border/30 bg-muted/20 text-muted-foreground rounded-lg border px-3 py-2 text-center text-[11px]'
              >
                {route}
              </code>
            )
          )}
        </div>
      ),
    },
    {
      id: 'keys',
      num: '02',
      title: t('API Keys'),
      desc: t(
        'Add your API keys, set up channels and configure access permissions'
      ),
      span: 'md:col-span-1',
      icon: <KeyRound className='size-4 text-emerald-400' />,
      visual: (
        <div className='mt-4 rounded-lg border border-emerald-500/20 bg-emerald-500/5 px-4 py-3'>
          <div className='flex items-center justify-between gap-3'>
            <code className='text-muted-foreground text-xs'>sk-••••••••</code>
            <span className='size-2 rounded-full bg-emerald-500' />
          </div>
        </div>
      ),
    },
    {
      id: 'billing',
      num: '03',
      title: t('Billing'),
      desc: t('Pay-as-you-go with real-time usage monitoring'),
      span: 'md:col-span-1',
      icon: <ReceiptText className='size-4 text-violet-400' />,
      visual: (
        <div className='mt-4 grid grid-cols-3 gap-2'>
          {[t('Usage'), t('Billing'), t('Tokens')].map((label) => (
            <span
              key={label}
              className='border-border/40 bg-muted/20 text-muted-foreground rounded-lg border px-2 py-2 text-center text-[10px]'
            >
              {label}
            </span>
          ))}
        </div>
      ),
    },
    {
      id: 'logs',
      num: '04',
      title: t('Usage Logs'),
      desc: t('Track usage, costs and performance with real-time analytics'),
      span: 'md:col-span-2',
      icon: <ScrollText className='size-4 text-amber-400' />,
      visual: (
        <div className='mt-4 space-y-2'>
          {['200 · /v1/responses', '200 · /v1/chat/completions'].map(
            (entry) => (
              <div
                key={entry}
                className='border-border/30 bg-muted/20 text-muted-foreground flex items-center justify-between rounded-lg border px-3 py-2 font-mono text-[11px]'
              >
                <span>{entry}</span>
                <span>{t('Usage')}</span>
              </div>
            )
          )}
        </div>
      ),
    },
  ]

  const additionalFeatures = [
    {
      icon: <Cable className='size-5' strokeWidth={1.5} />,
      title: t('Channel Management'),
      desc: t('Create channels or edit keys, base URLs, and overrides.'),
    },
    {
      icon: <Gauge className='size-5' strokeWidth={1.5} />,
      title: t('Rate Limiting'),
      desc: t('Configure rate limiting rules for a specific user group.'),
    },
    {
      icon: <Shapes className='size-5' strokeWidth={1.5} />,
      title: t('Model Square'),
      desc: t('Compatible API routes for common AI application workflows'),
    },
    {
      icon: <Settings className='size-5' strokeWidth={1.5} />,
      title: t('System Settings'),
      desc: t('Choose how quota values are shown to users'),
    },
  ]

  return (
    <section className='relative z-10 px-6 py-24 md:py-32'>
      <div className='mx-auto max-w-6xl'>
        <AnimateInView className='mb-16 max-w-lg'>
          <p className='text-muted-foreground mb-3 text-xs font-medium tracking-widest uppercase'>
            {t('Core Features')}
          </p>
          <h2 className='text-2xl leading-tight font-bold tracking-tight md:text-3xl'>
            {t('Built for developers,')}
            <br />
            {t('designed for scale')}
          </h2>
        </AnimateInView>

        <div className='border-border/40 bg-border/40 grid gap-px overflow-hidden rounded-xl border md:grid-cols-3'>
          {features.map((feature, index) => (
            <AnimateInView
              key={feature.id}
              delay={index * 100}
              animation='scale-in'
              className={`bg-background group hover:bg-muted/20 p-7 transition-colors duration-300 md:p-8 ${feature.span}`}
            >
              <div className='mb-3 flex items-center gap-3'>
                <span className='border-border/40 bg-muted text-muted-foreground flex size-7 items-center justify-center rounded-md border text-[10px] font-semibold tabular-nums'>
                  {feature.num}
                </span>
                {feature.icon}
                <h3 className='text-sm font-semibold'>{feature.title}</h3>
              </div>
              <p className='text-muted-foreground text-sm leading-relaxed'>
                {feature.desc}
              </p>
              {feature.visual}
            </AnimateInView>
          ))}
        </div>

        <div className='mt-12 grid grid-cols-2 gap-8 md:grid-cols-4 md:gap-12'>
          {additionalFeatures.map((feature, index) => (
            <AnimateInView
              key={feature.title}
              delay={index * 100}
              animation='fade-up'
              className='flex flex-col items-center text-center'
            >
              <div className='text-muted-foreground border-border/50 bg-muted/30 mb-3 flex size-12 items-center justify-center rounded-xl border transition-colors'>
                {feature.icon}
              </div>
              <h3 className='mb-1.5 text-sm font-semibold'>{feature.title}</h3>
              <p className='text-muted-foreground max-w-[220px] text-xs leading-relaxed'>
                {feature.desc}
              </p>
            </AnimateInView>
          ))}
        </div>
      </div>
    </section>
  )
}
