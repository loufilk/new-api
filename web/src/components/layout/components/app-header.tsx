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
import { Menu } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitcher } from '@/components/language-switcher'
import { NotificationPopover } from '@/components/notification-popover'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { Button } from '@/components/ui/button'
import { useSidebar } from '@/components/ui/sidebar'
import { extensionBrandLogo, extensionPublicHomeUrl } from '@/extensions'
import { SystemUpdateAction } from '@/features/system-update/system-update-action'
import { useNotifications } from '@/hooks/use-notifications'
import { useSystemConfig } from '@/hooks/use-system-config'
import { useTopNavLinks } from '@/hooks/use-top-nav-links'
import { useAuthStore } from '@/stores/auth-store'

import { defaultTopNavLinks } from '../config/top-nav.config'
import type { TopNavLink } from '../types'
import { Header } from './header'
import { HeaderLogo } from './header-logo'
import { MobileDrawer } from './mobile-drawer'
import { SystemBrand } from './system-brand'
import { TopNav } from './top-nav'

/**
 * General application Header component
 * Integrates navigation bar, search, configuration and profile functions
 *
 * @example
 * // Basic usage
 * <AppHeader />
 *
 * @example
 * // Custom navigation links
 * <AppHeader navLinks={customLinks} />
 *
 * @example
 * // Hide navigation bar and search box
 * <AppHeader showTopNav={false} showSearch={false} />
 *
 * @example
 * // Fully customize left and right content
 * <AppHeader
 *   leftContent={<CustomLeft />}
 *   rightContent={<CustomRight />}
 * />
 */
type AppHeaderProps = {
  /**
   * Custom navigation links, uses default global navigation or dynamically generated from backend if not provided
   */
  navLinks?: TopNavLink[]
  /**
   * Whether to show top navigation bar
   * @default true
   */
  showTopNav?: boolean
  /**
   * Left content, overrides TopNav if provided
   */
  leftContent?: React.ReactNode
  /**
   * Whether to show search box
   * @default true
   */
  showSearch?: boolean
  /**
   * Custom right content, overrides default right content if provided
   */
  rightContent?: React.ReactNode
  /**
   * Whether to show notification button
   * @default true
   */
  showNotifications?: boolean
  /**
   * Whether to show config drawer
   * @default true
   */
  showConfigDrawer?: boolean
  /**
   * Whether to show profile dropdown
   * @default true
   */
  showProfileDropdown?: boolean
}

export function AppHeader({
  navLinks = defaultTopNavLinks,
  showTopNav = true,
  leftContent,
  showSearch = true,
  rightContent,
  showNotifications = true,
  showConfigDrawer = true,
  showProfileDropdown = true,
}: AppHeaderProps) {
  const { t } = useTranslation()
  // Prioritize dynamically generated links from backend
  const dynamicLinks = useTopNavLinks()
  const links = dynamicLinks.length > 0 ? dynamicLinks : navLinks

  // Notifications hook
  const notifications = useNotifications({ autoOpen: showNotifications })

  // Mobile navigation drawer; opening it always yields to the console sidebar
  // so the two overlays can never cover each other.
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const { openMobile, setOpenMobile } = useSidebar()
  const { systemName, loading } = useSystemConfig()
  const user = useAuthStore((state) => state.auth.user)

  useEffect(() => {
    if (openMobile) setMobileNavOpen(false)
  }, [openMobile])

  const openMobileNavigation = () => {
    setOpenMobile(false)
    setMobileNavOpen(true)
  }

  return (
    <>
      <Header>
        <div className='@container/system-brand flex min-w-0 flex-1 items-center gap-1'>
          <SystemBrand variant='inline' />
          <SystemUpdateAction presentation='version' />
        </div>

        {leftContent ? (
          <div className='ms-2 flex items-center'>{leftContent}</div>
        ) : null}

        {rightContent ?? (
          <div className='ms-auto flex shrink-0 items-center gap-1 sm:gap-2'>
            {showTopNav && (
              <>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon-sm'
                  className='lg:hidden'
                  onClick={openMobileNavigation}
                  aria-label={t('Open menu')}
                  aria-expanded={mobileNavOpen}
                  aria-haspopup='dialog'
                  data-slot='mobile-nav-trigger'
                >
                  <Menu className='size-4' aria-hidden='true' />
                </Button>
                <div className='me-1 hidden lg:block'>
                  <TopNav links={links} />
                </div>
              </>
            )}
            {showSearch && (
              <Search className='w-8 flex-none [&>span]:hidden sm:[&>span]:inline' />
            )}
            {showNotifications && (
              <NotificationPopover
                open={notifications.popoverOpen}
                onOpenChange={notifications.setPopoverOpen}
                unreadCount={notifications.unreadCount}
                activeTab={notifications.activeTab}
                onTabChange={notifications.setActiveTab}
                notice={notifications.notice}
                announcements={notifications.announcements}
                loading={notifications.loading}
                onCloseToday={notifications.closeToday}
              />
            )}
            <LanguageSwitcher />
            {showConfigDrawer && <ConfigDrawer />}
            {showProfileDropdown && <ProfileDropdown />}
          </div>
        )}
      </Header>

      {showTopNav && (
        <MobileDrawer
          isOpen={mobileNavOpen}
          onClose={() => setMobileNavOpen(false)}
          homeUrl={extensionPublicHomeUrl()}
          displayLogo={
            <HeaderLogo
              src={extensionBrandLogo()}
              alt={t('Logo')}
              loading={loading}
              logoLoaded
              className='size-full object-contain'
            />
          }
          displaySiteName={systemName}
          loading={loading}
          logoLoaded
          mobileLinksList={links}
          showAuthButtons={showProfileDropdown}
          user={user}
        />
      )}
    </>
  )
}
