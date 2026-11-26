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

// Billing and recovery messages for the API Key diagnostics dialog.
export const keyDiagnosticTranslations = {
  en: {
    'Your hourly test limit has been reached.':
      'Your hourly test limit has been reached.',
    'You already have a test in progress. Wait for it to finish.':
      'You already have a test in progress. Wait for it to finish.',
    'All test slots are busy. Please try again later.':
      'All test slots are busy. Please try again later.',
    'Test requests were made too quickly.':
      'Test requests were made too quickly.',
    'The test limit was reached. Wait before trying again.':
      'The test limit was reached. Wait before trying again.',
    'This test was not started or charged.':
      'This test was not started or charged.',
    'Result lookup is temporarily limited. Check this ID again later; do not submit another test.':
      'Result lookup is temporarily limited. Check this ID again later; do not submit another test.',
    'Try again after {{time}}.': 'Try again after {{time}}.',
  },
  zhCN: {
    'Your hourly test limit has been reached.': '已达到每小时测试次数上限。',
    'You already have a test in progress. Wait for it to finish.':
      '你已有测试正在运行，请等它结束。',
    'All test slots are busy. Please try again later.':
      '测试名额暂时已满，请稍后再试。',
    'Test requests were made too quickly.': '测试请求过于频繁。',
    'The test limit was reached. Wait before trying again.':
      '已达到测试限制，请稍后再试。',
    'This test was not started or charged.': '本次测试未开始，也未扣费。',
    'Result lookup is temporarily limited. Check this ID again later; do not submit another test.':
      '结果查询暂时受限。请稍后查询同一测试 ID，不要重复提交。',
    'Try again after {{time}}.': '可在 {{time}} 后再试。',
  },
  zhTW: {
    'Your hourly test limit has been reached.': '已達每小時測試次數上限。',
    'You already have a test in progress. Wait for it to finish.':
      '你已有測試正在執行，請等它結束。',
    'All test slots are busy. Please try again later.':
      '測試名額暫時已滿，請稍後再試。',
    'Test requests were made too quickly.': '測試請求過於頻繁。',
    'The test limit was reached. Wait before trying again.':
      '已達測試限制，請稍後再試。',
    'This test was not started or charged.': '本次測試未開始，也未扣費。',
    'Result lookup is temporarily limited. Check this ID again later; do not submit another test.':
      '結果查詢暫時受限。請稍後查詢同一測試 ID，不要重複提交。',
    'Try again after {{time}}.': '可在 {{time}} 後再試。',
  },
  fr: {
    'Your hourly test limit has been reached.':
      'Vous avez atteint la limite horaire de tests.',
    'You already have a test in progress. Wait for it to finish.':
      'Un test est déjà en cours. Attendez sa fin.',
    'All test slots are busy. Please try again later.':
      'Tous les créneaux de test sont occupés. Réessayez plus tard.',
    'Test requests were made too quickly.':
      'Les demandes de test sont trop fréquentes.',
    'The test limit was reached. Wait before trying again.':
      'La limite de tests est atteinte. Patientez avant de réessayer.',
    'This test was not started or charged.':
      'Ce test n’a pas été lancé ni facturé.',
    'Result lookup is temporarily limited. Check this ID again later; do not submit another test.':
      'La consultation des résultats est temporairement limitée. Vérifiez cet identifiant plus tard sans soumettre un nouveau test.',
    'Try again after {{time}}.': 'Réessayez après {{time}}.',
  },
  ja: {
    'Your hourly test limit has been reached.':
      '1 時間あたりのテスト回数の上限に達しました。',
    'You already have a test in progress. Wait for it to finish.':
      '実行中のテストが終了するまでお待ちください。',
    'All test slots are busy. Please try again later.':
      'テスト枠がすべて使用中です。しばらくしてからお試しください。',
    'Test requests were made too quickly.':
      'テストのリクエスト間隔が短すぎます。',
    'The test limit was reached. Wait before trying again.':
      'テストの上限に達しました。しばらくしてからお試しください。',
    'This test was not started or charged.':
      'このテストは開始されておらず、課金もされていません。',
    'Result lookup is temporarily limited. Check this ID again later; do not submit another test.':
      '現在、結果の照会が制限されています。再送信せず、後で同じテスト ID を確認してください。',
    'Try again after {{time}}.': '{{time}} 以降にもう一度お試しください。',
  },
  ru: {
    'Your hourly test limit has been reached.':
      'Достигнут часовой лимит тестов.',
    'You already have a test in progress. Wait for it to finish.':
      'Тест уже выполняется. Дождитесь его завершения.',
    'All test slots are busy. Please try again later.':
      'Все слоты для тестов заняты. Повторите попытку позже.',
    'Test requests were made too quickly.':
      'Запросы тестов отправляются слишком часто.',
    'The test limit was reached. Wait before trying again.':
      'Достигнут лимит тестов. Повторите попытку позже.',
    'This test was not started or charged.':
      'Этот тест не был запущен, плата не взималась.',
    'Result lookup is temporarily limited. Check this ID again later; do not submit another test.':
      'Проверка результата временно ограничена. Позже проверьте этот же ID, не отправляя новый тест.',
    'Try again after {{time}}.': 'Повторите попытку после {{time}}.',
  },
  vi: {
    'Your hourly test limit has been reached.':
      'Bạn đã đạt giới hạn kiểm tra mỗi giờ.',
    'You already have a test in progress. Wait for it to finish.':
      'Bạn đang có bài kiểm tra chạy. Hãy đợi bài đó kết thúc.',
    'All test slots are busy. Please try again later.':
      'Tất cả các suất kiểm tra đều đang bận. Vui lòng thử lại sau.',
    'Test requests were made too quickly.':
      'Các yêu cầu kiểm tra được gửi quá nhanh.',
    'The test limit was reached. Wait before trying again.':
      'Đã đạt giới hạn kiểm tra. Hãy đợi trước khi thử lại.',
    'This test was not started or charged.':
      'Bài kiểm tra này chưa bắt đầu và không bị tính phí.',
    'Result lookup is temporarily limited. Check this ID again later; do not submit another test.':
      'Việc tra cứu kết quả tạm thời bị giới hạn. Hãy kiểm tra lại ID này sau, đừng gửi bài kiểm tra khác.',
    'Try again after {{time}}.': 'Hãy thử lại sau {{time}}.',
  },
} satisfies Record<InterfaceLanguageCode, Record<string, string>>
