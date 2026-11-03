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
import { Loader2 } from 'lucide-react'
import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  formatQuota,
  parseQuotaFromDollars,
  quotaUnitsToDollars,
} from '@/lib/format'

interface TransferDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: (amount: number, target: 'wallet' | 'agent') => Promise<boolean>
  allowAgentTarget?: boolean
  availableQuota: number
  transferring: boolean
}

export function TransferDialog({
  open,
  onOpenChange,
  onConfirm,
  availableQuota,
  transferring,
  allowAgentTarget,
}: TransferDialogProps) {
  const { t } = useTranslation()
  const minimumQuota = 1
  const minimumAmount = quotaUnitsToDollars(minimumQuota)
  const maximumAmount = quotaUnitsToDollars(availableQuota)
  const initialAmount = Math.min(1, Math.max(0, maximumAmount))
  const [amount, setAmount] = useState(initialAmount)
  const [target, setTarget] = useState<'wallet' | 'agent'>('wallet')
  const transferQuota = parseQuotaFromDollars(amount)
  const canTransfer =
    Number.isFinite(amount) &&
    transferQuota >= minimumQuota &&
    transferQuota <= availableQuota

  useEffect(() => {
    if (open) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setAmount(initialAmount)
      setTarget('wallet')
    }
  }, [initialAmount, open])

  const handleConfirm = async () => {
    if (!canTransfer) return

    const success = await onConfirm(transferQuota, target)
    if (success) {
      onOpenChange(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Transfer Rewards')}
      description={t('Transfer released referral rewards 1:1 without a fee.')}
      contentClassName='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-md'
      titleClassName='text-xl font-semibold'
      footerClassName='grid grid-cols-2 gap-2 sm:flex'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => onOpenChange(false)}
            disabled={transferring}
          >
            {t('Cancel')}
          </Button>
          <Button
            onClick={handleConfirm}
            disabled={transferring || !canTransfer}
          >
            {transferring && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
            {t('Transfer')}
          </Button>
        </>
      }
    >
      <div className='space-y-4 py-3 sm:space-y-6 sm:py-4'>
        {allowAgentTarget && (
          <div className='space-y-2'>
            <Label htmlFor='transfer-target'>{t('Transfer target')}</Label>
            <NativeSelect
              id='transfer-target'
              value={target}
              onChange={(event) =>
                setTarget(event.target.value as 'wallet' | 'agent')
              }
              disabled={transferring}
            >
              <NativeSelectOption value='wallet'>
                {t('User wallet')}
              </NativeSelectOption>
              <NativeSelectOption value='agent'>
                {t('Agent balance')}
              </NativeSelectOption>
            </NativeSelect>
          </div>
        )}
        <div className='space-y-2'>
          <Label className='text-muted-foreground text-xs font-medium tracking-wider uppercase'>
            {t('Available Rewards')}
          </Label>
          <div className='text-2xl font-semibold'>
            {formatQuota(availableQuota)}
          </div>
        </div>

        <div className='space-y-3'>
          <Label
            htmlFor='transfer-amount'
            className='text-muted-foreground text-xs font-medium tracking-wider uppercase'
          >
            {t('Transfer Amount')}
          </Label>
          <Input
            id='transfer-amount'
            type='number'
            value={amount}
            onChange={(e) => setAmount(Number(e.target.value))}
            min={minimumAmount}
            max={maximumAmount}
            step={0.01}
            className='font-mono text-lg'
          />
        </div>
      </div>
    </Dialog>
  )
}
