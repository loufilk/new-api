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
import { ChevronDown, Menu } from 'lucide-react'
import { useMemo } from 'react'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'

import type { TopNavLink } from '../types'

type TopNavProps = React.HTMLAttributes<HTMLElement> & {
  links: TopNavLink[]
}

/**
 * 顶部导航栏组件
 * 在大屏幕显示水平导航，在小屏幕显示下拉菜单
 */
export function TopNav({ className, links, ...props }: TopNavProps) {
  // 规范化链接，确保所有可选属性都有默认值
  const normalizedLinks = useMemo(
    () =>
      links.map((link) => ({
        isActive: false,
        disabled: false,
        external: false,
        documentNavigation: false,
        ...link,
      })),
    [links]
  )

  return (
    <>
      {/* 移动端下拉菜单 */}
      <div className='lg:hidden'>
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger
            render={<Button size='icon' variant='outline' className='size-7' />}
          >
            <Menu />
          </DropdownMenuTrigger>
          <DropdownMenuContent side='bottom' align='start'>
            {normalizedLinks.map((link) => {
              if (link.children?.length) {
                return (
                  <DropdownMenuGroup key={link.href}>
                    <DropdownMenuLabel>{link.title}</DropdownMenuLabel>
                    {link.children.map((child) => (
                      <DropdownMenuItem
                        key={child.href}
                        render={<Link to={child.href} />}
                      >
                        {child.title}
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuGroup>
                )
              }
              const {
                title,
                href,
                isActive,
                disabled,
                external,
                documentNavigation,
              } = link
              let target
              if (documentNavigation) {
                target = (
                  <a
                    href={href}
                    className={!isActive ? 'text-muted-foreground' : ''}
                  >
                    {title}
                  </a>
                )
              } else if (external) {
                target = (
                  <a
                    href={href}
                    target='_blank'
                    rel='noopener noreferrer'
                    className={!isActive ? 'text-muted-foreground' : ''}
                  >
                    {title}
                  </a>
                )
              } else {
                target = (
                  <Link
                    to={href}
                    className={!isActive ? 'text-muted-foreground' : ''}
                    disabled={disabled}
                  >
                    {title}
                  </Link>
                )
              }
              return (
                <DropdownMenuItem key={`${title}-${href}`} render={target} />
              )
            })}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {/* 桌面端水平导航 */}
      <nav
        className={cn(
          'hidden items-center gap-4 whitespace-nowrap lg:flex',
          className
        )}
        {...props}
      >
        {normalizedLinks.map((link) => {
          const {
            title,
            href,
            isActive,
            disabled,
            external,
            documentNavigation,
          } = link
          const linkClassName = `hover:text-primary text-sm font-medium transition-colors ${isActive ? '' : 'text-muted-foreground'}`
          if (link.children?.length) {
            return (
              <DropdownMenu key={href} modal={false}>
                <DropdownMenuTrigger
                  render={
                    <Button
                      variant='ghost'
                      className={cn(
                        'h-auto gap-1 p-0 hover:bg-transparent',
                        linkClassName
                      )}
                    />
                  }
                >
                  {title}
                  <ChevronDown className='size-3' aria-hidden='true' />
                </DropdownMenuTrigger>
                <DropdownMenuContent align='end' className='min-w-40'>
                  {link.children.map((child) => (
                    <DropdownMenuItem
                      key={child.href}
                      render={<Link to={child.href} />}
                    >
                      {child.title}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            )
          }
          if (documentNavigation) {
            return (
              <a key={`${title}-${href}`} href={href} className={linkClassName}>
                {title}
              </a>
            )
          }
          if (external) {
            return (
              <a
                key={`${title}-${href}`}
                href={href}
                target='_blank'
                rel='noopener noreferrer'
                className={linkClassName}
              >
                {title}
              </a>
            )
          }
          return (
            <Link
              key={`${title}-${href}`}
              to={href}
              disabled={disabled}
              className={linkClassName}
            >
              {title}
            </Link>
          )
        })}
      </nav>
    </>
  )
}
