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
import { Button } from '@/components/ui/button'
import {
  Pagination,
  PaginationContent,
  PaginationItem,
} from '@/components/ui/pagination'

import { PAGE_SIZE } from './api'
import { useInvoiceText } from './copy'

export function InvoicePager(props: {
  page: number
  total: number
  onChange: (page: number) => void
  label: string
}) {
  const { text } = useInvoiceText()
  const pages = Math.max(1, Math.ceil(props.total / PAGE_SIZE))
  if (pages === 1 && props.page === 1) return null
  return (
    <Pagination aria-label={props.label} className='mt-3 justify-end'>
      <PaginationContent>
        <PaginationItem>
          <Button
            size='sm'
            variant='ghost'
            disabled={props.page <= 1}
            onClick={() => props.onChange(props.page - 1)}
          >
            {text('Previous page')}
          </Button>
        </PaginationItem>
        <PaginationItem className='text-muted-foreground px-1 text-xs'>
          {text('Page {{page}} of {{pages}}', { page: props.page, pages })}
        </PaginationItem>
        <PaginationItem>
          <Button
            size='sm'
            variant='ghost'
            disabled={props.page >= pages}
            onClick={() => props.onChange(props.page + 1)}
          >
            {text('Next page')}
          </Button>
        </PaginationItem>
      </PaginationContent>
    </Pagination>
  )
}
