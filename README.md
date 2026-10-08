# 角色设定表工作台 · 桌面版

一个本地运行的 AI 角色设定表 / 插画生成工作台。开箱即用，无需浏览器，所有数据保存在本机。

> 本项目基于 [yang0/handraw-style](https://github.com/yang0/handraw-style) 的手绘风格编号体系与提示词方法论二次开发，感谢原作者的出色工作。

## 功能

- **设定表**：按模板生成整页角色设定表（角色档案 / 三视图 / 表情包等分格）
- **自由出图**：选风格（可选）+ 想画什么写什么，纯自由生图
- **图文卡**：一个主题批量生成社媒卡 / 信息图 / 漫画分镜 / IP 角色 / 电商图
- **作品库**：本机出图历史画廊，点击卡片一键回填参数
- **分镜成片**：多镜头分镜脚本 + 逐镜出图 + 拼接成片
- **图生视频**：参考图 + 提示词生成视频

其他：多 API Key 轮换与冷却显示、失败重试、玩法进度跟踪、数据自动滚动备份、上游风格库在线更新。

## 技术栈

[Wails v2](https://wails.io)（Go + WebView2 + Vue 3），单文件 exe，Windows 10/11。

## 构建

依赖：Go 1.22+、Node.js 18+

```bash
# 一键构建（Windows）
build.bat

# 或手动
cd frontend && npx vite build && cd ..
go build -tags desktop,production -ldflags "-w -s -H=windowsgui" -o build/bin/workbench-desktop.exe .
```

> 图标与版本资源由 `rsrc_windows_amd64.syso` 提供（go build 自动链接），配置见 `.winres/winres.json`，重新生成：
> `go-winres make --in .winres/winres.json --arch amd64`

## 使用前配置

1. 双击启动，打开「设置」，填入你的生图 API Key（默认对接 AGNES 系模型，兼容 OpenAI 风格接口）
2. Key 保存于本机 `data/settings.json`，**该文件含密钥，已被 .gitignore 排除，切勿提交或外传**
3. 所有生成记录、角色数据均保存在本机 `data/` 目录，随软件滚动自动备份

## 目录说明

```
├── *.go              # Go 后端（任务调度 / 提示词 / 存储 / 更新器）
├── frontend/         # Vue 3 前端
├── _vendor/          # go-webview2 定制依赖（go.mod replace 指向）
├── build/            # 图标、manifest、构建产物
└── .winres/          # 资源(图标/manifest)生成配置
```

## 协议

- 本项目代码以 [MIT](LICENSE) 协议开源
- 风格编号体系、提示词方法论、画廊数据来自 [yang0/handraw-style](https://github.com/yang0/handraw-style)（MIT with Attribution），按其协议要求在此显著标明原作者与仓库链接
- 风格名称中提及的在世/历史插画师均为风格参考描述，与本人无关
