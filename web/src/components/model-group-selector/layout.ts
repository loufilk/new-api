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
export const modelGroupSelectorLayoutClasses = {
  desktopPanel:
    'max-h-[min(70dvh,28rem,var(--available-height,70dvh))] overflow-hidden',
  desktopContent:
    'grid h-[min(70dvh,28rem,var(--available-height,70dvh))] min-h-0 grid-rows-[minmax(0,1fr)] gap-3 p-2 md:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]',
  groupColumn: 'flex min-h-0 min-w-0 flex-col overflow-hidden',
  groupScroll:
    'mt-2 grid min-h-0 flex-1 auto-rows-min content-start gap-1 overflow-y-auto overscroll-contain pr-1',
  modelColumn: 'flex min-h-0 min-w-0 overflow-hidden rounded-lg border',
  modelCommand: 'min-h-0 flex-1 rounded-lg border-0 bg-transparent p-1',
  modelList:
    'min-h-0 flex-1 max-h-none [scrollbar-color:var(--border)_transparent] [scrollbar-width:thin] [&::-webkit-scrollbar]:block [&::-webkit-scrollbar]:w-1.5 [&::-webkit-scrollbar-thumb]:rounded-full [&::-webkit-scrollbar-thumb]:bg-border [&::-webkit-scrollbar-track]:bg-transparent',
  modelItem:
    'relative mb-0.5 flex items-center justify-between rounded-md border border-transparent px-2 py-1.5 pl-3 text-[12px] leading-4 transition-colors before:absolute before:inset-y-2 before:left-1 before:w-1 before:rounded-full before:bg-transparent',
  selectedModelItem:
    'border-primary/40 bg-primary/12 text-foreground shadow-sm before:bg-primary',
  unselectedModelItem:
    'text-muted-foreground hover:bg-accent hover:text-foreground',
  selectedModelText: 'font-semibold text-foreground',
  unselectedModelText: 'font-medium',
} as const

type ScrollableOption = {
  getBoundingClientRect?: () => Pick<DOMRect, 'top' | 'height'>
  scrollIntoView: (options?: ScrollIntoViewOptions) => void
}

type ScrollableOptionContainer = {
  clientHeight: number
  getBoundingClientRect: () => Pick<DOMRect, 'top'>
  scrollTo?: (options: ScrollToOptions) => void
  scrollTop: number
}

export function scrollSelectedOptionIntoView(
  selectedOption: ScrollableOption | null,
  scrollContainer?: ScrollableOptionContainer | null
): void {
  if (scrollContainer && selectedOption?.getBoundingClientRect) {
    const optionRect = selectedOption.getBoundingClientRect()
    const containerRect = scrollContainer.getBoundingClientRect()
    const scrollTop = Math.max(
      0,
      scrollContainer.scrollTop +
        optionRect.top -
        containerRect.top -
        (scrollContainer.clientHeight - optionRect.height) / 2
    )
    if (scrollContainer.scrollTo) {
      scrollContainer.scrollTo({ top: scrollTop, behavior: 'auto' })
    } else {
      scrollContainer.scrollTop = scrollTop
    }
    return
  }

  selectedOption?.scrollIntoView({
    block: 'center',
    inline: 'nearest',
  })
}
