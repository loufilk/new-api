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
import { ExternalLink, ChevronRight } from 'lucide-react'
import { lazy, Suspense, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { LoadingState } from '@/components/loading-state'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  useSidebar,
} from '@/components/ui/sidebar'
import { useChatPresets } from '@/features/chat/hooks/use-chat-presets'
import {
  chatLinkRequiresApiKey,
  resolveChatUrl,
  type ChatPreset,
} from '@/features/chat/lib/chat-links'
import {
  isCCSwitchPreset,
  isSafeClientLink,
} from '@/features/chat/lib/client-import'
import { useAuthStore } from '@/stores/auth-store'

import type { NavChatPresets } from '../types'

const ClientImportDialog = lazy(() =>
  import('@/features/chat/components/client-import-dialog').then((module) => ({
    default: module.ClientImportDialog,
  }))
)

export function ChatPresetsItem(props: { item: NavChatPresets }) {
  const userId = useAuthStore((state) => state.auth.user?.id)
  // Remount the attempt whenever the authenticated account changes.
  return (
    <ChatPresetsSession
      key={userId ?? 'signed-out'}
      item={props.item}
      userId={userId}
    />
  )
}

function ChatPresetsSession(props: { item: NavChatPresets; userId?: number }) {
  const { t } = useTranslation()
  const { chatPresets, serverAddress } = useChatPresets()
  const { state, isMobile, setOpenMobile } = useSidebar()
  const [preset, setPreset] = useState<ChatPreset | null>(null)
  const visiblePresets = chatPresets.filter((item) => item.type !== 'fluent')
  const openClient = (selected: ChatPreset) => {
    if (
      chatLinkRequiresApiKey(selected.url) ||
      isCCSwitchPreset(selected.url)
    ) {
      if (props.userId) setPreset(selected)
      return
    }
    const url = resolveChatUrl({ template: selected.url, serverAddress })
    if (!isSafeClientLink(url)) {
      toast.error(t('Invalid chat link. Please contact the administrator.'))
      return
    }
    window.open(url, '_blank', 'noopener,noreferrer')
    setOpenMobile(false)
  }
  if (visiblePresets.length === 0) return null
  return (
    <>
      {state === 'collapsed' && !isMobile ? (
        <SidebarMenuItem>
          <DropdownMenu>
            <DropdownMenuTrigger
              render={<SidebarMenuButton tooltip={props.item.title} />}
            >
              {props.item.icon && (
                <props.item.icon className='size-4 shrink-0' />
              )}
              <span className='min-w-0 flex-1 truncate'>
                {props.item.title}
              </span>
              <ChevronRight className='ms-auto size-4 shrink-0 opacity-70' />
            </DropdownMenuTrigger>
            <DropdownMenuContent align='start'>
              {visiblePresets.map((item) => (
                <DropdownMenuItem
                  key={item.id}
                  onClick={() => openClient(item)}
                >
                  <span className='min-w-0 flex-1 truncate'>{item.name}</span>
                  <ExternalLink
                    className='ml-auto size-4 shrink-0'
                    aria-hidden='true'
                  />
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </SidebarMenuItem>
      ) : (
        <Collapsible className='group/collapsible' render={<SidebarMenuItem />}>
          <CollapsibleTrigger
            className='group/collapsible-trigger'
            render={<SidebarMenuButton />}
          >
            {props.item.icon && <props.item.icon className='shrink-0' />}
            <span className='min-w-0 flex-1 truncate'>{props.item.title}</span>
            <ChevronRight className='ms-auto size-4 shrink-0 transition-transform duration-200 group-data-[panel-open]/collapsible-trigger:rotate-90' />
          </CollapsibleTrigger>
          <CollapsibleContent className='CollapsibleContent'>
            <SidebarMenuSub>
              {visiblePresets.map((item) => (
                <SidebarMenuSubItem key={item.id}>
                  <SidebarMenuSubButton
                    render={<button type='button' />}
                    onClick={() => openClient(item)}
                    className='w-full justify-between'
                    title={item.name}
                  >
                    <span className='min-w-0 flex-1 truncate text-left'>
                      {item.name}
                    </span>
                    <ExternalLink
                      className='size-4 shrink-0'
                      aria-hidden='true'
                    />
                  </SidebarMenuSubButton>
                </SidebarMenuSubItem>
              ))}
            </SidebarMenuSub>
          </CollapsibleContent>
        </Collapsible>
      )}
      {preset && props.userId && (
        <Suspense fallback={<LoadingState inline message={t('Loading...')} />}>
          <ClientImportDialog
            preset={preset}
            serverAddress={serverAddress}
            userId={props.userId}
            onClose={() => {
              setPreset(null)
              setOpenMobile(false)
            }}
          />
        </Suspense>
      )}
    </>
  )
}
