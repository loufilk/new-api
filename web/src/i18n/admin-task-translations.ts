import type { InterfaceLanguageCode } from './languages'

// Operational recovery copy, kept separate from the large upstream locale datasets.
export const adminTaskTranslations: Partial<
  Record<InterfaceLanguageCode, Record<string, string>>
> = {
  zhCN: {
    'Cleanup cutoff: {{date}}': '清理截止时间：{{date}}',
    'Could not start log cleanup.': '无法启动日志清理。',
    'Another log cleanup is already running. Your request was not started.':
      '已有其他日志清理任务在运行，本次请求未启动。',
    'Could not confirm the log cleanup request. Check its status before starting another.':
      '无法确认日志清理请求。请先查询原任务状态，不要重复启动。',
    'Could not check log cleanup status.': '暂时无法查询日志清理状态。',
    'Check status': '查询状态',
    'Balance update status is unavailable. Check the latest status instead of starting it again.':
      '暂时无法查询余额更新状态。请查询最新任务，不要重复启动。',
    'Latest balance update completed.': '最近的余额更新已完成。',
    'Latest balance update failed.': '最近的余额更新失败。',
    'No balance update was found.': '未找到余额更新任务。',
    'Could not confirm the balance update. Checking the latest task.':
      '无法确认余额更新已启动，正在查询最新任务。',
    'Check balance update status': '查询余额更新状态',
  },
  zhTW: {
    'Cleanup cutoff: {{date}}': '清理截止時間：{{date}}',
    'Could not start log cleanup.': '無法啟動日誌清理。',
    'Another log cleanup is already running. Your request was not started.':
      '已有其他日誌清理任務在執行，本次請求未啟動。',
    'Could not confirm the log cleanup request. Check its status before starting another.':
      '無法確認日誌清理請求。請先查詢原任務狀態，不要重複啟動。',
    'Could not check log cleanup status.': '暫時無法查詢日誌清理狀態。',
    'Check status': '查詢狀態',
    'Balance update status is unavailable. Check the latest status instead of starting it again.':
      '暫時無法查詢餘額更新狀態。請查詢最新任務，不要重複啟動。',
    'Latest balance update completed.': '最近的餘額更新已完成。',
    'Latest balance update failed.': '最近的餘額更新失敗。',
    'No balance update was found.': '未找到餘額更新任務。',
    'Could not confirm the balance update. Checking the latest task.':
      '無法確認餘額更新已啟動，正在查詢最新任務。',
    'Check balance update status': '查詢餘額更新狀態',
  },
  fr: {
    'Cleanup cutoff: {{date}}': 'Date limite du nettoyage : {{date}}',
    'Could not start log cleanup.':
      'Impossible de démarrer le nettoyage des journaux.',
    'Another log cleanup is already running. Your request was not started.':
      'Un autre nettoyage est en cours. Votre demande n’a pas été lancée.',
    'Could not confirm the log cleanup request. Check its status before starting another.':
      'Impossible de confirmer le nettoyage. Vérifiez son état avant d’en lancer un autre.',
    'Could not check log cleanup status.':
      'Impossible de vérifier l’état du nettoyage.',
    'Check status': 'Vérifier l’état',
    'Balance update status is unavailable. Check the latest status instead of starting it again.':
      'L’état de la mise à jour est indisponible. Vérifiez la dernière tâche plutôt que de la relancer.',
    'Latest balance update completed.':
      'La dernière mise à jour des soldes est terminée.',
    'Latest balance update failed.':
      'La dernière mise à jour des soldes a échoué.',
    'No balance update was found.': 'Aucune mise à jour des soldes trouvée.',
    'Could not confirm the balance update. Checking the latest task.':
      'Impossible de confirmer le démarrage. Vérification de la dernière tâche.',
    'Check balance update status':
      'Vérifier l’état de la mise à jour des soldes',
  },
  ru: {
    'Cleanup cutoff: {{date}}': 'Дата отсечения очистки: {{date}}',
    'Could not start log cleanup.': 'Не удалось запустить очистку журналов.',
    'Another log cleanup is already running. Your request was not started.':
      'Уже выполняется другая очистка. Ваш запрос не запущен.',
    'Could not confirm the log cleanup request. Check its status before starting another.':
      'Запуск очистки не подтверждён. Проверьте её состояние перед повторным запуском.',
    'Could not check log cleanup status.':
      'Не удалось проверить состояние очистки журналов.',
    'Check status': 'Проверить состояние',
    'Balance update status is unavailable. Check the latest status instead of starting it again.':
      'Состояние обновления балансов недоступно. Проверьте последнюю задачу, не запуская её повторно.',
    'Latest balance update completed.':
      'Последнее обновление балансов завершено.',
    'Latest balance update failed.':
      'Последнее обновление балансов не удалось.',
    'No balance update was found.': 'Задача обновления балансов не найдена.',
    'Could not confirm the balance update. Checking the latest task.':
      'Запуск не подтверждён. Проверяем последнюю задачу.',
    'Check balance update status': 'Проверить обновление балансов',
  },
  ja: {
    'Cleanup cutoff: {{date}}': '削除の基準日時：{{date}}',
    'Could not start log cleanup.': 'ログの削除を開始できませんでした。',
    'Another log cleanup is already running. Your request was not started.':
      '別のログ削除が実行中です。今回のリクエストは開始されませんでした。',
    'Could not confirm the log cleanup request. Check its status before starting another.':
      'ログ削除リクエストを確認できませんでした。再度開始する前に状態を確認してください。',
    'Could not check log cleanup status.':
      'ログ削除の状態を確認できませんでした。',
    'Check status': '状態を確認',
    'Balance update status is unavailable. Check the latest status instead of starting it again.':
      '残高更新の状態を確認できません。再度開始せず、最新の状態を確認してください。',
    'Latest balance update completed.': '最新の残高更新が完了しました。',
    'Latest balance update failed.': '最新の残高更新に失敗しました。',
    'No balance update was found.': '残高更新タスクが見つかりませんでした。',
    'Could not confirm the balance update. Checking the latest task.':
      '残高更新の開始を確認できません。最新のタスクを確認しています。',
    'Check balance update status': '残高更新の状態を確認',
  },
  vi: {
    'Cleanup cutoff: {{date}}': 'Mốc dọn nhật ký: {{date}}',
    'Could not start log cleanup.': 'Không thể bắt đầu dọn nhật ký.',
    'Another log cleanup is already running. Your request was not started.':
      'Một tác vụ dọn nhật ký khác đang chạy. Yêu cầu này chưa được bắt đầu.',
    'Could not confirm the log cleanup request. Check its status before starting another.':
      'Chưa xác nhận được yêu cầu dọn nhật ký. Hãy kiểm tra trạng thái trước khi bắt đầu lại.',
    'Could not check log cleanup status.':
      'Không thể kiểm tra trạng thái dọn nhật ký.',
    'Check status': 'Kiểm tra trạng thái',
    'Balance update status is unavailable. Check the latest status instead of starting it again.':
      'Chưa xem được trạng thái cập nhật số dư. Hãy kiểm tra tác vụ mới nhất thay vì khởi động lại.',
    'Latest balance update completed.':
      'Tác vụ cập nhật số dư mới nhất đã hoàn tất.',
    'Latest balance update failed.': 'Tác vụ cập nhật số dư mới nhất thất bại.',
    'No balance update was found.': 'Không tìm thấy tác vụ cập nhật số dư.',
    'Could not confirm the balance update. Checking the latest task.':
      'Chưa xác nhận việc cập nhật số dư. Đang kiểm tra tác vụ mới nhất.',
    'Check balance update status': 'Kiểm tra trạng thái cập nhật số dư',
  },
}
