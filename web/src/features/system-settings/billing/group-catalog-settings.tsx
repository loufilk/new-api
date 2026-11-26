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

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import { ArrowDown, ArrowUp, ChevronRight, UserRound } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { groupCatalogBadges as badgeOptions } from '@/features/pricing/lib/group-catalog-badges'
import { searchUsers } from '@/features/users/api'
import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'
import {
  getServerErrorSources,
  getServerErrorStatus,
  requireServerSuccess,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { GroupCatalogList } from './group-catalog-list'
import { catalogPlatforms } from './group-catalog-platforms'
import { GroupDisplayPreview } from './group-display-preview'
import type { CatalogPanel } from './group-pricing-workspace'
import { normalizePersonalRatio } from './personal-ratio'

type Badge = keyof typeof badgeOptions
type GroupEntry = {
  label: string
  description: string
  platform: string
  icon?: string
  sort_order: number
  exclusive: boolean
  badges: Badge[]
}
type Catalogue = { revision: number; groups: Record<string, GroupEntry> }
type Entitlements = { groups: string[]; revision: number }
type RateRule = {
  group: string
  ratio: string | number | null
  active?: boolean
}
type RateData = {
  revision: number
  enabled: boolean
  rules: RateRule[]
  available_groups: {
    group: string
    inherited_ratio: number | string
    effective_ratio: number | string
  }[]
}
type Envelope<T> = { success: boolean; data?: T; message?: string }
type UserChoice = { id: number; username: string }

const emptyGroup: GroupEntry = {
  label: '',
  description: '',
  platform: '',
  sort_order: 0,
  exclusive: false,
  badges: [],
}
const selectClass =
  'border-input bg-background h-9 w-full rounded-lg border px-2 text-sm'
const catalogueURL = '/api/option/group-catalog'
const entitlementURL = '/api/option/group-entitlements'
const rateURL = '/api/option/user-group-pricing'

function rateLabel(
  t: TFunction,
  value: number | string | null | undefined
): string {
  if (value == null || value === '') return '—'
  return Number(value) === 0 ? t('0× · Free') : `${String(value)}×`
}

function isRolloutLocked(error: unknown) {
  return getServerErrorSources(error).some(
    (source) => source.code === 'IM21_ROLLOUT_LOCKED'
  )
}

function hasRevisionConflict(error: unknown) {
  return getServerErrorStatus(error) === 409 && !isRolloutLocked(error)
}

function required<T>(response: Envelope<T>): T {
  const value = requireServerSuccess(response).data
  if (value === undefined || value === null) {
    throw new Error('The server did not return the configuration.')
  }
  return value
}

function configuredBaseRates(groupRatios: string): Record<string, number> {
  try {
    const parsed: unknown = JSON.parse(groupRatios)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {}
    }
    return Object.fromEntries(
      Object.entries(parsed).filter(
        ([group, value]) =>
          group !== 'all' &&
          group !== 'auto' &&
          typeof value === 'number' &&
          Number.isFinite(value) &&
          value >= 0
      )
    )
  } catch {
    return {}
  }
}

function ErrorNote({ error }: { error: unknown }) {
  const { t } = useTranslation()
  if (!error) return null
  return (
    <p className='text-destructive text-sm' role='alert'>
      {isRolloutLocked(error)
        ? t('This change is locked until the im.21 rollout is complete.')
        : t(
            'Could not load or save the settings. Reload them before retrying to avoid overwriting another administrator’s changes.'
          )}
    </p>
  )
}

function GroupCatalogue({
  baseRates,
  policy = false,
  active = true,
  revisionData,
  scope,
  reload,
}: {
  baseRates: Record<string, number>
  policy?: boolean
  active?: boolean
  revisionData: Catalogue
  scope: string
  reload: () => Promise<{ data: Catalogue | undefined; isError: boolean }>
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const client = useQueryClient()
  const [draft, setDraft] = useState<{
    group: string
    revision: number
    entries: Catalogue['groups']
    entry: GroupEntry
  }>()
  const allGroups = Object.keys(baseRates).sort()
  const [confirmPolicy, setConfirmPolicy] = useState(false)
  const [saved, setSaved] = useState(false)
  const [groupFilter, setGroupFilter] = useState('')
  const [detailsOpen, setDetailsOpen] = useState(false)
  const detailsTrigger = useRef<HTMLButtonElement>(null)
  const [latest, setLatest] = useState<Catalogue>()
  const [reviewLoading, setReviewLoading] = useState(false)
  const [reviewFailed, setReviewFailed] = useState(false)
  const mutation = useMutation({
    mutationFn: async (value: NonNullable<typeof draft>) => {
      const result = await api.patch<Envelope<Catalogue>>(catalogueURL, {
        revision: value.revision,
        groups: { ...value.entries, [value.group]: value.entry },
      })
      return required(result.data)
    },
    onSuccess: async (data) => {
      await client.cancelQueries({
        queryKey: ['admin-group-catalog', scope],
        exact: true,
      })
      client.setQueryData(['admin-group-catalog', scope], data)
      // Keep the selected group and its server-normalized display visible after saving.
      setDraft((current) => {
        if (!current || !data.groups?.[current.group]) return undefined
        const entry = data.groups[current.group]
        return {
          group: current.group,
          revision: data.revision,
          entries: data.groups,
          entry: { ...emptyGroup, ...entry, badges: entry.badges ?? [] },
        }
      })
      setLatest(undefined)
      setSaved(true)
      setConfirmPolicy(false)
      await Promise.all([
        client.invalidateQueries({ queryKey: ['pricing'] }),
        ...(policy
          ? [client.invalidateQueries({ queryKey: ['user-groups'] })]
          : []),
        client.invalidateQueries({
          queryKey: ['admin-group-entitlements', scope],
        }),
        client.invalidateQueries({
          queryKey: ['admin-personal-pricing', scope],
        }),
      ])
    },
  })

  const pick = (group: string) => {
    setSaved(false)
    setLatest(undefined)
    setReviewFailed(false)
    mutation.reset()
    setDraft(
      group
        ? {
            group,
            revision: revisionData.revision,
            entries: revisionData.groups ?? {},
            entry: {
              ...emptyGroup,
              ...revisionData.groups?.[group],
              badges: revisionData.groups?.[group]?.badges ?? [],
            },
          }
        : undefined
    )
  }
  const change = (patch: Partial<GroupEntry>) => {
    setSaved(false)
    setDraft(
      (current) =>
        current && { ...current, entry: { ...current.entry, ...patch } }
    )
  }
  const move = (index: number, shift: number) => {
    const badges = [...(draft?.entry.badges ?? [])]
    const next = index + shift
    if (next < 0 || next >= badges.length) return
    ;[badges[index], badges[next]] = [badges[next], badges[index]]
    change({ badges })
  }
  const original = draft && {
    ...emptyGroup,
    ...draft.entries[draft.group],
    badges: draft.entries[draft.group]?.badges ?? [],
  }
  const dirty =
    !!draft && JSON.stringify(draft.entry) !== JSON.stringify(original)
  const stale =
    !!draft &&
    (hasRevisionConflict(mutation.error) ||
      revisionData.revision !== draft.revision)
  const available = !!draft && Object.hasOwn(baseRates, draft.group)
  const validSort =
    !!draft &&
    Number.isInteger(draft.entry.sort_order) &&
    Math.abs(draft.entry.sort_order) <= 10000
  const latestEntry = draft &&
    latest && {
      ...emptyGroup,
      ...latest.groups[draft.group],
    }

  const editor = (
    <div className='space-y-4'>
      {draft && (
        <>
          {!available && (
            <p role='alert' className='text-destructive'>
              {t(
                'This group is no longer configured. Discard this draft to edit another group.'
              )}
            </p>
          )}
          {!policy && (
            <div className='grid gap-3 sm:grid-cols-2'>
              <label className='space-y-1'>
                <span>{t('Display name')}</span>
                <Input
                  value={draft.entry.label}
                  maxLength={64}
                  onChange={(e) => change({ label: e.target.value })}
                  disabled={mutation.isPending}
                />
              </label>
              <label className='space-y-1'>
                <span>{t('Description')}</span>
                <Input
                  value={draft.entry.description}
                  maxLength={240}
                  onChange={(e) => change({ description: e.target.value })}
                  disabled={mutation.isPending}
                />
              </label>
              <label className='space-y-1'>
                <span>{t('Platform')}</span>
                <NativeSelect
                  className='w-full'
                  value={draft.entry.platform}
                  disabled={mutation.isPending}
                  onChange={(e) => change({ platform: e.target.value })}
                >
                  {catalogPlatforms.map(([key, title]) => (
                    <NativeSelectOption key={key} value={key}>
                      {t(title)}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </label>
              <label className='space-y-1'>
                <span>{t('Icon source')}</span>
                <NativeSelect
                  className='w-full'
                  value={draft.entry.icon ?? ''}
                  disabled={mutation.isPending}
                  onChange={(event) => change({ icon: event.target.value })}
                >
                  <NativeSelectOption value=''>
                    {t('Inherit platform icon')}
                  </NativeSelectOption>
                  {catalogPlatforms
                    .filter(([key]) => key !== '')
                    .map(([key, title]) => (
                      <NativeSelectOption key={key} value={key}>
                        {t(title)}
                      </NativeSelectOption>
                    ))}
                </NativeSelect>
              </label>
              <label className='space-y-1'>
                <span>{t('Display order')}</span>
                <Input
                  type='number'
                  step='1'
                  min={-10000}
                  max={10000}
                  value={
                    Number.isNaN(draft.entry.sort_order)
                      ? ''
                      : draft.entry.sort_order
                  }
                  onChange={(e) =>
                    change({ sort_order: e.target.valueAsNumber })
                  }
                  disabled={mutation.isPending}
                />
                {!validSort && (
                  <span className='text-destructive block text-xs'>
                    {t('Enter a whole number from -10000 to 10000.')}
                  </span>
                )}
              </label>
            </div>
          )}
          {policy && (
            <>
              <label className='flex items-center gap-2'>
                <input
                  type='checkbox'
                  checked={draft.entry.exclusive}
                  onChange={(e) => change({ exclusive: e.target.checked })}
                  disabled={mutation.isPending || draft.group === 'default'}
                />
                {t('Exclusive group (only visible to authorized users)')}
              </label>
              {draft.group === 'default' && (
                <p className='text-muted-foreground text-xs'>
                  {t('The default group cannot be exclusive.')}
                </p>
              )}
              {draft.entry.exclusive !==
                !!draft.entries[draft.group]?.exclusive && (
                <p className='text-muted-foreground'>
                  {t(
                    'Changing visibility may affect requests made with existing keys. Check group usage before saving.'
                  )}
                </p>
              )}
            </>
          )}
          {!policy && (
            <>
              <fieldset className='space-y-2' disabled={mutation.isPending}>
                <legend>{t('Group badges')}</legend>
                <div className='flex flex-wrap gap-3'>
                  {(Object.keys(badgeOptions) as Badge[]).map((key) => (
                    <label className='flex items-center gap-1' key={key}>
                      <input
                        type='checkbox'
                        checked={draft.entry.badges.includes(key)}
                        onChange={(e) =>
                          change({
                            badges: e.target.checked
                              ? [...draft.entry.badges, key]
                              : draft.entry.badges.filter(
                                  (value) => value !== key
                                ),
                          })
                        }
                      />
                      {t(badgeOptions[key].label)}
                    </label>
                  ))}
                </div>
                <div
                  hidden={!draft.entry.badges.length}
                  className='flex flex-wrap items-center gap-2 rounded-lg border p-3 empty:hidden'
                  aria-label={t('Group badges')}
                >
                  {draft.entry.badges.map((key, index) => {
                    const badge = badgeOptions[key]
                    if (!badge) return null
                    return (
                      <span
                        className='inline-flex items-center gap-1'
                        key={key}
                      >
                        <span
                          className={`inline-flex items-center gap-1 rounded px-1.5 text-xs ${badge.color}`}
                        >
                          <badge.icon size={12} aria-hidden />
                          {t(badge.label)}
                        </span>
                        <button
                          type='button'
                          aria-label={t('Move {{name}} up', {
                            name: t(badge.label),
                          })}
                          disabled={index === 0}
                          onClick={() => move(index, -1)}
                        >
                          <ArrowUp size={14} />
                        </button>
                        <button
                          type='button'
                          aria-label={t('Move {{name}} down', {
                            name: t(badge.label),
                          })}
                          disabled={index === draft.entry.badges.length - 1}
                          onClick={() => move(index, 1)}
                        >
                          <ArrowDown size={14} />
                        </button>
                      </span>
                    )
                  })}
                </div>
              </fieldset>
              {active && (
                <GroupDisplayPreview
                  group={draft.group}
                  entry={draft.entry}
                  baseRatio={baseRates[draft.group]}
                  scope={scope}
                />
              )}
            </>
          )}
          {stale && (
            <div
              role='alert'
              className='space-y-2 rounded-xl border p-3 text-sm'
            >
              <p>
                {t(
                  'Group settings have changed. Your draft is intact. Load the latest settings to compare.'
                )}
              </p>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={reviewLoading || mutation.isPending}
                onClick={async () => {
                  setReviewLoading(true)
                  setReviewFailed(false)
                  setLatest(undefined)
                  try {
                    const result = await reload()
                    if (result.data && !result.isError) setLatest(result.data)
                    else setReviewFailed(true)
                  } catch {
                    setReviewFailed(true)
                  } finally {
                    setReviewLoading(false)
                  }
                }}
              >
                {reviewLoading ? t('Loading…') : t('Load latest settings')}
              </Button>
              {reviewFailed && (
                <p>
                  {t(
                    'Could not load the settings. Your draft is intact. Try again.'
                  )}
                </p>
              )}
              {latestEntry && (
                <div className='space-y-2 break-all'>
                  <p>
                    {t(
                      'Latest: {{name}} · {{platform}} · Order {{order}} · {{visibility}}',
                      {
                        name: latestEntry.label || draft.group,
                        platform: latestEntry.platform
                          ? t(
                              catalogPlatforms.find(
                                ([key]) => key === latestEntry.platform
                              )?.[1] ?? latestEntry.platform
                            )
                          : t('No platform specified'),
                        order: latestEntry.sort_order,
                        visibility: latestEntry.exclusive
                          ? t('Exclusive')
                          : t('Not exclusive'),
                      }
                    )}
                  </p>
                  {!policy && (
                    <p>
                      {t('Icon source')}:{' '}
                      {t(
                        catalogPlatforms.find(
                          ([key]) => key && key === latestEntry.icon
                        )?.[1] ?? 'Inherit platform icon'
                      )}
                    </p>
                  )}
                  {latestEntry.description && (
                    <p>
                      {t('Description: {{description}}', {
                        description: latestEntry.description,
                      })}
                    </p>
                  )}
                  <p>
                    {t('Badges: {{badges}}', {
                      badges:
                        new Intl.ListFormat(locale, {
                          style: 'short',
                          type: 'unit',
                        }).format(
                          (latestEntry.badges ?? []).map((badge) =>
                            t(badgeOptions[badge]?.label ?? badge)
                          )
                        ) || t('None'),
                    })}
                  </p>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    disabled={
                      !latest || latest.revision !== revisionData.revision
                    }
                    onClick={() => {
                      if (
                        !latest ||
                        !original ||
                        latest.revision !== revisionData.revision
                      ) {
                        return
                      }
                      const changes = Object.fromEntries(
                        Object.entries(draft.entry).filter(
                          ([key, value]) =>
                            JSON.stringify(value) !==
                            JSON.stringify(original[key as keyof GroupEntry])
                        )
                      )
                      setDraft({
                        ...draft,
                        revision: latest.revision,
                        entries: latest.groups,
                        entry: { ...latestEntry, ...changes },
                      })
                      setLatest(undefined)
                      mutation.reset()
                    }}
                  >
                    {t('Continue editing after review')}
                  </Button>
                </div>
              )}
            </div>
          )}
          <div className='flex flex-wrap gap-2'>
            <Button
              onClick={() =>
                policy ? setConfirmPolicy(true) : mutation.mutate(draft)
              }
              disabled={
                mutation.isPending ||
                !available ||
                !dirty ||
                stale ||
                !validSort
              }
            >
              {policy ? t('Save visibility policy') : t('Save group display')}
            </Button>
            {dirty && (
              <Button
                variant='outline'
                onClick={() => pick(draft.group)}
                disabled={mutation.isPending}
              >
                {policy
                  ? t('Discard access draft')
                  : t('Discard display draft')}
              </Button>
            )}
          </div>
        </>
      )}
      {policy && draft && (
        <ConfirmDialog
          open={active && confirmPolicy}
          onOpenChange={setConfirmPolicy}
          title={t('Change group visibility?')}
          desc={t(
            'Changing visibility may affect requests made with existing keys. Check group usage before saving.'
          )}
          confirmText={t('Save visibility policy')}
          disabled={stale || !available || !dirty || draft.group === 'default'}
          isLoading={mutation.isPending}
          handleConfirm={() => {
            if (
              !stale &&
              available &&
              dirty &&
              draft.group !== 'default' &&
              !mutation.isPending
            ) {
              setConfirmPolicy(false)
              mutation.mutate(draft)
            }
          }}
        >
          <p className='text-sm break-all'>
            {draft.group} ·{' '}
            {draft.entry.exclusive
              ? t('Visible only to authorized accounts')
              : t('Visible according to existing group permissions')}
          </p>
          {draft.entry.exclusive && (
            <p className='text-sm'>
              {t(
                'Without a grant, existing fixed API keys may return 403 and Auto will skip this group.'
              )}
            </p>
          )}
        </ConfirmDialog>
      )}
      <ErrorNote
        error={!hasRevisionConflict(mutation.error) && mutation.error}
      />
      {saved && (
        <p role='status'>
          {policy ? t('Group visibility saved.') : t('Group display saved.')}
        </p>
      )}
    </div>
  )

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          {policy
            ? t('Group visibility policy')
            : t('Group display in the marketplace')}
        </CardTitle>
        <CardDescription>
          {policy
            ? t(
                'Changing visibility may affect requests made with existing keys. Check group usage before saving.'
              )
            : t(
                'Set the descriptions, badges and order customers see. Badges do not affect pricing or routing.'
              )}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        {policy ? (
          <>
            <label className='block space-y-1 text-sm font-medium'>
              <span>{t('Search groups')}</span>
              <Input
                value={groupFilter}
                placeholder={t('Display name or group ID')}
                onChange={(e) => setGroupFilter(e.target.value)}
              />
            </label>
            <label className='block space-y-1'>
              <span>{t('Group to edit')}</span>
              <select
                className={selectClass}
                value={draft?.group ?? ''}
                onChange={(e) => pick(e.target.value)}
                disabled={mutation.isPending || dirty}
              >
                <option value=''>{t('Select a group')}</option>
                {allGroups
                  .filter((group) => {
                    const label = revisionData.groups?.[group]?.label ?? ''
                    const term = groupFilter.trim().toLocaleLowerCase()
                    return (
                      group === draft?.group ||
                      `${label} ${group}`.toLocaleLowerCase().includes(term)
                    )
                  })
                  .map((group) => (
                    <option key={group} value={group}>
                      {revisionData.groups?.[group]?.label
                        ? `${revisionData.groups[group].label} · ${group}`
                        : group}
                    </option>
                  ))}
              </select>
            </label>
            {editor}
          </>
        ) : (
          <>
            <GroupCatalogList
              groups={revisionData.groups}
              baseRates={baseRates}
              lockedGroup={
                dirty || mutation.isPending ? draft?.group : undefined
              }
              onOpen={(group, trigger) => {
                if ((dirty || mutation.isPending) && group !== draft?.group) {
                  return
                }
                detailsTrigger.current = trigger
                if (
                  group !== draft?.group ||
                  (!dirty && !mutation.isPending && stale)
                ) {
                  pick(group)
                }
                setDetailsOpen(true)
              }}
            />
            <Sheet
              open={active && detailsOpen && !!draft}
              onOpenChange={setDetailsOpen}
            >
              {draft && (
                <SheetContent
                  className={sideDrawerContentClassName('sm:max-w-xl')}
                  finalFocus={detailsTrigger}
                >
                  <SheetHeader
                    className={sideDrawerHeaderClassName('pe-12 sm:pe-12')}
                  >
                    <SheetTitle className='[overflow-wrap:anywhere]'>
                      {t('Group details')}:{' '}
                      {draft.entry.label.trim() || draft.group}
                    </SheetTitle>
                    <SheetDescription className='[overflow-wrap:anywhere]'>
                      {t('ID')}: <code>{draft.group}</code>
                    </SheetDescription>
                  </SheetHeader>
                  <div className={sideDrawerFormClassName('gap-4')}>
                    {editor}
                  </div>
                </SheetContent>
              )}
            </Sheet>
          </>
        )}
      </CardContent>
    </Card>
  )
}

function UserGroupEditor({
  user,
  panel,
  exclusiveGroups,
  catalogue,
  scope,
  onEditing,
}: {
  user: UserChoice
  panel: CatalogPanel
  exclusiveGroups: string[]
  catalogue: Catalogue['groups']
  scope: string
  onEditing: (active: boolean) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const client = useQueryClient()
  const grantsKey = ['admin-group-entitlements', scope, user.id]
  const ratesKey = ['admin-personal-pricing', scope, user.id]
  const grants = useQuery({
    queryKey: grantsKey,
    enabled: panel === 'access',
    queryFn: async ({ signal }) =>
      required(
        (
          await api.get<Envelope<Entitlements>>(
            `${entitlementURL}/${user.id}`,
            { signal }
          )
        ).data
      ),
  })
  const rates = useQuery({
    queryKey: ratesKey,
    enabled: panel === 'personal',
    queryFn: async ({ signal }) =>
      required(
        (
          await api.get<Envelope<RateData>>(rateURL, {
            params: { user_id: user.id },
            signal,
          })
        ).data
      ),
  })
  const [pendingGrant, setPendingGrant] = useState<Entitlements>()
  const [price, setPrice] = useState<{
    group: string
    text: string
    original: string
    revision: number
    restore?: boolean
  }>()
  const [rateConflict, setRateConflict] = useState(false)
  const [grantConflict, setGrantConflict] = useState(false)
  const [notification, setNotification] = useState<
    '' | 'access' | 'checking' | 'saved'
  >('')
  const saveGrants = useMutation({
    mutationFn: async (data: Entitlements) =>
      required(
        (
          await api.put<Envelope<Entitlements>>(
            `${entitlementURL}/${user.id}`,
            data
          )
        ).data
      ),
    onSuccess: async (data) => {
      await client.cancelQueries({ queryKey: grantsKey, exact: true })
      client.setQueryData(grantsKey, data)
      setPendingGrant(undefined)
      setGrantConflict(false)
      setNotification('access')
      await Promise.all([
        client.invalidateQueries({ queryKey: ratesKey }),
        client.invalidateQueries({ queryKey: ['pricing'] }),
        client.invalidateQueries({ queryKey: ['admin-group-catalog', scope] }),
        client.invalidateQueries({
          queryKey: ['admin-group-entitlements', scope],
        }),
        client.invalidateQueries({ queryKey: ['user-groups'] }),
      ])
    },
    onError: (error) => {
      if (hasRevisionConflict(error)) {
        setGrantConflict(true)
        void grants.refetch()
      }
    },
  })
  const saveRate = useMutation({
    mutationFn: async (data: {
      group: string
      ratio: string | null
      revision: number
    }) =>
      required(
        (
          await api.put<Envelope<Pick<RateData, 'revision' | 'enabled'>>>(
            rateURL,
            {
              user_id: user.id,
              group: data.group,
              ratio: data.ratio,
              expected_revision: data.revision,
            }
          )
        ).data
      ),
    onSuccess: async () => {
      setPrice(undefined)
      setRateConflict(false)
      setNotification('checking')
      await Promise.all([
        client.invalidateQueries({ queryKey: ratesKey }),
        client.invalidateQueries({ queryKey: ['pricing'] }),
      ])
      setNotification('saved')
    },
    onError: (error) => {
      if (hasRevisionConflict(error)) {
        setRateConflict(true)
        void rates.refetch()
      }
    },
  })
  const grantDraft = pendingGrant ?? grants.data
  const staleGrant =
    !!pendingGrant &&
    (grantConflict || pendingGrant.revision !== grants.data?.revision)
  const rateData = rates.data
  const availableRates = rateData?.available_groups ?? []
  const rules = new Map(
    (rateData?.rules ?? []).map((item) => [item.group, item])
  )
  const availableNames = new Set(availableRates.map((row) => row.group))
  const inactiveRules = [...rules.values()].filter(
    (rule) => !availableNames.has(rule.group)
  )
  const selectedRate = availableRates.find((row) => row.group === price?.group)
  const selectedRule = price ? rules.get(price.group) : undefined
  const locked = saveGrants.isPending || saveRate.isPending
  const staleDraft =
    !!price && (rateConflict || price.revision !== rateData?.revision)
  const rateUnavailable = rates.isError || rates.isFetching
  const normalized = price ? normalizePersonalRatio(price.text) : null
  const canSubmit = !locked && !staleDraft && !rateUnavailable
  const dirtyPrice =
    !!price && (price.text !== price.original || !!price.restore)
  const dirty = !!pendingGrant || dirtyPrice || locked
  useEffect(() => {
    onEditing(dirty)
    return () => onEditing(false)
  }, [dirty, onEditing])
  const pickRate = (group: string) => {
    if (!rateData || locked || dirtyPrice || price?.group === group) return
    setNotification('')
    setRateConflict(false)
    saveRate.reset()
    const text = String(rules.get(group)?.ratio ?? '')
    setPrice({ group, text, original: text, revision: rateData.revision })
  }
  return (
    <>
      <div className='bg-muted/30 flex items-center gap-3 rounded-2xl border p-3'>
        <UserRound
          aria-hidden
          className='text-muted-foreground size-5 shrink-0'
        />
        <div className='min-w-0'>
          <p className='truncate text-sm font-semibold'>{user.username}</p>
          <p className='text-muted-foreground text-xs'>
            {t('User ID: {{id}}', { id: user.id })}
          </p>
        </div>
      </div>
      <fieldset
        hidden={panel !== 'access'}
        className='space-y-2 rounded-2xl border p-3'
        disabled={locked || !grantDraft}
      >
        <legend className='px-1'>{t('Exclusive group access')}</legend>
        <p className='text-muted-foreground text-xs'>
          {t(
            'This only controls group access. Revoking access does not remove personal rate rules.'
          )}
        </p>
        {grants.isLoading && (
          <p role='status'>{t('Loading access settings…')}</p>
        )}
        <ErrorNote error={grants.error || (!staleGrant && saveGrants.error)} />
        {staleGrant && grants.data && (
          <div role='alert' className='space-y-2 rounded-lg border p-3 text-sm'>
            <p>
              {t(
                'Access settings have changed. Compare the latest settings with your draft before saving.'
              )}
            </p>
            <p className='break-all'>
              {t('Latest access: {{groups}}', {
                groups: grants.isFetching
                  ? t('Checking…')
                  : new Intl.ListFormat(locale, {
                      style: 'short',
                      type: 'unit',
                    }).format(grants.data.groups) || t('None'),
              })}
            </p>
            <p className='break-all'>
              {t('Draft access: {{groups}}', {
                groups:
                  new Intl.ListFormat(locale, {
                    style: 'short',
                    type: 'unit',
                  }).format(pendingGrant?.groups ?? []) || t('None'),
              })}
            </p>
            <Button
              type='button'
              variant='outline'
              size='sm'
              disabled={grants.isError || grants.isFetching}
              onClick={() => {
                const revision = grants.data?.revision
                if (revision == null) return
                setPendingGrant((draft) => draft && { ...draft, revision })
                setGrantConflict(false)
                saveGrants.reset()
              }}
            >
              {t('Continue editing access after review')}
            </Button>
          </div>
        )}
        <p className='text-muted-foreground text-xs'>
          {t(
            'Revoking access may cause existing fixed API keys to return 403; Auto skips inaccessible groups. Regranting access can restore a saved personal rate.'
          )}
        </p>
        {exclusiveGroups.length === 0 && (
          <p className='text-muted-foreground'>
            {t('No exclusive groups are configured.')}
          </p>
        )}
        {exclusiveGroups.map((group) => (
          <label key={group} className='flex items-center gap-2'>
            <input
              type='checkbox'
              checked={grantDraft?.groups.includes(group) ?? false}
              onChange={(e) => {
                if (!grantDraft) return
                setNotification('')
                setPendingGrant({
                  revision: grantDraft.revision,
                  groups: e.target.checked
                    ? [...new Set([...grantDraft.groups, group])]
                    : grantDraft.groups.filter((item) => item !== group),
                })
              }}
            />
            <span className='break-all'>
              {catalogue[group]?.label && catalogue[group].label !== group
                ? `${catalogue[group].label} · ${group}`
                : group}
            </span>
          </label>
        ))}
        <div className='flex flex-wrap gap-2'>
          <Button
            onClick={() => pendingGrant && saveGrants.mutate(pendingGrant)}
            disabled={
              !pendingGrant ||
              locked ||
              staleGrant ||
              grants.isFetching ||
              grants.isError
            }
          >
            {t('Save user access')}
          </Button>
          {pendingGrant && (
            <Button
              variant='outline'
              disabled={locked}
              onClick={() => {
                setPendingGrant(undefined)
                setGrantConflict(false)
                saveGrants.reset()
                if (hasRevisionConflict(saveGrants.error)) void grants.refetch()
              }}
            >
              {t('Discard access draft')}
            </Button>
          )}
        </div>
      </fieldset>
      <fieldset
        hidden={panel !== 'personal'}
        className='space-y-3 rounded-2xl border p-3'
        disabled={locked || !rateData}
      >
        <legend className='px-1'>{t('Personal pricing')}</legend>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Override the rate for this account and group only. This does not grant access or change past charges.'
          )}
        </p>
        {rates.isLoading && <p role='status'>{t('Loading pricing…')}</p>}
        <ErrorNote error={rates.error || (!rateConflict && saveRate.error)} />
        {rateData?.enabled === false && (
          <p
            className='bg-muted/40 rounded-xl border px-3 py-2 text-sm'
            role='status'
          >
            {t(
              'Personal pricing is disabled. You can save rules, but new requests still use the inherited rate.'
            )}
          </p>
        )}
        {availableRates.length > 0 && (
          <ul
            className='divide-y overflow-hidden rounded-xl border'
            aria-label={t('Available group rates')}
          >
            {availableRates.map((row) => {
              const rule = rules.get(row.group)
              const label = catalogue[row.group]?.label
              return (
                <li key={row.group}>
                  <button
                    type='button'
                    aria-pressed={price?.group === row.group}
                    disabled={
                      rates.isError ||
                      (dirtyPrice && price?.group !== row.group)
                    }
                    className='hover:bg-muted/50 aria-pressed:bg-muted flex w-full items-center gap-3 px-3 py-2.5 text-start'
                    onClick={() => pickRate(row.group)}
                  >
                    <span className='min-w-0 flex-1'>
                      <span className='block truncate text-sm font-medium'>
                        {label || row.group}
                      </span>
                      <span className='text-muted-foreground block text-xs break-all'>
                        {label && label !== row.group && `${row.group} · `}
                        {t('Inherited: {{rate}}', {
                          rate: rateLabel(t, row.inherited_ratio),
                        })}
                        {rule && (
                          <>
                            {' '}
                            ·{' '}
                            {rule.active
                              ? t('Personal rule active')
                              : t('Personal rule not applied')}
                          </>
                        )}
                      </span>
                    </span>
                    <span className='shrink-0 text-end'>
                      <span className='block text-sm font-semibold tabular-nums'>
                        {rateUnavailable
                          ? '—'
                          : rateLabel(t, row.effective_ratio)}
                      </span>
                      <span className='text-muted-foreground text-xs'>
                        {rates.isFetching ? t('Checking…') : t('Effective now')}
                      </span>
                    </span>
                    <ChevronRight
                      aria-hidden
                      className='text-muted-foreground size-4 shrink-0'
                    />
                  </button>
                </li>
              )
            })}
          </ul>
        )}
        {rateData && availableRates.length === 0 && (
          <p className='text-muted-foreground text-sm'>
            {t('No groups are available to this user.')}
          </p>
        )}
        {inactiveRules.length > 0 && (
          <div className='space-y-1'>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Saved rules for unavailable groups can be removed. They do not affect billing.'
              )}
            </p>
            <ul className='divide-y overflow-hidden rounded-xl border'>
              {inactiveRules.map((rule) => (
                <li key={rule.group}>
                  <button
                    type='button'
                    aria-pressed={price?.group === rule.group}
                    disabled={dirtyPrice && price?.group !== rule.group}
                    className='hover:bg-muted/50 aria-pressed:bg-muted flex w-full justify-between gap-3 px-3 py-2 text-start text-sm'
                    onClick={() => pickRate(rule.group)}
                  >
                    <span className='break-all'>{rule.group}</span>
                    <span className='text-muted-foreground shrink-0'>
                      {t('Saved: {{rate}}', { rate: rateLabel(t, rule.ratio) })}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}
        {price && (
          <div className='bg-muted/30 space-y-3 rounded-xl border p-4'>
            <p className='text-sm font-semibold break-all'>
              {t('Editing group: {{group}}', { group: price.group })}
            </p>
            {selectedRate && (
              <dl className='grid grid-cols-2 gap-3 text-sm'>
                <div>
                  <dt className='text-muted-foreground text-xs'>
                    {t('Inherited rate')}
                  </dt>
                  <dd>{rateLabel(t, selectedRate.inherited_ratio)}</dd>
                </div>
                <div>
                  <dt className='text-muted-foreground text-xs'>
                    {t('Effective rate')}
                  </dt>
                  <dd className='font-semibold'>
                    {rateUnavailable
                      ? '—'
                      : rateLabel(t, selectedRate.effective_ratio)}
                  </dd>
                </div>
              </dl>
            )}
            {staleDraft && (
              <div
                role='alert'
                className='space-y-2 rounded-lg border p-3 text-sm'
              >
                <p>
                  {t(
                    'Pricing for this user has changed. Your draft is intact. Check the current rate.'
                  )}
                </p>
                {selectedRule && (
                  <p>
                    {t('Latest saved personal rate: {{rate}}', {
                      rate: rateLabel(t, selectedRule.ratio),
                    })}
                  </p>
                )}
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  disabled={rateUnavailable}
                  onClick={() => {
                    const revision = rateData?.revision
                    if (rateUnavailable || revision == null) return
                    const original = String(selectedRule?.ratio ?? '')
                    setPrice((draft) =>
                      draft?.group === price.group
                        ? { ...draft, revision, original }
                        : draft
                    )
                    setRateConflict(false)
                    saveRate.reset()
                  }}
                >
                  {t('Continue editing after review')}
                </Button>
              </div>
            )}
            {selectedRate && !price.restore && (
              <label className='block space-y-1 text-sm'>
                <span>{t('Personal rate')}</span>
                <Input
                  inputMode='decimal'
                  placeholder={t('e.g. 0.5')}
                  value={price.text}
                  onChange={(e) => setPrice({ ...price, text: e.target.value })}
                />
                <span className='text-muted-foreground block text-xs'>
                  {t(
                    '0× means free. Leaving this blank does not restore the inherited rate.'
                  )}
                </span>
                {price.text.trim() && normalized === null && (
                  <span className='text-destructive block text-xs'>
                    {t(
                      'Enter a number from 0 to 10000 with up to eight decimal places.'
                    )}
                  </span>
                )}
              </label>
            )}
            {selectedRate &&
              !staleDraft &&
              !rateUnavailable &&
              (price.restore ? (
                <p className='text-sm'>
                  {t('Rate after restoring:')}{' '}
                  <strong>{rateLabel(t, selectedRate.inherited_ratio)}</strong>
                </p>
              ) : (
                normalized &&
                normalized !== price.original && (
                  <p className='text-sm'>
                    {rateData?.enabled
                      ? t('Expected rate:')
                      : t('Saved rule only (not billed):')}{' '}
                    <strong>{rateLabel(t, normalized)}</strong>
                  </p>
                )
              ))}
            <div className='flex flex-wrap gap-2'>
              {price.restore ? (
                <>
                  <Button
                    disabled={!canSubmit || !selectedRule}
                    onClick={() => saveRate.mutate({ ...price, ratio: null })}
                  >
                    {selectedRate ? t('Confirm restore') : t('Confirm removal')}
                  </Button>
                  <Button
                    variant='outline'
                    onClick={() => setPrice({ ...price, restore: false })}
                  >
                    {t('Cancel')}
                  </Button>
                </>
              ) : (
                <>
                  {selectedRate && (
                    <Button
                      disabled={
                        !canSubmit ||
                        normalized === null ||
                        normalized === price.original
                      }
                      onClick={() =>
                        normalized !== null &&
                        saveRate.mutate({ ...price, ratio: normalized })
                      }
                    >
                      {t('Save personal rate')}
                    </Button>
                  )}
                  {selectedRule && (
                    <Button
                      variant='outline'
                      disabled={!canSubmit}
                      onClick={() => setPrice({ ...price, restore: true })}
                    >
                      {selectedRate
                        ? t('Restore inherited rate')
                        : t('Remove old rule')}
                    </Button>
                  )}
                  {dirtyPrice && (
                    <Button
                      variant='ghost'
                      onClick={() => {
                        setPrice(undefined)
                        setRateConflict(false)
                        saveRate.reset()
                      }}
                    >
                      {t('Discard pricing draft')}
                    </Button>
                  )}
                </>
              )}
            </div>
          </div>
        )}
      </fieldset>
      {notification &&
        (notification === 'access'
          ? panel === 'access'
          : panel === 'personal') && (
          <p role='status'>
            {
              {
                access: t(
                  'Exclusive group access saved. Personal rates have not changed.'
                ),
                checking: t(
                  'Personal rate saved. Checking the effective rate…'
                ),
                saved: t('Personal rate saved.'),
              }[notification]
            }
          </p>
        )}
    </>
  )
}

type GroupCatalogSettingsProps = {
  groupRatios: string
  panel: CatalogPanel
}

export function GroupCatalogSettings(props: GroupCatalogSettingsProps) {
  const userID = useAuthStore((state) => state.auth.user?.id)
  const session = useAuthStore((state) => state.auth.session?.sid)
  const scope = `${userID ?? ''}:${session ?? ''}`
  return (
    <GroupCatalogWorkspace
      key={scope}
      {...props}
      scope={scope}
      enabled={!!userID}
    />
  )
}

function GroupCatalogWorkspace({
  groupRatios,
  panel,
  scope,
  enabled,
}: GroupCatalogSettingsProps & { scope: string; enabled: boolean }) {
  const { t } = useTranslation()
  const [keyword, setKeyword] = useState('')
  const [user, setUser] = useState<UserChoice>()
  const [editing, setEditing] = useState(false)
  const search = useMutation({
    mutationFn: async (term: string) =>
      required(await searchUsers({ keyword: term, page_size: 15 })),
  })
  const catalogue = useQuery({
    queryKey: ['admin-group-catalog', scope],
    queryFn: async ({ signal }) =>
      required(
        (await api.get<Envelope<Catalogue>>(catalogueURL, { signal })).data
      ),
    enabled,
  })
  if (catalogue.isLoading) {
    return <p role='status'>{t('Loading group display settings…')}</p>
  }
  if (!catalogue.data) {
    return (
      <div className='space-y-2'>
        <ErrorNote error={catalogue.error} />
        <Button variant='outline' onClick={() => catalogue.refetch()}>
          {t('Retry loading group settings')}
        </Button>
      </div>
    )
  }

  const baseRates = configuredBaseRates(groupRatios)
  const exclusiveGroups = Object.keys(catalogue.data.groups ?? {})
    .filter((key) => catalogue.data.groups[key]?.exclusive)
    .sort()
  return (
    <div className='space-y-4'>
      <div hidden={panel !== 'groups'}>
        <GroupCatalogue
          baseRates={baseRates}
          active={panel === 'groups'}
          revisionData={catalogue.data}
          scope={scope}
          reload={catalogue.refetch}
        />
      </div>
      <div hidden={panel !== 'access'}>
        <GroupCatalogue
          policy
          active={panel === 'access'}
          baseRates={baseRates}
          scope={scope}
          revisionData={catalogue.data}
          reload={catalogue.refetch}
        />
      </div>
      <div hidden={panel === 'groups'}>
        <Card>
          <CardHeader>
            <CardTitle>{t('Target user')}</CardTitle>
            <CardDescription>
              {t(
                'Manage group access and billing rates for each account. Save them separately.'
              )}
            </CardDescription>
          </CardHeader>
          <CardContent className='space-y-4'>
            <form
              className='flex flex-wrap items-end gap-2'
              onSubmit={(e) => {
                e.preventDefault()
                if (!keyword.trim() || editing || search.isPending) return
                setUser(undefined)
                search.mutate(keyword.trim())
              }}
            >
              <label className='flex-1 space-y-1'>
                <span>{t('Username or user ID')}</span>
                <Input
                  value={keyword}
                  onChange={(e) => setKeyword(e.target.value)}
                  disabled={search.isPending || editing}
                />
              </label>
              <Button
                type='submit'
                disabled={!keyword.trim() || search.isPending || editing}
              >
                {t('Search users')}
              </Button>
            </form>
            <ErrorNote error={search.error} />
            {editing && (
              <p className='text-muted-foreground text-xs'>
                {t('Save or discard your draft before switching accounts.')}
              </p>
            )}
            {search.data && search.data.items.length === 0 && (
              <p>{t('No users found.')}</p>
            )}
            {Boolean(search.data?.items.length) && (
              <label className='block space-y-1'>
                <span>{t('Target user')}</span>
                <select
                  className={selectClass}
                  value={user?.id ?? ''}
                  disabled={editing}
                  onChange={(e) =>
                    setUser(
                      search.data?.items.find(
                        (item) => item.id === Number(e.target.value)
                      )
                    )
                  }
                >
                  <option value=''>{t('Select a user')}</option>
                  {search.data?.items.map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.username} · #{item.id}
                    </option>
                  ))}
                </select>
              </label>
            )}
            {user && (
              <UserGroupEditor
                key={`${scope}:${user.id}`}
                user={user}
                panel={panel}
                catalogue={catalogue.data.groups ?? {}}
                exclusiveGroups={exclusiveGroups}
                scope={scope}
                onEditing={setEditing}
              />
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
