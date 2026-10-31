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
  parseAgreementTiers,
  parseReceivableAllocations,
  parseStatementCSV,
  STATEMENT_CSV_HEADER,
  validGovernanceReference,
} from '../lib/financial-inputs'
import { operationFormPayload } from '../lib/operation-form'

const statement = (amount = '9007199254740993', currency = 'USD') =>
  `${STATEMENT_CSV_HEADER}\nbank:txn-001,1700000000,incoming,${currency},${amount},purchase:001,,evidence:001\n`
describe('exact financial input contracts', () => {
  test('receivable allocations preserve integer precision and require exact total', () => {
    expect(
      parseReceivableAllocations(
        '12,9007199254740993\n13,2',
        '9007199254740995'
      )
    ).toEqual([
      { receivable_id: 12, amount_minor: '9007199254740993' },
      { receivable_id: 13, amount_minor: '2' },
    ])
    expect(() =>
      parseReceivableAllocations(
        '12,9007199254740993\n13,2',
        '9007199254740994'
      )
    ).toThrow()
  })
  test.each([
    '1,2\n1,3',
    '1,1.2',
    '9007199254740993,1',
    '1,01',
    '1,1e3',
    '1,-2',
    '1,9223372036854775808',
  ])('rejects invalid or ambiguous allocations %s', (input) =>
    expect(() => parseReceivableAllocations(input)).toThrow()
  )
  test('pricing tiers support adjacent half-open ranges without compounding or implicit rates', () => {
    expect(
      parseAgreementTiers(
        'first,base_paid_minor,0,100,8700\nsecond,base_paid_minor,100,200,8500'
      )
    ).toHaveLength(2)
    expect(parseAgreementTiers('')).toEqual([])
    expect(() =>
      parseAgreementTiers(
        'first,quota,0,100,8800\nsecond,face_minor,100,200,8500'
      )
    ).toThrow()
    expect(() =>
      parseAgreementTiers('first,quota,0,101,8800\nsecond,quota,100,200,8500')
    ).toThrow()
  })
  test('bank CSV preserves large amounts and original bytes in the final payload', () => {
    expect(parseStatementCSV(statement())[0].amount_minor).toBe(
      '9007199254740993'
    )
    expect(
      operationFormPayload(
        [{ name: 'csv', label: 'Statement CSV', kind: 'csv' }],
        { csv: statement() }
      ).csv
    ).toBe(statement())
  })
  test.each([
    statement('1e3'),
    statement('01'),
    statement('1.00'),
    statement('-1'),
    statement('1', 'XYZ'),
    `\uFEFF${statement()}`,
    statement().replace('bank:txn-001', '=HYPERLINK'),
    statement().replace('incoming', 'outgoing'),
    `${statement() + statement().split('\n')[1]}\n`,
    statement().replace('1700000000', '999999999999999999'),
  ])(
    'rejects unsafe or ambiguous CSV without accepting an inferred amount',
    (csv) => expect(() => parseStatementCSV(csv)).toThrow()
  )
  test('empty, oversized and too-many-row imports are rejected before prepare', () => {
    expect(() => parseStatementCSV(STATEMENT_CSV_HEADER)).toThrow()
    expect(() => parseStatementCSV('x'.repeat(49153))).toThrow()
    const rows = Array.from(
      { length: 201 },
      (_, i) =>
        `bank:txn-${i},1700000000,incoming,USD,1,purchase:001,,evidence:001`
    )
    expect(() =>
      parseStatementCSV(`${STATEMENT_CSV_HEADER}\n${rows.join('\n')}`)
    ).toThrow()
  })
  test('optional blank numbers are omitted rather than approved as zero', () => {
    expect(
      operationFormPayload(
        [
          {
            name: 'agent_user_id',
            label: 'Agent ID',
            kind: 'integer',
            required: false,
          },
        ],
        { agent_user_id: '' }
      )
    ).toEqual({})
  })
  test('governance references reject raw numeric identities and URLs but accept bounded dossier tokens', () => {
    expect(validGovernanceReference('dossier:case-0001', 'dossier')).toBe(true)
    for (const value of [
      'dossier:123456789012345678',
      'https://identity.example/document',
      'agreement:case-0001',
      'dossier:short',
      'dossier:abc defgh',
    ]) {
      expect(validGovernanceReference(value, 'dossier')).toBe(false)
    }
  })
})
