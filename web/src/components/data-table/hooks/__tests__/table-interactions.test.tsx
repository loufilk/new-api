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
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'

import type { ColumnDef } from '@/lib/console-table'

import { DataTableView } from '../../core/data-table-view'
import { DataTableFacetedFilter } from '../../toolbar/faceted-filter'
import { useDataTable } from '../use-data-table'

type Channel = { name: string; status: string; children?: Channel[] }
const channels = [
  {
    name: 'Route 12',
    status: 'offline',
    children: [{ name: 'East', status: 'online' }],
  },
  { name: 'Route 2', status: 'online' },
]
const columns: ColumnDef<Channel>[] = [
  {
    accessorKey: 'name',
    header: 'Name',
    size: 180,
    cell: ({ row }) =>
      row.getCanExpand() ? (
        <button
          type='button'
          aria-expanded={row.getIsExpanded()}
          onClick={row.getToggleExpandedHandler()}
        >
          {row.original.name}
        </button>
      ) : (
        row.original.name
      ),
  },
  {
    accessorKey: 'status',
    header: 'Status',
    filterFn: (row, id, values: string[]) => values.includes(row.getValue(id)),
  },
]
const statuses = [{ value: 'online', label: 'Online' }]

function Fixture(props: { data?: Channel[]; descending?: boolean }) {
  const { table } = useDataTable({
    data: props.data ?? channels,
    columns,
    getSubRows: (row) => row.children,
    withExpandedRowModel: true,
    enableColumnResizing: true,
    initialSorting: [{ id: 'name', desc: false }],
    sorting:
      props.descending === undefined
        ? undefined
        : [{ id: 'name', desc: props.descending }],
  })
  return (
    <>
      <DataTableFacetedFilter
        column={table.getColumn('status')}
        title='Availability'
        options={statuses}
      />
      <DataTableView table={table} applyHeaderSize />
    </>
  )
}

it('updates row order when an external sort changes or a background refresh replaces the data', () => {
  const view = render(<Fixture descending={false} />)
  expect(screen.getAllByRole('row')[1]).toHaveTextContent('Route 2')
  view.rerender(<Fixture descending />)
  expect(screen.getAllByRole('row')[1]).toHaveTextContent('Route 12')
  view.rerender(
    <Fixture descending data={[{ name: 'New route', status: 'online' }]} />
  )
  expect(screen.getAllByRole('row')).toHaveLength(2)
  expect(screen.queryByText('Route 12')).not.toBeInTheDocument()
  expect(screen.getByRole('cell', { name: 'New route' })).toBeVisible()
})

it('keeps a mounted facet chip and visible rows in sync as a user selects and clears a filter', async () => {
  render(<Fixture />)
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Availability' }))
  await user.click(screen.getByRole('option', { name: /Online/ }))
  expect(
    screen.queryByRole('cell', { name: 'Route 12' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: /Availability/ })
  ).toHaveTextContent('Online')
  await user.click(screen.getByRole('option', { name: 'Clear filters' }))
  expect(
    screen.getByRole('button', { name: 'Availability' })
  ).not.toHaveTextContent('Online')
  expect(screen.getByRole('cell', { name: 'Route 12' })).toBeVisible()
})

it('expands and collapses a nested channel without changing its parent row', async () => {
  render(<Fixture />)
  const user = userEvent.setup()
  const parent = screen.getByRole('button', { name: 'Route 12' })
  expect(screen.queryByRole('cell', { name: 'East' })).not.toBeInTheDocument()
  await user.click(parent)
  expect(parent).toHaveAttribute('aria-expanded', 'true')
  expect(screen.getByRole('cell', { name: 'East' })).toBeVisible()
  await user.click(parent)
  expect(parent).toHaveAttribute('aria-expanded', 'false')
  expect(screen.queryByRole('cell', { name: 'East' })).not.toBeInTheDocument()
})

it('applies a keyboard column resize to the rendered header', () => {
  render(<Fixture />)
  const header = screen.getByRole('columnheader', { name: /Name/ })
  expect(header).toHaveStyle({ width: '180px' })
  const resizer = within(header).getByRole('separator', {
    name: 'Resize column',
  })
  fireEvent.keyDown(resizer, { key: 'ArrowRight' })
  expect(header).toHaveStyle({ width: '190px' })
})
