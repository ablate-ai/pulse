package server

// web.go 原来负责托管 WASM 面板静态文件。
// 现已切换为 HTMX + 服务端模板方案，静态资源由 internal/panel 包通过 embed.FS 内联托管。
// 此文件保留但不再注册任何路由。
