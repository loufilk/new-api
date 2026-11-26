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
import type { InterfaceLanguageCode } from './languages'

export const adminQuotaTranslations = {
  en: {
    'Quota adjustment recovery is unavailable in this browser. Reopen it with storage enabled and check the ledger before making another adjustment.':
      'Quota adjustment recovery is unavailable in this browser. Reopen it with storage enabled and check the ledger before making another adjustment.',
    'The result is unconfirmed. Retrying sends the same adjustment: it may apply if it has not already been applied. Closing does not cancel it.':
      'The result is unconfirmed. Retrying sends the same adjustment: it may apply if it has not already been applied. Closing does not cancel it.',
    'Another page is processing this quota adjustment. Reopen it before continuing.':
      'Another page is processing this quota adjustment. Reopen it before continuing.',
  },
  zhCN: {
    'Quota adjustment recovery is unavailable in this browser. Reopen it with storage enabled and check the ledger before making another adjustment.':
      '此浏览器无法恢复额度调整记录。请启用存储后重新打开，并在再次调整前核对账本。',
    'The result is unconfirmed. Retrying sends the same adjustment: it may apply if it has not already been applied. Closing does not cancel it.':
      '结果尚未确认。重试会提交同一笔调整；如果上次未生效，本次可能生效。关闭窗口不会撤销操作。',
    'Another page is processing this quota adjustment. Reopen it before continuing.':
      '另一页面已处理或正在处理这笔调整。请重新打开后再继续。',
  },
  zhTW: {
    'Quota adjustment recovery is unavailable in this browser. Reopen it with storage enabled and check the ledger before making another adjustment.':
      '此瀏覽器無法復原額度調整紀錄。請啟用儲存後重新開啟，並在再次調整前核對帳本。',
    'The result is unconfirmed. Retrying sends the same adjustment: it may apply if it has not already been applied. Closing does not cancel it.':
      '結果尚未確認。重試會送出同一筆調整；如果上次未生效，本次可能生效。關閉視窗不會撤銷操作。',
    'Another page is processing this quota adjustment. Reopen it before continuing.':
      '其他頁面已處理或正在處理這筆調整。請重新開啟後再繼續。',
  },
  fr: {
    'Quota adjustment recovery is unavailable in this browser. Reopen it with storage enabled and check the ledger before making another adjustment.':
      'La récupération de cet ajustement est indisponible. Activez le stockage, rouvrez la fenêtre et vérifiez le registre avant tout autre ajustement.',
    'The result is unconfirmed. Retrying sends the same adjustment: it may apply if it has not already been applied. Closing does not cancel it.':
      'Le résultat est incertain. Réessayer renvoie le même ajustement, qui pourra prendre effet si ce n’est pas déjà fait. Fermer la fenêtre ne l’annule pas.',
    'Another page is processing this quota adjustment. Reopen it before continuing.':
      'Une autre page traite ou a traité cet ajustement. Rouvrez la fenêtre avant de continuer.',
  },
  ru: {
    'Quota adjustment recovery is unavailable in this browser. Reopen it with storage enabled and check the ledger before making another adjustment.':
      'Восстановление операции недоступно. Включите хранилище браузера, откройте окно заново и сверьте журнал перед следующей корректировкой.',
    'The result is unconfirmed. Retrying sends the same adjustment: it may apply if it has not already been applied. Closing does not cancel it.':
      'Результат не подтверждён. Повторная отправка той же операции может изменить квоту, если это ещё не сделано. Закрытие окна не отменяет операцию.',
    'Another page is processing this quota adjustment. Reopen it before continuing.':
      'На другой странице обрабатывается или уже обработана эта операция. Откройте окно заново.',
  },
  ja: {
    'Quota adjustment recovery is unavailable in this browser. Reopen it with storage enabled and check the ledger before making another adjustment.':
      'このブラウザーでは調整記録を復元できません。ストレージを有効にして開き直し、次の調整の前に台帳を確認してください。',
    'The result is unconfirmed. Retrying sends the same adjustment: it may apply if it has not already been applied. Closing does not cancel it.':
      '結果は未確認です。再試行すると同じ調整を送信し、未適用なら適用されます。閉じても取り消されません。',
    'Another page is processing this quota adjustment. Reopen it before continuing.':
      '別のページでこの調整が処理されています。開き直してから続行してください。',
  },
  vi: {
    'Quota adjustment recovery is unavailable in this browser. Reopen it with storage enabled and check the ledger before making another adjustment.':
      'Trình duyệt không thể khôi phục lần điều chỉnh này. Hãy bật bộ nhớ, mở lại và kiểm tra sổ cái trước khi điều chỉnh tiếp.',
    'The result is unconfirmed. Retrying sends the same adjustment: it may apply if it has not already been applied. Closing does not cancel it.':
      'Chưa xác nhận được kết quả. Thử lại sẽ gửi cùng một điều chỉnh và có thể áp dụng nếu chưa được ghi nhận. Đóng cửa sổ không hủy thao tác.',
    'Another page is processing this quota adjustment. Reopen it before continuing.':
      'Một trang khác đang hoặc đã xử lý lần điều chỉnh này. Hãy mở lại trước khi tiếp tục.',
  },
} satisfies Record<InterfaceLanguageCode, Record<string, string>>
