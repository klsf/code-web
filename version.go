package main

import (
	"runtime/debug"
	"strings"
)

const defaultAppVersion = "1.2.2"

// appVersion 返回当前服务应暴露给前端的版本号。
func appVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info != nil {
		if version := normalizeReleaseVersion(strings.TrimSpace(info.Main.Version)); version != "" {
			return version
		}
	}
	return defaultAppVersion
}

// normalizeReleaseVersion 过滤 Go 自动生成的伪版本与 dirty 后缀，只保留正式发布版本。
func normalizeReleaseVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "(devel)" {
		return ""
	}
	if strings.Contains(version, "+dirty") {
		return ""
	}
	if strings.Count(version, "-") >= 2 {
		return ""
	}
	return version
}
