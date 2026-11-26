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
import { useId, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { groupCatalogBadges } from '@/features/pricing/lib/group-catalog-badges'

type LineDraft = {
  label: string
  sort_order?: number
  representative_model?: string
  publish_health?: boolean
  badges?: string[]
}
type Settings = {
  revision: number
  groups: Record<string, LineDraft & { id: string }>
}

const endpoint = '/api/option/public-model-lines'

export function PublicModelLineSettings(props: { groupRatios: string }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const section = useId()
  const userID = useAuthStore((state) => state.auth.user?.id)
  const sid = useAuthStore((state) => state.auth.session?.sid)
  const queryKey = ['admin-public-model-lines', userID, sid]
  const settings = useQuery({
    queryKey,
    enabled: !!userID,
    queryFn: async ({ signal }) => requireServerSuccess((await api.get(endpoint, {signal})).data).data as Settings,
    gcTime: 0,
    retry: false,
  })
  const [group, setGroup] = useState('')
  const [draft, setDraft] = useState<LineDraft>()
  const [confirm, setConfirm] = useState(false)
  const selected = settings.data?.groups[group]
  const mutation = useMutation({
    mutationFn: async (remove: boolean) => {
      if (!settings.data || !group) return
      const groups: Record<string, LineDraft> = Object.fromEntries(
        Object.entries(settings.data.groups).map(([name, line]) => [
          name, {
            label: line.label,
            sort_order: line.sort_order,
            representative_model: line.representative_model,
            publish_health: line.publish_health,
            badges: line.badges,
          },
        ])
      )
      if (remove) delete groups[group]
      else if (draft) groups[group] = draft
      return requireServerSuccess((await api.put(endpoint, { revision: settings.data.revision, groups })).data).data as Settings
    },
    onSuccess: async (data) => {
      if (!data) return
      await client.cancelQueries({ queryKey })
      client.setQueryData(queryKey, data)
      setDraft(data.groups[group] ? {
        label: data.groups[group].label,
        sort_order: data.groups[group].sort_order,
        representative_model: data.groups[group].representative_model,
        publish_health: data.groups[group].publish_health,
        badges: data.groups[group].badges,
      } : {label: ''})
      setConfirm(false)
      await client.invalidateQueries({ queryKey: ['public-model-lines'] })
    },
    onError: () => {
      setConfirm(false)
      toast.error(t('Could not save public model lines. Reload and check the group and model.'))
      void settings.refetch()
    },
  })

  let names = Object.keys(settings.data?.groups ?? {})
  try {
    names = [...new Set([...names, ...Object.keys(JSON.parse(props.groupRatios) ?? {})])]
  } catch {
    // Legacy invalid ratios still allow an existing publication to be removed.
  }
  names = names.filter((name) => name !== 'auto' && name !== 'all').sort()
  return (
    <Card className='mb-5'>
      <CardHeader>
        <CardTitle>{t('Public model lines')}</CardTitle>
        <CardDescription>{t('Choose what signed-out visitors see. This does not change routing or prices.')}</CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        {settings.isPending && <p role='status' className='text-muted-foreground text-sm'>{t('Loading...')}</p>}
        {settings.isError && (
          <Button type='button' variant='outline' onClick={() => void settings.refetch()}>
            {t('Retry')}
          </Button>
        )}
        {settings.data && (
          <div className='grid gap-2 sm:max-w-lg'>
            <label className='text-sm font-medium' htmlFor={`${section}-group`}>{t('Group')}</label>
            <select
              className='border-input bg-background h-10 w-full rounded-md border px-3 text-sm'
              id={`${section}-group`}
              value={group}
              disabled={mutation.isPending}
              onChange={(event) => {
                const name = event.target.value
                setGroup(name)
                const line = settings.data?.groups[name]
                setDraft(line ? {
                  label: line.label, sort_order: line.sort_order,
                  representative_model: line.representative_model,
                  publish_health: line.publish_health, badges: line.badges,
                } : { label: '' })
                setConfirm(false)
              }}
            >
              <option value=''>{t('Select a group')}</option>
              {names.map((name) => <option key={name} value={name}>{name}</option>)}
            </select>
          </div>
        )}
        {group && draft && settings.data && (
          <form className='space-y-4' onSubmit={(event) => {
            event.preventDefault()
            if (confirm) mutation.mutate(false)
            else setConfirm(true)
          }}>
            <fieldset disabled={mutation.isPending} className='grid gap-3 sm:grid-cols-2'>
              <label className='grid gap-1.5 text-sm font-medium'>
                {t('Public display name')}
                <Input required maxLength={64} autoComplete='off' value={draft.label} onChange={(event) => {
                  setDraft({ ...draft, label: event.target.value })
                  setConfirm(false)
                }} />
              </label>
              <label className='grid gap-1.5 text-sm font-medium'>
                {t('Sort order')}
                <Input type='number' min={-10000} max={10000} value={draft.sort_order ?? 0} onChange={(event) => {
                  setDraft({ ...draft, sort_order: Number(event.target.value) })
                  setConfirm(false)
                }} />
              </label>
              <label className='grid gap-1.5 text-sm font-medium'>
                {t('Representative model ID')}
                <Input value={draft.representative_model ?? ''} required={draft.publish_health} maxLength={128} autoComplete='off' onChange={(event) => {
                  setDraft({ ...draft, representative_model: event.target.value })
                  setConfirm(false)
                }} />
              </label>
              <label className='flex items-center gap-2 self-end py-2 text-sm'>
                <input type='checkbox' checked={!!draft.publish_health} onChange={(event) => {
                  setDraft({ ...draft, publish_health: event.target.checked })
                  setConfirm(false)
                }} />
                {t('Publish line health')}
              </label>
              <fieldset className='flex flex-wrap gap-x-4 gap-y-2 text-sm sm:col-span-2'>
                <legend className='mb-1.5 font-medium'>{t('Badges')}</legend>
                {Object.entries(groupCatalogBadges).map(([value, { label }]) => (
                  <label key={value} className='flex items-center gap-1.5'>
                    <input type='checkbox' checked={draft.badges?.includes(value) ?? false} onChange={(event) => {
                      const badges = draft.badges ?? []
                      setDraft({ ...draft, badges: event.target.checked ? [...badges, value] : badges.filter((badge) => badge !== value) })
                      setConfirm(false)
                    }} />
                    {t(label)}
                  </label>
                ))}
              </fieldset>
            </fieldset>
            {confirm && <p role='status' className='text-sm'>{t('This name and the selected badges will be visible to anyone. Confirm to publish.')}</p>}
            <div className='flex flex-wrap gap-2'>
              <Button type='submit' disabled={mutation.isPending || !draft.label.trim()}>
                {confirm ? t('Confirm publication') : selected ? t('Update public line') : t('Publish line')}
              </Button>
              {selected && (
                <Button type='button' variant='outline' disabled={mutation.isPending} onClick={() => mutation.mutate(true)}>
                  {t('Unpublish line')}
                </Button>
              )}
            </div>
          </form>
        )}
      </CardContent>
    </Card>
  )
}
