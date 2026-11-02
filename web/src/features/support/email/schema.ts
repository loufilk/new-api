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
import { z } from 'zod'

export function supportEmailRecipients(value: string): string[] {
  return [
    ...new Set(
      value
        .split(/[,;\n]/)
        .map((address) => address.trim().toLowerCase())
        .filter(Boolean)
    ),
  ]
}

export function isSupportEmailOrigin(value: string): boolean {
  try {
    const url = new URL(value)
    return (
      url.protocol === 'https:' &&
      url.origin === value &&
      !url.username &&
      !url.password &&
      Boolean(url.hostname) &&
      !url.hostname.includes('*')
    )
  } catch {
    return false
  }
}

export const supportEmailFormSchema = z
  .object({
    enabled: z.boolean(),
    recipients: z
      .string()
      .max(1500, 'Enter up to five administrator email addresses.')
      .refine((value) => {
        const addresses = supportEmailRecipients(value)
        return (
          !value.includes('\r') &&
          addresses.length <= 5 &&
          addresses.every((address) => z.email().safeParse(address).success)
        )
      }, 'Enter up to five administrator email addresses.'),
    siteOrigin: z
      .string()
      .trim()
      .refine(
        (value) => !value || isSupportEmailOrigin(value),
        'Enter an HTTPS origin without a path, query or fragment.'
      ),
  })
  .superRefine((values, context) => {
    if (!values.enabled) return
    if (supportEmailRecipients(values.recipients).length === 0) {
      context.addIssue({
        code: 'custom',
        path: ['recipients'],
        message: 'Enter up to five administrator email addresses.',
      })
    }
    if (!values.siteOrigin) {
      context.addIssue({
        code: 'custom',
        path: ['siteOrigin'],
        message: 'Enter an HTTPS origin without a path, query or fragment.',
      })
    }
  })
export type SupportEmailFormValues = z.infer<typeof supportEmailFormSchema>

export function supportTicketSearch(search: Record<string, unknown>): {
  ticket?: number
} {
  const raw = search.ticket
  if (
    (typeof raw !== 'number' && typeof raw !== 'string') ||
    !/^\d+$/.test(String(raw))
  ) {
    return {}
  }
  const ticket = Number(raw)
  return Number.isSafeInteger(ticket) && ticket > 0 ? { ticket } : {}
}
