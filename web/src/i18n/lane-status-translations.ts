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

export const laneStatusTranslations = {
  en: {
    'laneStatus.title': 'Billing sharding status',
    'laneStatus.scope':
      'Read-only status for the instance that answered. It does not show fleet-wide rollout or live production traffic.',
    'laneStatus.unknown':
      'The current status is unknown. Refresh to check again.',
    'laneStatus.build': 'Build profile',
    'laneStatus.ordinary': 'Standard build',
    'laneStatus.lab': 'Isolated lab build',
    'laneStatus.labGateway': 'Lab gateway',
    'laneStatus.runtimeGate': 'Production runtime gate',
    'laneStatus.gatePassed': 'Reported open',
    'laneStatus.gateClosed': 'Blocked',
  },
  zhCN: {
    'laneStatus.title': '计费分片状态',
    'laneStatus.scope':
      '只读。仅显示应答实例的状态，不代表全站发布进度或生产流量。',
    'laneStatus.unknown': '当前状态未知。请刷新重查。',
    'laneStatus.build': '构建类型',
    'laneStatus.ordinary': '标准构建',
    'laneStatus.lab': '隔离实验构建',
    'laneStatus.labGateway': '实验网关',
    'laneStatus.runtimeGate': '生产运行门禁',
    'laneStatus.gatePassed': '报告为开放',
    'laneStatus.gateClosed': '未开放',
  },
  zhTW: {
    'laneStatus.title': '計費分片狀態',
    'laneStatus.scope':
      '唯讀。僅顯示回應執行個體的狀態，不代表全站部署進度或正式環境流量。',
    'laneStatus.unknown': '目前狀態未知。請重新整理再確認。',
    'laneStatus.build': '建置類型',
    'laneStatus.ordinary': '標準建置',
    'laneStatus.lab': '隔離實驗建置',
    'laneStatus.labGateway': '實驗閘道',
    'laneStatus.runtimeGate': '正式環境執行門檻',
    'laneStatus.gatePassed': '回報為開放',
    'laneStatus.gateClosed': '未開放',
  },
  fr: {
    'laneStatus.title': 'État du partitionnement de la facturation',
    'laneStatus.scope':
      'Lecture seule, pour l’instance qui a répondu. N’indique ni le déploiement global ni le trafic réel en production.',
    'laneStatus.unknown':
      'État actuel inconnu. Actualisez pour vérifier de nouveau.',
    'laneStatus.build': 'Type de build',
    'laneStatus.ordinary': 'Build standard',
    'laneStatus.lab': 'Build de laboratoire isolé',
    'laneStatus.labGateway': 'Passerelle de laboratoire',
    'laneStatus.runtimeGate': 'Verrou d’exécution en production',
    'laneStatus.gatePassed': 'Déclaré ouvert',
    'laneStatus.gateClosed': 'Bloqué',
  },
  ja: {
    'laneStatus.title': '課金シャーディングの状態',
    'laneStatus.scope':
      '応答したインスタンスの読み取り専用の状態です。全体の展開状況や本番トラフィックは示しません。',
    'laneStatus.unknown': '現在の状態は不明です。更新して確認してください。',
    'laneStatus.build': 'ビルド種別',
    'laneStatus.ordinary': '標準ビルド',
    'laneStatus.lab': '隔離ラボ用ビルド',
    'laneStatus.labGateway': 'ラボゲートウェイ',
    'laneStatus.runtimeGate': '本番実行ゲート',
    'laneStatus.gatePassed': '開放と報告',
    'laneStatus.gateClosed': 'ブロック中',
  },
  ru: {
    'laneStatus.title': 'Состояние шардирования биллинга',
    'laneStatus.scope':
      'Только чтение: состояние ответившего экземпляра. Не отражает развёртывание всего кластера или рабочий трафик.',
    'laneStatus.unknown':
      'Текущее состояние неизвестно. Обновите данные для проверки.',
    'laneStatus.build': 'Профиль сборки',
    'laneStatus.ordinary': 'Стандартная сборка',
    'laneStatus.lab': 'Изолированная лабораторная сборка',
    'laneStatus.labGateway': 'Лабораторный шлюз',
    'laneStatus.runtimeGate': 'Допуск к рабочему режиму',
    'laneStatus.gatePassed': 'Сообщается, что открыт',
    'laneStatus.gateClosed': 'Заблокирован',
  },
  vi: {
    'laneStatus.title': 'Trạng thái phân mảnh tính phí',
    'laneStatus.scope':
      'Chỉ đọc trạng thái của phiên bản máy chủ đã phản hồi. Không phản ánh tiến độ toàn cụm hoặc lưu lượng thật.',
    'laneStatus.unknown':
      'Chưa rõ trạng thái hiện tại. Hãy làm mới để kiểm tra lại.',
    'laneStatus.build': 'Kiểu bản dựng',
    'laneStatus.ordinary': 'Bản dựng tiêu chuẩn',
    'laneStatus.lab': 'Bản dựng thử nghiệm cô lập',
    'laneStatus.labGateway': 'Cổng thử nghiệm',
    'laneStatus.runtimeGate': 'Cổng kiểm soát môi trường thật',
    'laneStatus.gatePassed': 'Báo cáo đang mở',
    'laneStatus.gateClosed': 'Đang chặn',
  },
} satisfies Record<InterfaceLanguageCode, Record<string, string>>
