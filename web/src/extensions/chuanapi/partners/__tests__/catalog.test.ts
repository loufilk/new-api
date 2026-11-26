import { expect, it } from 'vitest'

import { contactTitle, platformTitle, partnerPlatforms } from '../catalog'

it('keeps old stored platform/contact labels distinct from new choices', () => {
  const t = (value: string) => value
  expect(platformTitle('other', t)).toBe('Other')
  expect(platformTitle('custom', t)).toBe('Custom')
  expect(platformTitle('zhipu', t)).toBe('Zhipu')
  expect(platformTitle('zhipu-glm', t)).toBe('Zhipu GLM')
  expect(contactTitle('phone', t)).toBe('Phone')
  expect(contactTitle('telegram', t)).toBe('Telegram')
  expect(partnerPlatforms).not.toContain('other')
})
