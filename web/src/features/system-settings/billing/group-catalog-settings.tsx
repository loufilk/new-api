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
import {
  ArrowDown,
  ArrowUp,
  Shield,
  Building2,
  Gift,
  Award,
  FlaskConical,
} from 'lucide-react'
import { useState } from 'react'

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
}: {
  allGroups: string[]
  revisionData: Catalogue
  scope: string
}) {
  const client = useQueryClient()
  const [draft, setDraft] = useState<{
    group: string
    revision: number
    entries: Catalogue['groups']
    entry: GroupEntry
  }>()
  const [saved, setSaved] = useState(false)
  const mutation = useMutation({
    mutationFn: async (value: NonNullable<typeof draft>) => {
      const result = await api.patch<Envelope<Catalogue>>(catalogueURL, {
        revision: value.revision,
        groups: { ...value.entries, [value.group]: value.entry },
      })
      return required(result.data)
    },
    onSuccess: (data) => {
      client.setQueryData(['admin-group-catalog', scope], data)
      client.invalidateQueries({ queryKey: ['pricing'] })
      setDraft(undefined)
      setSaved(true)
    },
  })

  const pick = (group: string) => {
    setSaved(false)
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

  return (
    <Card>
      <CardHeader>
        <CardTitle>分组与广场展示</CardTitle>
        <CardDescription>
          设置客户能看到的分组说明、标签和顺序。标签不修改价格或路由。
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        <label className='block space-y-1'>
          <span>分组</span>
          <select
            className={selectClass}
            value={draft?.group ?? ''}
            onChange={(e) => pick(e.target.value)}
            disabled={mutation.isPending}
          >
            <option value=''>请选择分组</option>
            {allGroups.map((group) => (
              <option key={group} value={group}>
                {revisionData.groups?.[group]?.label || group}
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
                  value={draft.entry.sort_order}
                  onChange={(e) =>
                    change({ sort_order: Number(e.target.value) })
                  }
                  disabled={mutation.isPending}
                />
              </label>
            </div>
            <label className='flex items-center gap-2'>
              <input
                type='checkbox'
                checked={draft.entry.exclusive}
                onChange={(e) => change({ exclusive: e.target.checked })}
                disabled={mutation.isPending}
              />
              个人专属分组（只对单独授权的用户可见）
            </label>
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
                <span>{draft.entry.label || draft.group}</span>
              </div>
            </fieldset>
            <Button
              onClick={() => mutation.mutate(draft)}
              disabled={mutation.isPending}
            >
              保存分组展示
            </Button>
          </>
        )}
        <ErrorNote error={mutation.error} />
        {saved && <p role='status'>分组展示已保存。</p>}
      </CardContent>
    </Card>
  )
}

function UserGroupEditor({
  user,
  exclusiveGroups,
  scope,
}: {
  user: UserChoice
  exclusiveGroups: string[]
  scope: string
}) {
  const client = useQueryClient()
  const grantsKey = ['admin-group-entitlements', scope, user.id]
  const ratesKey = ['admin-personal-pricing', scope, user.id]
  const grants = useQuery({
    queryKey: grantsKey,
    queryFn: async () =>
      required(
        (await api.get<Envelope<Entitlements>>(`${entitlementURL}/${user.id}`))
          .data
      ),
  })
  const rates = useQuery({
    queryKey: ratesKey,
    queryFn: async () =>
      required(
        (
          await api.get<Envelope<RateData>>(rateURL, {
            params: { user_id: user.id },
          })
        ).data
      ),
  })
  const [pendingGrant, setPendingGrant] = useState<Entitlements>()
  const [price, setPrice] = useState<{
    group: string
    text: string
    revision: number
  }>()
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
    onSuccess: (data) => {
      client.setQueryData(grantsKey, data)
      client.invalidateQueries({ queryKey: ratesKey })
      client.invalidateQueries({ queryKey: ['pricing'] })
      setPendingGrant(undefined)
      setPrice(undefined)
      setNotification('专属分组授权已保存。')
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
          await api.put<Envelope<RateData>>(rateURL, {
            user_id: user.id,
            group: data.group,
            ratio: data.ratio,
            expected_revision: data.revision,
          })
        ).data
      ),
    onSuccess: () => {
      setPrice(undefined)
      client.invalidateQueries({ queryKey: ratesKey })
      client.invalidateQueries({ queryKey: ['pricing'] })
      setNotification('个人专价已保存。')
    },
  })
  const grantDraft = pendingGrant ?? grants.data
  const selectedRate = rates.data?.available_groups?.find(
    (row) => row.group === price?.group
  )
  const locked = saveGrants.isPending || saveRate.isPending
  return (
    <>
      <fieldset
        className='space-y-2 rounded-lg border p-3'
        disabled={locked || !grantDraft}
      >
        <legend className='px-1'>专属分组授权</legend>
        {grants.isLoading && <p role='status'>正在读取授权…</p>}
        <ErrorNote error={grants.error || saveGrants.error} />
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
            {group}
          </label>
        ))}
        <Button
          onClick={() => pendingGrant && saveGrants.mutate(pendingGrant)}
          disabled={!pendingGrant || locked}
        >
          保存用户授权
        </Button>
      </fieldset>
      <fieldset
        className='space-y-3 rounded-lg border p-3'
        disabled={locked || !rates.data}
      >
        <legend className='px-1'>指定分组倍率</legend>
        {rates.isLoading && <p role='status'>正在读取定价…</p>}
        <ErrorNote error={rates.error || saveRate.error} />
        {rates.data?.enabled === false && (
          <p className='text-muted-foreground'>
            个人专价当前不参与新请求计费。
          </p>
        )}
        <label className='block space-y-1'>
          <span>目标分组</span>
          <select
            className={selectClass}
            value={price?.group ?? ''}
            onChange={(e) => {
              const group = e.target.value
              const current = rates.data
              setNotification('')
              saveRate.reset()
              const rule = current?.rules?.find((item) => item.group === group)
              setPrice(
                group && current
                  ? {
                      group,
                      text: rule?.ratio == null ? '' : String(rule.ratio),
                      revision: current.revision,
                    }
                  : undefined
              )
            }}
          >
            <option value=''>请选择目标分组</option>
            {Array.from(
              new Set([
                ...(rates.data?.available_groups ?? []).map(
                  (entry) => entry.group
                ),
                ...(rates.data?.rules ?? []).map((rule) => rule.group),
              ])
            )
              .sort()
              .map((group) => (
                <option key={group} value={group}>
                  {group}
                </option>
              ))}
          </select>
        </label>
        {price && (
          <>
            {selectedRate && (
              <p className='text-muted-foreground'>
                继承倍率 {selectedRate.inherited_ratio}× · 当前有效倍率{' '}
                {selectedRate.effective_ratio}×
              </p>
            )}
            <label className='block space-y-1'>
              <span>个人倍率（例如 0.5）</span>
              <Input
                inputMode='decimal'
                value={price.text}
                onChange={(e) => setPrice({ ...price, text: e.target.value })}
              />
            </label>
            <div className='flex flex-wrap gap-2'>
              <Button
                disabled={
                  locked || !/^\d+(?:\.\d{1,8})?$/.test(price.text.trim())
                }
                onClick={() =>
                  saveRate.mutate({ ...price, ratio: price.text.trim() })
                }
              >
                保存个人倍率
              </Button>
              <Button
                variant='outline'
                disabled={locked}
                onClick={() => saveRate.mutate({ ...price, ratio: null })}
              >
                恢复继承
              </Button>
            </div>
            <p className='text-muted-foreground'>
              个人倍率优先且不叠乘；0× 为显式免费，恢复继承不等于
              0×。改价不会授予分组访问权。
            </p>
          </>
        )}
      </fieldset>
      {notification && <p role='status'>{notification}</p>}
    </>
  )
}

export function GroupCatalogSettings({ groupRatios }: { groupRatios: string }) {
  const userID = useAuthStore((state) => state.auth.user?.id)
  const session = useAuthStore((state) => state.auth.session?.sid)
  const scope = `${userID ?? ''}:${session ?? ''}`
  const [keyword, setKeyword] = useState('')
  const [user, setUser] = useState<UserChoice>()
  const search = useMutation({
    mutationFn: async (term: string) =>
      required(await searchUsers({ keyword: term, page_size: 15 })),
  })
  const catalogue = useQuery({
    queryKey: ['admin-group-catalog', scope],
    queryFn: async () =>
      required((await api.get<Envelope<Catalogue>>(catalogueURL)).data),
    enabled: Boolean(userID),
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
      <GroupCatalogue
        allGroups={allGroups}
        revisionData={catalogue.data}
        scope={scope}
      />
      <Card>
        <CardHeader>
          <CardTitle>个人专价与授权</CardTitle>
          <CardDescription>
            以账号为单位管理分组授权和实际结算倍率，两者分别保存。
          </CardDescription>
        </CardHeader>
        <CardContent className='space-y-4'>
          <form
            className='flex flex-wrap items-end gap-2'
            onSubmit={(e) => {
              e.preventDefault()
              if (!keyword.trim()) return
              setUser(undefined)
              search.mutate(keyword.trim())
            }}
          >
            <label className='flex-1 space-y-1'>
              <span>用户名或用户 ID</span>
              <Input
                value={keyword}
                onChange={(e) => setKeyword(e.target.value)}
                disabled={search.isPending}
              />
            </label>
            <Button
              type='submit'
              disabled={!keyword.trim() || search.isPending}
            >
              搜索用户
            </Button>
          </form>
          <ErrorNote error={search.error} />
          {search.data && search.data.items.length === 0 && <p>未找到用户。</p>}
          {Boolean(search.data?.items.length) && (
            <label className='block space-y-1'>
              <span>指定用户</span>
              <select
                className={selectClass}
                value={user?.id ?? ''}
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
              exclusiveGroups={exclusiveGroups}
              scope={scope}
            />
          )}
        </CardContent>
      </Card>
    </div>
  )
}
