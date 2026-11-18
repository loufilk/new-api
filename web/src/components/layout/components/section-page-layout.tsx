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
  Children,
  isValidElement,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react'

import { Main } from './main'
import { PageFooterProvider } from './page-footer'

type SlotProps = { children?: ReactNode }

function SectionPageLayoutTitle(_props: SlotProps) {
  return null
}
SectionPageLayoutTitle.displayName = 'SectionPageLayout.Title'

function SectionPageLayoutDescription(_props: SlotProps) {
  return null
}
SectionPageLayoutDescription.displayName = 'SectionPageLayout.Description'

function SectionPageLayoutActions(_props: SlotProps) {
  return null
}
SectionPageLayoutActions.displayName = 'SectionPageLayout.Actions'

function SectionPageLayoutContent(_props: SlotProps) {
  return null
}
SectionPageLayoutContent.displayName = 'SectionPageLayout.Content'

function SectionPageLayoutBreadcrumb(_props: SlotProps) {
  return null
}
SectionPageLayoutBreadcrumb.displayName = 'SectionPageLayout.Breadcrumb'

export type SectionPageLayoutProps = {
  children: ReactNode
  fixedContent?: boolean
  stackActionsOnMobile?: boolean
}

export function SectionPageLayout(props: SectionPageLayoutProps) {
  const [footerContainer, setFooterContainer] = useState<HTMLDivElement | null>(
    null
  )

  let title: ReactNode = null
  let description: ReactNode = null
  let actions: ReactNode = null
  let content: ReactNode = null
  let breadcrumb: ReactNode = null

  Children.forEach(props.children, (node) => {
    if (!isValidElement(node)) return
    const child = node as ReactElement<SlotProps>
    if (child.type === SectionPageLayoutTitle) title = child.props.children
    else if (child.type === SectionPageLayoutDescription) {
      description = child.props.children
    } else if (child.type === SectionPageLayoutActions) {
      actions = child.props.children
    } else if (child.type === SectionPageLayoutContent) {
      content = child.props.children
    } else if (child.type === SectionPageLayoutBreadcrumb) {
      breadcrumb = child.props.children
    }
  })

  return (
    <PageFooterProvider container={footerContainer}>
      <Main data-slot='section-page'>
        {(title != null ||
          description != null ||
          breadcrumb != null ||
          actions != null) && (
          <div
            className='shrink-0 px-4 pt-4 pb-3 sm:px-6 sm:pt-6 sm:pb-4'
            data-slot='section-page-header'
          >
            {breadcrumb != null && (
              <div className='mb-2 sm:mb-3'>{breadcrumb}</div>
            )}
            <div className='flex flex-wrap items-center justify-between gap-x-3 gap-y-2 sm:gap-x-4'>
              <div
                className={
                  props.stackActionsOnMobile !== false
                    ? 'min-w-0 flex-1 max-sm:basis-full'
                    : 'min-w-0 flex-1'
                }
              >
                {title != null && (
                  <h2 className='text-lg font-semibold tracking-tight break-words sm:text-xl'>
                    {title}
                  </h2>
                )}
                {description != null && (
                  <div
                    className='text-muted-foreground mt-1.5 max-w-3xl text-xs leading-relaxed sm:text-sm'
                    data-slot='section-page-description'
                  >
                    {description}
                  </div>
                )}
              </div>
              {actions != null && (
                <div className='flex max-w-full shrink-0 flex-wrap items-center justify-end gap-2 sm:gap-x-4'>
                  {actions}
                </div>
              )}
            </div>
          </div>
        )}

        <div
          className={
            props.fixedContent
              ? 'min-h-0 flex-1 overflow-hidden px-4 pt-1 pb-4 sm:px-6 sm:pt-1.5 sm:pb-6'
              : 'min-h-0 flex-1 overflow-auto px-4 pt-1 pb-4 sm:px-6 sm:pt-1.5 sm:pb-6'
          }
          data-slot='section-page-content'
        >
          {content}
        </div>

        <div
          ref={setFooterContainer}
          className='bg-background shrink-0 border-t px-4 py-2.5 empty:hidden sm:px-6 sm:py-3'
        />
      </Main>
    </PageFooterProvider>
  )
}

SectionPageLayout.Title = SectionPageLayoutTitle
SectionPageLayout.Description = SectionPageLayoutDescription
SectionPageLayout.Actions = SectionPageLayoutActions
SectionPageLayout.Content = SectionPageLayoutContent
SectionPageLayout.Breadcrumb = SectionPageLayoutBreadcrumb
