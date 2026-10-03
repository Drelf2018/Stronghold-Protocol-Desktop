# 卫戍协议：盟约 启动器

把 [Stronghold-Protocol](https://github.com/sganggs/Stronghold-Protocol) 的网页界面放进一个原生窗口，常驻系统托盘。

一个**启动器**，不是游戏本身：它把上游文档里那串手工步骤接管过来——取回源码、装依赖、下载素材、起服务、开窗口。

## 它做什么

| 步骤 | 怎么做 |
|---|---|
| 取回源码 | 直接下 GitHub 的 tar.gz（codeload），**不需要装 git** |
| 装依赖 | 游戏自己的 `tools/setup.mjs`：`npm ci`、复制前端库、校验游戏数据 |
| 下载素材 | 同一个脚本，约 250 MB，可中断、可续传 |
| 起服务 | `node server/index.js`，`PORT=3000`、`HOST=0.0.0.0` |
| 开窗口 | WebView2 指向 `http://127.0.0.1:3000/`，等 `/healthz` 回应之后才算就绪 |

## 安装

到 Releases 下载最新的 `.exe`，双击运行。

- **需要 WebView2 运行时**：Windows 11 自带，Windows 10 大多随 Edge 装过。缺了它程序会直接报「缺少 WebView2 运行时」。
- **需要 Node.js 22 或更高**（游戏 `package.json` 里写的 `engines.node >= 22`）。找不到或版本太旧时，窗口会显示一页说明和 `winget install OpenJS.NodeJS.LTS`。
- **第一次运行需要网络**：要从 GitHub 下源码、从 npm 下依赖、再下约 250 MB 素材。之后都是本地的。

## 使用

### 菜单分两处

```text
右键标题栏 / Alt+空格（系统菜单，插在最前面）
  置顶 / 居中 / 刷新
  ──────── 之后是系统自己的：还原 / 移动 / 大小 / 最小化 / 最大化
  恢复参数尺寸 / 参考尺寸 ▸ / 相对占比 ▸ / 宽高比 ▸
  ──────── 最后仍是系统自己的：关闭

托盘
  显示窗口 / 开机自启动
  ────────────────
  打开 ▸    打开日志文件 / 打开游戏目录 / 打开程序目录
  更多 ▸    更新游戏 / 输入加入链接 / 在浏览器中打开 / 重新准备游戏文件
  ────────────────
  退出
```

本地构建（`Version` 仍是 `dev`）在「更多」那组之后还多一个**测试**子菜单：七张提示页平时要等真实的故障才看得到，那里可以直接把它们调出来看。发布版没有它——`-X main.Version=<tag>` 每次都会把 `Version` 填上，所以 `"dev"` 就等于「这一份是自己编的」。

### 第一次启动

窗口会先显示「正在准备」那一页，进度写在日志里：

1. 下载源码包，解压到 `%LOCALAPPDATA%\stronghold-protocol-launcher\game\`
2. 跑 `tools/setup.mjs`：`npm ci` → `public/vendor` → 下载 `public/assets`（约 250 MB）
3. 起 `server/index.js`，之后就打开游戏

中断了也没关系：再启动一次会接着来。**「重新准备游戏文件」**是唯一能把中断的素材下载接下去的入口——它会先停掉服务（`npm ci` 会重写 `node_modules`，边跑边换是自找麻烦），准备完再重启。

### 更新游戏

上游的**代码**更新只能靠这一步。游戏自己的 `tools/setup.mjs` 检查的全是**派生物**——`node_modules`、`public/vendor`、`data`、`public/assets`——它连代码变没变都不知道，更不会去取新版；上游文档里让你做的是 `git pull`，而这个启动器没有 git（故意的：需要 git 的启动器，在一台只有 Node.js 的机器上就是坏的）。

「更新游戏」做的四件事，每一件都对应别人脚本里的一个开关：

| 做什么 | 谁做 | 为什么必须做 |
|---|---|---|
| 取回最新源码（覆盖式解包 GitHub 的 `master` 归档） | 本程序（[launcher.go](launcher.go) 的 `fetchSource`） | 代码不在上游任何脚本的检查范围里 |
| 删掉 `node_modules` | 本程序 | `checkDeps()` 只看包目录**在不在**、不看版本：不删就永远是旧库配新代码。删掉是逼 `npm ci` 跑起来的唯一开关 |
| 删掉上游索引表 `.cache/gamedata/excel/audio_data.json` 与 `.cache/ark-models/models_data.json` | 本程序 | `assets/cache.mjs` 只在它们缺失时才重新下载，之后一直复用。留旧表：一旦新版带来新素材、触发 `fetch-assets.mjs`，它重建出来的清单就会按旧表算、漏掉新条目（甚至因为清单"缩水"而被它自己的防缩水保护挡下） |
| 装依赖、重建 `public/vendor`、按新清单**只补新增**的美术音频 | 游戏的 `tools/setup.mjs` | 素材是增量的，不会重下那 250 MB |

更新前后各读一次 `game\package.json` 的 `version` 写进日志（`game source updated from=0.1.1 to=0.1.2`）——不然按完了不知道到底有没有生效。

两个已知边界：

- **覆盖式解包只增改、不删**。上游删掉或改名的文件会留在磁盘上（`git pull` 会删）。真遇到麻烦时，删掉整个 `game\` 再启动，从头下一遍即可。
- **取的是 `master` 的当前状态，不是某个 tag**，所以理论上可能撞上上游的中间态。

### 联机

游戏是 1–4 人合作，**一个后端，所有人连它**：服务监听 `0.0.0.0:3000`，第二条连接就是第二个玩家。身份是按**连接**发的——服务端给每条连接一个 `playerId` 和一枚只发给本人的重连 token（浏览器存在 `sessionStorage`/`localStorage` 里，刷新或重开标签能坐回原位），**跟 IP 无关**。所以同一台机器上开两个窗口，就是两个玩家。

流程：建房 → 把 4 位「同盟密钥」或「复制链接」发给朋友 → 对方打开那个链接，或者用托盘里的「输入加入链接」。

两个容易踩的点：

- **双方必须连同一个后端**（同一个端口）。两个互不相干的启动器各自开出来的服务端是**两局**，不是一队；
- 自己和自己联机走同一条路：建一个**不是单人**的房间（`solo` 模式的房间会明确拒绝加入者），再开第二个窗口用密钥加入——托盘「在浏览器中打开」就是那个入口。

### 输入加入链接

托盘 `更多 ▸ 输入加入链接` 弹出一个输入框（问题在页面里问，见 `js/askForRoom.js`），三种输入都认：

| 输入 | 结果 |
|---|---|
| `ABCD`（4 位密钥，大小写都行） | 补成 `http://127.0.0.1:3000/?room=ABCD`——加入**本机这一局** |
| `192.168.1.5:3000/?room=ABCD` | 补成 `http://192.168.1.5:3000/?room=ABCD`——加入**朋友那一台** |
| `http://192.168.1.5:3000/?room=ABCD` | 原样使用 |

判定规则和游戏自己的大厅一致（`public/js/screens/lobby.js` 的 `normalizeCode`：接受整条链接、取 `?room=`、大写、只留字母数字）。输入的地址会被记住，下次启动直接开在那儿；想回到本机自己的游戏，输入 `http://127.0.0.1:3000/` 即可。

**准备步骤会排队**：取源码与跑 `tools\setup.mjs` 都要写同一份 `game\`（解压会截断重写每个文件，`npm ci` 会重写 `node_modules`），所以两份实例在 `%LOCALAPPDATA%\stronghold-protocol-launcher\prepare.lock` 上轮流来，等到手之后多半发现活已经干完了。锁随进程结束自动释放，中途被杀也不会卡住。

### 打开游戏目录

这一项开的是**本程序自己那份**源码（`%LOCALAPPDATA%\stronghold-protocol-launcher\game\`），不是谁另外 clone 的一份。你如果另外 clone 了仓库，那份跟这个启动器没关系。

### 命令行开关

| 开关 | 作用 |
|---|---|
| `-no-game` / `--no-game` | 什么都不启动：窗口对着上一次的地址（没有就用本机自己的 `http://127.0.0.1:3000/`），服务由别处提供 |
| `-data-dir <目录>` | 把本程序写东西的地方换到别处（默认 `%LOCALAPPDATA%\stronghold-protocol-launcher`）。配置文件不可写，或者想把这份安装（源码与素材）和别处完全分开时用 |

## 构建

需要 Go 1.27 或更高。不需要 C 工具链，也不需要 `rsrc.exe` —— 图标资源由本仓库里的程序自己写出来。

```powershell
go run ./internal/genicon                   # 生成 rsrc.syso
go build -ldflags=-H=windowsgui -o "Stronghold Protocol Launcher.exe" .
```

- **`rsrc.syso`** 是 exe 里的图标资源：资源管理器、快捷方式、固定到任务栏之后读的都是它。它**不进仓库**，由 `internal/genicon` 现做现用。少了这一步构建照样成功，只是图标静默退回系统默认，没有任何警告——所以 `go build` 之前先跑上面那一条。
- **`-ldflags=-H=windowsgui`**：默认的控制台子系统会在窗口旁边多开一个控制台，它的任务栏按钮用的是系统控制台图标，看起来就像这个程序没有图标。

## 发布

由 GitHub Action 发布：先由 `pre_command` 跑 `go run ./internal/genicon amd64`，再带 `-ldflags "-H=windowsgui -X main.Version=<tag>"` 构建上传。版本只在启动时写进日志。

## 项目结构

| 文件 | 管什么 |
|---|---|
| `main.go` | 程序本身：名字、菜单、启动与退出的顺序 |
| `launcher.go` | 游戏那一侧：下载并解压源码（首次启动与「更新游戏」）、跑 `tools/setup.mjs`、起 `server/index.js`、等 `/healthz`、退出时结束整棵进程树 |
| `window.go` | WebView2 窗口和它下面的 Win32：窗口矩形/可见框的换算、移动、置顶，以及窗口自己的图标（标题栏、任务栏、Alt+Tab） |
| `fullscreen.go` | 全屏：页面那个全屏按钮在 WebView2 里只让页面填满控件，所以由这一边把**窗口**真的铺满显示器，退出时把样式、位置、置顶还回去 |
| `dispatch.go` | 窗口自己的线程：消息过程与 `Dispatch` 队列，以及四段注入脚本与 `Bind` 的挂载点 |
| `sysmenu.go` | 窗口系统菜单上那一段：置顶/居中/刷新/恢复参数尺寸 + 三组尺寸 |
| `size.go` | 三组尺寸菜单描述的那个尺寸、它的下限，以及 `smallIconSize`（托盘与窗口图标画多大） |
| `notice.go` + `loading.html` | 服务给不出页面时本程序自己那几页：文字在 Go 里，版式在模板里，转义交给 `html/template` |
| `startup.go` | 开机自启动（注册表 `HKCU\...\Run`） |
| `integrity.go` | 查明本程序是在哪个完整性级别下运行的——`%LOCALAPPDATA%` 写不进去、WebView2 建不起来时，这一条就是解释 |
| `appdata.go` | 本程序的路径：`%LOCALAPPDATA%` 下的日志、窗口状态与**游戏自己**，以及 exe 所在的程序目录 |
| `js/` | 注入到页面里的四段脚本：`reload.js`（刷新就是在同一个地址上再取一次）、`mute.js`（窗口藏进托盘时把页面的声音收掉）、`askForRoom.js`（「输入加入链接」那个输入框）、`fullscreen.js`（页面的全屏状态报给 Go） |
| `internal/artwork/` | 图标源按任意尺寸缩放、居中，拼成多尺寸 .ico，或只装一张。**与平台无关**，所以生成器能在 Linux 容器里跑 |
| `internal/artwork/stronghold.png` | 图标源，位图 |
| `internal/genicon/` | 生成器：写 `rsrc.syso`（`go run ./internal/genicon [arch]`） |
| `launcher_test.go` | 游戏那一侧能离线测的全在这儿：源码包的路径消毒与版本号解析（拿字节造一个 tar.gz 来解）、加入链接的归一化、准备锁真的排他、日志轮转在别人开着时让路。都不联网 |
| `notice_test.go` | 提示页确实是自包含的 data URL、标题与底部的框都在 |
| `*_test.go` | 尺寸下限、自启动、系统菜单 id、窗口线程队列、注入脚本的名字、提示页、完整性级别 |

## 实现细节

<details>
<summary>为什么不是 npx，以及源码是怎么来的</summary>

`npx` 只能跑已发布到 npm 的包，而 `stronghold-protocol-covenant` 的 `package.json` 写着 `"private": true`、也没有 `bin` 字段——registry 上查不到它，`npx github:...` 也找不到可执行入口。

所以取的是另一条路：GitHub 的归档接口 `codeload.github.com/<owner>/<repo>/tar.gz/refs/heads/master`，也就是网页上「Download ZIP」给的那个文件。它不需要 git（一台只装了 Node.js 的机器也跑得起来），解压由 Go 自己做。

解压是**覆盖**而不是清空：原地替换同名文件、补上缺的，于是更新不会把 `node_modules` 和已下载的 250 MB 素材一起扔掉。归档里每条路径都先过一遍路径消毒（丢掉最外层那个 `<仓库>-<分支>/`，拒绝绝对路径和任何爬出目录的名字），因为归档的名字是别人写的。

</details>

<details>
<summary>什么时候算准备好了</summary>

`setupNeeded`（`launcher.go`）只做几个 `Stat`：`node_modules\ws\package.json`、`public\vendor\pixi.min.js`、`data\stages.json`、`public\assets`。全在就跳过准备步骤直接起服务，省掉一次 Node 进程、一次依赖检查和一次对 250 MB 目录的遍历。

它看不见的是**缺了某一个素材文件**这种情况，这正是「重新准备游戏文件」存在的原因：那一项带 `force` 跑一遍完整的 `tools/setup.mjs`，它会自己查全并补上。

</details>

<details>
<summary>怎么知道服务起来了</summary>

问 `/healthz`——这是上游 Dockerfile 里 healthcheck 用的那个端点。每 500 毫秒问一次，最多等 60 秒。

端口上已经有游戏在回应就直接开它，不再起第二个。等的时候还盯着服务进程：它一退出就不用等满 60 秒，直接换页。60 秒内没有回应，窗口就换成「游戏服务没能启动」那一页；端口上有别的东西在听的话，是「端口 3000 已经被占用」。

</details>

<details>
<summary>页面里的全屏按钮</summary>

游戏自己带一个全屏按钮（`public/js/ui/device.js`），用的是标准 Fullscreen API。在浏览器里它让页面铺满屏幕，在 WebView2 里它只让页面铺满**控件**——窗口的边框和尺寸一动不动，所以那个按钮看着像坏的。

于是这件事改成由本程序落实：`js/fullscreen.js` 监听页面的 `fullscreenchange`（按钮、Esc、页面自己退出，三条路都走这一个事件），通过 `_fullscreen` 绑定告诉 Go，Go 再把窗口本身铺满显示器（去掉边框、按显示器**整块**而不是工作区、临时置顶压住任务栏），退出时把样式、位置和置顶三样都还回原样。

靠事件而不是去改页面：Go 从不主动改页面的全屏状态，所以两边不会来回弹。全屏时窗口没有标题栏，系统菜单那几项够不到；真被托盘叫起来（居中、改尺寸），也会先把窗口从全屏里放出来再执行——否则会得到没有边框、又不再铺满的窗口。

</details>

<details>
<summary>窗口尺寸：三组参数与那个下限</summary>

菜单算出来的尺寸有下限（内容需要 480x360 CSS 像素，按显示缩放换算成设备像素）：「屏幕高度 + 1/3 + 9:16」这种组合能算出 256 像素宽，比页面能用的还窄，所以在 `size.go` 里夹住。鼠标拖拽的下限用同一个数，通过库的 `SetSize(..., HintMin)` 交给 Windows。

</details>

<details>
<summary>服务跟着窗口一起走</summary>

启动的那一份服务被放进一个 Job Object，规则是 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`：本程序以任何方式结束——正常退出、崩溃、任务管理器里被杀——最后一个句柄一关，Windows 就结束整棵树。正常退出时先关 job，再补一条 `taskkill /T /F`。

这不是洁癖：一个被留下的服务端，前端已经不在了，却还占着端口和内存，而且谁也说不清它属于谁。

</details>

<details>
<summary>日志的写法</summary>

全程序只用 `log/slog`，出口只有一个：`setupLogging` 里那一句 `slog.SetDefault`。服务与准备步骤的输出也接到这里（`gameWriter`），带 `what=setup` 或 `what=game` 字段。

- **消息是稳定的模板，变量进字段**：`slog.Info("game server started", "pid", pid)`，而不是 `log.Printf`。前者的每一行都是同一条 `msg`，可以按 `msg=` 检索、按字段过滤。
- **级别按语义**：`Error` 是「这件事失败了」，`Warn` 是「还能继续，但值得知道」，`Info` 是启动与状态。
- `windowState` 自己实现了 `LogValue`，于是落成 `state.width=726 state.onTop=false` 这样的分组。

</details>

## 数据与日志

都在 `%LOCALAPPDATA%\stronghold-protocol-launcher\`：

| 路径 | 是什么 |
|---|---|
| `window-state.json` | 窗口大小、是否最大化、是否置顶、三组尺寸菜单选的是哪一套，以及**上一次停在的地址**（加入链接也算；位置有意不记） |
| `prepare.lock` | 两份实例轮流写 `game\` 时用的锁文件，空文件，进程一结束锁就没了 |
| `app.log` | 运行日志，超过 1 MiB 轮转一次。**几份实例共写这一个文件**：每行都带 `pid`，混在一起也分得清；轮转时如果另一份还开着它，这一次轮转会被跳过——Windows 不允许改一个正被打开的文件名，接着写同一份就是了。**数据目录写不了时会退到 `%TEMP%\stronghold-protocol-launcher.log`** |
| `game\` | 游戏自己：源码、`node_modules\`、`public\assets\`（约 250 MB） |
| `EBWebView\` | WebView2 的浏览器配置目录 |

整个目录就是这份安装的全部；不想要了，删掉它即可。

出问题时的顺序永远是：**先看那一页提示、再看日志**。还没有窗口的时候就失败（数据目录建不出来、WebView2 起不来），程序会弹一个消息框，把原因和日志路径写在上面，而不是悄无声息地消失。

## 致谢

| 依赖 | 在这里做什么 |
|---|---|
| [sganggs/Stronghold-Protocol](https://github.com/sganggs/Stronghold-Protocol) | 游戏本体，按 GPL-3.0-or-later 授权 |
| [Drelf2018/systray](https://github.com/Drelf2018/systray) | 托盘图标与菜单 |
| [jchv/go-webview2](https://github.com/jchv/go-webview2) | WebView2 窗口 |
| [akavel/rsrc](https://github.com/akavel/rsrc) + [biessek/golang-ico](https://github.com/biessek/golang-ico) | 写 PE 图标资源、打包 .ico |
| [golang.org/x/image](https://pkg.go.dev/golang.org/x/image) | 把那张图缩到每个尺寸 |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | Win32 与 COM 调用 |

## 许可证

[MIT](LICENSE) © 2026 Drelf2018

游戏本体（`Stronghold-Protocol`）是 sganggs 的作品，按 GPL-3.0-or-later 授权；它不在本仓库里，只在运行时下载到本机的数据目录。游戏中的美术与音频版权归 Hypergryph / Yostar，仅供非商业的同人用途。
