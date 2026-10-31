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
import i18next from 'i18next'
import { z } from 'zod'

import { CURRENCY_EXPONENTS } from './currencies'

const allocationSchema = z
  .array(
    z.object({
      receivable_id: z.number().int().positive().max(Number.MAX_SAFE_INTEGER),
      amount_minor: z
        .string()
        .regex(/^[1-9]\d*$/)
        .refine(
          (value) =>
            /^[1-9]\d*$/.test(value) && BigInt(value) <= 9223372036854775807n
        ),
    })
  )
  .min(1)
  .max(100)

/** Exact, intentionally narrow input; never infer currencies or parse principal through floating point. */
export function parseReceivableAllocations(
  text: string,
  expectedMinor?: string
): { receivable_id: number; amount_minor: string }[] {
  const parsed = allocationSchema.safeParse(
    text
      .trim()
      .split(/\r?\n/)
      .map((line) => {
        const fields = line
          .trim()
          .split(',')
          .map((field) => field.trim())
        if (fields.length !== 2 || !/^[1-9]\d*$/.test(fields[0])) return null
        return { receivable_id: Number(fields[0]), amount_minor: fields[1] }
      })
  )
  if (
    !parsed.success ||
    new Set(parsed.data.map((row) => row.receivable_id)).size !==
      parsed.data.length
  ) {
    throw new Error(i18next.t('Enter valid, unique receivable allocations.'))
  }
  const total = parsed.data.reduce(
    (sum, row) => sum + BigInt(row.amount_minor),
    0n
  )
  if (
    total > 9223372036854775807n ||
    (expectedMinor !== undefined &&
      (!/^[1-9]\d*$/.test(expectedMinor) || total !== BigInt(expectedMinor)))
  ) {
    throw new Error(
      i18next.t('Allocation amounts must exactly equal the received amount.')
    )
  }
  return parsed.data
}

const tierSchema = z
  .array(
    z.object({
      key: z.string().regex(/^[a-zA-Z0-9_.:-]{1,96}$/),
      basis: z.enum(['quota', 'face_minor', 'base_paid_minor']),
      minimum: z.string().regex(/^(0|[1-9]\d*)$/),
      maximum: z.string().regex(/^[1-9]\d*$/),
      rate_bps: z.number().int().min(1).max(10000),
    })
  )
  .max(50)
export function parseAgreementTiers(text: string): z.infer<typeof tierSchema> {
  if (!text.trim()) return []
  const parsed = tierSchema.safeParse(
    text
      .trim()
      .split(/\r?\n/)
      .map((line) => {
        const [key, basis, minimum, maximum, rate, ...extra] = line
          .trim()
          .split(',')
          .map((part) => part.trim())
        if (extra.length || !/^[1-9]\d*$/.test(rate)) return null
        return { key, basis, minimum, maximum, rate_bps: Number(rate) }
      })
  )
  if (!parsed.success) {
    throw new Error(
      i18next.t(
        'Enter valid non-overlapping pricing tiers with one threshold unit.'
      )
    )
  }
  const rows = parsed.data
  if (
    new Set(rows.map((row) => row.key)).size !== rows.length ||
    new Set(rows.map((row) => row.basis)).size > 1 ||
    rows.some(
      (row, index) =>
        BigInt(row.minimum) >= BigInt(row.maximum) ||
        BigInt(row.maximum) > 9223372036854775807n ||
        rows
          .slice(0, index)
          .some(
            (prior) =>
              BigInt(row.minimum) < BigInt(prior.maximum) &&
              BigInt(prior.minimum) < BigInt(row.maximum)
          )
    )
  ) {
    throw new Error(
      i18next.t(
        'Enter valid non-overlapping pricing tiers with one threshold unit.'
      )
    )
  }
  return rows
}

export const STATEMENT_CSV_HEADER =
  'external_reference,occurred_at,direction,currency,amount_minor,business_reference,beneficiary_digest,evidence_reference'
const dossier = z.string().regex(/^[a-zA-Z0-9_.:/-]{3,128}$/)
const statementLineSchema = z.object({
  external_reference: dossier,
  occurred_at: z.number().int().positive().max(Number.MAX_SAFE_INTEGER),
  direction: z.enum(['incoming', 'outgoing']),
  currency: z
    .string()
    .refine((value) => Object.hasOwn(CURRENCY_EXPONENTS, value)),
  amount_minor: z
    .string()
    .regex(/^[1-9]\d*$/)
    .refine(
      (value) =>
        /^[1-9]\d*$/.test(value) && BigInt(value) <= 9223372036854775807n
    ),
  business_reference: dossier,
  beneficiary_digest: z.string().regex(/^(?:[a-f0-9]{64})?$/),
  evidence_reference: dossier,
})
export type StatementLineInput = z.infer<typeof statementLineSchema>
/** The accepted bank format uses controlled ASCII references, so quoted commas/newlines cannot be meaningful data. */
export function parseStatementCSV(raw: string): StatementLineInput[] {
  const invalid = () =>
    new Error(
      i18next.t(
        'Invalid statement CSV. Check the exact header, integer amounts, dates, currencies and controlled references.'
      )
    )
  if (
    !raw ||
    new TextEncoder().encode(raw).length > 49152 ||
    raw.includes('\u0000') ||
    raw.startsWith('\uFEFF')
  ) {
    throw invalid()
  }
  const lines = raw.replaceAll('\r\n', '\n').split('\n')
  if (lines.at(-1) === '') lines.pop()
  if (
    lines.shift() !== STATEMENT_CSV_HEADER ||
    !lines.length ||
    lines.length > 200
  ) {
    throw invalid()
  }
  const rows: StatementLineInput[] = []
  for (const line of lines) {
    const cells = line.split(',').map((cell) => {
      if (
        cell.startsWith('"') &&
        cell.endsWith('"') &&
        !cell.slice(1, -1).includes('"')
      ) {
        return cell.slice(1, -1)
      }
      return cell
    })
    if (cells.length !== 8 || !/^[1-9]\d*$/.test(cells[1])) throw invalid()
    const parsed = statementLineSchema.safeParse({
      external_reference: cells[0],
      occurred_at: Number(cells[1]),
      direction: cells[2],
      currency: cells[3],
      amount_minor: cells[4],
      business_reference: cells[5],
      beneficiary_digest: cells[6],
      evidence_reference: cells[7],
    })
    if (
      !parsed.success ||
      parsed.data.occurred_at > Math.floor(Date.now() / 1000) ||
      (parsed.data.direction === 'outgoing' && !parsed.data.beneficiary_digest)
    ) {
      throw invalid()
    }
    rows.push(parsed.data)
  }
  if (new Set(rows.map((row) => row.external_reference)).size !== rows.length) {
    throw new Error(
      i18next.t(
        'A statement cannot contain duplicate bank transaction references.'
      )
    )
  }
  return rows
}

export function validGovernanceReference(
  value: string,
  prefix: string
): boolean {
  if (!value.startsWith(`${prefix}:`)) return false
  const suffix = value.slice(prefix.length + 1)
  return /^[a-zA-Z0-9_-]{8,96}$/.test(suffix) && /[a-zA-Z]/.test(suffix)
}
