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
import {
  ArrowUpRight,
  BookOpen,
  Handshake,
  LifeBuoy,
  Radio,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

export function ContactPage() {
  const { t } = useTranslation()
  const entries = [
    {
      title: 'Support Center',
      description:
        'Get help with API calls, payments and account access. Replies and attachments stay together.',
      icon: LifeBuoy,
      to: '/support',
    },
    {
      title: 'Documentation',
      description:
        'Choose a model, configure your client, and make your first request.',
      icon: BookOpen,
      to: '/help',
    },
    {
      title: 'Platform health',
      description: 'Group availability and actual request outcomes',
      icon: Radio,
      to: '/health',
    },
  ] as const
  return (
    <PublicLayout>
      <section className='mx-auto max-w-5xl py-8 sm:py-16'>
        <header className='mx-auto mb-10 max-w-2xl space-y-4 text-center'>
          <span className='bg-primary/10 text-primary mx-auto flex size-14 items-center justify-center rounded-2xl'>
            <LifeBuoy aria-hidden='true' className='size-6' />
          </span>
          <h1 className='text-3xl font-semibold tracking-tight sm:text-4xl'>
            {t('Support Center')}
          </h1>
          <p className='text-muted-foreground leading-7'>
            {t(
              'Get help with API calls, payments and account access. Replies and attachments stay together.'
            )}
          </p>
        </header>
        <div className='grid gap-5 md:grid-cols-3'>
          {entries.map((entry) => (
            <Card key={entry.to} className='rounded-2xl py-6'>
              <CardHeader className='space-y-4'>
                <entry.icon
                  className='text-primary size-6'
                  aria-hidden='true'
                />
                <CardTitle>
                  <h2>{t(entry.title)}</h2>
                </CardTitle>
              </CardHeader>
              <CardContent className='flex flex-1 flex-col gap-6'>
                <p className='text-muted-foreground flex-1 text-sm leading-6'>
                  {t(entry.description)}
                </p>
                <Button variant='outline' render={<Link to={entry.to} />}>
                  {t(entry.title)}
                  <ArrowUpRight aria-hidden='true' />
                </Button>
              </CardContent>
            </Card>
          ))}
        </div>
        <div className='mt-8 flex flex-wrap items-center justify-center gap-4'>
          <Button variant='ghost' render={<Link to='/system-announcements' />}>
            {t('System Announcements')}
          </Button>
          <Button variant='ghost' render={<Link to='/partner' />}>
            {t('Partner application')}
          </Button>
        </div>
      </section>
    </PublicLayout>
  )
}

export function PartnerEntryPage() {
  const { t } = useTranslation()
  return (
    <PublicLayout>
      <section className='mx-auto max-w-3xl py-8 sm:py-16'>
        <Card className='rounded-3xl p-3 sm:p-8'>
          <CardHeader className='space-y-4'>
            <span className='bg-primary/10 text-primary flex size-14 items-center justify-center rounded-2xl'>
              <Handshake aria-hidden='true' className='size-6' />
            </span>
            <CardTitle>
              <h1 className='text-3xl font-semibold tracking-tight'>
                {t('Partner application')}
              </h1>
            </CardTitle>
            <p className='text-muted-foreground leading-7'>
              {t(
                'Submit your service and quotes to start a partnership review.'
              )}
            </p>
          </CardHeader>
          <CardContent className='space-y-6'>
            <ol className='space-y-4'>
              {[
                'Tell us about your site, supply and support.',
                'List supported models, absolute rates and service notes.',
                'Submitted details remain unchanged. Decisions and notes appear in your application.',
              ].map((step, index) => (
                <li key={step} className='flex gap-3 text-sm leading-6'>
                  <span className='bg-muted flex size-7 shrink-0 items-center justify-center rounded-full text-xs font-semibold'>
                    {index + 1}
                  </span>
                  <span>{t(step)}</span>
                </li>
              ))}
            </ol>
            <div className='flex flex-wrap gap-3'>
              <Button render={<Link to='/partner-apply' />}>
                {t('Submit application')}
                <ArrowUpRight aria-hidden='true' />
              </Button>
              <Button variant='outline' render={<Link to='/contact' />}>
                {t('Support Center')}
              </Button>
            </div>
          </CardContent>
        </Card>
      </section>
    </PublicLayout>
  )
}
