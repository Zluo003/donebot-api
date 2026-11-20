# 客户端版本发布与删除验收

在隔离环境运行以下检查，不使用线上管理员会话或真实安装包：

```sh
cd backend
go test ./internal/service ./internal/handler/admin ./internal/server/routes -run 'TestClientRelease' -count=1 -v
cd ../frontend
pnpm exec vitest run src/components/admin/donebot/__tests__/ClientReleasesPanel.spec.ts src/api/admin/__tests__/donebot.spec.ts
pnpm exec vue-tsc --noEmit
```

HTTP fixture 使用临时存储和合成安装包，覆盖上传、发布、更新清单、下载、新版本检测及旧版本删除。切换存储设置后，删除必须使用安装包上传时保存的存储配置，不能误删当前存储中的文件。

当前已发布的最高版本不可删除，更高版本的草稿不解除这一限制。删除旧版本会同时删除 Windows、macOS DMG 和 macOS ZIP 文件，再移除历史记录；当前版本的更新清单和下载仍然可用。开始删除前先持久化 `deleting` 状态，存储清理失败时保留记录供重试，该状态下禁止上传或发布。

组件测试覆盖删除确认、取消、当前版本保护、TOTP 验证重试及存储错误后的再次删除。浏览器验收需在挂载真实 `ClientReleasesPanel`、`ConfirmDialog` 的隔离页面中替换列表及删除 API，以合成数据检查上述交互和窄屏深色布局。不得删除线上历史版本来验证功能。

客户端安装包还必须包含 `Contents/Resources/app-update.yml`。网关返回新版本信息不代表客户端已启用更新；缺少该文件的旧安装包需要手动安装修复版一次。客户端的打包、HTTP 更新检查及签名包验收步骤见 DoneBot 项目的 `docs/verification/client-updates.md`。
