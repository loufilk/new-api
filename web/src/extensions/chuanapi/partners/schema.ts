import { z } from 'zod'

import { maxPartnerGroups, partnerContacts, partnerPlatforms } from './catalog'

export const maxPartnerModelsLength = 500

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
          models: z.string().trim().max(maxPartnerModelsLength),
          description: z.string().trim().max(500),
        })
      )
      .min(1)
      .max(maxPartnerGroups),
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
      const group = quote.group.toLowerCase()
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
