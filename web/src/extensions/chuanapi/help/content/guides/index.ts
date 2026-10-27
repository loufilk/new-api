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
import type { GuideDoc } from '../types'
import { anthropicSdk } from './anthropic-sdk'
import { authentication } from './authentication'
import { billing } from './billing'
import { ccSwitch } from './cc-switch'
import { cherryStudio } from './cherry-studio'
import { claudeCode } from './claude-code'
import { clineRoo } from './cline-roo'
import { codexCli } from './codex-cli'
import { compatibility } from './compatibility'
import { consoleTour } from './console-tour'
import { functionCalling } from './function-calling'
import { immersiveTranslate } from './immersive-translate'
import { openaiSdk } from './openai-sdk'
import { playground } from './playground'
import { quickStart } from './quick-start'
import { streaming } from './streaming'
import { troubleshooting } from './troubleshooting'
import { vision } from './vision'

export const guides: readonly GuideDoc[] = [
  quickStart,
  authentication,
  consoleTour,
  billing,
  playground,
  claudeCode,
  codexCli,
  ccSwitch,
  cherryStudio,
  clineRoo,
  immersiveTranslate,
  openaiSdk,
  anthropicSdk,
  compatibility,
  streaming,
  functionCalling,
  vision,
  troubleshooting,
]
