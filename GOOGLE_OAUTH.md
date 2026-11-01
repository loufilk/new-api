# Google 账户登录（默认关闭）

这是 NewAPI 的账户登录 / 绑定功能，不是 Gmail、Drive、日历读取授权。
它复用现有 OAuth 回调、用户注册、邀请关系、登录二次验证、操作重新认证及会话框架；
原 OIDC 和自定义提供者配置不变。站点首页仍链接到 NewAPI 登录页，无需修改 web-home。

## 管理员接入

以下仅是操作说明；源码发布不会自动创建 Google Cloud 项目或真实 OAuth 客户端，不会保存真实凭据、发起真实授权或部署。

1. 在 Google Cloud / Google Auth Platform 创建 **Web application** 客户端，配置应用品牌、受众、测试用户、隐私政策和授权域名。
2. 把 NewAPI 的“服务器地址 / ServerAddress”设为规范的公开 HTTPS origin，例如 `https://api.example.invalid`。不支持路径前缀、userinfo、query、fragment 或通配回调；仅本地开发允许 `http://localhost:3000`、loopback IP。
3. 在该 Google 客户端登记**完全一致**的 Authorized redirect URI：`https://api.example.invalid/oauth/google`。不是 `/api/oauth/google`；后者是前端回调页调用的内部接口。不要登记 web-home 的域名，除非 NewAPI 本身确实在那里提供服务。
4. 配置安全的站点会话环境（HTTPS 生产环境必须设置 `SESSION_COOKIE_SECURE=true` 和准确的 `SESSION_COOKIE_TRUSTED_URL`；后端会检查Secure和可信origin，不合格拒绝启用/回调），并在多节点共享数据库和 `SESSION_SECRET`。反向代理须让 `/oauth/google` 返回 NewAPI SPA，让 `/api/oauth/*` 转发后端；不要在代理/CDN日志记录回调查询串，不要缓存身份接口。
5. 进入管理员“身份验证 → OAuth → Google”，先保存 Client ID 和 Client Secret，再启用 Google OAuth。权限仍由现有 root-only `/api/option/` 路由控制。关闭时登录/注册按钮隐藏，后端也拒绝新授权。
6. Client Secret 只由后端使用。GET 设置只返回 `google.client_secret_configured` 状态，不返回值。**留空保存表示保留现有密钥**，不会清空；替换时输入新密钥。停用使用 `GoogleOAuthEnabled=false`，不要依靠清空输入框。密钥轮换、撤销、生产部署及持久配置属于运维授权范围，不由此代码自动执行。
7. 若以前已有 slug 为 `google` 的自定义提供者，升级不会删除或覆盖它；内置 Google 失败关闭并提示冲突。先由管理员明确重命名旧提供者，再启用。大小写变体也被保留。

持久设置键：`GoogleOAuthEnabled`（默认 false）、`google.client_id`、`google.client_secret`。
公开状态仅提供 Google 可用标志，不发布 secret、nonce、PKCE verifier 或 tokens。
无后台 Google 数据轮询，无 refresh-token 离线访问，无 Gmail scope。

## 账户行为

- 原有 Google 绑定按 Google 的固定 issuer + 区分大小写的 `sub` 识别；邮箱和 Workspace 域不代表身份或权限。
- 首次使用 Google 可在站点允许注册时创建**普通用户**，沿用普通邀请关系；不会获得管理员、代理资格或 LinuxDO 专属赠金。
- 相同邮箱已属于另一 NewAPI 用户时，**绝不自动合并或绑定**。权威邮箱冲突会拒绝新注册；不采纳的第三方邮箱不参与本地账号匹配，可创建独立、无恢复邮箱的 Google 用户。如需使用原账号，请先用既有方式登录，再在账户安全页经过重新认证后主动绑定 Google。
- 只有 Google 当前具有权威性的 Gmail / 已签名 Workspace 邮箱会自动保存为恢复邮箱。第三方邮箱即使 `email_verified=true` 也可能已易主；此类 Google 账号仍可按 `sub` 注册/登录，但不自动保存该邮箱，需在账户安全页经过本地邮箱验证后添加。
- 已绑定用户在关闭新注册后仍可登录；封禁、软删除和已有 2FA/Passkey 登录验证继续生效。Google 登录不会直接跳过 NewAPI 的第二步验证。
- 绑定/解绑都需要操作和当前会话绑定的单次安全 proof。Google 解绑必须保留另一个当前可用的登录方式，不能把自己锁在账户外。
- 解绑会推进账户身份版本，阻止解绑前的在途 Google 登录/2FA挑战继续换取会话；当前操作会话按现有安全设置流程轮换，其他会话须重新登录。管理员清绑定也重新检查管理员及目标账户当前权限。
- 新账号不会因为 Google 邮箱发生变化自动改写 NewAPI 的恢复邮箱。Google API access/refresh token 在后端立即丢弃，不储存在用户记录、浏览器或日志。

### Google 用于敏感操作重新认证

普通 Google 登录不要求额外声明。若用户选择 Google 来确认“设置密码/绑定/解绑”等敏感操作，
仅从已有 Google SSO 会话得到一个新 ID token **不等于**用户刚刚认证。
本实现请求 Google 签名的 `auth_time`，只接受过去 5 分钟内的 Google 认证（未来最多 60 秒时钟偏差）。
缺失或过旧时安全拒绝，要求重新登录 Google，或选择本地密码、2FA、Passkey。

Google 官方说明 `auth_time` / `amr` session metadata 需要已验证应用并在 Google 侧启用相应能力。
不要仅为绕过这个检查扩大 scope；不支持该声明时，不能假称 Google 重新认证成功。
生产启用前应为账户准备可用的恢复/额外验证方式并验证该能力。

## 协议与数据边界

- 授权码仅由服务器发往固定 Google token endpoint；S256 PKCE、随机 nonce、10 分钟持久状态和 HttpOnly host-only浏览器绑定共同防护登录 CSRF。
- HTTPS state cookie使用 `__Host-` / Secure / SameSite=Lax；loopback开发使用非Secure的专用本地cookie。新启动覆盖旧浏览器尝试；过期、错浏览器、错provider、已用状态会被拒绝。
- 先持久标记一次 exchange attempt，再请求 Google：取消、失败、并发或重复 callback都不能重放同一次交换。失败后应重新点击 Google 登录，而非刷新旧 callback。
- 固定 Google issuer / token / JWKS地址，不使用管理员可编辑端点、JWT `jku` / `x5u` 或前端注入ID token。拒绝 token/JWKS 重定向，响应大小和网络时间有限。
- 使用已有 `coreos/go-oidc` 库验证 RS256 签名、issuer、audience、expiry；另校验nonce、iat、azp、多aud条件、sub格式、邮箱格式及 `email_verified=true`。
- Google 的两种官方 issuer 字面值统一映射到 `https://accounts.google.com` 身份域。`sub` 最长255字节、区分大小写；原值存在 `users.google_id`，唯一claim使用 SHA-256(固定issuer + NUL + sub)，复用既有外部身份唯一索引。
- 用户创建和claim同事务；绑定/解绑锁用户/会话，依靠 provider-subject 与 provider-user 双唯一约束处理并发。软删仍保留占用；真正硬删沿用原项目释放身份的语义。
- Google secret写入检查实际数据库错误，失败不发布运行时配置；Google secret及包含PKCE的AuthFlow SQL禁止输出参数，即使DEBUG。数据库和备份仍包含敏感配置，需按既有密钥存储权限保护。

## 验收与限制

本次使用合成账号、测试签名key和本地 mock token/JWKS，覆盖正常登录/注册、取消/失败、wrong state/browser/nonce/issuer/audience/signature、过期/重放/并发、相同邮箱、2FA、注册开关、绑定/解绑/锁死、配置secret与失败持久化。
使用 PostgreSQL 18.6 隔离临时库验证，**未执行 SQLite/MySQL，也不宣称这两者已验证**。
本次整合以公告提交 `5174128d7bc8f1562c5e805d71d54efbf0a32e91` 为精确基线；旧schema→新schema和重复启动验证及完整命令见私有交付恢复包。保留公告图片与普通邀请返利，未合入此基线之后的额外上游提交。
同版号池页面开关位于“系统设置 → 内容 → 账号池”，同样默认关闭；它独立于 Google 登录开关，不控制 Sub2 转发或模型自测。缺失旧站点 option 时也按关闭处理；只有 root 可以显式启用。
真实 Google client/consent/授权最终grant、生产反向代理和真实浏览器跨站cookie行为，仍需在单独授权的预生产/生产验收中确认。

参考（核验于2026-11-01；应用控制验证，不是ASVS认证）：
- [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect)
- [Google ID token 邮箱权威性规则](https://developers.google.com/identity/gsi/web/guides/verify-google-id-token)
- [Google Web Server OAuth](https://developers.google.com/identity/protocols/oauth2/web-server)
- [Google authentication session metadata](https://developers.googleblog.com/enhance-security-and-trust-new-session-metadata-in-sign-in-with-google/)
- [OWASP ASVS 5.0.0](https://asvs.owasp.org/)
- [OWASP OAuth2](https://cheatsheetseries.owasp.org/cheatsheets/OAuth2_Cheat_Sheet.html)
- [OWASP Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)
- [OWASP Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
