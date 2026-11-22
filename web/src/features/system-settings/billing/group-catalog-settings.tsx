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
import { isAxiosError } from 'axios'
import {
  ArrowDown,
  ArrowUp,
  ChevronRight,
  UserRound,
  Shield,
  Building2,
  Gift,
  Award,
  FlaskConical,
} from 'lucide-react'
import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { searchUsers } from '@/features/users/api'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

const badgeOptions = {
  self_operated: {
    title: '自营',
    icon: Building2,
    className: 'bg-slate-700 text-white',
  },
  authentic: {
    title: '保真',
    icon: Shield,
    className: 'bg-emerald-700 text-white',
  },
  benefit: { title: '福利', icon: Gift, className: 'bg-blue-700 text-white' },
  stable: { title: '不降', icon: Award, className: 'bg-violet-700 text-white' },
  distill: {
    title: '蒸馏',
    icon: FlaskConical,
    className: 'bg-amber-700 text-white',
  },
} as const

const platforms = [
  ['', '不指定'],
  ['openai', 'OpenAI'],
  ['anthropic', 'Anthropic'],
  ['gemini', 'Gemini'],
  ['deepseek', 'DeepSeek'],
  ['moonshot', 'Moonshot'],
  ['xai', 'xAI'],
  ['zhipu', '智谱 GLM'],
  ['azure', 'Azure'],
  ['other', '其他'],
] as const

type Badge = keyof typeof badgeOptions
type GroupEntry = {
  label: string
  description: string
  platform: string
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

export function normalizePersonalRatio(input: string): string | null {
  const value = input.trim()
  return value.length <= 14 &&
    /^\d+(?:\.\d{1,8})?$/.test(value) &&
    Number(value) <= 10000
    ? value
    : null
}

function rateLabel(value: number | string | null | undefined): string {
  if (value == null || value === '') return '—'
  return Number(value) === 0 ? '0× · 免费' : String(value) + '×'
}

function hasRevisionConflict(error: unknown) {
  return isAxiosError(error) && error.response?.status === 409
}

function required<T>(response: Envelope<T>): T {
  const value = requireServerSuccess(response).data
  if (value === undefined || value === null)
    throw new Error('服务器没有返回配置')
  return value
}

function groupKeys(groupRatios: string) {
  try {
    const parsed: unknown = JSON.parse(groupRatios)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
      return []
    return Object.keys(parsed)
      .filter((key) => !['all', 'auto'].includes(key))
      .sort((a, b) => a.localeCompare(b))
  } catch {
    return []
  }
}

function ErrorNote({ error }: { error: unknown }) {
  if (!error) return null
  return (
    <p className='text-destructive text-sm' role='alert'>
      未保存或读取失败。请刷新配置后重试，避免覆盖其他管理员的修改。
    </p>
  )
}

function GroupCatalogue({
  allGroups,
  revisionData,
  scope,
  reload,
}: {
  allGroups: string[]
  revisionData: Catalogue
  scope: string
  reload: () => Promise<{ data: Catalogue | undefined; isError: boolean }>
}) {
  const client = useQueryClient()
  const [draft, setDraft] = useState<{
    group: string
    revision: number
    entries: Catalogue['groups']
    entry: GroupEntry
  }>()
  const [saved, setSaved] = useState(false)
  const [groupFilter, setGroupFilter] = useState('')
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
      setDraft(undefined)
      setLatest(undefined)
      setSaved(true)
      await Promise.all([
        client.invalidateQueries({ queryKey: ['pricing'] }),
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
  const validSort =
    !!draft &&
    Number.isInteger(draft.entry.sort_order) &&
    Math.abs(draft.entry.sort_order) <= 10000
  const latestEntry = draft &&
    latest && {
      ...emptyGroup,
      ...latest.groups[draft.group],
    }

  return (
    <Card>
      <CardHeader>
        <CardTitle>分组与广场展示</CardTitle>
        <CardDescription>
          设置客户能看到的分组说明、标签和顺序。标签不修改价格或路由。
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        <label className='block space-y-1 text-sm font-medium'>
          <span>搜索分组</span>
          <Input
            value={groupFilter}
            placeholder='展示名称或分组标识'
            onChange={(e) => setGroupFilter(e.target.value)}
          />
        </label>
        <label className='block space-y-1'>
          <span>选择要编辑的分组</span>
          <select
            className={selectClass}
            value={draft?.group ?? ''}
            onChange={(e) => pick(e.target.value)}
            disabled={mutation.isPending || dirty}
          >
            <option value=''>请选择分组</option>
            {allGroups
              .filter((group) => {
                const label = revisionData.groups?.[group]?.label ?? ''
                const term = groupFilter.trim().toLocaleLowerCase()
                return (
                  group === draft?.group ||
                  (label + ' ' + group).toLocaleLowerCase().includes(term)
                )
              })
              .map((group) => (
                <option key={group} value={group}>
                  {revisionData.groups?.[group]?.label
                    ? revisionData.groups[group].label + ' · ' + group
                    : group}
                </option>
              ))}
          </select>
        </label>
        {draft && (
          <>
            <div className='grid gap-3 sm:grid-cols-2'>
              <label className='space-y-1'>
                <span>展示名称</span>
                <Input
                  value={draft.entry.label}
                  maxLength={64}
                  onChange={(e) => change({ label: e.target.value })}
                  disabled={mutation.isPending}
                />
              </label>
              <label className='space-y-1'>
                <span>说明</span>
                <Input
                  value={draft.entry.description}
                  maxLength={240}
                  onChange={(e) => change({ description: e.target.value })}
                  disabled={mutation.isPending}
                />
              </label>
              <label className='space-y-1'>
                <span>所属平台</span>
                <select
                  className={selectClass}
                  value={draft.entry.exclusive ? 'other' : draft.entry.platform}
                  disabled={draft.entry.exclusive || mutation.isPending}
                  onChange={(e) => change({ platform: e.target.value })}
                >
                  {platforms.map(([key, title]) => (
                    <option key={key} value={key}>
                      {title}
                    </option>
                  ))}
                </select>
              </label>
              <label className='space-y-1'>
                <span>展示顺序</span>
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
                    请输入 -10000 至 10000 的整数。
                  </span>
                )}
              </label>
            </div>
            <label className='flex items-center gap-2'>
              <input
                type='checkbox'
                checked={draft.entry.exclusive}
                onChange={(e) => change({ exclusive: e.target.checked })}
                disabled={mutation.isPending || draft.group === 'default'}
              />
              个人专属分组（只对单独授权的用户可见）
            </label>
            {draft.group === 'default' && (
              <p className='text-muted-foreground text-xs'>
                默认分组不能设为个人专属。
              </p>
            )}
            {draft.entry.exclusive !==
              !!draft.entries[draft.group]?.exclusive && (
              <p className='text-muted-foreground'>
                更改公开范围会影响现有 Key 能否接流。保存前请核对分组使用情况。
              </p>
            )}
            <fieldset className='space-y-2' disabled={mutation.isPending}>
              <legend>分组图标</legend>
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
                    {badgeOptions[key].title}
                  </label>
                ))}
              </div>
              <div
                className='flex flex-wrap items-center gap-2 rounded-lg border p-3'
                aria-label='分组展示预览'
              >
                {draft.entry.badges.map((key, index) => {
                  const badge = badgeOptions[key]
                  if (!badge) return null
                  return (
                    <span className='inline-flex items-center gap-1' key={key}>
                      <span
                        className={`inline-flex items-center gap-1 rounded px-1.5 text-xs ${badge.className}`}
                      >
                        <badge.icon size={12} aria-hidden />
                        {badge.title}
                      </span>
                      <button
                        type='button'
                        aria-label={`${badge.title}前移`}
                        disabled={index === 0}
                        onClick={() => move(index, -1)}
                      >
                        <ArrowUp size={14} />
                      </button>
                      <button
                        type='button'
                        aria-label={`${badge.title}后移`}
                        disabled={index === draft.entry.badges.length - 1}
                        onClick={() => move(index, 1)}
                      >
                        <ArrowDown size={14} />
                      </button>
                    </span>
                  )
                })}
                <span className='font-medium'>
                  {draft.entry.label || draft.group}
                </span>
                <span className='text-muted-foreground text-xs'>
                  {draft.entry.exclusive
                    ? '仅已授权账号可见'
                    : '按现有分组权限展示'}
                </span>
                {draft.entry.description && (
                  <p className='text-muted-foreground w-full text-xs'>
                    {draft.entry.description}
                  </p>
                )}
              </div>
            </fieldset>
            {stale && (
              <div
                role='alert'
                className='space-y-2 rounded-xl border p-3 text-sm'
              >
                <p>分组设置已变更，草稿仍在。请读取最新版本并核对。</p>
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
                  {reviewLoading ? '正在读取…' : '读取最新设置'}
                </Button>
                {reviewFailed && <p>读取失败，未修改草稿。请重试。</p>}
                {latestEntry && (
                  <div className='space-y-2 break-all'>
                    <p>
                      最新版本：{latestEntry.label || draft.group} ·{' '}
                      {latestEntry.platform || '未指定平台'} · 顺序{' '}
                      {latestEntry.sort_order} ·{' '}
                      {latestEntry.exclusive ? '个人专属' : '非个人专属'}
                    </p>
                    {latestEntry.description && (
                      <p>说明：{latestEntry.description}</p>
                    )}
                    <p>
                      标签：
                      {latestEntry.badges
                        .map((badge) => badgeOptions[badge]?.title ?? badge)
                        .join('、') || '无'}
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
                        )
                          return
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
                      核对后继续编辑
                    </Button>
                  </div>
                )}
              </div>
            )}
            <div className='flex flex-wrap gap-2'>
              <Button
                onClick={() => mutation.mutate(draft)}
                disabled={mutation.isPending || !dirty || stale || !validSort}
              >
                保存分组展示
              </Button>
              {dirty && (
                <Button
                  variant='outline'
                  onClick={() => pick(draft.group)}
                  disabled={mutation.isPending}
                >
                  放弃展示草稿
                </Button>
              )}
            </div>
          </>
        )}
        <ErrorNote
          error={!hasRevisionConflict(mutation.error) && mutation.error}
        />
        {saved && <p role='status'>分组展示已保存。</p>}
      </CardContent>
    </Card>
  )
}

function UserGroupEditor({
  user,
  exclusiveGroups,
  catalogue,
  scope,
  onEditing,
}: {
  user: UserChoice
  exclusiveGroups: string[]
  catalogue: Catalogue['groups']
  scope: string
  onEditing: (active: boolean) => void
}) {
  const client = useQueryClient()
  const grantsKey = ['admin-group-entitlements', scope, user.id]
  const ratesKey = ['admin-personal-pricing', scope, user.id]
  const grants = useQuery({
    queryKey: grantsKey,
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
  const [notification, setNotification] = useState('')
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
      setNotification('专属分组授权已保存，未修改个人专价。')
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
      setNotification('个人专价已保存，正在重新核对有效倍率。')
      await Promise.all([
        client.invalidateQueries({ queryKey: ratesKey }),
        client.invalidateQueries({ queryKey: ['pricing'] }),
      ])
      setNotification('个人专价已保存。')
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
          <p className='text-muted-foreground text-xs'>用户 ID：{user.id}</p>
        </div>
      </div>
      <fieldset
        className='space-y-2 rounded-2xl border p-3'
        disabled={locked || !grantDraft}
      >
        <legend className='px-1'>专属分组授权</legend>
        <p className='text-muted-foreground text-xs'>
          此处只管理分组访问权。撤销授权不会清除个人倍率规则。
        </p>
        {grants.isLoading && <p role='status'>正在读取授权…</p>}
        <ErrorNote error={grants.error || (!staleGrant && saveGrants.error)} />
        {staleGrant && grants.data && (
          <div role='alert' className='space-y-2 rounded-lg border p-3 text-sm'>
            <p>授权已被修改，草稿仍在。请比较后再决定是否保存。</p>
            <p className='break-all'>
              最新授权：
              {grants.isFetching
                ? '核对中'
                : grants.data.groups.join('、') || '无'}
            </p>
            <p className='break-all'>
              授权草稿：{pendingGrant?.groups.join('、') || '无'}
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
              核对后继续编辑授权
            </Button>
          </div>
        )}
        {exclusiveGroups.length === 0 && (
          <p className='text-muted-foreground'>暂未设置专属分组。</p>
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
                ? catalogue[group].label + ' · ' + group
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
            保存用户授权
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
              放弃授权草稿
            </Button>
          )}
        </div>
      </fieldset>
      <fieldset
        className='space-y-3 rounded-2xl border p-3'
        disabled={locked || !rateData}
      >
        <legend className='px-1'>个人专价</legend>
        <p className='text-muted-foreground text-xs'>
          只覆盖该账号调用指定分组的倍率，不授予访问权，也不改变历史账单。
        </p>
        {rates.isLoading && <p role='status'>正在读取定价…</p>}
        <ErrorNote error={rates.error || (!rateConflict && saveRate.error)} />
        {rateData?.enabled === false && (
          <p
            className='bg-muted/40 rounded-xl border px-3 py-2 text-sm'
            role='status'
          >
            个人专价已停用。规则可保存，但新请求仍按继承倍率计费。
          </p>
        )}
        {availableRates.length > 0 && (
          <ul
            className='divide-y overflow-hidden rounded-xl border'
            aria-label='可用分组倍率'
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
                        {label && label !== row.group && row.group + ' · '}
                        继承 {rateLabel(row.inherited_ratio)}
                        {rule &&
                          (rule.active
                            ? ' · 个人规则已启用'
                            : ' · 个人规则未计费')}
                      </span>
                    </span>
                    <span className='shrink-0 text-end'>
                      <span className='block text-sm font-semibold tabular-nums'>
                        {rateUnavailable ? '—' : rateLabel(row.effective_ratio)}
                      </span>
                      <span className='text-muted-foreground text-xs'>
                        {rates.isFetching ? '核对中' : '当前有效'}
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
          <p className='text-muted-foreground text-sm'>该用户暂无可用分组。</p>
        )}
        {inactiveRules.length > 0 && (
          <div className='space-y-1'>
            <p className='text-muted-foreground text-xs'>
              不可用分组的存量规则 · 可清除，不参与扣费
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
                      已存 {rateLabel(rule.ratio)}
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
              编辑分组：{price.group}
            </p>
            {selectedRate && (
              <dl className='grid grid-cols-2 gap-3 text-sm'>
                <div>
                  <dt className='text-muted-foreground text-xs'>继承倍率</dt>
                  <dd>{rateLabel(selectedRate.inherited_ratio)}</dd>
                </div>
                <div>
                  <dt className='text-muted-foreground text-xs'>
                    当前有效倍率
                  </dt>
                  <dd className='font-semibold'>
                    {rateUnavailable
                      ? '—'
                      : rateLabel(selectedRate.effective_ratio)}
                  </dd>
                </div>
              </dl>
            )}
            {staleDraft && (
              <div
                role='alert'
                className='space-y-2 rounded-lg border p-3 text-sm'
              >
                <p>该用户的定价已变更。草稿仍在，请核对当前倍率。</p>
                {selectedRule && (
                  <p>最新已存个人倍率：{rateLabel(selectedRule.ratio)}</p>
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
                  核对后继续编辑
                </Button>
              </div>
            )}
            {selectedRate && !price.restore && (
              <label className='block space-y-1 text-sm'>
                <span>个人倍率</span>
                <Input
                  inputMode='decimal'
                  placeholder='例如 0.5'
                  value={price.text}
                  onChange={(e) => setPrice({ ...price, text: e.target.value })}
                />
                <span className='text-muted-foreground block text-xs'>
                  0× 表示免费。留空不会恢复继承。
                </span>
                {price.text.trim() && normalized === null && (
                  <span className='text-destructive block text-xs'>
                    请输入 0 至 10000 的小数，最多八位小数。
                  </span>
                )}
              </label>
            )}
            {selectedRate &&
              !staleDraft &&
              !rateUnavailable &&
              (price.restore ? (
                <p className='text-sm'>
                  恢复后的倍率：
                  <strong>{rateLabel(selectedRate.inherited_ratio)}</strong>
                </p>
              ) : (
                normalized &&
                normalized !== price.original && (
                  <p className='text-sm'>
                    {rateData?.enabled
                      ? '预计改后倍率'
                      : '仅保存规则（暂不计费）'}
                    ：<strong>{rateLabel(normalized)}</strong>
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
                    确认{selectedRate ? '恢复继承' : '清除旧规则'}
                  </Button>
                  <Button
                    variant='outline'
                    onClick={() => setPrice({ ...price, restore: false })}
                  >
                    取消
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
                      保存个人倍率
                    </Button>
                  )}
                  {selectedRule && (
                    <Button
                      variant='outline'
                      disabled={!canSubmit}
                      onClick={() => setPrice({ ...price, restore: true })}
                    >
                      {selectedRate ? '恢复继承' : '清除旧规则'}
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
                      放弃价格草稿
                    </Button>
                  )}
                </>
              )}
            </div>
          </div>
        )}
      </fieldset>
      {notification && <p role='status'>{notification}</p>}
    </>
  )
}

type GroupCatalogSettingsProps = {
  groupRatios: string
  panel?: 'groups' | 'people'
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
  if (catalogue.isLoading) return <p role='status'>正在加载分组展示设置…</p>
  if (!catalogue.data)
    return (
      <div className='space-y-2'>
        <ErrorNote error={catalogue.error} />
        <Button variant='outline' onClick={() => catalogue.refetch()}>
          重试读取分组设置
        </Button>
      </div>
    )

  const allGroups = groupKeys(groupRatios)
  const exclusiveGroups = Object.keys(catalogue.data.groups ?? {})
    .filter((key) => catalogue.data.groups[key]?.exclusive)
    .sort()
  return (
    <div className='space-y-4'>
      <div hidden={panel === 'people'}>
        <GroupCatalogue
          allGroups={allGroups}
          revisionData={catalogue.data}
          scope={scope}
          reload={catalogue.refetch}
        />
      </div>
      <div hidden={panel === 'groups'}>
        <Card>
          <CardHeader>
            <CardTitle>账号定价与访问</CardTitle>
            <CardDescription>
              以账号为单位管理分组授权和实际结算倍率，两者分别保存。
            </CardDescription>
          </CardHeader>
          <CardContent className='space-y-4'>
            <form
              className='flex flex-wrap items-end gap-2'
              onSubmit={(e) => {
                e.preventDefault()
                if (!keyword.trim() || editing) return
                setUser(undefined)
                search.mutate(keyword.trim())
              }}
            >
              <label className='flex-1 space-y-1'>
                <span>用户名或用户 ID</span>
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
                搜索用户
              </Button>
            </form>
            <ErrorNote error={search.error} />
            {editing && (
              <p className='text-muted-foreground text-xs'>
                请先保存或放弃当前草稿，再切换账号。
              </p>
            )}
            {search.data && search.data.items.length === 0 && (
              <p>未找到用户。</p>
            )}
            {Boolean(search.data?.items.length) && (
              <label className='block space-y-1'>
                <span>指定用户</span>
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
                  <option value=''>请选择用户</option>
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
