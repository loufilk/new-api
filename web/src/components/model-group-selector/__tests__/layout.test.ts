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
import { describe, expect, test } from 'vitest'

import {
  modelGroupSelectorLayoutClasses,
  scrollSelectedOptionIntoView,
} from '../layout'

describe('model group selector layout', () => {
  test('bounds the desktop row while allowing long group labels to grow', () => {
    const groupScrollClasses =
      modelGroupSelectorLayoutClasses.groupScroll.split(' ')

    expect(groupScrollClasses.includes('auto-rows-min')).toBeTruthy()
    expect(groupScrollClasses.includes('content-start')).toBeTruthy()
    expect(groupScrollClasses.includes('overflow-y-auto')).toBeTruthy()
    expect(modelGroupSelectorLayoutClasses.desktopContent).toContain(
      'grid-rows-[minmax(0,1fr)]'
    )
  })

  test('centers the selected group inside its own scroll container', () => {
    const scrollCalls: ScrollToOptions[] = []
    const selectedOption = {
      offsetHeight: 32,
      offsetTop: 160,
      getBoundingClientRect: () => ({ top: 240, height: 32 }),
      scrollIntoView() {},
    }
    const scrollContainer = {
      clientHeight: 200,
      scrollTop: 60,
      getBoundingClientRect: () => ({ top: 80, height: 200 }),
      scrollTo(options: ScrollToOptions) {
        scrollCalls.push(options)
      },
    }

    scrollSelectedOptionIntoView(selectedOption, scrollContainer)

    // The option's offset parent need not be its scroll container (for
    // example, the popover positioner). Use their relative viewport positions.
    expect(scrollCalls).toEqual([{ top: 136, behavior: 'auto' }])
  })

  test('falls back to scrollIntoView when no group container is provided', () => {
    const scrollCalls: ScrollIntoViewOptions[] = []
    const selectedOption = {
      scrollIntoView(options?: ScrollIntoViewOptions) {
        scrollCalls.push(options ?? {})
      },
    }

    scrollSelectedOptionIntoView(selectedOption)

    expect(scrollCalls).toEqual([{ block: 'center', inline: 'nearest' }])
  })
})
