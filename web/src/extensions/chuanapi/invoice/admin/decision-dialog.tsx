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
import { Controller, useForm } from 'react-hook-form'
import { z } from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import { maxPdfSize, type ReviewDecision } from './api'
import { useFinanceLabels } from './i18n'
import { useSaveReview } from './use-save-review'

const invoiceNumber = z
  .string()
  .trim()
  .min(1)
  .max(64)
  .regex(/^(?=.*[A-Za-z0-9])[A-Za-z0-9-]+$/)
const reasonText = z
  .string()
  .trim()
  .min(1)
  .refine((v) => [...v].length <= 500 && !/\p{Cc}/u.test(v))

type DecisionKind = Exclude<ReviewDecision['kind'], 'claim' | 'release'>
interface DecisionFields {
  reason: string
  number: string
  file: File | null
  verified: boolean
}

export function DecisionDialog(props: {
  id: number
  kind: DecisionKind
  onCancel: () => void
  onSaved: () => void
}) {
  const { label } = useFinanceLabels()
  const save = useSaveReview(props.id, props.onSaved)
  const form = useForm<DecisionFields>({
    mode: 'onChange',
    defaultValues: { reason: '', number: '', verified: false, file: null },
  })
  const record = props.kind === 'record'
  const reject = props.kind === 'reject'
  const action = {
    record: label('Save issued invoice', '保存已开具发票'),
    reject: label('Reject application', '驳回申请'),
    reconciliation: label('Flag for refund reconciliation', '标记退款待核对'),
  }[props.kind]

  const submit = form.handleSubmit((values) => {
    let decision: ReviewDecision
    if (record) {
      if (!values.verified || !values.file) return
      decision = {
        kind: 'record',
        number: values.number.trim(),
        verified: true,
        file: values.file,
      }
    } else {
      decision = {
        kind: reject ? 'reject' : 'reconciliation',
        reason: values.reason.trim(),
      }
    }
    save.submit(decision)
  })

  const descriptions = {
    record: label(
      'Upload the original PDF only after finance issued it outside this console. The customer will be able to download it. No invoice is issued or emailed here.',
      '请上传财务在站外开具的原始 PDF。保存后客户可登录下载，本站不会开票或发送邮件。'
    ),
    reject: label(
      'Tell the applicant what needs correcting. This reason is visible to them.',
      '请说明需要更正的内容，申请人可查看驳回原因。'
    ),
    reconciliation: label(
      'Use this for a reported refund after issuance. It adds an internal finance note and keeps the invoice and its orders unchanged.',
      '用于开票后发现退款的情况。仅登记内部核对原因，保留原发票和订单，不会作废或重新开放开票。'
    ),
  }
  const numberHint = label(
    'Use 1–64 letters, digits or hyphens with at least one letter or digit.',
    '限 1 至 64 个英文字母、数字或连字符，至少含一个字母或数字。'
  )
  const pdfHint = label(
    'Choose a non-empty PDF up to 5 MiB.',
    '请选择不超过 5 MiB 的非空 PDF。'
  )
  const verifyHint = label(
    'I verified each payment against merchant settlement and refund records, and the authorized issuer has already issued this invoice.',
    '我已逐笔核对商户实收款项和退款记录，并确认授权开票主体已在站外开具此发票。'
  )
  return (
    <ConfirmDialog
      open
      title={`${action} · #${props.id}`}
      desc={descriptions[props.kind]}
      onOpenChange={(open) => {
        if (!open && !save.locked.current) props.onCancel()
      }}
      confirmText={action}
      handleConfirm={() => void submit()}
      isLoading={save.isPending}
      destructive={reject}
      disabled={!form.formState.isValid}
    >
      <fieldset className='grid gap-4' disabled={save.isPending}>
        {record ? (
          <>
            <Field>
              <FieldLabel htmlFor='finance-invoice-number'>
                {label('Invoice number', '发票号码')}
              </FieldLabel>
              <Input
                id='finance-invoice-number'
                autoComplete='off'
                maxLength={64}
                aria-invalid={!!form.formState.errors.number}
                {...form.register('number', {
                  validate: (v) =>
                    invoiceNumber.safeParse(v).success || numberHint,
                  onChange: () =>
                    form.setValue('verified', false, { shouldValidate: true }),
                })}
              />
              <FieldError errors={[form.formState.errors.number]} />
            </Field>
            <Field>
              <FieldLabel htmlFor='finance-invoice-file'>
                {label(
                  'Issued PDF (up to 5 MiB)',
                  '已开具的 PDF（最大 5 MiB）'
                )}
              </FieldLabel>
              <Controller
                control={form.control}
                name='file'
                rules={{
                  validate: (file) =>
                    (!!file &&
                      file.size > 0 &&
                      file.size <= maxPdfSize &&
                      (file.type === 'application/pdf' ||
                        (!file.type && /\.pdf$/i.test(file.name)))) ||
                    pdfHint,
                }}
                render={({ field, fieldState }) => (
                  <Input
                    id='finance-invoice-file'
                    type='file'
                    accept='application/pdf,.pdf'
                    aria-invalid={fieldState.invalid}
                    ref={field.ref}
                    name={field.name}
                    onBlur={field.onBlur}
                    onChange={(e) => {
                      field.onChange(e.target.files?.[0] ?? null)
                      form.setValue('verified', false, { shouldValidate: true })
                    }}
                  />
                )}
              />
              <FieldError errors={[form.formState.errors.file]} />
            </Field>
            <Field orientation='horizontal'>
              <Controller
                control={form.control}
                name='verified'
                rules={{ validate: (v) => v === true }}
                render={({ field }) => (
                  <Checkbox
                    id='finance-verified'
                    checked={field.value}
                    disabled={save.isPending}
                    onCheckedChange={field.onChange}
                  />
                )}
              />
              <FieldLabel htmlFor='finance-verified'>{verifyHint}</FieldLabel>
            </Field>
          </>
        ) : (
          <Field>
            <FieldLabel htmlFor='finance-review-reason'>
              {reject
                ? label('Reason for rejection', '驳回原因')
                : label('Internal review reason', '内部核对原因')}
            </FieldLabel>
            <Input
              id='finance-review-reason'
              autoComplete='off'
              aria-invalid={!!form.formState.errors.reason}
              {...form.register('reason', {
                validate: (v) =>
                  reasonText.safeParse(v).success ||
                  label(
                    'Enter a single-line reason (1–500 characters).',
                    '请输入 1 至 500 字的单行原因。'
                  ),
              })}
            />
            <FieldError errors={[form.formState.errors.reason]} />
          </Field>
        )}
      </fieldset>
    </ConfirmDialog>
  )
}
