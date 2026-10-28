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
import { Link, useNavigate } from '@tanstack/react-router'
import { X, User, Wallet, LogOut, ShieldCheck } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SignOutDialog } from '@/components/sign-out-dialog'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import { Drawer, DrawerContent, DrawerTitle } from '@/components/ui/drawer'
import { Skeleton } from '@/components/ui/skeleton'
import { useIsSidebarModuleVisible } from '@/hooks/use-sidebar-config'
import { useUserDisplay } from '@/hooks/use-user-display'
import { cn } from '@/lib/utils'
import type { AuthUser } from '@/stores/auth-store'

import { MOBILE_DRAWER_CONFIG } from '../constants'
import type { TopNavLink } from '../types'

/**
 * Brand logo component with skeleton loading
 */
interface BrandLogoProps {
  homeUrl: string
  displayLogo: React.ReactNode
  displaySiteName: string
  loading: boolean
  logoLoaded: boolean
  onClick?: () => void
}

function BrandLogo(props: BrandLogoProps) {
  const content = (
    <>
      <div className='relative h-6 w-6'>
        {props.loading || !props.logoLoaded ? (
          <Skeleton className='absolute inset-0 rounded-full' />
        ) : null}
        {props.displayLogo}
      </div>
      {props.loading ? (
        <Skeleton className='h-5 w-20' />
      ) : (
        props.displaySiteName
      )}
    </>
  )
  const className = 'flex items-center gap-2 text-xl font-bold'

  // Extension home URLs may be absolute and must leave the SPA document.
  if (/^https?:\/\//i.test(props.homeUrl)) {
    return (
      <a href={props.homeUrl} className={className} onClick={props.onClick}>
        {content}
      </a>
    )
  }
  return (
    <Link to={props.homeUrl} className={className} onClick={props.onClick}>
      {content}
    </Link>
  )
}

/**
 * Renders a single top navigation link with the correct navigation semantics:
 * SPA route, same-tab document navigation, or external tab. Authentication-only
 * entries send unauthenticated visitors to the sign-in page instead.
 */
interface MobileNavLinkProps {
  link: TopNavLink
  authenticated: boolean
  onNavigate: () => void
}

function MobileNavLink(props: MobileNavLinkProps) {
  const navigate = useNavigate()
  const className = cn(
    'text-primary/60 hover:text-primary/80 transition-colors',
    props.link.disabled && 'pointer-events-none opacity-50'
  )

  const handleClick = (event: React.MouseEvent<HTMLAnchorElement>) => {
    if (props.link.disabled) {
      event.preventDefault()
      return
    }
    props.onNavigate()
    if (props.link.requiresAuth && !props.authenticated) {
      event.preventDefault()
      void navigate({
        to: '/sign-in',
        search: { redirect: props.link.href },
      })
    }
  }

  if (props.link.documentNavigation) {
    return (
      <a
        href={props.link.href}
        className={className}
        aria-disabled={props.link.disabled}
        onClick={handleClick}
      >
        {props.link.title}
      </a>
    )
  }

  if (props.link.external) {
    return (
      <a
        href={props.link.href}
        target='_blank'
        rel='noopener noreferrer'
        className={className}
        aria-disabled={props.link.disabled}
        onClick={handleClick}
      >
        {props.link.title}
      </a>
    )
  }

  return (
    <Link
      to={props.link.href}
      disabled={props.link.disabled}
      className={className}
      onClick={handleClick}
    >
      {props.link.title}
    </Link>
  )
}

/**
 * Mobile user profile section with navigation links
 */
interface MobileUserProfileProps {
  user: AuthUser | null
  onNavigate?: () => void
  onSignOut: () => void
}

function MobileUserProfile(props: MobileUserProfileProps) {
  const { t } = useTranslation()
  const { displayName, initials, roleLabel } = useUserDisplay(props.user)
  const isSecurityVisible = useIsSidebarModuleVisible('/security')

  if (!props.user) return null

  return (
    <div className='flex flex-col text-sm'>
      {/* User header - simplified */}
      <div className='border-border flex items-center gap-2.5 border-b p-2.5'>
        <Avatar className='size-9'>
          <AvatarImage src='/avatars/01.png' alt={`@${displayName}`} />
          <AvatarFallback className='text-xs'>{initials}</AvatarFallback>
        </Avatar>
        <div className='flex flex-1 flex-col gap-0.5 overflow-hidden'>
          <p className='text-foreground truncate font-medium'>{displayName}</p>
          <div className='flex items-center gap-1.5'>
            <span className='text-muted-foreground text-xs'>{roleLabel}</span>
            {props.user.group && (
              <>
                <span className='text-muted-foreground text-xs'>·</span>
                <span className='text-muted-foreground text-xs'>
                  {String(props.user.group)}
                </span>
              </>
            )}
          </div>
        </div>
      </div>

      {/* Navigation links - same style as top nav */}
      <Link
        to='/profile'
        onClick={props.onNavigate}
        className='text-primary/60 hover:text-primary/80 border-border flex items-center gap-2.5 border-b p-2.5 transition-colors'
      >
        <User className='size-4' />
        {t('Profile')}
      </Link>

      {isSecurityVisible && (
        <Link
          to='/security'
          onClick={props.onNavigate}
          className='text-primary/60 hover:text-primary/80 border-border flex items-center gap-2.5 border-b p-2.5 transition-colors'
        >
          <ShieldCheck className='size-4' />
          {t('Security & Access')}
        </Link>
      )}

      <Link
        to='/wallet'
        onClick={props.onNavigate}
        className='text-primary/60 hover:text-primary/80 border-border flex items-center gap-2.5 border-b p-2.5 transition-colors'
      >
        <Wallet className='size-4' />
        {t('Wallet')}
      </Link>

      {/* Sign out - consistent style */}
      <Button
        variant='ghost'
        onClick={props.onSignOut}
        className='text-destructive hover:text-destructive/80 h-auto w-full justify-start gap-2.5 p-2.5 hover:bg-transparent'
      >
        <LogOut className='size-4' />
        {t('Sign out')}
      </Button>
    </div>
  )
}

/**
 * Mobile sign in button for unauthenticated users
 */
interface MobileSignInButtonProps {
  onNavigate?: () => void
}

function MobileSignInButton(props: MobileSignInButtonProps) {
  const { t } = useTranslation()
  return (
    <Button
      variant='secondary'
      size='sm'
      className='h-10 w-full'
      render={<Link to='/sign-in' onClick={props.onNavigate} />}
    >
      {t('Sign in')}
    </Button>
  )
}

/**
 * Mobile drawer component props
 */
export interface MobileDrawerProps {
  isOpen: boolean
  onClose: () => void
  homeUrl: string
  displayLogo: React.ReactNode
  displaySiteName: string
  loading: boolean
  logoLoaded: boolean
  mobileLinksList: TopNavLink[]
  showAuthButtons: boolean
  user: AuthUser | null
}

/**
 * Mobile bottom drawer with the shared Drawer primitive, which provides the
 * modal focus trap, Escape handling, overlay dismissal and background scroll
 * lock. Displays navigation links and the user profile section.
 */
export function MobileDrawer(props: MobileDrawerProps) {
  const { t } = useTranslation()
  const restoreFocusRef = useRef<HTMLElement | null>(null)
  const signOutRequestedRef = useRef(false)
  const [signOutOpen, setSignOutOpen] = useState(false)

  // The Drawer primitive only restores focus to its own DrawerTrigger. This
  // drawer is opened from the app header, so remember and restore that element.
  useEffect(() => {
    if (props.isOpen) {
      restoreFocusRef.current =
        document.activeElement instanceof HTMLElement
          ? document.activeElement
          : null
      return
    }
    const target = restoreFocusRef.current
    restoreFocusRef.current = null
    target?.focus()
  }, [props.isOpen])

  const handleSignOutRequest = () => {
    signOutRequestedRef.current = true
    props.onClose()
  }

  // The drawer unmounts before its close autofocus fires, so the standalone
  // confirmation never shares the modal layer or focus trap with the drawer.
  const handleDrawerCloseAutoFocus = () => {
    if (!signOutRequestedRef.current) return
    signOutRequestedRef.current = false
    setSignOutOpen(true)
  }

  return (
    <>
      <Drawer
        open={props.isOpen}
        autoFocus
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
      >
        <DrawerContent
          className={MOBILE_DRAWER_CONFIG.drawerClassName}
          aria-describedby={undefined}
          onCloseAutoFocus={handleDrawerCloseAutoFocus}
        >
          <DrawerTitle className='sr-only'>
            {t('Toggle navigation menu')}
          </DrawerTitle>
          <div className='flex flex-col gap-4 overflow-y-auto p-4'>
            {/* Header with logo and close button */}
            <div className='flex items-center justify-between'>
              <BrandLogo
                homeUrl={props.homeUrl}
                displayLogo={props.displayLogo}
                displaySiteName={props.displaySiteName}
                loading={props.loading}
                logoLoaded={props.logoLoaded}
                onClick={props.onClose}
              />
              <Button
                variant='ghost'
                size='icon-sm'
                onClick={props.onClose}
                className='hover:text-primary cursor-pointer'
                aria-label={t('Close menu')}
              >
                <X className='size-5' />
              </Button>
            </div>

            {/* Navigation links */}
            <div className='border-border mb-4 flex flex-col rounded-md border text-sm'>
              {props.loading ? (
                <div className='flex flex-col gap-1 p-2'>
                  {Array.from({ length: 4 }, (_, i) => (
                    <Skeleton key={i} className='h-8 w-full' />
                  ))}
                </div>
              ) : (
                props.mobileLinksList.map((link) => (
                  <div
                    key={`${link.title}-${link.href}`}
                    className='border-border border-b p-2.5 last:border-b-0'
                  >
                    <MobileNavLink
                      link={link}
                      authenticated={!!props.user}
                      onNavigate={props.onClose}
                    />
                  </div>
                ))
              )}
            </div>

            {/* User profile section */}
            {props.showAuthButtons &&
              (props.user ? (
                <MobileUserProfile
                  user={props.user}
                  onNavigate={props.onClose}
                  onSignOut={handleSignOutRequest}
                />
              ) : (
                <MobileSignInButton onNavigate={props.onClose} />
              ))}
          </div>
        </DrawerContent>
      </Drawer>

      <SignOutDialog open={signOutOpen} onOpenChange={setSignOutOpen} />
    </>
  )
}
