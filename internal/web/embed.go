package web

import "embed"

// Files 包含构建后的前端资源。
//
//go:embed dist/*
var Files embed.FS
