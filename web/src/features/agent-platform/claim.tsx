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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useLocation, useNavigate } from '@tanstack/react-router'
import type { ColumnDef } from '@tanstack/react-table'
import { useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { getSelf } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'

import { postPlatform } from './api'
import { ResourceTable } from './components/resource-table'
import { BusinessDate, BusinessState, Quota } from './components/values'
import { useRequestId } from './hooks/use-request-id'
import type { Delivery } from './types'

export function DeliveryClaim() {
  const { t } = useTranslation()
  const hash = useLocation({ select: (location) => location.hash })
  const navigate = useNavigate()
  const client = useQueryClient()
  const requestId = useRequestId()
  const redeemRequestId = useRequestId()
  const locked = useRef(false)
  const [delivery, setDelivery] = useState<Delivery | null>(null)
  const form = useForm<{ claim_code: string }>({
    resolver: zodResolver(
      z.object({
        claim_code: z.string().trim().min(1, t('Enter a claim code')).max(128),
      })
    ),
    defaultValues: { claim_code: '' },
  })
  useEffect(() => {
    if (!hash) return
    const code = new URLSearchParams(hash.replace(/^#/, '')).get('code')
    if (code && code.length <= 128) form.setValue('claim_code', code)
    void navigate({ to: '.', hash: '', replace: true })
  }, [hash, navigate, form])
  const claim = useMutation({
    mutationFn: (code: string) =>
      postPlatform<Delivery>('/deliveries/claim', {
        request_id: requestId({ code }),
        claim_code: code,
      }),
    retry: false,
    onSuccess: (result) => {
      form.reset()
      setDelivery(result)
      void client.invalidateQueries({ queryKey: ['agent-platform'] })
    },
    onError: (error) => handleServerError(error),
    onSettled: () => {
      locked.current = false
    },
  })
  const redeem = useMutation({
    mutationFn: (current: Delivery) =>
      postPlatform(`/deliveries/${current.id}/confirm`, {
        request_id: redeemRequestId({
          id: current.id,
          version: current.version,
        }),
        delivery_id: current.id,
        version: current.version,
      }),
    retry: false,
    onSuccess: () => {
      toast.success(t('Delivery redeemed into your wallet'))
      setDelivery(null)
      void client.invalidateQueries({ queryKey: ['agent-platform'] })
      void client.invalidateQueries({ queryKey: ['wallet'] })
      void getSelf()
    },
    onError: (error) => handleServerError(error),
    onSettled: () => {
      locked.current = false
    },
  })
  const columns: ColumnDef<Delivery>[] = [
    {
      accessorKey: 'id',
      header: t('Delivery ID'),
      meta: { mobileTitle: true },
    },
    { accessorKey: 'name', header: t('Name') },
    {
      accessorKey: 'state',
      header: t('Status'),
      cell: ({ row }) => <BusinessState value={row.original.state} />,
    },
    {
      accessorKey: 'quota',
      header: t('Delivered quota'),
      cell: ({ row }) => <Quota value={row.original.quota} />,
    },
    {
      accessorKey: 'claimed_at',
      header: t('Claimed at'),
      cell: ({ row }) => <BusinessDate value={row.original.claimed_at} />,
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) =>
        row.original.state === 'claimed' ? (
          <Button size='sm' onClick={() => setDelivery(row.original)}>
            {t('Review and redeem')}
          </Button>
        ) : null,
    },
  ]
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Claim customer delivery')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-5'>
          <Alert>
            <AlertDescription>
              {t(
                'Claiming binds the quota to your account but does not redeem it. You must confirm redemption separately. Your existing invitation attribution does not change.'
              )}
            </AlertDescription>
          </Alert>
          <p className='text-muted-foreground text-sm'>
            {t(
              'If you signed in after opening a delivery link, reopen the original link or paste its claim code. Claim codes are not saved during sign-in.'
            )}
          </p>
          <Form {...form}>
            <form
              className='flex max-w-xl flex-col gap-3'
              onSubmit={form.handleSubmit((values) => {
                if (locked.current) return
                locked.current = true
                claim.mutate(values.claim_code)
              })}
            >
              <FormField
                control={form.control}
                name='claim_code'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Claim code')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        autoComplete='off'
                        disabled={claim.isPending}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <Button
                type='submit'
                disabled={claim.isPending || redeem.isPending}
              >
                {t('Bind delivery to my account')}
              </Button>
            </form>
          </Form>
          <h2 className='font-semibold'>{t('My claimed deliveries')}</h2>
          <ResourceTable resource='/deliveries/received' columns={columns} />
        </div>
        {delivery && (
          <ConfirmDialog
            open
            onOpenChange={(open) => {
              if (!open && !redeem.isPending) setDelivery(null)
            }}
            title={t('Confirm redemption into your wallet')}
            desc={t(
              'This delivery is already bound to you. Confirm to redeem its fixed quota. Cancel keeps the bound delivery available for later; it does not release it to the agent.'
            )}
            confirmText={t('Redeem into my wallet')}
            isLoading={redeem.isPending}
            disabled={delivery.state !== 'claimed'}
            handleConfirm={() => {
              if (locked.current) return
              locked.current = true
              redeem.mutate(delivery)
            }}
          >
            <p>
              {delivery.name} #{delivery.id} · <Quota value={delivery.quota} />
            </p>
          </ConfirmDialog>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
