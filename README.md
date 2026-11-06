# Hermes Haven

Hermes Haven 是面向个人使用的轻量 Web 界面，通过已有的 Hermes API 和 Dashboard 账号，在浏览器中管理会话、发送消息、查看流式回复和工具调用。项目使用 Go 与 Vue，前端构建后嵌入 Go 程序，以单个可执行文件运行。

## 功能

- 使用已有的 Hermes 用户名和密码登录。
- 新建、切换、搜索会话，修改 Haven 中显示的会话标题。
- 用户消息靠右显示，发送后立即展示；支持草稿、会话位置和运行状态恢复。
- 查看流式回复与工具调用，展开参数、结果和错误信息，停止正在进行的运行。
- 审批支持“允许一次”和“拒绝”；补充提问支持选项、多选及自由回答，提交后继续当前运行。刷新恢复与网络异常核对不会自动提交回答。
- 普通文件上传、按消息关联、下载及文本、图片、PDF 基础预览；单文件最多 50 MiB，每条最多 4 个附件、合计不超过 100 MiB，图片仍遵守原有图片限制。
- 上传图片、粘贴截图，查看历史图片及助手生成的图片原文件。
- 向上滚动接近顶部时自动预加载更早的消息，保持阅读位置；可配置严格只读的完整历史读取。

本项目面向单个 Hermes 实例的个人空间，不提供不同账号之间的历史隔离。

## 安装与快速启动

### 1. 准备依赖

构建需要 Git、Go 1.24 或更高版本、Node.js 22 或更高版本以及 npm。

运行前需要准备：

- 已启动的 Hermes API，支持会话与 Runs 接口。
- 已启用用户名/密码登录的 Hermes Dashboard，以及可用的登录账号。
- Hermes API Key，可直接配置，也可从现有 Hermes `.env` 文件中的 `API_SERVER_KEY` 读取。
- 如需读取完整旧历史，还需要同一套 Hermes 安装的 Python 环境、源码和历史数据库，配置见下文。

### 2. 下载并构建

以下命令以 Bash 为例：

```bash
git clone https://github.com/EdikKing/hermes-haven.git
cd hermes-haven

npm --prefix web ci
npm --prefix web run build
go build -o hermes-haven .
```

必须先构建前端，再构建 Go 程序。构建产物已包含网页资源，部署时无需安装 Node.js 或运行前端开发服务器。可执行文件的平台与架构应和运行环境一致。

### 3. 配置并启动

将 `/absolute/path/to/hermes/.env` 替换为实际配置文件路径，并确认其中包含 `API_SERVER_KEY=...`、运行用户有权读取该文件：

```bash
export HERMES_URL="http://127.0.0.1:8642"
export HERMES_DASHBOARD_URL="http://127.0.0.1:9119"
export HERMES_ENV_FILE="/absolute/path/to/hermes/.env"
export HAVEN_LISTEN="127.0.0.1:18770"
export HAVEN_DATA_DIR="$PWD/data"

./hermes-haven
```

在浏览器打开 <http://127.0.0.1:18770/>，使用已有的 Hermes Dashboard 账号登录。Haven 无需单独创建账号或登录密码。

也可以使用 `HERMES_API_KEY` 直接传入 API Key；同时配置时，它优先于 `HERMES_ENV_FILE`。不要将真实密钥或包含密钥的环境文件提交到 Git。

Windows 使用 PowerShell 时，构建输出改为 `go build -o hermes-haven.exe .`，环境变量和启动方式如下：

```powershell
$env:HERMES_URL = 'http://127.0.0.1:8642'
$env:HERMES_DASHBOARD_URL = 'http://127.0.0.1:9119'
$env:HERMES_ENV_FILE = 'C:\path\to\hermes\.env'
$env:HAVEN_LISTEN = '127.0.0.1:18770'
$env:HAVEN_DATA_DIR = Join-Path $PWD 'data'
.\hermes-haven.exe
```

## 配置说明

配置通过进程环境变量传入；Haven 不会自动加载当前目录中的 `.env` 文件。使用 `HERMES_ENV_FILE` 时，只从指定文件读取 `API_SERVER_KEY`。

| 环境变量 | 说明 | 默认值 |
| --- | --- | --- |
| `HAVEN_LISTEN` | Haven 的监听地址 | `127.0.0.1:18770` |
| `HAVEN_DATA_DIR` | Haven 状态、发送记录、图片和文件数据目录 | `data`，相对于工作目录 |
| `HERMES_URL` | Hermes API 地址 | `http://127.0.0.1:8642` |
| `HERMES_DASHBOARD_URL` | 用于验证账号的 Dashboard 地址 | `http://127.0.0.1:9119` |
| `HERMES_AUTH_PROVIDER` | Dashboard 用户名/密码登录方式的标识 | `basic` |
| `HERMES_ENV_FILE` | 包含 `API_SERVER_KEY` 的 Hermes 环境文件路径 | 未设置 |
| `HERMES_API_KEY` | 直接提供 Hermes API Key | 未设置，优先于环境文件 |
| `HERMES_HISTORY_DB` | 当前 Hermes profile 的历史数据库路径 | 未设置 |
| `HERMES_HISTORY_PYTHON` | 可加载 Hermes 模块的 Python 解释器 | `python3` |
| `HERMES_SOURCE_DIR` | 已安装的 Hermes 源码目录 | `/usr/local/lib/hermes-agent` |
| `HAVEN_IMAGE_ROOTS` | 允许读取历史和生成图片的目录列表 | 内置的 root 用户 Hermes 上传/缓存目录，其他用户部署时应显式设置 |
| `HAVEN_SECURE_COOKIE` | 是否只通过 HTTPS 发送登录 Cookie | `false` |

### 完整历史与图片目录

Haven 与 Hermes 应位于同一主机，或能够访问同一套必要文件路径。建议使用与 Hermes 相同的系统用户运行，并按实际安装位置设置权限与路径。

如需完整旧历史，在启动前补充以下配置。下面的路径均为示例，需要替换为实际位置：

```bash
export HERMES_HISTORY_DB="/absolute/path/to/profile/state.db"
export HERMES_HISTORY_PYTHON="/absolute/path/to/hermes/venv/bin/python"
export HERMES_SOURCE_DIR="/absolute/path/to/hermes/source"
export HAVEN_IMAGE_ROOTS="/absolute/path/to/uploads:/absolute/path/to/native-inputs:/absolute/path/to/generated-images"
```

- Python 环境与源码应来自对应的 Hermes 安装，并能读取所指定 profile 的数据库。
- `HAVEN_IMAGE_ROOTS` 应包含历史图片和生成图片实际所在的目录；Linux/macOS 使用冒号分隔，Windows 使用分号分隔。
- 运行用户需要对 Haven 数据目录有写权限，对 Hermes 历史、源码、Python 环境及允许的图片目录有相应访问权限。

完整历史通过 Hermes 的只读 `SessionDB` 和 SQLite `query_only` 读取。配置后会在启动时检查读取能力，失败则停止启动。未配置时仅通过 HTTP 读取活动消息，页面会提示完整旧历史暂未启用。

## Linux 服务部署

以下示例适用于使用 systemd 的 Linux。示例中的 `hermes` 指已有的 Hermes 运行用户，请替换为实际系统用户；环境文件中的路径也需要对应调整。

### 安装程序与目录

在已完成构建的源码目录执行：

```bash
sudo install -d -m 755 /opt/hermes-haven
sudo install -m 755 ./hermes-haven /opt/hermes-haven/hermes-haven
sudo install -d -m 700 -o hermes /var/lib/hermes-haven
sudo install -d -m 750 /etc/hermes-haven
sudoedit /etc/hermes-haven/haven.env
```

在 `/etc/hermes-haven/haven.env` 中填写：

```ini
HAVEN_LISTEN=127.0.0.1:18770
HAVEN_DATA_DIR=/var/lib/hermes-haven
HERMES_URL=http://127.0.0.1:8642
HERMES_DASHBOARD_URL=http://127.0.0.1:9119
HERMES_AUTH_PROVIDER=basic
HERMES_ENV_FILE=/home/hermes/.hermes/.env
HAVEN_IMAGE_ROOTS=/home/hermes/.hermes-web-ui/upload:/home/hermes/.hermes/cache/documents/native-inputs:/home/hermes/.hermes/cache/generated/images
HAVEN_SECURE_COOKIE=false

# 需要完整旧历史时，按实际安装位置填写并启用以下三项
# HERMES_HISTORY_DB=/home/hermes/.hermes/state.db
# HERMES_HISTORY_PYTHON=/opt/hermes-agent/venv/bin/python
# HERMES_SOURCE_DIR=/opt/hermes-agent
```

systemd 环境文件不会按 Shell 方式展开 `$HOME` 等变量，请填写实际绝对路径。随后限制文件读取权限：

```bash
sudo chmod 600 /etc/hermes-haven/haven.env
```

### 创建并启动服务

编辑 `/etc/systemd/system/hermes-haven.service`：

```ini
[Unit]
Description=Hermes Haven
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=hermes
WorkingDirectory=/opt/hermes-haven
EnvironmentFile=/etc/hermes-haven/haven.env
ExecStart=/opt/hermes-haven/hermes-haven
Restart=on-failure
RestartSec=5
UMask=0077

[Install]
WantedBy=multi-user.target
```

启动服务并设置开机启动：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now hermes-haven.service
sudo systemctl status hermes-haven.service --no-pager -l
```

常用管理命令：

```bash
# 重启服务
sudo systemctl restart hermes-haven.service

# 查看状态
sudo systemctl status hermes-haven.service --no-pager -l

# 查看最近 100 行日志
sudo journalctl -u hermes-haven.service -n 100 --no-pager

# 实时查看日志，按 Ctrl+C 退出
sudo journalctl -u hermes-haven.service -f
```

## 远程访问与 HTTPS

默认监听本机地址。临时远程访问可使用 SSH 隧道，将 `user@your-server` 替换为实际 SSH 账号与主机：

```bash
ssh -L 18770:127.0.0.1:18770 user@your-server
```

隧道建立后，在本机浏览器打开 <http://127.0.0.1:18770/>。

正式远程访问应使用 HTTPS 反向代理，并将 `HAVEN_SECURE_COOKIE=true` 后重启 Haven。应用使用站点根路径，反向代理应同时转发网页、`/api/` 和静态资源；流式回复需要关闭代理缓冲并设置足够长的读取超时。

例如，在已配置 HTTPS 的 Nginx `server` 中添加：

```nginx
client_max_body_size 51m;

location / {
    proxy_pass http://127.0.0.1:18770;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_buffering off;
    proxy_read_timeout 3600s;
}
```

Haven 的 API Key 只保留在后端。Hermes API 应保持本机监听，浏览器通过 Haven 访问，不需要直接连接 Hermes API。代理选项可参考 [Nginx 官方文档](https://nginx.org/en/docs/http/ngx_http_proxy_module.html)。

## 使用说明

1. **登录**：输入现有 Hermes Dashboard 用户名和密码。Dashboard 与 Haven 的网页登录状态各自独立。
2. **开始对话**：点击“新建会话”，输入消息；Enter 发送，Shift+Enter 换行。用户消息会立即显示在右侧。
3. **使用图片**：通过输入区的附件按钮选择图片，或直接粘贴截图，确认预览后发送。支持 PNG、JPEG、GIF、WebP；单张最多 5 MiB，每条消息最多 4 张且图片合计不超过 6 MiB。
4. **查看回复**：回复实时展示；历史工具调用默认折叠，点击后可查看参数、结果及错误。生成图片在原文件可访问且目录已允许时直接展示。
5. **管理会话**：通过侧栏切换、搜索会话，使用顶部重命名按钮修改 Haven 本地标题。“加载更早的消息”用于继续读取历史。
6. **停止运行**：点击输入区的停止按钮，等待运行状态确认。继续旧来源会话时，是否接受新消息由 Hermes 决定。

草稿和会话位置按账号保存在当前浏览器中。刷新或重连会核对已有运行，不自动重新发送消息。更换浏览器或清理站点数据会丢失浏览器中的草稿；服务重启或登录到期后，需要重新登录。

### 发送结果待核对

| 操作 | 作用 |
| --- | --- |
| 核对结果 | 查询原请求与运行状态，不重新发送消息 |
| 安全重试原请求 | 使用原请求编号重试，后端会检查是否满足安全重试条件 |
| 已核对，解除等待 | 在你确认历史和运行状态后解除等待；不停止或重新执行原运行 |

遇到结果未知时，应先核对历史与运行状态，再决定后续操作。

### 审批与补充提问

Hermes 实际请求审批时，页面显示命令和说明，可选择“允许一次”或“拒绝”。原有永久允许规则继续生效，不会因 Haven 增加审批卡片而强制弹出。补充提问允许选择选项或直接输入回答，回答后继续原运行；结果不确定时，先点“核对状态”，确认仍在等待后再手动提交。

Hermes 0.23.0 的 API 默认工具集不包含 `clarify`。如需模型主动发起补充提问，应保留现有 API 工具集，仅增加 `clarify`；不能将工具集整体替换为只含该工具的列表。界面依据原生 `pending_controls` 和精确执行代次处理请求，过期或被其他入口处理的请求会失效。实现与验证见 [审批与补充提问验收](docs/审批与补充提问验收.md)。

## 升级与备份

- 备份整个 `HAVEN_DATA_DIR`、服务环境文件与旧程序。数据目录包含会话归属、发送记录及图片/文件数据；不要只备份 `state.json`。
- Hermes 自己的历史数据库和其他数据应通过 Hermes 对应的方式另行备份。
- 升级前先确认没有正在进行的对话，停止 Haven 后取得一致的数据备份。
- 在源码目录执行 `git pull --ff-only`，重新运行 `npm --prefix web ci`、`npm --prefix web run build` 和 `go build`，再替换可执行文件并启动服务。
- 更新后检查服务状态和页面。需要回退时恢复旧程序；避免用旧数据备份覆盖升级后产生的新记录。

同一数据目录只运行一个 Haven 实例。重启会断开实时连接并使当前登录失效，但发送记录和 Hermes 已保存的历史会保留。

## 常见问题

| 现象 | 检查项 |
| --- | --- |
| 提示必须配置 API 密钥 | `HERMES_ENV_FILE` 路径、读取权限及 `API_SERVER_KEY` 是否正确，或检查 `HERMES_API_KEY` |
| 无法登录 | Dashboard 地址、用户名/密码登录是否启用，以及 `HERMES_AUTH_PROVIDER` 是否匹配 |
| 登录成功后仍回到登录页 | 检查访问协议与 `HAVEN_SECURE_COOKIE`：本机 HTTP 用 `false`，HTTPS 用 `true` |
| 旧历史未显示或只读检查失败 | 检查历史数据库、Python、Hermes 源码是否对应同一安装，及运行用户的访问权限 |
| 旧图片或生成图片打不开 | 检查原文件是否存在、图片格式、`HAVEN_IMAGE_ROOTS` 以及文件访问权限 |
| 反向代理后回复不实时显示 | 检查代理缓冲、读取超时和 SSE 连接是否被提前关闭 |

提交问题时，请通过 [GitHub Issues](https://github.com/EdikKing/hermes-haven/issues) 提供复现步骤、版本和已脱敏的错误日志。

## 开发自检

```bash
npm --prefix web test
npm --prefix web run build
go test ./...
go vet ./...
```

`dev/mock_hermes.py` 可用于不连接真实 Hermes 的本机模拟测试。它默认监听 `127.0.0.1:18642`；将两个 Hermes 服务地址均指向该地址，使用独立的数据目录和测试 API Key，再启动 Haven。测试账号为 `preview`，密码为 `local-preview-password`，仅适用于模拟服务。

## 许可

本项目采用通用的 [PolyForm Noncommercial License 1.0.0](LICENSE)，官方条文保持原样，版权声明见 [NOTICE](NOTICE)。

允许条款所规定的非商业使用、个人学习与研究，以及相应的修改和分享；原文还规定了慈善、教育、公共研究等机构的允许用途。分发时应附上许可条文或其链接，并保留 NOTICE 中以 `Required Notice:` 开头的声明。商业用途须取得版权人的单独授权，具体权限以许可原文为准。

这是一份非商业源码许可，不属于 [OSI 定义的开源许可](https://opensource.org/osd)。第三方依赖和 Hermes 本身继续遵循各自的许可证。
