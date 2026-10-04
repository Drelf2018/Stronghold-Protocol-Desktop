//go:build windows

package main

// 登录时自启动：一条指向本程序的命令，写进当前用户的自启动项。
//
// 用注册表，而不是往「启动」文件夹里丢一个快捷方式——同样有效，
// 但勾选状态可以直接从同一个地方读回来，所以谁都不必去文件夹里
// 翻找它到底生效没有。
//
// 它用 golang.org/x/sys/windows/registry，也不起子进程：本程序是
// windowsgui，而任何控制台子进程都要额外做工作，才能不让它的窗口闪一下
// （见 launcher.go 里的 hiddenCommand）。

import (
	"log/slog"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// runKey 是当前用户的自启动项。用 HKCU 而不是 HKLM：不需要管理员
// 权限，而且「这个用户登录时启动」本来就是用户自己的选择。
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// autostartCommand 是写进注册表的那条命令：整条路径都加了引号，
// 因为可执行文件的名字里有空格。
func autostartCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return `"` + exe + `"`, nil
}

// autostartEnabled 读出的是自启动此刻有没有设置，以及它是不是真会
// 启动本程序。
//
// 一个存着值却指向别处的项——程序写进去之后被移动或改名，而一个能解压到
// 任何地方的文件夹最容易招来这种事——会被报告成未设置。勾选是对下次
// 登录的承诺，而这一条 Windows 不会兑现。
// 把它读成已设置，还会让下一次点击把自启动关掉而不是修好它，
// 因为菜单先问状态，然后把被告知状态的反面写回去。
//
// 值的名字是 appID，这个名字绝不能改：一改名就会在自启动项里
// 留下第二条过时的命令。
func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	command, _, err := k.GetStringValue(appID)
	if err != nil || command == "" {
		return false
	}
	// 取不到自己的路径（几乎不会发生）时，照注册表说的算：一次查询失败不该被说成「没有启用」。
	want, err := autostartCommand()
	if err != nil {
		slog.Warn("autostart: cannot find this program's path", "error", err)
		return true
	}
	// 路径比较不区分大小写，Windows 的路径本来就是这样。
	return strings.EqualFold(command, want)
}

// setAutostart 设置或清除自启动，并报告**实际结果**：注册表写入
// 可能被拒绝，而菜单的勾选必须匹配真实状态，而不是意图。
func setAutostart(on bool) bool {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		slog.Error("autostart: cannot open the startup key", "error", err)
		return autostartEnabled()
	}
	defer k.Close()

	if !on {
		if err := k.DeleteValue(appID); err != nil && err != registry.ErrNotExist {
			slog.Error("autostart: cannot remove the entry", "error", err)
		}
		return autostartEnabled()
	}
	command, err := autostartCommand()
	if err != nil {
		slog.Error("autostart: cannot find this program's path", "error", err)
		return autostartEnabled()
	}
	if err := k.SetStringValue(appID, command); err != nil {
		slog.Error("autostart: cannot write the entry", "error", err)
		return autostartEnabled()
	}
	slog.Info("autostart: entry written", "command", command)
	return autostartEnabled()
}
