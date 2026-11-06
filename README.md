# Hermes Haven

Hermes Haven 是面向个人使用的轻量 Web 界面，通过 Hermes 官方 API 管理会话、显示流式回复和工具调用。

## 第一阶段能力

- 使用现有 Hermes Dashboard 用户名和密码登录、新建和切换会话、标题搜索，以及仅修改 Haven 本地标题。
- 流式回复、工具调用、停止运行和刷新后的运行状态恢复。
- 自动返回上次会话；按账号和会话保留草稿、图片附件及待核对请求。
- 发送记录先落盘再提交；刷新和重连只查询状态，用户明确点击后才使用原编号安全重试。
- 登录过期后重新登录并恢复草稿；网络中断后逐步重连并核对最终历史。
- 工具失败原因、结果摘要及耗时；会话按需分页，标题搜索继续检索更早的页面。
- 图片选择、剪贴板粘贴、旧图片展示。
- 查看 Hermes 历史，并允许用户尝试继续原会话；Hermes 拒绝时直接显示原因，不自动重试或创建替代会话。
- 不提供删除会话或修改历史消息的接口。当前不支持普通文档上传、审批交互、Studio 独有历史导入或全文搜索。

Hermes 原生历史由 Hermes 保存；Haven 的会话归属、标题及运行关联保存在自己的数据目录中。请备份 Haven 数据目录和 Hermes 数据目录，不要直接修改其状态文件。

## 构建

需要 Go 1.24+、Node.js 和 npm。前端资源编译后嵌入 Go 二进制文件：

```shell
npm --prefix web ci
npm --prefix web run build
npm --prefix web test
go test ./...
go build -o hermes-haven .
```

先构建前端，再构建 Go 程序。前端依赖不会部署到服务器运行。

## 配置

| 环境变量 | 作用 | 默认值 |
| --- | --- | --- |
| `HAVEN_LISTEN` | Haven 的监听地址 | `127.0.0.1:18770` |
| `HAVEN_DATA_DIR` | Haven 自己的状态和图片目录 | `data` |
| `HERMES_DASHBOARD_URL` | 用于验证账号的 Hermes Dashboard 地址 | `http://127.0.0.1:9119` |
| `HERMES_AUTH_PROVIDER` | Dashboard 用户名/密码登录方式的标识 | `basic` |
| `HERMES_URL` | Hermes 官方 API 地址 | `http://127.0.0.1:8642` |
| `HERMES_ENV_FILE` | 从 Hermes `.env` 读取 `API_SERVER_KEY` | 无 |
| `HERMES_API_KEY` | 直接提供 API Key；优先于 `HERMES_ENV_FILE` | 无 |
| `HAVEN_IMAGE_ROOTS` | 允许读取历史图片的目录，按操作系统路径列表分隔 | `/root/.hermes-web-ui/upload:/root/.hermes/cache/documents/native-inputs` |
| `HAVEN_SECURE_COOKIE` | HTTPS 反向代理下设为 `true` | `false` |

Haven 将用户输入的账号密码通过 Dashboard 的 `/auth/password-login` 验证，再通过 `/api/auth/me` 确认身份。密码不落盘、不写日志，Hermes 返回的 Cookie 不返回浏览器；Haven 只保留自己的内存登录状态，有效期不超过本次 Hermes 身份的到期时间和 24 小时，服务重启后需要重新登录。到期后重新输入 Hermes 账号即可；不持有上游刷新令牌。

无需单独设置 Haven 密码，旧的 `HAVEN_PASSWORD` 和 `HAVEN_PASSWORD_FILE` 不再使用。这是复用账号验证，Haven 和 Dashboard 的网页登录状态各自独立；在 Haven 退出不会退出 Dashboard。此界面仍面向同一 Hermes 实例的个人空间，不提供多用户历史隔离。API Key 仅保留在后端，Hermes API 应保持本机监听。

如果使用明文 HTTP，密码和会话 Cookie 在网络中不加密。公网或不可信网络访问应先配置 HTTPS 和适当的访问控制；配置 HTTPS 后设置 `HAVEN_SECURE_COOKIE=true`。

## 当前服务器部署

当前部署在 `10.1.1.52`，访问地址为 `http://10.1.1.52:18770/`。服务名是 `hermes-haven.service`，已配置开机启动；二进制文件和 Haven 数据分别位于：

- `/opt/hermes-haven/hermes-haven`
- `/var/lib/hermes-haven/`

2026-11-06 已上线清爽配色 UI，部署校验和备份位置见 [第一阶段验收记录](docs/第一阶段验收.md)。

使用现有 Hermes Dashboard 账号登录，身份由本机 `127.0.0.1:9119` 的 Hermes 服务验证。此前生成的独立密码文件不再使用。该服务以 root 运行，沿用当前 Hermes 的文件访问权限。

```shell
systemctl status hermes-haven
journalctl -u hermes-haven -n 100 --no-pager
```

停止或重启 Haven 会断开当前网页连接，但不会重启 Hermes Gateway。进行版本更新前，先确认没有正在进行的对话。

## 数据边界

Haven 不调用 Hermes 的会话删除、消息修改、历史截断或历史分支接口。用户主动在旧会话发送消息时，后端会尝试调用 Hermes Runs；Hermes 拒绝该来源的续聊时，界面显示拒绝信息。能否继续由 Hermes 的会话归属规则决定。

历史图片仅在消息本身引用、文件位于允许目录且解析后的路径没有越界时读取。支持的上传图片格式为 PNG、JPEG、GIF、WebP：单张不超过 5 MB，每次最多四张且总大小不超过 6 MB。

历史图片的新链接由实际读取的消息签发加密引用，绑定会话、消息和图片位置，仍需登录并检查允许目录；不再为了展示图片扫描整条历史。旧链接继续按页兼容读取，不设置 3,000 条消息的扫描上限。

## 恢复与重试

浏览器只保存当前账号的会话位置、草稿、附件标识和实时事件检查点，不保存密码或 API Key。退出登录后这些草稿仍保留在当前浏览器，重新登录同一账号可恢复；浏览器存储不可用时会明确提示。更换浏览器或清理站点数据会丢失本地草稿，Hermes 已保存的历史不受影响。

Haven 数据目录中的 `requests/` 保存发送记录。记录写入并同步成功后才调用 Hermes；收到确定结果后保留运行关联。Haven 服务重启后可继续核对原请求，不能将一个编号用于不同消息。该目录与 `state.json` 一起备份；只启动一个使用该数据目录的 Haven 实例。

出现“发送结果待核对”时，“核对结果”只读取状态。“安全重试原请求”沿用原编号，后端会检查 Hermes 的持久幂等能力、认证作用域及保留期；不满足条件时拒绝再次执行。超过 24 小时的旧浏览器待确认请求也不能作为新请求提交。

用户确认已经检查原会话历史和运行状态后，可以点击“已核对，解除等待”。这只解除 Haven 自己的等待标记，不会取消、回滚或重新执行 Hermes 运行；已经执行的消息仍保存在原会话中。原请求编号关闭后不能再次重放。

会话初始读取 200 条，点击“加载更多会话”继续分页；标题搜索会继续逐页读取可见历史，不再设置 2,000 条的界面上限。当前只覆盖该 Hermes API 可见的 profile 范围。

验收范围、真实联调和数据完整性检查见 [第一阶段验收记录](docs/第一阶段验收.md)。

## 本机模拟预览

`dev/mock_hermes.py` 是不连接实际 Hermes 的测试服务。先运行该脚本，再将 `HERMES_URL` 和 `HERMES_DASHBOARD_URL` 均指向 `http://127.0.0.1:18642`，设置测试 API Key 和独立的 `HAVEN_DATA_DIR` 后启动 Haven。预览账号为 `preview`，密码为 `local-preview-password`；这些固定测试凭据只在模拟服务生效，不能用于真实服务器。模拟数据仅用于界面验证。
