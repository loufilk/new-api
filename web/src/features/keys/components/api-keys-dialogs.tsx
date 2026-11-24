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
import type { MarketKeyDraft } from '../lib/market-key-draft'
import { ApiKeysDeleteDialog } from './api-keys-delete-dialog'
import { ApiKeysMutateDrawer } from './api-keys-mutate-drawer'
import { useApiKeys } from './api-keys-provider'
import { CCSwitchDialog } from './dialogs/cc-switch-dialog'
import { MagpieImportDialog } from './dialogs/magpie-import-dialog'

export function ApiKeysDialogs({ draft }: { draft?: MarketKeyDraft }) {
  const { open, setOpen, currentRow, resolvedKey } = useApiKeys()

  return (
    <>
      <ApiKeysMutateDrawer
        key={
          open === 'update'
            ? `update:${currentRow?.id ?? 'none'}`
            : (open ?? 'closed')
        }
        open={open === 'create' || open === 'update'}
        onOpenChange={(isOpen) => !isOpen && setOpen(null)}
        currentRow={open === 'update' ? currentRow || undefined : undefined}
        draft={open === 'create' ? draft : undefined}
      />
      <ApiKeysDeleteDialog />
      <CCSwitchDialog
        open={open === 'cc-switch'}
        onOpenChange={(isOpen) => !isOpen && setOpen(null)}
        tokenKey={resolvedKey}
        token={currentRow}
      />
      {open === 'magpie' && currentRow ? (
        <MagpieImportDialog
          key={currentRow.id}
          token={currentRow}
          onClose={() => setOpen(null)}
        />
      ) : null}
    </>
  )
}
