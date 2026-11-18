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
  Activity,
  BookOpen,
  Box,
  ClipboardList,
  CreditCard,
  FileText,
  FlaskConical,
  Image as ImageIcon,
  Key,
  LayoutDashboard,
  Megaphone,
  LifeBuoy,
  ListTodo,
  PlugZap,
  Radio,
  ReceiptText,
  Rocket,
  ServerCog,
  Settings,
  ShieldCheck,
  TrendingUp,
  Ticket,
  User,
  Users,
  Wallet,
  Workflow,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import type { SidebarBadges, SidebarData } from '@/components/layout/types'
import { useStatus } from '@/hooks/use-status'
import { hasPermission } from '@/lib/admin-permissions'
import { isAgentWorkspaceUser } from '@/lib/agent-permissions'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

/**
 * Root navigation groups for the application sidebar.
 *
 * These are shown when the URL does not match any nested sidebar view
 * registered in `layout/lib/sidebar-view-registry.ts`.
 */
export function useSidebarData(badges: SidebarBadges = {}): SidebarData {
  const { t } = useTranslation()
  const { status } = useStatus()
  const user = useAuthStore((state) => state.auth.user)
  const agentGroups: SidebarData['navGroups'] = isAgentWorkspaceUser(user)
    ? [
        {
          id: 'agent',
          title: t('Agent'),
          items: [
            {
              title: t('Agent Overview'),
              url: '/agent-platform',
              icon: Users,
            },
            {
              title: t('Redemption Code Center'),
              url: '/redemption-codes?view=center',
              icon: Ticket,
            },
            {
              title: t('Redemption Code Records'),
              url: '/redemption-codes?view=records',
              icon: ClipboardList,
            },
          ],
        },
      ]
    : []

  return {
    navGroups: [
      ...agentGroups,
      {
        id: 'general',
        title: t('General'),
        items: [
          {
            title: t('Overview'),
            url: '/dashboard/overview',
            icon: Activity,
          },
          {
            title: t('Dashboard'),
            url: '/dashboard/models',
            icon: LayoutDashboard,
          },
          {
            title: t('Flow Analysis'),
            url: '/dashboard/flow',
            icon: Workflow,
          },
          {
            title: t('Model catalog'),
            url: '/pricing',
            icon: Box,
          },
          {
            title: t('Getting started'),
            url: '/getting-started',
            icon: Rocket,
          },
          {
            title: t('API Keys'),
            url: '/keys',
            icon: Key,
          },
          {
            title: t('Usage Logs'),
            url: '/usage-logs/common',
            icon: FileText,
          },
          {
            title: t('Audit Logs'),
            url: '/usage-logs/audit',
            icon: ClipboardList,
          },
          {
            title: t('Task Logs'),
            url: '/usage-logs/task',
            activeUrls: ['/usage-logs/drawing'],
            configUrls: ['/usage-logs/drawing', '/usage-logs/task'],
            icon: ListTodo,
          },
          {
            title: t('Channel status'),
            url: '/channel-status',
            icon: Activity,
          },
          {
            title: t('Usage rankings'),
            url: '/rankings',
            icon: TrendingUp,
          },
          {
            title: t('System announcements'),
            url: '/system-announcements',
            icon: Megaphone,
          },
        ],
      },
      {
        id: 'chat',
        title: t('Chat'),
        items: [
          {
            title: t('Text Generation'),
            url: '/playground',
            icon: FlaskConical,
          },
          {
            title: t('Image Generation'),
            url: '/image',
            icon: ImageIcon,
          },
          {
            title: t('Third-party'),
            icon: PlugZap,
            type: 'chat-presets',
          },
        ],
      },
      {
        id: 'personal',
        title: t('Personal'),
        items: [
          {
            title: t('Wallet'),
            url: '/wallet',
            icon: Wallet,
          },
          ...(status?.account_pool_enabled === true
            ? [
                {
                  title: t('pool.title'),
                  url: '/account-pool',
                  icon: ServerCog,
                },
              ]
            : []),
          ...(user
            ? [
                {
                  title: t('intelligence.title'),
                  url: '/intelligence',
                  icon: FlaskConical,
                },
              ]
            : []),
          {
            title: t('Invitation Rewards'),
            url: '/affiliate',
            icon: Users,
          },
          {
            title: t('Invoices'),
            url: '/invoice',
            icon: ReceiptText,
          },
          {
            title: t('Partner application'),
            url: '/partner-apply',
            icon: PlugZap,
          },
          {
            title: t('Support Center'),
            url: '/support',
            icon: LifeBuoy,
          },
          {
            title: t('Profile'),
            url: '/profile',
            icon: User,
          },
          {
            title: t('Security & Access'),
            url: '/security',
            icon: ShieldCheck,
          },
          {
            title: t('Documentation'),
            url: '/help',
            icon: BookOpen,
          },
        ],
      },
      {
        id: 'admin',
        title: t('Admin'),
        items: [
          {
            title: t('Channels'),
            url: '/channels',
            icon: Radio,
          },
          {
            title: t('Models'),
            url: '/model-management/metadata',
            icon: Box,
          },
          {
            title: t('Users'),
            url: '/users',
            icon: Users,
          },
          ...(hasPermission(user, 'referral_withdrawals', 'read') ||
          hasPermission(user, 'referral_withdrawal_settings', 'read')
            ? [
                {
                  title: t('Referral withdrawals'),
                  url: '/referral-withdrawals',
                  icon: Wallet,
                },
              ]
            : []),
          ...(hasPermission(user, 'commerce_invoices', 'read')
            ? [
                {
                  title: t('Invoice review'),
                  url: '/operations/invoices',
                  icon: ReceiptText,
                },
              ]
            : []),
          ...(hasPermission(user, 'commerce_partners', 'read')
            ? [
                {
                  title: t('Partner review'),
                  url: '/operations/partners',
                  icon: PlugZap,
                },
              ]
            : []),
          ...(hasPermission(user, 'commerce_settings', 'read')
            ? [
                {
                  title: t('Commerce settings'),
                  url: '/operations/commerce-settings',
                  icon: Settings,
                },
              ]
            : []),
          {
            title: t('Redemption Codes'),
            url: '/redemption-codes',
            icon: Ticket,
          },
          {
            title: t('Agent management'),
            url: '/agent-platform/finance',
            icon: Users,
            requiredRole: ROLE.SUPER_ADMIN,
          },
          {
            title: t('Subscriptions'),
            url: '/subscriptions',
            icon: CreditCard,
          },
          {
            title: t('System Info'),
            url: '/system-info',
            icon: ServerCog,
            requiredRole: ROLE.SUPER_ADMIN,
          },
          {
            title: t('Plugins'),
            url: '/task-plugins',
            icon: PlugZap,
            requiredRole: ROLE.SUPER_ADMIN,
          },
          {
            title: t('Request processing'),
            url: '/request-processing',
            icon: Workflow,
            requiredRole: ROLE.SUPER_ADMIN,
          },
          {
            title: t('System Settings'),
            url: '/system-settings/site',
            activeUrls: ['/system-settings'],
            icon: Settings,
            requiredRole: ROLE.SUPER_ADMIN,
            // Model pricing lives in System Settings.
            badge: badges.modelPricing,
          },
        ],
      },
    ],
  }
}
