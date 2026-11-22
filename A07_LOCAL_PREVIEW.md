# A07 本地预览

本预览用于检查当前开发界面和流程，尚不是上线验收版本。不连接生产数据库；演示支付网关是不可收款的 `https://payment.example.test`，用于查看报价和二维码，不能用于实际支付。

2026-11-03 已重新构建前后端并重启，纳入 A08–A16 赠金提示、尾款结清、收益先抵扣、旧入口退役、已付款兜底和每日提醒；最新检查记录见 `A07_PROGRESS_A16.md`，完整验收状态见 `A07_ACCEPTANCE_A03.md`。服务已启动，当前只允许这台电脑访问。切换测试身份时先退出当前账号，再登录另一账号；保留测试期间生成的记录，不会在重启时清空。

## 查看

- 登录：http://127.0.0.1:3107/sign-in
- 代理总览：http://127.0.0.1:3107/agent-platform
- 兑换码中心：http://127.0.0.1:3107/redemption-codes?view=center
- 兑换码记录：http://127.0.0.1:3107/redemption-codes?view=records
- 钱包：http://127.0.0.1:3107/wallet
- root 管理：http://127.0.0.1:3107/agent-platform/finance

演示账号：代理 `a07_andy`、root `a07_root`、普通用户 `a07_user`。本机生成的登录密码保存在 `~/Library/Application Support/WanchuanA07Preview/ACCOUNTS.txt`，不提交到 Git。

初始演示数据：代理余额 $1,000、可用授信 $1,000、邀请返利 $30 已到账和 $5 冻结中。数据仅用于演示，不对应任何实际资金。

已完成的 A15 演示验收保留：直接码已作废、合成收款订单已退款；代理邀请收益曾划转 $1 到本人钱包并由 root 登记 $20 合成提现，因此当前可提现返利为 $9，冻结仍为 $5。这些记录只供查看业务流程，不对应实际收付款。

## 构建与运行

在 `web/` 执行 `./node_modules/.bin/rsbuild build`，然后在仓库根目录执行：

```sh
GOWORK=off go build -o /tmp/wanchuan-a07-preview-server .
python3 scripts/a07-preview.py start
python3 scripts/a07-preview.py status
```

停止：`python3 scripts/a07-preview.py stop`。更新构建后先停止旧预览再启动；重复启动保留已有演示操作和密码。前端构建需要使用现有依赖目录。

后端将前端构建文件嵌入同一个程序，浏览器与 API 共用端口 3107；服务仅监听 `127.0.0.1`。SQLite 数据、程序日志、演示账号和会话信息都在独立目录 `~/Library/Application Support/WanchuanA07Preview/`，与数据库兼容性测试目录分开。

预览使用独立的演示支付配置和测试账户，可查看报价、生成收款二维码与记录；网关不能实际付款，不会发起真实扣款。演示库完成自己的合规测试配置，生产服务没有新增绕过合规确认的开关。钱包与代理总览共用邀请卡的说明、记录和联系客服提现窗口。root 来源详情和分类历史已接通，完整上线验收继续进行。

## 决定

提供本地开发预览供用户查看；继续完成 A07 功能、文案和验收，不部署生产。
