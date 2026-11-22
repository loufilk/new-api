import { z } from 'zod'

const partnerPlatforms = [
  'openai',
  'anthropic',
  'google',
  'deepseek',
  'alibaba',
  'bytedance',
  'zhipu',
  'moonshot',
  'other',
] as const

const partnerContacts = ['wechat', 'qq', 'email', 'phone', 'other'] as const

const rateSchema = z
  .string()
  .regex(/^\d{1,4}(?:\.\d{1,6})?$/)
  .refine((value) => Number(value) <= 1000)

export const partnerApplicationSchema = z
  .object({
    site_name: z.string().trim().min(2).max(120),
    site_url: z
      .string()
      .trim()
      .max(2048)
      .refine(
        (value) =>
          value === '' ||
          (() => {
            try {
              const url = new URL(value)
              return (
                url.protocol === 'https:' &&
                !url.username &&
                !url.password &&
                !url.hash
              )
            } catch {
              return false
            }
          })(),
        'Enter a valid HTTPS address.'
      ),
    contact_type: z.enum(partnerContacts),
    contact: z.string().trim().min(1).max(200),
    description: z.string().trim().max(3000),
    quotes: z
      .array(
        z.object({
          platform: z.enum(partnerPlatforms),
          group: z.string().trim().min(1).max(120),
          rate: rateSchema,
          models: z.string().trim().max(500),
          description: z.string().trim().max(500),
        })
      )
      .min(1)
      .max(20),
  })
  .superRefine((value, context) => {
    if (
      value.contact_type === 'email' &&
      !z.email().safeParse(value.contact).success
    ) {
      context.addIssue({
        code: 'custom',
        path: ['contact'],
        message: 'Enter a valid email address.',
      })
    }
    const seen = new Set<string>()
    value.quotes.forEach((quote, index) => {
      const group = quote.group.toLocaleLowerCase()
      if (seen.has(group)) {
        context.addIssue({
          code: 'custom',
          path: ['quotes', index, 'group'],
          message: 'Group names must be unique.',
        })
      }
      seen.add(group)
    })
  })

export type PartnerApplicationFormValues = z.infer<
  typeof partnerApplicationSchema
>
