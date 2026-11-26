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

import { useTranslation } from 'react-i18next'

import { CatalogModelIcon } from '@/components/catalog-model-icon'
import { CopyButton } from '@/components/copy-button'
import {
  BadgeCell,
  BadgeListCell,
  DataTableColumnHeader,
} from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import type { ColumnDef } from '@/lib/console-table'

import { parseTags } from '../lib/filters'
import {
  catalogModelPlatformIcon,
  getMarketPriceModel,
} from '../lib/market-groups'
import { quotedMarketGroups } from '../lib/market-result-groups'
import {
  getModelEndpoints,
  getModelEndpointTypes,
  type ModelEndpointMap,
} from '../lib/model-endpoints'
import type { PricingModel } from '../types'
import { CachedPriceCell } from './cached-price-cell'
import { CatalogGroupBadge } from './catalog-group-badge'
import { ModelBillingModeBadge } from './model-billing-mode-badge'
import { ModelPriceCell, type ModelPriceCellOptions } from './model-price-cell'

// ----------------------------------------------------------------------------
// Pricing Table Columns
// ----------------------------------------------------------------------------

export type PricingColumnsOptions = ModelPriceCellOptions & {
  onModelClick?: (name: string) => void
  endpointMap?: ModelEndpointMap
  platformPriceGroups?: ReadonlySet<string>
}

export function usePricingColumns(
  options: PricingColumnsOptions = {}
): ColumnDef<PricingModel>[] {
  const { t } = useTranslation()

  return [
    // Model column
    {
      accessorKey: 'model_name',
      meta: { label: t('Model') },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Model')} />
      ),
      cell: ({ row }) => {
        const model = row.original
        return (
          <div className='flex w-64 min-w-0 items-center gap-2'>
            <CatalogModelIcon
              icon={model.icon}
              vendorIcon={model.vendor_icon}
              vendorName={model.vendor_name}
              platformIcon={catalogModelPlatformIcon(model)}
              name={model.model_name}
              size={20}
            />
            <Button
              type='button'
              variant='link'
              className='h-auto min-w-0 flex-1 justify-start p-0 text-left font-mono'
              title={model.model_name}
              onClick={() => options.onModelClick?.(model.model_name)}
            >
              <span className='truncate'>{model.model_name}</span>
            </Button>
            <CopyButton
              value={model.model_name}
              tooltip={t('Copy model name')}
              className='size-7'
            />
          </div>
        )
      },
      size: 280,
      minSize: 200,
    },

    // Type column
    {
      accessorKey: 'quota_type',
      header: t('Type'),
      cell: ({ row }) => (
        <ModelBillingModeBadge model={row.original} className='-ml-1.5' />
      ),
      size: 110,
      enableSorting: false,
    },

    // Price column
    {
      accessorKey: 'price',
      meta: { label: t('Price') },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Price')} />
      ),
      cell: ({ row }) => (
        <ModelPriceCell
          model={getMarketPriceModel(row.original, options.platformPriceGroups)}
          options={options}
        />
      ),
      size: 180,
      enableSorting: false,
    },

    // Cached price column (Vercel AI Gateway style)
    {
      id: 'cached_price',
      header: t('Cached'),
      cell: ({ row }) => (
        <CachedPriceCell
          model={getMarketPriceModel(row.original, options.platformPriceGroups)}
          options={options}
        />
      ),
      size: 110,
      enableSorting: false,
    },

    // Vendor column
    {
      accessorKey: 'vendor_name',
      header: t('Vendor'),
      cell: ({ row }) => {
        const model = row.original
        if (!model.vendor_name) {
          return <span className='text-muted-foreground/50 text-xs'>—</span>
        }
        return (
          <BadgeCell className='gap-1.5'>
            <CatalogModelIcon
              icon={model.vendor_icon}
              vendorName={model.vendor_name}
              name={model.vendor_name}
              size={14}
            />
            <StatusBadge
              label={model.vendor_name}
              autoColor={model.vendor_name}
              size='sm'
              copyable={false}
            />
          </BadgeCell>
        )
      },
      size: 130,
      enableSorting: false,
    },

    // Tags column
    {
      accessorKey: 'tags',
      header: t('Tags'),
      cell: ({ row }) => {
        const tags = parseTags(row.original.tags)
        return (
          <BadgeListCell
            items={tags.map((tag) => (
              <StatusBadge
                key={tag}
                label={tag}
                autoColor={tag}
                size='sm'
                copyable={false}
              />
            ))}
          />
        )
      },
      size: 140,
      enableSorting: false,
    },

    // Endpoints column
    {
      accessorKey: 'supported_endpoint_types',
      header: t('Endpoints'),
      cell: ({ row }) => {
        const endpoints = options.endpointMap
          ? getModelEndpoints(
              row.original,
              options.endpointMap,
              options.selectedGroup
            ).map((ep) => ep.type)
          : getModelEndpointTypes(row.original, options.selectedGroup)
        return (
          <BadgeListCell
            items={endpoints.map((ep) => (
              <StatusBadge
                key={ep}
                label={ep}
                autoColor={ep}
                size='sm'
                copyable={false}
              />
            ))}
          />
        )
      },
      size: 130,
      enableSorting: false,
    },

    // Enable Groups column
    {
      accessorKey: 'enable_groups',
      header: t('Groups'),
      cell: ({ row }) => {
        const groups = quotedMarketGroups(
          getMarketPriceModel(row.original, options.platformPriceGroups),
          options.selectedGroup
        )
        return (
          <BadgeListCell
            max={1}
            items={groups.map((group) => (
              <CatalogGroupBadge
                key={group}
                group={group}
                catalog={row.original.group_catalog}
              />
            ))}
            tooltipClassName='max-w-[280px] p-2'
          />
        )
      },
      size: 130,
      enableSorting: false,
    },
  ]
}
