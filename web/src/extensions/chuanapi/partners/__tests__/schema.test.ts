import { describe, expect, it } from 'vitest'

import { partnerApplicationSchema } from '../schema'

const validApplication = {
  site_name: 'Example Provider',
  site_url: 'https://provider.example',
  contact_type: 'email' as const,
  contact: 'sales@provider.example',
  description: '',
  quotes: [
    {
      platform: 'openai' as const,
      group: 'standard',
      rate: '1',
      models: 'gpt-4.1',
      description: '',
    },
  ],
}

describe('partnerApplicationSchema', () => {
  it('accepts the supplier fields and quote group', () => {
    expect(partnerApplicationSchema.safeParse(validApplication).success).toBe(
      true
    )
  })

  const invalidApplications: Array<[string, Partial<typeof validApplication>]> =
    [
      ['non-HTTPS URL', { site_url: 'http://provider.example' }],
      ['invalid email contact', { contact: 'not-an-email' }],
      [
        'rate over the supported range',
        { quotes: [{ ...validApplication.quotes[0], rate: '1000.1' }] },
      ],
      [
        'duplicate group names ignoring case',
        {
          quotes: [
            validApplication.quotes[0],
            { ...validApplication.quotes[0], group: 'STANDARD' },
          ],
        },
      ],
    ]

  it.each(invalidApplications)('rejects %s', (_name, override) => {
    expect(
      partnerApplicationSchema.safeParse({
        ...validApplication,
        ...override,
      }).success
    ).toBe(false)
  })
})

it('retains globally unique group names even across different platforms', () => {
  const quote = validApplication.quotes[0]
  const result = partnerApplicationSchema.safeParse({
    ...validApplication,
    quotes: [quote, { ...quote, platform: 'sglang', group: '  StAnDaRd  ' }],
  })
  expect(result.success).toBe(false)
  expect(result.error?.issues[0]?.path).toEqual(['quotes', 1, 'group'])
})

it('accepts up to 20 quote groups, and rejects a twenty-first', () => {
  const quotes = Array.from({ length: 21 }, (_, i) => ({
    ...validApplication.quotes[0],
    group: String(i),
  }))
  expect(
    partnerApplicationSchema.safeParse({
      ...validApplication,
      quotes: quotes.slice(0, 20),
    }).success
  ).toBe(true)
  expect(
    partnerApplicationSchema.safeParse({ ...validApplication, quotes }).success
  ).toBe(false)
})
