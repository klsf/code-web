<p align="right">
  中文 | <a href="./README.en.md">English</a>
</p>

# <p align="center">Code Web</p>

<p align="center">
  <img alt="Go Version" src="https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white">
  <img alt="Version" src="https://img.shields.io/badge/version-v1.1.1-111827">
  <img alt="GitHub Repo stars" src="https://img.shields.io/github/stars/klsf/code-web?style=social">
</p>
<p align="center">
  中文 | <a href="./README.en.md">English</a>
</p>
`Code Web` 是一个基于 `Go + HTML + WebSocket` 构建的代码助手 Web UI，目前支持 `Codex` 和 `Claude`。

它面向移动端和桌面浏览器，目标是把本地代码助手 CLI 的连续会话体验搬到浏览器里：

- 浏览器关闭后，任务继续在服务端执行
- 重新打开页面后，自动恢复最新聊天内容

## 界面截图

<img alt="桌面端截图" width="500" src="./screen1.png" />
<img alt="移动端截图" height="252" src="./screen2.png" />

## 特性

- `codex` 模式基于 `codex app-server`，不是每条消息都重新启动一次独立 CLI
- 支持通过 `Claude` headless CLI 执行并续接会话
- 会话不再落盘到服务端，本地浏览器会保存远端会话引用用于恢复
- 支持图片随消息一起发送
- 支持流式输出、`Working...` 状态提示和自动重连
- 支持基础 Markdown 渲染
- 前端静态资源已打包进二进制，无需额外携带 `static/` 目录

## 运行要求

1. Go `1.22+`
2. 机器上可直接执行对应 provider 的 CLI
    - `codex` 模式：需要 `codex`
    - `claude` 模式：需要 `claude`
3. 如果使用 `codex` 模式，还需要先完成 `codex login`

## 配置文件

程序默认从二进制所在工作目录读取这两个配置文件：

- `claude-settings.json`
    - claude会话的配置，可以通过 `claude-settings.json` 配置环境变量，如代理、模型等
- `codex-settings.json`
    - 当前用于给 `codex app-server` 注入额外环境变量

`claude-settings.json` 格式示例：

```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "ms-******-ba92-4416-*****-d93c1124a9f9",
    "ANTHROPIC_BASE_URL": "https://api-inference.modelscope.cn",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "ZhipuAI/GLM-5",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "ZhipuAI/GLM-5",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "ZhipuAI/GLM-5",
    "ANTHROPIC_MODEL": "ZhipuAI/GLM-5"
  }
}
```

`codex-settings.json` 格式示例：

```json
{
  "env": {
    "HTTP_PROXY": "http://127.0.0.1:10808",
    "HTTPS_PROXY": "http://127.0.0.1:10808"
  }
}
```

## 启动

```bash
go build -o code-web 
./code-web
```

部署时只需要保留二进制本体，以及运行期间会写入的 `data/` 目录。

默认监听：

```text
0.0.0.0:991
```

## 登录密码

可通过启动参数设置登录密码：

```bash
./code-web -password "123456"
```

如果没有指定，默认密码是：

```text
codex
```

## 访问

浏览器打开：

```text
http://你的服务器IP:991
```

## 反向代理

如果放在 Nginx 后面，至少要转发 WebSocket：

```nginx
location / {
    proxy_pass http://127.0.0.1:991;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
}
```

## 说明

- 这个项目不是官方 OpenAI 产品
- 它是一个面向个人部署的 Codex / Claude Web 外壳
- 当前实现优先保证连续会话、恢复能力和移动端可用性

## License

本项目使用 `MIT` 许可证，见 [LICENSE](./LICENSE)。
