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
import { createFileRoute } from '@tanstack/react-router'

import { extensionHelpPages } from '@/extensions'

const { integrationWizard: IntegrationWizardPage } = extensionHelpPages()

export const Route = createFileRoute('/help/integrations')({
  validateSearch: (search: Record<string, unknown>) => ({
    public_line:
      typeof search.public_line === 'string' ? search.public_line : undefined,
    model: typeof search.model === 'string' ? search.model : undefined,
  }),
  component: IntegrationWizardRoute,
})

function IntegrationWizardRoute() {
  const { public_line, model } = Route.useSearch()
  return <IntegrationWizardPage publicLine={public_line} previewModel={model} />
}
