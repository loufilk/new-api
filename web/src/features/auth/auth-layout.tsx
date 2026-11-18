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
import { Link } from '@tanstack/react-router'
import { ArrowUpRight, Layers3, Route, ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { LanguageSwitcher } from '@/components/language-switcher'
import { HeaderLogo } from '@/components/layout/components/header-logo'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useSystemConfig } from '@/hooks/use-system-config'

type AuthLayoutProps = {
  children: React.ReactNode
}

export function AuthLayout({ children }: AuthLayoutProps) {
  const { t } = useTranslation()
  const { systemName, logo, loading } = useSystemConfig()

  return (
    <div className='relative grid min-h-svh max-w-none lg:grid-cols-[minmax(0,0.95fr)_minmax(0,1.05fr)]'>
      <a
        href='/'
        className='absolute top-5 left-5 z-10 flex items-center gap-2.5 transition-opacity hover:opacity-80 sm:top-8 sm:left-8 lg:text-white'
      >
        <div className='relative flex size-8 items-center justify-center lg:rounded-md lg:bg-white'>
          {loading ? (
            <Skeleton className='absolute inset-0 rounded-full' />
          ) : (
            <HeaderLogo
              src={logo}
              alt={t('Logo')}
              className='size-8 lg:size-6'
            />
          )}
        </div>
        {loading ? (
          <Skeleton className='h-6 w-24' />
        ) : (
          <h1 className='text-xl font-medium'>{systemName}</h1>
        )}
      </a>
      <div className='absolute top-5 right-5 z-10 sm:top-8 sm:right-8'>
        <LanguageSwitcher />
      </div>
      <aside
        className='relative hidden overflow-hidden bg-slate-950 p-12 text-white lg:flex lg:flex-col lg:justify-center xl:p-16'
        aria-label={t('Platform overview')}
      >
        <div
          className='pointer-events-none absolute -top-32 -right-32 size-[28rem] rounded-full bg-indigo-500/20 blur-3xl'
          aria-hidden='true'
        />
        <div
          className='pointer-events-none absolute -bottom-20 -left-20 size-80 rounded-full bg-cyan-400/10 blur-3xl'
          aria-hidden='true'
        />
        <div className='relative z-1 mx-auto w-full max-w-lg space-y-9 py-24'>
          <div className='space-y-5'>
            <span className='inline-flex rounded-full border border-white/15 bg-white/5 px-3 py-1.5 text-xs font-medium tracking-wide text-slate-300'>
              {t('API workspace')}
            </span>
            <h2 className='text-4xl leading-tight font-semibold tracking-tight xl:text-5xl'>
              {t('One workspace. Every model.')}
            </h2>
            <p className='max-w-md text-base leading-7 text-slate-300'>
              {t(
                'Connect your tools, choose the right models, and keep every request in view.'
              )}
            </p>
          </div>
          <div className='space-y-5'>
            {[
              {
                icon: Layers3,
                title: t('A clear model catalog'),
                detail: t(
                  'Compare capabilities, groups, and your effective price.'
                ),
              },
              {
                icon: Route,
                title: t('Routing that fits your work'),
                detail: t(
                  'Choose a primary group and the fallback order you need.'
                ),
              },
              {
                icon: ShieldCheck,
                title: t('Control over your access'),
                detail: t(
                  'Manage API keys, account security, and usage in one place.'
                ),
              },
            ].map((item) => (
              <div key={item.title} className='flex items-start gap-4'>
                <span className='flex size-10 shrink-0 items-center justify-center rounded-xl border border-white/10 bg-white/5'>
                  <item.icon
                    className='size-4 text-indigo-200'
                    aria-hidden='true'
                  />
                </span>
                <div>
                  <p className='text-sm font-medium'>{item.title}</p>
                  <p className='mt-1 text-sm leading-6 text-slate-400'>
                    {item.detail}
                  </p>
                </div>
              </div>
            ))}
          </div>
          <Button
            variant='outline'
            className='border-white/20 bg-transparent text-white hover:bg-white/10 hover:text-white'
            render={<Link to='/pricing' />}
          >
            {t('Explore models')}
            <ArrowUpRight className='size-4' aria-hidden='true' />
          </Button>
        </div>
      </aside>
      <main className='flex min-w-0 items-center justify-center px-5 pt-24 pb-10 sm:px-8 lg:px-12 lg:py-24'>
        <div className='mx-auto flex w-full max-w-[420px] flex-col justify-center space-y-2'>
          {children}
        </div>
      </main>
    </div>
  )
}
